package node

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/bridge"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/fee"
	"github.com/byunghee1994/fairspeed-dex/internal/funding"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/oracle"
	"github.com/byunghee1994/fairspeed-dex/internal/points"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
)

type LocalBlockProcessor struct {
	AccountKeeper      *account.AccountKeeper
	AssetKeeper        *asset.AssetKeeper
	OrderBookKeeper    *clob.OrderBookKeeper
	MatchingEngine     clob.MatchingEngine
	SettlementEngine   *settlement.SettlementEngine
	SettlementKeeper   *settlement.SettlementKeeper
	RiskChecker        *risk.RiskChecker
	ValidatorKeeper    *validator.ValidatorKeeper
	GovernanceKeeper   *governance.GovernanceKeeper
	GovernanceExecutor governance.ParameterExecutor
	SanctionsStore     compliance.SanctionsStore
	PositionTracker    *risk.PositionTracker
	InsuranceFund      *risk.InsuranceFund
	EventBus           *state.EventBus
	AppState           *state.AppState
	// DistributionAssets lists the asset IDs distributed from the treasury to validators.
	// Defaults to ["USDC"] when empty.
	DistributionAssets []string
	PointsKeeper       *points.PointsKeeper
}

func (p *LocalBlockProcessor) ProcessBlock(block LocalBlock) (BlockResult, error) {
	expected := p.AppState.CurrentHeight() + 1
	if block.Height != expected {
		return BlockResult{}, fmt.Errorf("invalid block height: expected %d got %d", expected, block.Height)
	}

	_ = p.OrderBookKeeper.ExpireOrders(block.Height)

	var allTrades []settlement.TradeExecution
	var allEvents []state.Event

	p.EventBus.SubscribeAll(func(e state.Event) {
		allEvents = append(allEvents, e)
	})

	// Emit OrderIncluded for every order transaction in the batch.
	for _, tx := range block.Batch.Transactions {
		if tx.TxType == fairbatch.TxSubmitOrder {
			p.emitOrderIncluded(tx, block.Height)
		}
	}

	txCount := 0
	for _, tx := range block.Batch.Transactions {
		if err := p.processTx(tx, block.Height); err != nil {
			return BlockResult{}, fmt.Errorf("processing tx type=%d: %w", tx.TxType, err)
		}
		txCount++
	}

	// Complete any validator unbondings whose delay has elapsed.
	if p.ValidatorKeeper != nil {
		p.ValidatorKeeper.ProcessUnbonding(block.Height)
	}

	// Tally governance proposals whose voting period ends at this block.
	if p.GovernanceKeeper != nil {
		p.GovernanceKeeper.TallyAndExecute(block.Height, p.GovernanceExecutor)
	}

	// Auto-finalize timelocked withdrawals that are now ready.
	p.processReadyWithdrawals(block.Height)

	// Distribute accumulated treasury fees to bonded validators.
	p.distributeFees(block.Height)

	// Settle funding payments for PERP markets that have reached their interval.
	p.settleFunding(block.Height)

	// Check all PERP positions for maintenance margin breach and liquidate as needed.
	p.checkAndLiquidate(block.Height)

	// Evaluate conditional orders (stop-loss / take-profit triggers).
	p.evaluateConditionalOrders(block.Height)

	// Expire conditional orders whose block height has been reached.
	p.expireConditionalOrders(block.Height)

	allTrades = p.SettlementKeeper.TradesForBlock(block.Height)
	p.AppState.IncrementBlock()

	p.EventBus.Publish(state.Event{
		Type:        state.EventBlockProcessed,
		BlockHeight: block.Height,
		Payload: state.BlockProcessedPayload{
			BlockHeight: block.Height,
			TxCount:     txCount,
			TradeCount:  len(allTrades),
		},
	})

	return BlockResult{
		Height:     block.Height,
		TxCount:    txCount,
		TradeCount: len(allTrades),
		Trades:     allTrades,
		Events:     allEvents,
	}, nil
}

func (p *LocalBlockProcessor) emitOrderIncluded(tx fairbatch.Transaction, blockHeight int64) {
	payload := tx.Payload.(fairbatch.SubmitOrderPayload)
	o := payload.Order
	p.EventBus.Publish(state.Event{
		Type:        state.EventOrderIncluded,
		BlockHeight: blockHeight,
		Payload: state.OrderIncludedPayload{
			OrderId:     o.OrderId,
			AccountId:   o.AccountId,
			MarketId:    o.MarketId,
			BlockHeight: blockHeight,
			TxHash:      tx.TxHash,
		},
	})
}

func (p *LocalBlockProcessor) processTx(tx fairbatch.Transaction, blockHeight int64) error {
	switch tx.TxType {
	case fairbatch.TxCreateAccount:
		payload := tx.Payload.(fairbatch.CreateAccountPayload)
		acc, err := p.AccountKeeper.CreateAccount(
			payload.OwnerAddress,
			payload.RootPublicKey,
			payload.WithdrawalPublicKey,
			blockHeight,
			tx.TxHash,
		)
		if err == nil && p.PointsKeeper != nil {
			p.PointsKeeper.RegisterAccount(acc.AccountId, "")
		}
		return err

	case fairbatch.TxCreateSession:
		payload := tx.Payload.(fairbatch.CreateSessionPayload)
		_, err := p.AccountKeeper.CreateSession(payload.AccountId, payload.Opts, blockHeight, tx.TxHash)
		return err

	case fairbatch.TxDeposit:
		payload := tx.Payload.(fairbatch.DepositPayload)
		if err := p.AssetKeeper.Deposit(payload.AccountId, payload.AssetId, payload.Amount); err != nil {
			return err
		}
		p.EventBus.Publish(state.Event{
			Type:        state.EventDepositSimulated,
			BlockHeight: blockHeight,
			Payload: state.DepositSimulatedPayload{
				AccountId: payload.AccountId,
				AssetId:   payload.AssetId,
				Amount:    payload.Amount,
			},
		})
		return nil

	case fairbatch.TxSubmitOrder:
		payload := tx.Payload.(fairbatch.SubmitOrderPayload)
		return p.processOrder(payload.Order, tx.TxHash, blockHeight)

	case fairbatch.TxCancelOrder:
		payload := tx.Payload.(fairbatch.CancelOrderPayload)
		return p.OrderBookKeeper.CancelOrder(payload.OrderId, payload.AccountId, blockHeight)

	case fairbatch.TxWithdraw:
		payload := tx.Payload.(fairbatch.WithdrawPayload)
		return p.processWithdraw(payload, tx.TxHash, blockHeight)

	case fairbatch.TxKYCApprove:
		payload := tx.Payload.(fairbatch.KYCApprovePayload)
		return p.AccountKeeper.UpdateKYCFull(
			payload.AccountId,
			account.KYCStatus(payload.Status),
			account.KYCTier(payload.Tier),
			account.Jurisdiction(payload.Jurisdiction),
			blockHeight,
		)

	case fairbatch.TxBondValidator:
		payload := tx.Payload.(fairbatch.BondValidatorPayload)
		if p.ValidatorKeeper == nil {
			return fmt.Errorf("validator keeper not configured")
		}
		return p.ValidatorKeeper.BondValidator(
			payload.ValidatorId, payload.Moniker, payload.PubKey, payload.StakeAmount, blockHeight)

	case fairbatch.TxUnbondValidator:
		payload := tx.Payload.(fairbatch.UnbondValidatorPayload)
		if p.ValidatorKeeper == nil {
			return fmt.Errorf("validator keeper not configured")
		}
		return p.ValidatorKeeper.UnbondValidator(payload.ValidatorId, blockHeight)

	case fairbatch.TxSubmitProposal:
		payload := tx.Payload.(fairbatch.SubmitProposalPayload)
		if p.GovernanceKeeper == nil {
			return fmt.Errorf("governance keeper not configured")
		}
		govPayload := p.buildGovernancePayload(payload)
		_, err := p.GovernanceKeeper.SubmitProposal(
			governance.ProposalType(payload.ProposalType),
			payload.Title, payload.Description,
			govPayload, payload.VoteEndHeight, blockHeight)
		return err

	case fairbatch.TxVote:
		payload := tx.Payload.(fairbatch.VotePayload)
		if p.GovernanceKeeper == nil {
			return fmt.Errorf("governance keeper not configured")
		}
		return p.GovernanceKeeper.CastVote(
			payload.ProposalId, payload.ValidatorId, payload.Choice, payload.Stake, blockHeight)

	case fairbatch.TxSanctionAccount:
		payload := tx.Payload.(fairbatch.SanctionAccountPayload)
		if p.SanctionsStore == nil {
			return fmt.Errorf("sanctions store not configured")
		}
		p.SanctionsStore.AddSanction(compliance.SanctionEntry{
			AccountId:   payload.AccountId,
			Reason:      payload.Reason,
			ListName:    payload.ListName,
			AddedHeight: blockHeight,
		})
		return nil

	case fairbatch.TxUnsanctionAccount:
		payload := tx.Payload.(fairbatch.UnsanctionAccountPayload)
		if p.SanctionsStore == nil {
			return fmt.Errorf("sanctions store not configured")
		}
		p.SanctionsStore.RemoveSanction(payload.AccountId)
		return nil

	case fairbatch.TxHaltMarket:
		payload := tx.Payload.(fairbatch.HaltMarketPayload)
		p.AppState.HaltMarket(payload.MarketId, payload.Reason, blockHeight)
		p.EventBus.Publish(state.Event{
			Type:        state.EventMarketHalted,
			BlockHeight: blockHeight,
			Payload: state.MarketHaltedPayload{
				MarketId:    payload.MarketId,
				Reason:      payload.Reason,
				BlockHeight: blockHeight,
			},
		})
		return nil

	case fairbatch.TxResumeMarket:
		payload := tx.Payload.(fairbatch.ResumeMarketPayload)
		p.AppState.ResumeMarket(payload.MarketId)
		p.EventBus.Publish(state.Event{
			Type:        state.EventMarketResumed,
			BlockHeight: blockHeight,
			Payload: state.MarketResumedPayload{
				MarketId:    payload.MarketId,
				BlockHeight: blockHeight,
			},
		})
		return nil

	case fairbatch.TxSubmitPrice:
		payload := tx.Payload.(fairbatch.SubmitPricePayload)
		p.AppState.SetOraclePrice(oracle.PriceSubmission{
			MarketId:    payload.MarketId,
			ValidatorId: payload.ValidatorId,
			Price:       payload.Price,
			BlockHeight: blockHeight,
		})
		markPrice := p.AppState.GetMarkPrice(payload.MarketId)
		p.AppState.RecordPricePoint(payload.MarketId, markPrice, blockHeight)
		p.EventBus.Publish(state.Event{
			Type:        state.EventMarkPriceUpdated,
			BlockHeight: blockHeight,
			Payload: state.MarkPriceUpdatedPayload{
				MarketId:    payload.MarketId,
				MarkPrice:   markPrice,
				BlockHeight: blockHeight,
			},
		})
		return nil

	case fairbatch.TxWithdrawRequest:
		payload := tx.Payload.(fairbatch.WithdrawRequestPayload)
		return p.processWithdrawRequest(payload, tx.TxHash, blockHeight)

	case fairbatch.TxBridgeAttest:
		payload := tx.Payload.(fairbatch.BridgeAttestPayload)
		return p.processBridgeAttest(payload, blockHeight)

	case fairbatch.TxRegisterPerpMarket:
		payload := tx.Payload.(fairbatch.RegisterPerpMarketPayload)
		return p.processRegisterPerpMarket(payload, blockHeight)

	case fairbatch.TxSubmitIndexPrice:
		payload := tx.Payload.(fairbatch.SubmitIndexPricePayload)
		median := p.AppState.SubmitIndexOraclePrice(payload.MarketId, payload.ValidatorId, payload.Price)
		p.EventBus.Publish(state.Event{
			Type:        state.EventIndexPriceUpdated,
			BlockHeight: blockHeight,
			Payload: state.IndexPriceUpdatedPayload{
				MarketId:    payload.MarketId,
				IndexPrice:  median,
				ValidatorId: payload.ValidatorId,
				Source:      payload.Source,
				BlockHeight: blockHeight,
			},
		})
		return nil

	case fairbatch.TxSubmitConditionalOrder:
		payload := tx.Payload.(fairbatch.SubmitConditionalOrderPayload)
		return p.processSubmitConditionalOrder(payload, blockHeight)

	case fairbatch.TxCancelConditionalOrder:
		payload := tx.Payload.(fairbatch.CancelConditionalOrderPayload)
		return p.processCancelConditionalOrder(payload, blockHeight)

	default:
		return fmt.Errorf("unknown transaction type: %d", tx.TxType)
	}
}

func (p *LocalBlockProcessor) processOrder(o clob.Order, txHash string, blockHeight int64) error {
	// All validation failures below are "soft" rejections: emit OrderRejected and return nil
	// so the block continues processing subsequent transactions.

	// Circuit breaker: reject new orders when market is halted.
	if p.AppState.GetMarketStatus(o.MarketId) == clob.MarketStatusHalted {
		p.emitOrderRejected(o, "market "+o.MarketId+" is halted", blockHeight)
		return nil
	}

	sess, err := p.AccountKeeper.ValidateSession(o.SessionId, o.MarketId, blockHeight)
	if err != nil {
		p.emitOrderRejected(o, err.Error(), blockHeight)
		return nil
	}

	// Verify ed25519 signature: the order must be signed by the session key.
	if err := account.VerifyOrderSignature(txHash, o.Signature, sess.SessionPublicKey); err != nil {
		p.emitOrderRejected(o, "invalid signature: "+err.Error(), blockHeight)
		return nil
	}

	// Replay protection: AccountSequence must match the current account nonce.
	acc, ok := p.AppState.GetAccount(o.AccountId)
	if !ok {
		p.emitOrderRejected(o, "account not found", blockHeight)
		return nil
	}
	if o.AccountSequence != acc.AccountSequence {
		msg := fmt.Sprintf("sequence mismatch: expected %d got %d", acc.AccountSequence, o.AccountSequence)
		p.emitOrderRejected(o, msg, blockHeight)
		return nil
	}
	if _, err := p.AccountKeeper.IncrementSequence(o.AccountId); err != nil {
		return fmt.Errorf("increment sequence: %w", err) // internal error → hard fail
	}

	if err := p.RiskChecker.CheckOrder(&o, sess, blockHeight); err != nil {
		p.emitOrderRejected(o, err.Error(), blockHeight)
		return nil
	}

	if err := p.reserveCollateral(&o, blockHeight); err != nil {
		p.emitOrderRejected(o, err.Error(), blockHeight)
		return nil
	}

	ob := p.OrderBookKeeper.GetOrCreateOrderBook(o.MarketId)
	results := p.MatchingEngine.MatchOrder(&o, ob, blockHeight)

	if len(results) > 0 {
		trades, err := p.SettlementEngine.Settle(results, blockHeight)
		if err != nil {
			return fmt.Errorf("settlement: %w", err)
		}
		for _, t := range trades {
			p.SettlementKeeper.RecordTrade(t)
			if p.PointsKeeper != nil {
				notional := t.Price * t.Quantity
				isPerp := p.AppState.IsPerp(t.MarketId)
				p.PointsKeeper.RecordTradePair(t.MakerAccountId, t.TakerAccountId, notional, isPerp, blockHeight)
			}
		}
	}

	if o.RemainingQuantity > 0 && o.TimeInForce == clob.TimeInForceGtc && o.Status != clob.OrderStatusRejected {
		p.AppState.SetOrder(&o)
		ob.AddOrder(p.getOrStoreOrder(&o))
		p.AppState.SetOrderBook(ob)
		p.emitOrderSubmitted(o, blockHeight)
	} else {
		if o.RemainingQuantity > 0 && o.Status != clob.OrderStatusRejected {
			p.releaseCollateral(o, o.RemainingQuantity)
		} else if o.Status == clob.OrderStatusRejected {
			p.releaseCollateral(o, o.Quantity)
		}
		p.AppState.SetOrder(&o)
		// Record completed (non-open) orders in account history.
		if o.Status != clob.OrderStatusOpen && o.Status != clob.OrderStatusPartiallyFilled {
			p.AppState.RecordOrderHistory(&o)
		}
		if o.Status == clob.OrderStatusRejected {
			p.emitOrderRejected(o, "FOK not fillable", blockHeight)
		} else {
			p.emitOrderSubmitted(o, blockHeight)
		}
	}

	return nil
}

// reserveCollateral reserves collateral before matching.
// SPOT: reserves full notional (BUY) or base asset qty (SELL).
// PERP: reserves InitialMargin in quote asset for both sides.
// ReduceOnly PERP orders require no collateral.
func (p *LocalBlockProcessor) reserveCollateral(o *clob.Order, blockHeight int64) error {
	cfg, isPerp := p.AppState.GetPerpConfig(o.MarketId)
	if !isPerp {
		return p.OrderBookKeeper.ReserveForOrder(*o)
	}
	if o.ReduceOnly {
		return nil
	}
	notional := o.Price * o.RemainingQuantity
	leverage := o.Leverage
	if leverage <= 0 {
		leverage = 10_000 / cfg.InitialMarginBps
		if leverage == 0 {
			leverage = 1
		}
	}
	marginAmount := notional / leverage
	if marginAmount == 0 {
		marginAmount = 1
	}
	quoteAsset := cfg.QuoteAsset
	if quoteAsset == "" {
		quoteAsset = clob.SplitMarket(o.MarketId)[1]
	}
	if err := p.AssetKeeper.Reserve(o.AccountId, quoteAsset, marginAmount); err != nil {
		return err
	}
	if p.PositionTracker != nil {
		p.PositionTracker.AdjustMargin(o.AccountId, o.MarketId, marginAmount)
	}
	return nil
}

// releaseCollateral releases reserved collateral for unfilled quantity.
func (p *LocalBlockProcessor) releaseCollateral(o clob.Order, releasedQty int64) {
	cfg, isPerp := p.AppState.GetPerpConfig(o.MarketId)
	if !isPerp {
		_ = p.OrderBookKeeper.ReleaseForOrder(o, releasedQty)
		return
	}
	if o.ReduceOnly || releasedQty <= 0 {
		return
	}
	leverage := o.Leverage
	if leverage <= 0 {
		leverage = 10_000 / cfg.InitialMarginBps
		if leverage == 0 {
			leverage = 1
		}
	}
	// Release proportional to released quantity
	totalMargin := o.Price * o.Quantity / leverage
	releaseMargin := totalMargin * releasedQty / o.Quantity
	if releaseMargin <= 0 {
		return
	}
	quoteAsset := cfg.QuoteAsset
	if quoteAsset == "" {
		quoteAsset = clob.SplitMarket(o.MarketId)[1]
	}
	_ = p.AssetKeeper.Release(o.AccountId, quoteAsset, releaseMargin)
	if p.PositionTracker != nil {
		p.PositionTracker.AdjustMargin(o.AccountId, o.MarketId, -releaseMargin)
	}
}

func (p *LocalBlockProcessor) emitOrderSubmitted(o clob.Order, blockHeight int64) {
	p.EventBus.Publish(state.Event{
		Type:        state.EventOrderSubmitted,
		BlockHeight: blockHeight,
		Payload: state.OrderSubmittedPayload{
			OrderId:   o.OrderId,
			AccountId: o.AccountId,
			MarketId:  o.MarketId,
			Status:    string(o.Status),
		},
	})
}

func (p *LocalBlockProcessor) emitOrderRejected(o clob.Order, reason string, blockHeight int64) {
	p.EventBus.Publish(state.Event{
		Type:        state.EventOrderRejected,
		BlockHeight: blockHeight,
		Payload: state.OrderRejectedPayload{
			OrderId:   o.OrderId,
			AccountId: o.AccountId,
			MarketId:  o.MarketId,
			Reason:    reason,
		},
	})
}

func (p *LocalBlockProcessor) getOrStoreOrder(o *clob.Order) *clob.Order {
	stored, ok := p.AppState.GetOrder(o.OrderId)
	if ok {
		return stored
	}
	return o
}

func (p *LocalBlockProcessor) buildGovernancePayload(sp fairbatch.SubmitProposalPayload) any {
	switch governance.ProposalType(sp.ProposalType) {
	case governance.TypeUpdateFeePolicy:
		return governance.UpdateFeePolicyParams{MakerBps: sp.MakerBps, TakerBps: sp.TakerBps}
	case governance.TypeUpdateRiskPolicy:
		return governance.UpdateRiskPolicyParams{
			MaxOrderQuantity:            sp.MaxOrderQuantity,
			MinOrderQuantity:            sp.MinOrderQuantity,
			MaxDailyVolumePerSession:    sp.MaxDailyVolumePerSession,
			MaxPositionSize:             sp.MaxPositionSize,
			RequireKYC:                  sp.RequireKYC,
			AMLSingleTradeLimitNotional: sp.AMLSingleTradeLimitNotional,
		}
	case governance.TypeListMarket:
		return governance.ListMarketParams{
			MarketId:   sp.MarketId,
			BaseAsset:  sp.BaseAsset,
			QuoteAsset: sp.QuoteAsset,
		}
	case governance.TypeUpdatePerpConfig:
		return governance.UpdatePerpConfigParams{
			MarketId:             sp.MarketId,
			InitialMarginBps:     sp.InitialMarginBps,
			MaintenanceMarginBps: sp.MaintenanceMarginBps,
			MaxLeverage:          sp.MaxLeverage,
			MaxFundingRateBps:    sp.MaxFundingRateBps,
		}
	default:
		return nil
	}
}

// processWithdrawRequest handles TxWithdrawRequest: validates auth, reserves funds,
// and creates a pending withdrawal that auto-finalizes after the timelock delay.
func (p *LocalBlockProcessor) processWithdrawRequest(payload fairbatch.WithdrawRequestPayload, txHash string, blockHeight int64) error {
	acc, ok := p.AppState.GetAccount(payload.AccountId)
	if !ok {
		return fmt.Errorf("account not found: %s", payload.AccountId)
	}
	if payload.AccountSequence != acc.AccountSequence {
		return fmt.Errorf("withdraw request sequence mismatch: expected %d got %d", acc.AccountSequence, payload.AccountSequence)
	}
	if payload.Signature != "" && acc.WithdrawalPublicKey != "" {
		if err := account.VerifyOrderSignature(txHash, payload.Signature, acc.WithdrawalPublicKey); err != nil {
			return fmt.Errorf("invalid withdrawal signature: %w", err)
		}
	}
	if _, err := p.AccountKeeper.IncrementSequence(payload.AccountId); err != nil {
		return fmt.Errorf("increment sequence: %w", err)
	}
	// Vesting check: if a vesting schedule exists for this asset, ensure the
	// requested amount does not exceed what has vested but not yet been released.
	if sched := p.AppState.GetVestingSchedule(payload.AccountId, payload.AssetId); sched != nil {
		releasable := sched.ReleasableNow(blockHeight)
		if payload.Amount > releasable {
			return fmt.Errorf("withdrawal exceeds vested amount: requested %d, releasable %d", payload.Amount, releasable)
		}
	}
	// Reserve funds so they cannot be spent while the withdrawal is pending.
	if err := p.AssetKeeper.Reserve(payload.AccountId, payload.AssetId, payload.Amount); err != nil {
		return fmt.Errorf("reserve for withdrawal: %w", err)
	}
	readyAt := blockHeight + p.RiskChecker.WithdrawTimelockBlocks()
	// Deterministic ID: sha256 of txHash + blockHeight.
	sum := sha256.Sum256([]byte(txHash + ":" + strconv.FormatInt(blockHeight, 10)))
	wid := fmt.Sprintf("wd-%x", sum[:8])
	p.AppState.AddPendingWithdrawal(account.PendingWithdrawal{
		WithdrawalId:  wid,
		AccountId:     payload.AccountId,
		AssetId:       payload.AssetId,
		Amount:        payload.Amount,
		ReadyAtHeight: readyAt,
	})
	p.EventBus.Publish(state.Event{
		Type:        state.EventWithdrawRequested,
		BlockHeight: blockHeight,
		Payload: state.WithdrawRequestedPayload{
			WithdrawalId:  wid,
			AccountId:     payload.AccountId,
			AssetId:       payload.AssetId,
			Amount:        payload.Amount,
			ReadyAtHeight: readyAt,
		},
	})
	return nil
}

// processReadyWithdrawals finalizes all pending withdrawals whose timelock has elapsed.
func (p *LocalBlockProcessor) processReadyWithdrawals(blockHeight int64) {
	ready := p.AppState.ReadyWithdrawals(blockHeight)
	for _, w := range ready {
		// Transfer reserved funds out of the account (completed withdrawal).
		if err := p.AssetKeeper.Release(w.AccountId, w.AssetId, w.Amount); err == nil {
			if err := p.AssetKeeper.Withdraw(w.AccountId, w.AssetId, w.Amount); err == nil {
				// Update vesting released counter if applicable.
				p.AppState.UpdateVestingReleased(w.AccountId, w.AssetId, w.Amount)
				p.AppState.CompletePendingWithdrawal(w.WithdrawalId)
				p.EventBus.Publish(state.Event{
					Type:        state.EventWithdrawFinalized,
					BlockHeight: blockHeight,
					Payload: state.WithdrawFinalizedPayload{
						WithdrawalId: w.WithdrawalId,
						AccountId:    w.AccountId,
						AssetId:      w.AssetId,
						Amount:       w.Amount,
						BlockHeight:  blockHeight,
					},
				})
			}
		}
	}
}

func (p *LocalBlockProcessor) processWithdraw(payload fairbatch.WithdrawPayload, txHash string, blockHeight int64) error {
	acc, ok := p.AppState.GetAccount(payload.AccountId)
	if !ok {
		return fmt.Errorf("account not found: %s", payload.AccountId)
	}
	if payload.AccountSequence != acc.AccountSequence {
		return fmt.Errorf("withdraw sequence mismatch: expected %d got %d", acc.AccountSequence, payload.AccountSequence)
	}
	// Verify signature with the account's withdrawal key (skip if key is empty — test/bootstrap).
	if payload.Signature != "" && acc.WithdrawalPublicKey != "" {
		if err := account.VerifyOrderSignature(txHash, payload.Signature, acc.WithdrawalPublicKey); err != nil {
			return fmt.Errorf("invalid withdrawal signature: %w", err)
		}
	}
	if _, err := p.AccountKeeper.IncrementSequence(payload.AccountId); err != nil {
		return fmt.Errorf("increment sequence: %w", err)
	}
	if err := p.AssetKeeper.Withdraw(payload.AccountId, payload.AssetId, payload.Amount); err != nil {
		return fmt.Errorf("withdraw: %w", err)
	}
	p.EventBus.Publish(state.Event{
		Type:        state.EventWithdrawal,
		BlockHeight: blockHeight,
		Payload: state.WithdrawalPayload{
			AccountId: payload.AccountId,
			AssetId:   payload.AssetId,
			Amount:    payload.Amount,
		},
	})
	return nil
}

// processRegisterPerpMarket registers a new perpetual futures market.
func (p *LocalBlockProcessor) processRegisterPerpMarket(payload fairbatch.RegisterPerpMarketPayload, blockHeight int64) error {
	if payload.InitialMarginBps <= 0 || payload.MaintenanceMarginBps <= 0 {
		return fmt.Errorf("perp market %s: margin bps must be > 0", payload.MarketId)
	}
	if payload.MaintenanceMarginBps >= payload.InitialMarginBps {
		return fmt.Errorf("perp market %s: maintenance margin must be < initial margin", payload.MarketId)
	}
	if payload.MaxLeverage <= 0 {
		return fmt.Errorf("perp market %s: max leverage must be > 0", payload.MarketId)
	}
	cfg := clob.PerpConfig{
		BaseAsset:             payload.BaseAsset,
		QuoteAsset:            payload.QuoteAsset,
		InitialMarginBps:      payload.InitialMarginBps,
		MaintenanceMarginBps:  payload.MaintenanceMarginBps,
		MaxLeverage:           payload.MaxLeverage,
		FundingIntervalBlocks: payload.FundingIntervalBlocks,
		MaxFundingRateBps:     payload.MaxFundingRateBps,
	}
	p.AppState.SetMarketAsPerp(payload.MarketId, cfg)
	p.AppState.SetLastFundingBlock(payload.MarketId, blockHeight)
	// Register assets for the market.
	if payload.BaseAsset != "" {
		p.AssetKeeper.RegisterAsset(asset.Asset{AssetId: payload.BaseAsset, Symbol: payload.BaseAsset, Decimals: 8})
	}
	if payload.QuoteAsset != "" {
		p.AssetKeeper.RegisterAsset(asset.Asset{AssetId: payload.QuoteAsset, Symbol: payload.QuoteAsset, Decimals: 6})
	}
	p.EventBus.Publish(state.Event{
		Type:        state.EventPerpMarketRegistered,
		BlockHeight: blockHeight,
		Payload: state.PerpMarketRegisteredPayload{
			MarketId:              payload.MarketId,
			BaseAsset:             payload.BaseAsset,
			QuoteAsset:            payload.QuoteAsset,
			InitialMarginBps:      payload.InitialMarginBps,
			MaintenanceMarginBps:  payload.MaintenanceMarginBps,
			MaxLeverage:           payload.MaxLeverage,
			FundingIntervalBlocks: payload.FundingIntervalBlocks,
			MaxFundingRateBps:     payload.MaxFundingRateBps,
		},
	})
	return nil
}

// processBridgeAttest handles TxBridgeAttest: records a validator's cross-chain deposit
// attestation. When ⅔ quorum is reached for the first time, funds are credited.
func (p *LocalBlockProcessor) processBridgeAttest(payload fairbatch.BridgeAttestPayload, blockHeight int64) error {
	if p.ValidatorKeeper == nil {
		return fmt.Errorf("validator keeper not configured")
	}

	// Verify the attesting validator is bonded.
	v, ok := p.ValidatorKeeper.GetValidator(payload.ValidatorId)
	if !ok || v.Status != validator.StatusBonded {
		return fmt.Errorf("validator %s is not bonded", payload.ValidatorId)
	}

	totalStake := p.ValidatorKeeper.TotalStake()

	deposit := bridge.BridgeDeposit{
		DepositId:   payload.DepositId,
		AccountId:   payload.AccountId,
		AssetId:     payload.AssetId,
		Amount:      payload.Amount,
		SourceChain: payload.SourceChain,
	}

	attestedStake, alreadyCompleted := p.AppState.RecordAttestation(deposit, payload.ValidatorId, v.Stake)
	if alreadyCompleted {
		return nil // idempotent — quorum already reached previously
	}

	p.EventBus.Publish(state.Event{
		Type:        state.EventBridgeAttested,
		BlockHeight: blockHeight,
		Payload: state.BridgeAttestedPayload{
			DepositId:     payload.DepositId,
			ValidatorId:   payload.ValidatorId,
			AttestedStake: attestedStake,
			TotalStake:    totalStake,
			BlockHeight:   blockHeight,
		},
	})

	if bridge.QuorumReached(attestedStake, totalStake) {
		// Credit funds and mark deposit as complete.
		if err := p.AssetKeeper.Deposit(payload.AccountId, payload.AssetId, payload.Amount); err != nil {
			return fmt.Errorf("bridge deposit credit: %w", err)
		}
		p.AppState.CompleteBridgeDeposit(payload.DepositId)
		p.EventBus.Publish(state.Event{
			Type:        state.EventBridgeCompleted,
			BlockHeight: blockHeight,
			Payload: state.BridgeCompletedPayload{
				DepositId:   payload.DepositId,
				AccountId:   payload.AccountId,
				AssetId:     payload.AssetId,
				Amount:      payload.Amount,
				BlockHeight: blockHeight,
			},
		})
	}
	return nil
}

// distributeFees transfers the treasury balance to bonded validators proportional
// to their stake. Called once per block after all transactions are processed.
func (p *LocalBlockProcessor) distributeFees(blockHeight int64) {
	if p.ValidatorKeeper == nil {
		return
	}
	validators := p.ValidatorKeeper.ActiveSet()
	if len(validators) == 0 {
		return
	}
	totalStake := p.ValidatorKeeper.TotalStake()
	if totalStake == 0 {
		return
	}
	assets := p.DistributionAssets
	if len(assets) == 0 {
		assets = []string{"USDC"}
	}
	for _, assetId := range assets {
		treasury := p.AssetKeeper.GetBalance(fee.TreasuryAccountId, assetId)
		total := treasury.Available
		if total <= 0 {
			continue
		}
		distributed := int64(0)
		for i, v := range validators {
			var share int64
			if i == len(validators)-1 {
				// Last validator gets the remainder to avoid rounding loss.
				share = total - distributed
			} else {
				share = fee.DistributionShare(total, v.Stake, totalStake)
			}
			if share <= 0 {
				continue
			}
			rewardAccId := fee.ValidatorRewardAccountId(v.ValidatorId)
			_ = p.AssetKeeper.DeductAvailable(fee.TreasuryAccountId, assetId, share)
			p.AssetKeeper.CreditAvailable(rewardAccId, assetId, share)
			distributed += share
			p.EventBus.Publish(state.Event{
				Type:        state.EventFeeDistributed,
				BlockHeight: blockHeight,
				Payload: state.FeeDistributedPayload{
					ValidatorId: v.ValidatorId,
					AssetId:     assetId,
					Amount:      share,
					BlockHeight: blockHeight,
				},
			})
		}
	}
}

// settleFunding settles periodic funding payments for all PERP markets whose
// FundingIntervalBlocks has elapsed since the last settlement.
func (p *LocalBlockProcessor) settleFunding(blockHeight int64) {
	if p.PositionTracker == nil {
		return
	}
	perpMarkets := p.AppState.AllPerpMarkets()
	for marketId, cfg := range perpMarkets {
		if cfg.FundingIntervalBlocks <= 0 {
			continue
		}
		lastBlock := p.AppState.GetLastFundingBlock(marketId)
		if blockHeight-lastBlock < cfg.FundingIntervalBlocks {
			continue
		}

		markPrice := p.AppState.GetMarkPrice(marketId)
		indexPrice := p.AppState.GetIndexPrice(marketId)
		rateBps := funding.CalcFundingRate(markPrice, indexPrice, cfg.MaxFundingRateBps)

		epoch := funding.FundingEpoch{
			MarketId:    marketId,
			RateBps:     rateBps,
			MarkPrice:   markPrice,
			IndexPrice:  indexPrice,
			BlockHeight: blockHeight,
		}
		p.AppState.AppendFundingEpoch(epoch)
		p.AppState.SetLastFundingBlock(marketId, blockHeight)

		// Zero rate: record epoch but skip transfers.
		if rateBps == 0 {
			continue
		}

		quoteAsset := cfg.QuoteAsset

		positions := p.PositionTracker.AllPositions()
		var totalLongsPaid, totalShortsReceived int64

		// Two-pass: collect from payers first so treasury has funds for receivers.
		for _, pos := range positions {
			if pos.MarketId != marketId || pos.NetQuantity == 0 || pos.AvgEntryPrice == 0 {
				continue
			}
			payment := funding.FundingPayment(pos.NetQuantity, pos.AvgEntryPrice, rateBps)
			if payment <= 0 {
				continue
			}
			bal := p.AssetKeeper.GetBalance(pos.AccountId, quoteAsset)
			deduct := payment
			if deduct > bal.Available {
				deduct = bal.Available
			}
			if deduct > 0 {
				_ = p.AssetKeeper.DeductAvailable(pos.AccountId, quoteAsset, deduct)
				p.AssetKeeper.CreditAvailable(fee.TreasuryAccountId, quoteAsset, deduct)
				totalLongsPaid += deduct
			}
		}
		for _, pos := range positions {
			if pos.MarketId != marketId || pos.NetQuantity == 0 || pos.AvgEntryPrice == 0 {
				continue
			}
			payment := funding.FundingPayment(pos.NetQuantity, pos.AvgEntryPrice, rateBps)
			if payment >= 0 {
				continue
			}
			receive := -payment
			treasuryBal := p.AssetKeeper.GetBalance(fee.TreasuryAccountId, quoteAsset)
			if receive > treasuryBal.Available {
				receive = treasuryBal.Available
			}
			if receive > 0 {
				p.AssetKeeper.CreditAvailable(pos.AccountId, quoteAsset, receive)
				_ = p.AssetKeeper.DeductAvailable(fee.TreasuryAccountId, quoteAsset, receive)
				totalShortsReceived += receive
			}
		}

		p.EventBus.Publish(state.Event{
			Type:        state.EventFundingSettled,
			BlockHeight: blockHeight,
			Payload: state.FundingSettledPayload{
				MarketId:            marketId,
				RateBps:             rateBps,
				MarkPrice:           markPrice,
				TotalLongsPaid:      totalLongsPaid,
				TotalShortsReceived: totalShortsReceived,
				BlockHeight:         blockHeight,
			},
		})
	}
}

// checkAndLiquidate inspects all open PERP positions for maintenance margin breach
// and liquidates those that are undercollateralized.
func (p *LocalBlockProcessor) checkAndLiquidate(blockHeight int64) {
	if p.PositionTracker == nil {
		return
	}
	positions := p.PositionTracker.AllPositions()
	for _, pos := range positions {
		if pos.NetQuantity == 0 {
			continue
		}
		cfg, isPerp := p.AppState.GetPerpConfig(pos.MarketId)
		if !isPerp {
			continue
		}
		// Skip halted markets.
		if p.AppState.GetMarketStatus(pos.MarketId) != clob.MarketStatusActive {
			continue
		}

		markPrice := p.AppState.GetMarkPrice(pos.MarketId)
		if markPrice == 0 {
			continue
		}

		absQty := pos.NetQuantity
		if absQty < 0 {
			absQty = -absQty
		}

		unrealizedPnL := pos.UnrealizedPnL(markPrice)
		maintenanceMargin := (absQty * pos.AvgEntryPrice / 10_000) * cfg.MaintenanceMarginBps

		// Healthy position: equity > maintenance margin.
		equity := pos.AllocatedMargin + unrealizedPnL
		if equity > maintenanceMargin {
			continue
		}

		quoteAsset := cfg.QuoteAsset

		p.EventBus.Publish(state.Event{
			Type:        state.EventLiquidationTriggered,
			BlockHeight: blockHeight,
			Payload: state.LiquidationTriggeredPayload{
				AccountId:         pos.AccountId,
				MarketId:          pos.MarketId,
				NetQuantity:       pos.NetQuantity,
				MarkPrice:         markPrice,
				MaintenanceMargin: maintenanceMargin,
				BlockHeight:       blockHeight,
			},
		})

		// Release all reserved margin back to Available.
		if pos.AllocatedMargin > 0 {
			_ = p.AssetKeeper.Release(pos.AccountId, quoteAsset, pos.AllocatedMargin)
		}

		if equity >= 0 {
			// Position has remaining equity: deduct the loss (allocatedMargin - equity).
			// Net result: user keeps equity in Available.
			loss := pos.AllocatedMargin - equity
			if loss > 0 {
				_ = p.AssetKeeper.DeductAvailable(pos.AccountId, quoteAsset, loss)
			}
		} else {
			// Bankrupt: loss exceeds allocated margin.
			// Deduct the full margin from Available.
			_ = p.AssetKeeper.DeductAvailable(pos.AccountId, quoteAsset, pos.AllocatedMargin)
			// Additional shortfall = |equity|
			shortfall := -equity
			if p.InsuranceFund != nil {
				drawn := p.InsuranceFund.Drawdown(quoteAsset, shortfall)
				remaining := p.InsuranceFund.Balance(quoteAsset)
				p.EventBus.Publish(state.Event{
					Type:        state.EventInsuranceDrawdown,
					BlockHeight: blockHeight,
					Payload: state.InsuranceDrawdownPayload{
						MarketId:    pos.MarketId,
						Amount:      drawn,
						Remaining:   remaining,
						BlockHeight: blockHeight,
					},
				})
				if drawn < shortfall {
					socializedLoss := shortfall - drawn
					p.EventBus.Publish(state.Event{
						Type:        state.EventSocializedLoss,
						BlockHeight: blockHeight,
						Payload: state.SocializedLossPayload{
							MarketId:    pos.MarketId,
							LossAmount:  socializedLoss,
							BlockHeight: blockHeight,
						},
					})
				}
			}
		}

		// Zero out the position.
		p.PositionTracker.ForceClose(pos.AccountId, pos.MarketId)

		p.EventBus.Publish(state.Event{
			Type:        state.EventLiquidationFilled,
			BlockHeight: blockHeight,
			Payload: state.LiquidationFilledPayload{
				AccountId:   pos.AccountId,
				MarketId:    pos.MarketId,
				FilledQty:   absQty,
				FilledPrice: markPrice,
				PnL:         unrealizedPnL,
				BlockHeight: blockHeight,
			},
		})
	}
}

// processSubmitConditionalOrder stores a new conditional order (stop-loss / take-profit).
func (p *LocalBlockProcessor) processSubmitConditionalOrder(payload fairbatch.SubmitConditionalOrderPayload, blockHeight int64) error {
	o := payload.Order
	if o.OrderId == "" {
		return fmt.Errorf("conditional order: missing OrderId")
	}
	o.CreatedBlockHeight = blockHeight
	o.Status = clob.OrderStatusOpen
	p.AppState.AddConditionalOrder(o)
	p.EventBus.Publish(state.Event{
		Type:        state.EventConditionalOrderSubmitted,
		BlockHeight: blockHeight,
		Payload: state.ConditionalOrderSubmittedPayload{
			OrderId:   o.OrderId,
			AccountId: o.AccountId,
			MarketId:  o.MarketId,
		},
	})
	return nil
}

// processCancelConditionalOrder removes an open conditional order by ID.
func (p *LocalBlockProcessor) processCancelConditionalOrder(payload fairbatch.CancelConditionalOrderPayload, blockHeight int64) error {
	o, ok := p.AppState.GetConditionalOrder(payload.OrderId)
	if !ok {
		return fmt.Errorf("conditional order not found: %s", payload.OrderId)
	}
	if o.AccountId != payload.AccountId {
		return fmt.Errorf("conditional order %s does not belong to account %s", payload.OrderId, payload.AccountId)
	}
	p.AppState.RemoveConditionalOrder(payload.OrderId)
	p.EventBus.Publish(state.Event{
		Type:        state.EventConditionalOrderCancelled,
		BlockHeight: blockHeight,
		Payload: state.ConditionalOrderCancelledPayload{
			OrderId:   payload.OrderId,
			AccountId: payload.AccountId,
		},
	})
	return nil
}

// evaluateConditionalOrders checks each open conditional order against the current
// mark price and fires those whose trigger condition is satisfied.
func (p *LocalBlockProcessor) evaluateConditionalOrders(blockHeight int64) {
	perpMarkets := p.AppState.AllPerpMarkets()
	for marketId := range perpMarkets {
		markPrice := p.AppState.GetMarkPrice(marketId)
		triggered := p.AppState.TriggeredConditionals(marketId, markPrice)
		for _, co := range triggered {
			p.AppState.RemoveConditionalOrder(co.OrderId)

			// Convert the conditional order into a regular order and process it.
			newOrder := clob.Order{
				OrderId:         co.OrderId,
				AccountId:       co.AccountId,
				SessionId:       co.SessionId,
				MarketId:        co.MarketId,
				Side:            co.Side,
				OrderType:       co.OrderType,
				Price:           co.Price,
				Quantity:        co.Quantity,
				RemainingQuantity: co.Quantity,
				TimeInForce:     clob.TimeInForceGtc,
				ReduceOnly:      co.ReduceOnly,
				AccountSequence: co.AccountSequence,
				Signature:       co.Signature,
				Status:          clob.OrderStatusOpen,
			}
			triggerPayload := state.ConditionalOrderTriggeredPayload{
				OrderId:   co.OrderId,
				AccountId: co.AccountId,
				MarketId:  co.MarketId,
				MarkPrice: markPrice,
			}
			if err := p.processOrder(newOrder, co.Signature, blockHeight); err != nil {
				triggerPayload.Rejected = true
				triggerPayload.RejectMsg = err.Error()
			}

			p.EventBus.Publish(state.Event{
				Type:        state.EventConditionalOrderTriggered,
				BlockHeight: blockHeight,
				Payload:     triggerPayload,
			})
		}
	}
}

// expireConditionalOrders removes conditional orders whose ExpireBlockHeight has passed.
func (p *LocalBlockProcessor) expireConditionalOrders(blockHeight int64) {
	expired := p.AppState.ExpiredConditionals(blockHeight)
	for _, co := range expired {
		p.AppState.RemoveConditionalOrder(co.OrderId)
		p.EventBus.Publish(state.Event{
			Type:        state.EventConditionalOrderExpired,
			BlockHeight: blockHeight,
			Payload: state.ConditionalOrderExpiredPayload{
				OrderId:   co.OrderId,
				AccountId: co.AccountId,
				MarketId:  co.MarketId,
			},
		})
	}
}
