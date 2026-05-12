package node

import (
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/risk"
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

type LocalBlockProcessor struct {
	AccountKeeper    *account.AccountKeeper
	AssetKeeper      *asset.AssetKeeper
	OrderBookKeeper  *clob.OrderBookKeeper
	MatchingEngine   clob.MatchingEngine
	SettlementEngine *settlement.SettlementEngine
	SettlementKeeper *settlement.SettlementKeeper
	RiskChecker      *risk.RiskChecker
	EventBus         *state.EventBus
	AppState         *state.AppState
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

	default:
		return fmt.Errorf("unknown transaction type: %d", tx.TxType)
	}
}

func (p *LocalBlockProcessor) processOrder(o clob.Order, txHash string, blockHeight int64) error {
	sess, err := p.AccountKeeper.ValidateSession(o.SessionId, o.MarketId, blockHeight)
	if err != nil {
		p.emitOrderRejected(o, err.Error(), blockHeight)
		return fmt.Errorf("session validation: %w", err)
	}

	// Verify ed25519 signature: the order must be signed by the session key.
	if err := account.VerifyOrderSignature(txHash, o.Signature, sess.SessionPublicKey); err != nil {
		p.emitOrderRejected(o, "invalid signature: "+err.Error(), blockHeight)
		return fmt.Errorf("signature: %w", err)
	}

	if err := p.RiskChecker.CheckOrder(&o, sess, blockHeight); err != nil {
		p.emitOrderRejected(o, err.Error(), blockHeight)
		return fmt.Errorf("risk check: %w", err)
	}

	if err := p.OrderBookKeeper.ReserveForOrder(o); err != nil {
		p.emitOrderRejected(o, err.Error(), blockHeight)
		return fmt.Errorf("reserving collateral: %w", err)
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
