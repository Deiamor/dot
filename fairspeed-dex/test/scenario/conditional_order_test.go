package scenario_test

// Stage 25: Conditional orders (stop-loss / take-profit).
//
// Verifies:
//  1. TriggerLTE: mark price falls below trigger → automatic market sell
//  2. TriggerGTE: mark price rises above trigger → automatic market sell
//  3. Condition not yet met: order stays open, fires only when condition is satisfied
//  4. Order expires at ExpireBlockHeight without triggering
//  5. Cancel before trigger: TxCancelConditionalOrder removes the order
//  6. ReduceOnly conditional order does not reserve additional collateral

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// bootstrapConditional creates a node with a PERP market and deposits for Alice and Bob.
// Alice holds a long position (1 BTC-USDC-PERP) at price 10_000 opened against Bob's short.
// Returns (n, aliceId, bobId, aliceSessId, bobSessId, nextBlockHeight).
func bootstrapConditional(t *testing.T) (*node.LocalNode, string, string, string, string, int64) {
	t.Helper()
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	// Block 4: register PERP market
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100000,
			MaxFundingRateBps:     100,
		}).Build())
	if err != nil {
		t.Fatalf("register perp: %v", err)
	}

	// Block 5: deposits
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 500_000).
		AddDeposit(bobId, "USDC", 500_000).Build())
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Block 6: sessions
	var aliceSess, bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId {
			aliceSess = p.SessionId
		} else if p.AccountId == bobId {
			bobSess = p.SessionId
		}
	})
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 10_000_000}).
		Build())
	if err != nil {
		t.Fatalf("create sessions: %v", err)
	}

	// Block 7: Bob posts sell; Block 8: Alice buys → opens long/short pair
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell).Build())
	if err != nil {
		t.Fatalf("bob sell: %v", err)
	}

	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy).Build())
	if err != nil {
		t.Fatalf("alice buy: %v", err)
	}

	return n, aliceId, bobId, aliceSess, bobSess, 9
}

// TestConditional_StopLoss_TriggersBelow verifies that a TriggerLTE conditional order fires
// when the mark price falls to or below the trigger price.
func TestConditional_StopLoss_TriggersBelow(t *testing.T) {
	n, aliceId, bobId, aliceSess, _, nextH := bootstrapConditional(t)

	// Alice posts a stop-loss sell: trigger when mark ≤ 9_000
	stopSell := clob.ConditionalOrder{
		OrderId:           "stop-1",
		AccountId:         aliceId,
		SessionId:         aliceSess,
		MarketId:          "BTC-USDC-PERP",
		Side:              clob.OrderSideSell,
		OrderType:         clob.OrderTypeLimit,
		Price:             8_900,
		Quantity:          1,
		TriggerPrice:      9_000,
		TriggerCondition:  clob.TriggerLTE,
		ReduceOnly:        true,
		ExpireBlockHeight: nextH + 100,
		Status:            clob.OrderStatusOpen,
	}
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitConditionalOrder(stopSell).Build())
	if err != nil {
		t.Fatalf("submit conditional: %v", err)
	}
	nextH++

	// Verify it's stored
	co, ok := n.GetConditionalOrder("stop-1")
	if !ok {
		t.Fatal("conditional order not stored")
	}
	if co.TriggerCondition != clob.TriggerLTE {
		t.Errorf("wrong condition: %v", co.TriggerCondition)
	}

	// Set mark price above trigger — should NOT fire
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 9_500).Build())
	nextH++
	if _, ok := n.GetConditionalOrder("stop-1"); !ok {
		t.Fatal("order should still be open when mark > trigger")
	}

	// Bob needs to post a resting buy so Alice's stop-loss can fill
	var bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {}) // already subscribed
	_, bobSess2, err := func() (string, string, error) {
		var bs string
		n.Subscribe(state.EventSessionCreated, func(e state.Event) {
			p := e.Payload.(state.SessionCreatedPayload)
			if p.AccountId == bobId {
				bs = p.SessionId
			}
		})
		_, e := n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{"BTC-USDC-PERP"}, MaxOrderAmount: 10_000_000}).
			Build())
		return "", bs, e
	}()
	_ = bobSess
	nextH++

	// Bob posts a resting buy at 8_900 so Alice's triggered sell can match
	rBuy := clob.NewLimitOrder(bobId, bobSess2, "BTC-USDC-PERP", clob.OrderSideBuy, 8_900, 1, clob.TimeInForceGtc, nextH)
	rBuy.AccountSequence = n.GetAccountSequence(bobId)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitOrder(rBuy).Build())
	if err != nil {
		t.Fatalf("bob resting buy: %v", err)
	}
	nextH++

	// Drop mark price to 9_000 — stop-loss triggers
	var triggeredEvents []state.Event
	n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
		triggeredEvents = append(triggeredEvents, e)
	})

	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 9_000).Build())
	if err != nil {
		t.Fatalf("submit price: %v", err)
	}

	if len(triggeredEvents) == 0 {
		t.Fatal("expected EventConditionalOrderTriggered, got none")
	}
	payload := triggeredEvents[0].Payload.(state.ConditionalOrderTriggeredPayload)
	if payload.OrderId != "stop-1" {
		t.Errorf("wrong OrderId in trigger event: %s", payload.OrderId)
	}
	if payload.MarkPrice != 9_000 {
		t.Errorf("expected mark price 9000, got %d", payload.MarkPrice)
	}

	// Order should be removed from store after triggering
	if _, ok := n.GetConditionalOrder("stop-1"); ok {
		t.Error("conditional order should be removed after triggering")
	}
}

// TestConditional_TakeProfit_TriggersAbove verifies that a TriggerGTE order fires
// when mark price rises to or above the trigger price.
func TestConditional_TakeProfit_TriggersAbove(t *testing.T) {
	n, aliceId, _, aliceSess, _, nextH := bootstrapConditional(t)

	tp := clob.ConditionalOrder{
		OrderId:           "tp-1",
		AccountId:         aliceId,
		SessionId:         aliceSess,
		MarketId:          "BTC-USDC-PERP",
		Side:              clob.OrderSideSell,
		OrderType:         clob.OrderTypeLimit,
		Price:             11_100,
		Quantity:          1,
		TriggerPrice:      11_000,
		TriggerCondition:  clob.TriggerGTE,
		ReduceOnly:        true,
		ExpireBlockHeight: nextH + 100,
		Status:            clob.OrderStatusOpen,
	}
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(tp).Build())
	if err != nil {
		t.Fatalf("submit tp: %v", err)
	}
	nextH++

	// Mark below trigger — no fire
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 10_500).Build())
	nextH++
	if _, ok := n.GetConditionalOrder("tp-1"); !ok {
		t.Fatal("order removed prematurely")
	}

	// Mark at trigger
	var triggered []state.Event
	n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
		triggered = append(triggered, e)
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 11_000).Build())

	if len(triggered) == 0 {
		t.Fatal("take-profit did not trigger at GTE condition")
	}
	p := triggered[0].Payload.(state.ConditionalOrderTriggeredPayload)
	if p.OrderId != "tp-1" {
		t.Errorf("wrong OrderId: %s", p.OrderId)
	}
}

// TestConditional_NoTriggerUntilCondition verifies the order stays open for multiple
// blocks until the exact condition is satisfied.
func TestConditional_NoTriggerUntilCondition(t *testing.T) {
	n, aliceId, _, aliceSess, _, nextH := bootstrapConditional(t)

	co := clob.ConditionalOrder{
		OrderId:           "sl-wait",
		AccountId:         aliceId,
		SessionId:         aliceSess,
		MarketId:          "BTC-USDC-PERP",
		Side:              clob.OrderSideSell,
		OrderType:         clob.OrderTypeLimit,
		Price:             8_000,
		Quantity:          1,
		TriggerPrice:      8_500,
		TriggerCondition:  clob.TriggerLTE,
		ReduceOnly:        true,
		ExpireBlockHeight: nextH + 200,
	}
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
	nextH++

	// Submit prices above trigger for 3 blocks
	for i := 0; i < 3; i++ {
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
			AddSubmitPrice("BTC-USDC-PERP", "val1", 9_000).Build())
		nextH++
		if _, ok := n.GetConditionalOrder("sl-wait"); !ok {
			t.Fatalf("order removed prematurely at block %d", nextH-1)
		}
	}

	// Now drop to trigger
	var fired []state.Event
	n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
		fired = append(fired, e)
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 8_500).Build())

	if len(fired) == 0 {
		t.Fatal("order should have triggered when mark reached trigger price")
	}
}

// TestConditional_ExpiresAtBlockHeight verifies EventConditionalOrderExpired fires
// when ExpireBlockHeight is reached without triggering.
func TestConditional_ExpiresAtBlockHeight(t *testing.T) {
	n, aliceId, _, aliceSess, _, nextH := bootstrapConditional(t)

	expireAt := nextH + 3
	co := clob.ConditionalOrder{
		OrderId:           "expire-me",
		AccountId:         aliceId,
		SessionId:         aliceSess,
		MarketId:          "BTC-USDC-PERP",
		Side:              clob.OrderSideSell,
		OrderType:         clob.OrderTypeLimit,
		Price:             5_000,
		Quantity:          1,
		TriggerPrice:      5_000,
		TriggerCondition:  clob.TriggerLTE, // trigger far below; will never fire
		ReduceOnly:        true,
		ExpireBlockHeight: expireAt,
	}
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
	nextH++

	// Keep mark high so trigger never fires
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 10_000).Build())
	nextH++
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 10_000).Build())
	nextH++

	var expired []state.Event
	n.Subscribe(state.EventConditionalOrderExpired, func(e state.Event) {
		expired = append(expired, e)
	})

	// This block equals expireAt
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 10_000).Build())

	if len(expired) == 0 {
		t.Fatal("expected EventConditionalOrderExpired, got none")
	}
	p := expired[0].Payload.(state.ConditionalOrderExpiredPayload)
	if p.OrderId != "expire-me" {
		t.Errorf("wrong order in expiry event: %s", p.OrderId)
	}

	if _, ok := n.GetConditionalOrder("expire-me"); ok {
		t.Error("expired order should be removed from store")
	}
}

// TestConditional_CancelBeforeTrigger verifies that a conditional order can be cancelled
// before its trigger condition is met.
func TestConditional_CancelBeforeTrigger(t *testing.T) {
	n, aliceId, _, aliceSess, _, nextH := bootstrapConditional(t)

	co := clob.ConditionalOrder{
		OrderId:           "cancel-me",
		AccountId:         aliceId,
		SessionId:         aliceSess,
		MarketId:          "BTC-USDC-PERP",
		Side:              clob.OrderSideSell,
		OrderType:         clob.OrderTypeLimit,
		Price:             8_000,
		Quantity:          1,
		TriggerPrice:      8_000,
		TriggerCondition:  clob.TriggerLTE,
		ReduceOnly:        true,
		ExpireBlockHeight: nextH + 100,
	}
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).AddSubmitConditionalOrder(co).Build())
	nextH++

	if _, ok := n.GetConditionalOrder("cancel-me"); !ok {
		t.Fatal("order should be stored before cancel")
	}

	var cancelEvents []state.Event
	n.Subscribe(state.EventConditionalOrderCancelled, func(e state.Event) {
		cancelEvents = append(cancelEvents, e)
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddCancelConditionalOrder("cancel-me", aliceId).Build())
	if err != nil {
		t.Fatalf("cancel conditional: %v", err)
	}
	nextH++

	if len(cancelEvents) == 0 {
		t.Fatal("expected EventConditionalOrderCancelled")
	}
	p := cancelEvents[0].Payload.(state.ConditionalOrderCancelledPayload)
	if p.OrderId != "cancel-me" {
		t.Errorf("wrong OrderId in cancel event: %s", p.OrderId)
	}

	if _, ok := n.GetConditionalOrder("cancel-me"); ok {
		t.Error("cancelled order should be removed from store")
	}

	// Even if mark drops to trigger level, no event should fire
	var triggered []state.Event
	n.Subscribe(state.EventConditionalOrderTriggered, func(e state.Event) {
		triggered = append(triggered, e)
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice("BTC-USDC-PERP", "val1", 8_000).Build())

	if len(triggered) > 0 {
		t.Error("cancelled order should not trigger")
	}
}

// TestConditional_ReduceOnlyEnforced verifies that a ReduceOnly conditional order
// is accepted and stored without reserving additional margin.
func TestConditional_ReduceOnlyEnforced(t *testing.T) {
	n, aliceId, _, aliceSess, _, nextH := bootstrapConditional(t)

	balanceBefore := n.GetBalance(aliceId, "USDC")

	co := clob.ConditionalOrder{
		OrderId:           "ro-cond",
		AccountId:         aliceId,
		SessionId:         aliceSess,
		MarketId:          "BTC-USDC-PERP",
		Side:              clob.OrderSideSell,
		OrderType:         clob.OrderTypeLimit,
		Price:             9_000,
		Quantity:          1,
		TriggerPrice:      9_000,
		TriggerCondition:  clob.TriggerLTE,
		ReduceOnly:        true,
		ExpireBlockHeight: nextH + 100,
	}
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitConditionalOrder(co).Build())
	if err != nil {
		t.Fatalf("submit reduce-only conditional: %v", err)
	}

	balanceAfter := n.GetBalance(aliceId, "USDC")

	// Conditional order submission alone must not lock any margin
	if balanceBefore.Reserved != balanceAfter.Reserved {
		t.Errorf("ReduceOnly conditional order must not reserve margin: before=%d after=%d",
			balanceBefore.Reserved, balanceAfter.Reserved)
	}

	// Order should be stored correctly
	stored, ok := n.GetConditionalOrder("ro-cond")
	if !ok {
		t.Fatal("reduce-only conditional order not stored")
	}
	if !stored.ReduceOnly {
		t.Error("ReduceOnly flag not persisted")
	}
}
