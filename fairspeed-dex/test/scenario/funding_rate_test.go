package scenario_test

// Stage 23: Funding rate module.
//
// Verifies:
//  1. Positive funding rate: longs pay shorts when markPrice > indexPrice
//  2. Negative funding rate: shorts pay longs when markPrice < indexPrice
//  3. Zero rate: no transfers when markPrice == indexPrice
//  4. Rate clamp: premium 10%, maxRateBps=100 → effective rate = 100 bps
//  5. Interval respected: no settlement before FundingIntervalBlocks, settles at interval
//  6. Multi-account settlement: total longs paid == total shorts received

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/funding"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

const perpMarketId = "BTC-USDC-PERP"

// bootstrapFunding sets up a node with a PERP market and two traders with open positions.
// Alice is net long, Bob is net short. Returns (n, aliceId, bobId, aliceSess, bobSess, nextHeight).
func bootstrapFunding(t *testing.T, fundingInterval int64) (*node.LocalNode, string, string, string, string, int64) {
	t.Helper()
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	// Block 4: Register PERP market.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              perpMarketId,
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000, // 10%
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: fundingInterval,
			MaxFundingRateBps:     200, // 2% cap
		}).Build())

	// Block 5: Extra deposits.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 500_000).
		AddDeposit(bobId, "USDC", 500_000).Build())

	// Blocks 6-7: Create PERP sessions.
	var aliceSess, bobSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		if p.AccountId == aliceId && aliceSess == "" {
			aliceSess = p.SessionId
		} else if p.AccountId == bobId && bobSess == "" {
			bobSess = p.SessionId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{perpMarketId}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{perpMarketId}, MaxOrderAmount: 10_000_000}).
		Build())

	// Blocks 7-8: Open positions — Alice long 1, Bob short 1.
	sell := clob.NewLimitOrder(bobId, bobSess, perpMarketId, clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell).Build())
	buy := clob.NewLimitOrder(aliceId, aliceSess, perpMarketId, clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buy.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy).Build())

	return n, aliceId, bobId, aliceSess, bobSess, 9
}

// -------------------------------------------------------------------------
// Scenario 1: Positive rate — longs pay, shorts receive
// -------------------------------------------------------------------------
func TestFunding_PositiveRateLongPays(t *testing.T) {
	// Unit test of funding.CalcFundingRate and FundingPayment.
	markPrice := int64(10_500)
	indexPrice := int64(10_000)
	maxBps := int64(200)

	rate := funding.CalcFundingRate(markPrice, indexPrice, maxBps)
	// (10_500 - 10_000) * 10_000 / 10_000 = 500 bps, but clamped to 200.
	if rate != 200 {
		t.Errorf("expected rate 200, got %d", rate)
	}

	// Long position (netQty=1, avgEntry=10_000, rate=50 bps).
	payment := funding.FundingPayment(1, 10_000, 50)
	// 1 * 10_000 * 50 / 10_000 = 50 (long pays)
	if payment != 50 {
		t.Errorf("expected long payment 50, got %d", payment)
	}

	// Integration: set index below mark, confirm longs pay after interval.
	n, aliceId, bobId, _, _, nextH := bootstrapFunding(t, 2)
	aliceBalBefore := n.GetBalance(aliceId, "USDC").Available
	bobBalBefore := n.GetBalance(bobId, "USDC").Available

	// markPrice=10_500, indexPrice=10_000 → rate=(500/10000)*10000=500 bps, clamped to 200.
	n.SetIndexPrice(perpMarketId, 10_000)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice(perpMarketId, "val1", 10_500).Build())
	nextH++

	// Advance one block to trigger funding (interval=2; blocks 9 and 10 → settles at 10).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
	nextH++

	aliceBalAfter := n.GetBalance(aliceId, "USDC").Available
	bobBalAfter := n.GetBalance(bobId, "USDC").Available

	epochs := n.GetFundingHistory(perpMarketId)
	if len(epochs) == 0 {
		t.Fatal("expected at least one funding epoch")
	}
	epoch := epochs[len(epochs)-1]
	if epoch.RateBps <= 0 {
		t.Errorf("expected positive rate, got %d", epoch.RateBps)
	}

	// Alice (long) should have paid, Bob (short) should have received.
	if aliceBalAfter >= aliceBalBefore {
		t.Errorf("long (Alice) should have paid funding: before=%d after=%d", aliceBalBefore, aliceBalAfter)
	}
	if bobBalAfter <= bobBalBefore {
		t.Errorf("short (Bob) should have received funding: before=%d after=%d", bobBalBefore, bobBalAfter)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Negative rate — shorts pay, longs receive
// -------------------------------------------------------------------------
func TestFunding_NegativeRateShortPays(t *testing.T) {
	// markPrice < indexPrice → negative rate → shorts pay.
	markPrice := int64(9_500)
	indexPrice := int64(10_000)
	maxBps := int64(200)

	rate := funding.CalcFundingRate(markPrice, indexPrice, maxBps)
	// (9_500 - 10_000) * 10_000 / 10_000 = -500 bps, clamped to -200.
	if rate != -200 {
		t.Errorf("expected rate -200, got %d", rate)
	}

	// Short position (netQty=-1, avgEntry=10_000, rate=-50 bps).
	payment := funding.FundingPayment(-1, 10_000, -50)
	// -1 * 10_000 * -50 / 10_000 = 50 (positive → short pays)
	if payment != 50 {
		t.Errorf("expected short payment 50, got %d", payment)
	}

	// Integration: markPrice below indexPrice → Bob (short) pays.
	n, aliceId, bobId, _, _, nextH := bootstrapFunding(t, 2)
	aliceBalBefore := n.GetBalance(aliceId, "USDC").Available
	bobBalBefore := n.GetBalance(bobId, "USDC").Available

	n.SetIndexPrice(perpMarketId, 10_500)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice(perpMarketId, "val1", 10_000).Build())
	nextH++
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
	nextH++

	epochs := n.GetFundingHistory(perpMarketId)
	if len(epochs) == 0 {
		t.Fatal("expected at least one funding epoch")
	}
	epoch := epochs[len(epochs)-1]
	if epoch.RateBps >= 0 {
		t.Errorf("expected negative rate, got %d", epoch.RateBps)
	}

	aliceBalAfter := n.GetBalance(aliceId, "USDC").Available
	bobBalAfter := n.GetBalance(bobId, "USDC").Available

	// Bob (short) should pay, Alice (long) should receive.
	if bobBalAfter >= bobBalBefore {
		t.Errorf("short (Bob) should have paid funding: before=%d after=%d", bobBalBefore, bobBalAfter)
	}
	if aliceBalAfter <= aliceBalBefore {
		t.Errorf("long (Alice) should have received funding: before=%d after=%d", aliceBalBefore, aliceBalAfter)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Zero rate — no transfers when markPrice == indexPrice
// -------------------------------------------------------------------------
func TestFunding_ZeroRate(t *testing.T) {
	rate := funding.CalcFundingRate(10_000, 10_000, 200)
	if rate != 0 {
		t.Errorf("expected rate 0 when mark==index, got %d", rate)
	}

	n, aliceId, bobId, _, _, nextH := bootstrapFunding(t, 2)
	aliceBalBefore := n.GetBalance(aliceId, "USDC").Available
	bobBalBefore := n.GetBalance(bobId, "USDC").Available

	// Both mark and index at same price → rate = 0.
	n.SetIndexPrice(perpMarketId, 10_000)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice(perpMarketId, "val1", 10_000).Build())
	nextH++
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
	nextH++

	epochs := n.GetFundingHistory(perpMarketId)
	if len(epochs) == 0 {
		t.Fatal("expected funding epoch to be recorded even with zero rate")
	}
	if epochs[len(epochs)-1].RateBps != 0 {
		t.Errorf("expected rateBps=0, got %d", epochs[len(epochs)-1].RateBps)
	}

	// Balances should be unchanged.
	if n.GetBalance(aliceId, "USDC").Available != aliceBalBefore {
		t.Errorf("Alice balance changed on zero-rate funding")
	}
	if n.GetBalance(bobId, "USDC").Available != bobBalBefore {
		t.Errorf("Bob balance changed on zero-rate funding")
	}
}

// -------------------------------------------------------------------------
// Scenario 4: Rate clamp — premium 10%, maxRateBps=100 → effective rate=100
// -------------------------------------------------------------------------
func TestFunding_ClampEnforced(t *testing.T) {
	// Premium 10%: markPrice=11_000, indexPrice=10_000 → raw=1000 bps.
	// maxRateBps=100 → clamped to 100.
	rate := funding.CalcFundingRate(11_000, 10_000, 100)
	if rate != 100 {
		t.Errorf("expected clamped rate 100, got %d", rate)
	}

	// Same on the negative side.
	rateNeg := funding.CalcFundingRate(9_000, 10_000, 100)
	if rateNeg != -100 {
		t.Errorf("expected clamped rate -100, got %d", rateNeg)
	}

	// Zero indexPrice → 0.
	rateZero := funding.CalcFundingRate(10_000, 0, 100)
	if rateZero != 0 {
		t.Errorf("expected rate 0 for zero indexPrice, got %d", rateZero)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Interval respected — no settlement before interval, settles at interval
// -------------------------------------------------------------------------
func TestFunding_IntervalRespected(t *testing.T) {
	// interval = 5 blocks; registration at block 4 → lastFundingBlock=4.
	// bootstrapFunding processes blocks 4-8 (5 blocks, no settlement since 8-4=4 < 5).
	// nextH=9: block 9 triggers first settlement (9-4=5 >= 5).
	n, _, _, _, _, nextH := bootstrapFunding(t, 5)

	n.SetIndexPrice(perpMarketId, 9_000)
	// No settlement should have occurred during bootstrap (8-4=4 < 5).
	if len(n.GetFundingHistory(perpMarketId)) != 0 {
		t.Fatalf("unexpected funding settlement during bootstrap: got %d epochs",
			len(n.GetFundingHistory(perpMarketId)))
	}

	// Block 9: 9-4=5 >= 5 → first settlement.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).
		AddSubmitPrice(perpMarketId, "val1", 10_000).Build())
	nextH++

	if len(n.GetFundingHistory(perpMarketId)) != 1 {
		t.Fatalf("expected 1 epoch after first interval (block %d), got %d", nextH-1,
			len(n.GetFundingHistory(perpMarketId)))
	}
	lastH := n.GetLastFundingBlock(perpMarketId)

	// Advance 4 more blocks (10-13): lastFundingBlock+4 < lastFundingBlock+5 → no new settlement.
	for i := 0; i < 4; i++ {
		_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
		nextH++
	}
	if len(n.GetFundingHistory(perpMarketId)) != 1 {
		t.Errorf("expected still 1 epoch before second interval, got %d",
			len(n.GetFundingHistory(perpMarketId)))
	}

	// Block 14: lastH+5=14 → second settlement.
	_ = lastH
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(nextH).Build())
	nextH++

	if len(n.GetFundingHistory(perpMarketId)) != 2 {
		t.Errorf("expected 2 epochs after second interval (block %d), got %d", nextH-1,
			len(n.GetFundingHistory(perpMarketId)))
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Multi-account settlement — 3 longs total payment == 2 shorts total receipt
// -------------------------------------------------------------------------
func TestFunding_MultiAccountSettlement(t *testing.T) {
	n, aliceId, bobId, _, _ := bootstrapNode(t)

	// Also create a third account (Charlie).
	var charlieId string
	n.Subscribe(state.EventAccountCreated, func(e state.Event) {
		p := e.Payload.(state.AccountCreatedPayload)
		if p.OwnerAddress == "charlie" {
			charlieId = p.AccountId
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddCreateAccount("charlie", "charlie-root", "charlie-withdraw").
		AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
			MarketId:              perpMarketId,
			BaseAsset:             "BTC",
			QuoteAsset:            "USDC",
			InitialMarginBps:      1000,
			MaintenanceMarginBps:  500,
			MaxLeverage:           10,
			FundingIntervalBlocks: 3,
			MaxFundingRateBps:     500,
		}).Build())

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddDeposit(aliceId, "USDC", 500_000).
		AddDeposit(bobId, "USDC", 500_000).
		AddDeposit(charlieId, "USDC", 500_000).Build())

	// Create PERP sessions for all three.
	var aliceSess, bobSess, charlieSess string
	n.Subscribe(state.EventSessionCreated, func(e state.Event) {
		p := e.Payload.(state.SessionCreatedPayload)
		switch p.AccountId {
		case aliceId:
			if aliceSess == "" {
				aliceSess = p.SessionId
			}
		case bobId:
			if bobSess == "" {
				bobSess = p.SessionId
			}
		case charlieId:
			if charlieSess == "" {
				charlieSess = p.SessionId
			}
		}
	})
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddCreateSession(aliceId, account.SessionOptions{AllowedMarkets: []string{perpMarketId}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(bobId, account.SessionOptions{AllowedMarkets: []string{perpMarketId}, MaxOrderAmount: 10_000_000}).
		AddCreateSession(charlieId, account.SessionOptions{AllowedMarkets: []string{perpMarketId}, MaxOrderAmount: 10_000_000}).
		Build())

	// Open positions: Alice+Charlie long (1 each), Bob short (2).
	// Two separate trades: Alice buys from Bob, Charlie buys from Bob.
	sell1 := clob.NewLimitOrder(bobId, bobSess, perpMarketId, clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 7)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(7).AddSubmitOrder(sell1).Build())
	buy1 := clob.NewLimitOrder(aliceId, aliceSess, perpMarketId, clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 8)
	buy1.AccountSequence = n.GetAccountSequence(aliceId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(8).AddSubmitOrder(buy1).Build())

	sell2 := clob.NewLimitOrder(bobId, bobSess, perpMarketId, clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 9)
	sell2.AccountSequence = n.GetAccountSequence(bobId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(9).AddSubmitOrder(sell2).Build())
	buy2 := clob.NewLimitOrder(charlieId, charlieSess, perpMarketId, clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 10)
	buy2.AccountSequence = n.GetAccountSequence(charlieId)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(10).AddSubmitOrder(buy2).Build())

	// Verify positions.
	alicePos := n.GetPerpPosition(aliceId, perpMarketId)
	bobPos := n.GetPerpPosition(bobId, perpMarketId)
	charliePos := n.GetPerpPosition(charlieId, perpMarketId)
	if alicePos.NetQuantity != 1 {
		t.Fatalf("expected Alice NetQuantity=1, got %d", alicePos.NetQuantity)
	}
	if bobPos.NetQuantity != -2 {
		t.Fatalf("expected Bob NetQuantity=-2, got %d", bobPos.NetQuantity)
	}
	if charliePos.NetQuantity != 1 {
		t.Fatalf("expected Charlie NetQuantity=1, got %d", charliePos.NetQuantity)
	}

	// Snapshot balances before funding.
	aliceBefore := n.GetBalance(aliceId, "USDC").Available
	bobBefore := n.GetBalance(bobId, "USDC").Available
	charlieBefore := n.GetBalance(charlieId, "USDC").Available

	// Set mark>index → positive rate → longs (Alice, Charlie) pay, short (Bob) receives.
	n.SetIndexPrice(perpMarketId, 10_000)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(11).
		AddSubmitPrice(perpMarketId, "val1", 10_100).Build())

	// Trigger funding (interval=3, last settled at 0, now at 12 ≥ 3).
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(12).Build())
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(13).Build())

	aliceAfter := n.GetBalance(aliceId, "USDC").Available
	bobAfter := n.GetBalance(bobId, "USDC").Available
	charlieAfter := n.GetBalance(charlieId, "USDC").Available

	alicePaid := aliceBefore - aliceAfter
	charliePaid := charlieBefore - charlieAfter
	bobReceived := bobAfter - bobBefore

	// Both longs should have paid; short should have received.
	if alicePaid <= 0 {
		t.Errorf("Alice (long) should have paid funding, balance change: %d", -alicePaid)
	}
	if charliePaid <= 0 {
		t.Errorf("Charlie (long) should have paid funding, balance change: %d", -charliePaid)
	}
	if bobReceived <= 0 {
		t.Errorf("Bob (short) should have received funding, balance change: %d", bobReceived)
	}

	// Conservation: total paid should approximately equal total received.
	// (Treasury acts as intermediary; exact equality requires no rounding loss.)
	totalPaid := alicePaid + charliePaid
	if totalPaid != bobReceived {
		t.Errorf("funding conservation violation: paid=%d received=%d", totalPaid, bobReceived)
	}
}
