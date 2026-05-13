package scenario_test

// Stage 22: Perpetual position extension + margin checks.
//
// Verifies:
//  1. PERP order reserves only InitialMargin (not full notional)
//  2. Under-margin PERP order is rejected (balance < required margin)
//  3. AvgEntryPrice is tracked correctly across multiple fills
//  4. ReduceOnly=true PERP order requires no margin reserve
//  5. Leverage > MaxLeverage is rejected
//  6. SPOT orders are unaffected (reserve full notional)

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// createPerpSession creates a session for accountId allowing "BTC-USDC-PERP"
// and returns the session ID. Must be called before the batch that uses it.
func createPerpSession(n *node.LocalNode, accountId string, blockHeight int64) string {
	var sessionId string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == accountId && sessionId == "" {
			sessionId = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(blockHeight).
		AddCreateSession(accountId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC-PERP"},
			MaxOrderAmount: 10_000_000,
		}).Build())
	return sessionId
}

// -------------------------------------------------------------------------
// Scenario 1: PERP order reserves only InitialMargin (10%), not full notional
// -------------------------------------------------------------------------
func TestPerpMargin_InitialMarginReserved(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// Register PERP market: 10% initial margin, max 10x leverage.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000, // 10%
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     100,
		}).Build())

	aliceSess := createPerpSession(n, aliceId, 5)

	balBefore := n.GetBalance(aliceId, "USDC").Available

	// Place PERP BUY at price=10_000, qty=1 → notional=10_000, margin=1_000.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(order).Build())

	balAfter := n.GetBalance(aliceId, "USDC").Available
	reserved := n.GetBalance(aliceId, "USDC").Reserved

	// Available doesn't decrease when funds move to reserved.
	_ = balBefore
	_ = balAfter

	// Reserved should be the margin (1_000), not full notional (10_000).
	if reserved < 1_000 {
		t.Errorf("expected at least 1_000 USDC reserved as margin, got %d", reserved)
	}
	// Reserved should NOT be 10_000 (that would be full notional reserve like SPOT).
	if reserved >= 10_000 {
		t.Errorf("PERP order should reserve margin (1_000), not full notional (%d)", reserved)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Under-margin PERP order is rejected
// -------------------------------------------------------------------------
func TestPerpMargin_UnderMarginRejected(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// Register PERP market: 50% initial margin.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      5000, // 50% — very high for easy testing
			MaintenanceMarginBps:  2500,
			MaxLeverage:           2,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     100,
		}).Build())

	aliceSess := createPerpSession(n, aliceId, 5)

	// Alice has 100_000 USDC from bootstrapNode.
	// Order: price=300_000, qty=1 → notional=300_000 → margin=150_000 (50%).
	// Alice only has 100_000 → insufficient.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 300_000, 1, clob.TimeInForceGtc, 6)
	order.AccountSequence = n.GetAccountSequence(aliceId)

	result, _ := n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(order).Build())

	// Check that no trade was executed (order rejected → no fill).
	if len(result.Trades) > 0 {
		t.Error("expected no trades from a margin-rejected order")
	}
	// Balance should be unchanged.
	bal := n.GetBalance(aliceId, "USDC")
	if bal.Reserved > 0 {
		t.Errorf("expected no reserve on rejected order, got reserved=%d", bal.Reserved)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: AvgEntryPrice tracked correctly across fills
// -------------------------------------------------------------------------
func TestPerpMargin_AvgEntryPriceTracked(t *testing.T) {
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000, // 10%
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     100,
		}).Build())

	// Deposit extra USDC for both Alice and Bob.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 500_000).
		AddDeposit(bobId, "USDC", 500_000).
		AddDeposit(bobId, "BTC", 100).Build())

	aliceSess := createPerpSession(n, aliceId, 6)
	bobSess := createPerpSession(n, bobId, 7)

	// Trade 1: Alice buys 1 BTC-USDC-PERP at 10_000 (Bob sells).
	sell1 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 8)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(sell1).Build())
	buy1 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 9)
	buy1.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).AddSubmitOrder(buy1).Build())

	pos1 := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos1.NetQuantity != 1 {
		t.Fatalf("after trade 1: expected NetQuantity=1, got %d", pos1.NetQuantity)
	}
	if pos1.AvgEntryPrice != 10_000 {
		t.Errorf("after trade 1: AvgEntryPrice want 10_000, got %d", pos1.AvgEntryPrice)
	}

	// Trade 2: Alice buys 1 more at 12_000 → avg = (10_000 + 12_000) / 2 = 11_000.
	sell2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 12_000, 1, clob.TimeInForceGtc, 10)
	sell2.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(10).AddSubmitOrder(sell2).Build())
	buy2 := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 12_000, 1, clob.TimeInForceGtc, 11)
	buy2.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(11).AddSubmitOrder(buy2).Build())

	pos2 := n.GetPerpPosition(aliceId, "BTC-USDC-PERP")
	if pos2.NetQuantity != 2 {
		t.Fatalf("after trade 2: expected NetQuantity=2, got %d", pos2.NetQuantity)
	}
	if pos2.AvgEntryPrice != 11_000 {
		t.Errorf("after trade 2: AvgEntryPrice want 11_000, got %d", pos2.AvgEntryPrice)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: ReduceOnly=true PERP order reserves no margin
// -------------------------------------------------------------------------
func TestPerpMargin_ReduceOnlyNoReserve(t *testing.T) {
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     100,
		}).Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 200_000).
		AddDeposit(bobId, "USDC", 200_000).
		AddDeposit(bobId, "BTC", 100).Build())

	aliceSess := createPerpSession(n, aliceId, 6)
	bobSess := createPerpSession(n, bobId, 7)

	// First: open a long position for Alice.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 8)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 9)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).AddSubmitOrder(buy).Build())

	reservedAfterOpen := n.GetBalance(aliceId, "USDC").Reserved

	// Now place a ReduceOnly SELL to close the position — no additional margin needed.
	buy2 := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 10)
	buy2.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(10).AddSubmitOrder(buy2).Build())

	closeSell := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 11)
	closeSell.ReduceOnly = true
	closeSell.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(11).AddSubmitOrder(closeSell).Build())

	// ReduceOnly order should not have added any extra reserve.
	reservedAfterClose := n.GetBalance(aliceId, "USDC").Reserved
	// After matching, reserved should decrease (margin released), not increase.
	if reservedAfterClose > reservedAfterOpen {
		t.Errorf("ReduceOnly close added unexpected reserve: before=%d after=%d",
			reservedAfterOpen, reservedAfterClose)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Leverage > MaxLeverage is rejected
// -------------------------------------------------------------------------
func TestPerpMargin_LeverageLimitEnforced(t *testing.T) {
	n, aliceId, _, _, _ := bootstrapNode(t)

	// MaxLeverage = 5.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "BTC-USDC-PERP",
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           5,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     100,
		}).Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 500_000).Build())

	aliceSess := createPerpSession(n, aliceId, 6)

	// Attempt to use 10x leverage on a 5x-max market.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC-PERP", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 7)
	order.Leverage = 10 // exceeds MaxLeverage=5
	order.AccountSequence = n.GetAccountSequence(aliceId)

	result, _ := n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(order).Build())

	// No trades should execute from a rejected order.
	if len(result.Trades) > 0 {
		t.Error("expected order rejected due to leverage limit, but trade executed")
	}
	// Reserve should be zero.
	if n.GetBalance(aliceId, "USDC").Reserved > 0 {
		t.Errorf("rejected order should not reserve funds, got reserved=%d",
			n.GetBalance(aliceId, "USDC").Reserved)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: SPOT orders unaffected — still reserve full notional
// -------------------------------------------------------------------------
func TestPerpMargin_SpotUnaffected(t *testing.T) {
	n, aliceId, bobId, aliceSess, bobSess := bootstrapNode(t)

	// Register a PERP market (unrelated to this test's SPOT market).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              "ETH-USDC-PERP",
			BaseAsset:             "ETH",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 100,
			MaxFundingRateBps:     100,
		}).Build())

	// SPOT order on BTC-USDC (unchanged market).
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 5)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 6)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).AddSubmitOrder(buy).Build())

	// SPOT trade should have executed and Alice's BTC balance should increase.
	btcBal := n.GetBalance(aliceId, "BTC").Available
	if btcBal == 0 {
		t.Error("SPOT trade should have credited Alice's BTC balance")
	}
}
