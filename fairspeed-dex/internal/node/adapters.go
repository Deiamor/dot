package node

import (
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// busAdapter wraps *state.EventBus and implements all the keeper-specific publisher interfaces.
type busAdapter struct {
	bus         *state.EventBus
	blockHeight func() int64
}

// --- account.EventPublisher ---

func (a *busAdapter) PublishAccountCreated(accountId, ownerAddress string, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventAccountCreated,
		BlockHeight: blockHeight,
		Payload:     state.AccountCreatedPayload{AccountId: accountId, OwnerAddress: ownerAddress},
	})
}

func (a *busAdapter) PublishSessionCreated(sessionId, accountId string, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventSessionCreated,
		BlockHeight: blockHeight,
		Payload:     state.SessionCreatedPayload{SessionId: sessionId, AccountId: accountId},
	})
}

// --- clob.OrderEventPublisher ---

func (a *busAdapter) PublishOrderSubmitted(orderId, accountId, marketId, status string, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventOrderSubmitted,
		BlockHeight: blockHeight,
		Payload: state.OrderSubmittedPayload{
			OrderId:   orderId,
			AccountId: accountId,
			MarketId:  marketId,
			Status:    status,
		},
	})
}

func (a *busAdapter) PublishOrderCancelled(orderId, accountId string, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventOrderCancelled,
		BlockHeight: blockHeight,
		Payload:     state.OrderCancelledPayload{OrderId: orderId, AccountId: accountId},
	})
}

func (a *busAdapter) PublishOrderExpired(orderId, accountId string, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventOrderExpired,
		BlockHeight: blockHeight,
		Payload:     state.OrderExpiredPayload{OrderId: orderId, AccountId: accountId},
	})
}

// --- settlement.SettlementEventPublisher ---

func (a *busAdapter) PublishTradeExecuted(trade settlement.TradeExecution, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventTradeExecuted,
		BlockHeight: blockHeight,
		Payload: state.TradeExecutedPayload{
			TradeId:        trade.TradeId,
			MarketId:       trade.MarketId,
			MakerOrderId:   trade.MakerOrderId,
			TakerOrderId:   trade.TakerOrderId,
			MakerAccountId: trade.MakerAccountId,
			TakerAccountId: trade.TakerAccountId,
			Price:          trade.Price,
			Quantity:       trade.Quantity,
			MakerFeeAmount: trade.MakerFeeAmount,
			TakerFeeAmount: trade.TakerFeeAmount,
		},
	})
}

func (a *busAdapter) PublishBalanceUpdated(accountId, assetId string, newAvailable, newReserved, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventBalanceUpdated,
		BlockHeight: blockHeight,
		Payload: state.BalanceUpdatedPayload{
			AccountId:    accountId,
			AssetId:      assetId,
			NewAvailable: newAvailable,
			NewReserved:  newReserved,
		},
	})
}

func (a *busAdapter) PublishFeeCharged(accountId, tradeId, assetId string, amount int64, feeType string, blockHeight int64) {
	a.bus.Publish(state.Event{
		Type:        state.EventFeeCharged,
		BlockHeight: blockHeight,
		Payload: state.FeeChargedPayload{
			AccountId: accountId,
			TradeId:   tradeId,
			AssetId:   assetId,
			Amount:    amount,
			FeeType:   feeType,
		},
	})
}
