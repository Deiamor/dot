package node

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/governance"
	"github.com/byunghee1994/fairspeed-dex/internal/oracle"
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
	EventBus           *state.EventBus
	AppState           *state.AppState
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
		_, err := p.AccountKeeper.CreateAccount(
			payload.OwnerAddress,
			payload.RootPublicKey,
			payload.WithdrawalPublicKey,
			blockHeight,
			tx.TxHash,
		)
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

	if err := p.OrderBookKeeper.ReserveForOrder(o); err != nil {
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
		}
	}

	if o.RemainingQuantity > 0 && o.TimeInForce == clob.TimeInForceGtc && o.Status != clob.OrderStatusRejected {
		p.AppState.SetOrder(&o)
		ob.AddOrder(p.getOrStoreOrder(&o))
		p.AppState.SetOrderBook(ob)
		p.emitOrderSubmitted(o, blockHeight)
	} else {
		if o.RemainingQuantity > 0 && o.Status != clob.OrderStatusRejected {
			_ = p.OrderBookKeeper.ReleaseForOrder(o, o.RemainingQuantity)
		} else if o.Status == clob.OrderStatusRejected {
			_ = p.OrderBookKeeper.ReleaseForOrder(o, o.Quantity)
		}
		p.AppState.SetOrder(&o)
		if o.Status == clob.OrderStatusRejected {
			p.emitOrderRejected(o, "FOK not fillable", blockHeight)
		} else {
			p.emitOrderSubmitted(o, blockHeight)
		}
	}

	return nil
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
