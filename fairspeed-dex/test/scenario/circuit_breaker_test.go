package scenario_test

// Stage 15: Circuit breaker / market halt tests.
//
// Verifies:
//  1. Orders are rejected when market is HALTED
//  2. Orders succeed when market is ACTIVE (default)
//  3. Cancel orders are allowed even when market is halted
//  4. Market resumes: orders accepted after TxResumeMarket
//  5. Halt event carries marketId and reason
//  6. Halt status persists through snapshot round-trip

import (
	"os"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Scenario 1: Order rejected when market is HALTED
// -------------------------------------------------------------------------
func TestCircuitBreaker_OrderRejectedWhenHalted(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	// Halt the market.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddHaltMarket("BTC-USDC", "flash crash detected").Build())
	if err != nil {
		t.Fatalf("halt: %v", err)
	}

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(order).Build())

	if rejectedId == "" {
		t.Error("expected ORDER_REJECTED when market is halted")
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Orders succeed when market is ACTIVE (baseline / no halt)
// -------------------------------------------------------------------------
func TestCircuitBreaker_OrderAcceptedWhenActive(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build())
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rejectedId != "" {
		t.Errorf("order should not be rejected when market is active: %s", rejectedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Cancel orders are allowed even when market is halted
// -------------------------------------------------------------------------
func TestCircuitBreaker_CancelAllowedWhenHalted(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	// Place a GTC resting order while market is active.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 9_000, 1, clob.TimeInForceGtc, 4)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build())

	// Halt the market.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddHaltMarket("BTC-USDC", "emergency").Build())

	// Cancel should still succeed (always allowed regardless of market status).
	var cancelledId string
	n.Subscribe(state.EventOrderCancelled, func(e state.Event) {
		cancelledId = e.Payload.(state.OrderCancelledPayload).OrderId
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCancelOrder(order.OrderId, aliceId).Build())
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelledId == "" {
		t.Error("expected cancel to succeed even when market is halted")
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Orders accepted after market resumes
// -------------------------------------------------------------------------
func TestCircuitBreaker_OrderAcceptedAfterResume(t *testing.T) {
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddHaltMarket("BTC-USDC", "test halt").Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddResumeMarket("BTC-USDC").Build())

	var rejectedId string
	n.Subscribe(state.EventOrderRejected, func(e state.Event) {
		rejectedId = e.Payload.(state.OrderRejectedPayload).OrderId
	})

	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	order.AccountSequence = n.GetAccountSequence(aliceId)
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(order).Build())
	if err != nil {
		t.Fatalf("submit after resume: %v", err)
	}
	if rejectedId != "" {
		t.Errorf("order should not be rejected after market resumes: %s", rejectedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: EventMarketHalted carries correct marketId and reason
// -------------------------------------------------------------------------
func TestCircuitBreaker_HaltEventPayload(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	var haltedPayload state.MarketHaltedPayload
	n.Subscribe(state.EventMarketHalted, func(e state.Event) {
		haltedPayload = e.Payload.(state.MarketHaltedPayload)
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddHaltMarket("BTC-USDC", "volatility spike").Build())
	if err != nil {
		t.Fatalf("halt: %v", err)
	}

	if haltedPayload.MarketId != "BTC-USDC" {
		t.Errorf("marketId: want BTC-USDC got %s", haltedPayload.MarketId)
	}
	if haltedPayload.Reason != "volatility spike" {
		t.Errorf("reason: want 'volatility spike' got %s", haltedPayload.Reason)
	}
	if haltedPayload.BlockHeight == 0 {
		t.Error("blockHeight should be populated in MarketHaltedPayload")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Halt status persists through snapshot round-trip
// -------------------------------------------------------------------------
func TestCircuitBreaker_HaltPersistedInSnapshot(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddHaltMarket("BTC-USDC", "system maintenance").Build())
	if err != nil {
		t.Fatalf("halt: %v", err)
	}

	if n.GetMarketStatus("BTC-USDC") != clob.MarketStatusHalted {
		t.Fatal("expected HALTED before snapshot")
	}

	path := t.TempDir() + "/halt_snap.json"
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	if n2.GetMarketStatus("BTC-USDC") != clob.MarketStatusHalted {
		t.Error("market halt status not persisted through snapshot")
	}

	// Cleanup
	os.Remove(path)
}
