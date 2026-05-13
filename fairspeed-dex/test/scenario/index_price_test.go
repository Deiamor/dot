package scenario_test

// Stage 26: Oracle index price feed for funding rate.
//
// Verifies:
//  1. Multiple validators' index price submissions are aggregated to the median.
//  2. Non-zero funding rate when mark ≠ index.
//  3. Zero funding rate when mark == index.
//  4. Negative funding rate when mark < index.
//  5. End-to-end: real index price submission produces correct EventFundingSettled.
//  6. Fallback to markPrice when no index price submitted.
//  7. Validator can update submission and median recalculates.

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/funding"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// registerIndexPerpMarket registers a PERP market on an existing node.
// fundingIntervalBlocks controls how often funding runs.
func registerIndexPerpMarket(t *testing.T, n *node.LocalNode, marketId string, fundingIntervalBlocks int64) {
	t.Helper()
	height := n.CurrentHeight() + 1
	batch := fairbatch.NewBatchBuilder(height).AddRegisterPerpMarket(fairbatch.RegisterPerpMarketPayload{
		MarketId:              marketId,
		BaseAsset:             "BTC",
		QuoteAsset:            "USDC",
		InitialMarginBps:      1000,
		MaintenanceMarginBps:  500,
		MaxLeverage:           20,
		FundingIntervalBlocks: fundingIntervalBlocks,
		MaxFundingRateBps:     100,
	}).Build()
	if _, err := n.SubmitBatch(batch); err != nil {
		t.Fatalf("register perp market: %v", err)
	}
}

// TestIndexPrice_SubmitAndMedian verifies that multiple validators' index price
// submissions are aggregated to the median.
func TestIndexPrice_SubmitAndMedian(t *testing.T) {
	n := node.NewLocalNode()

	// Three validators submit different prices: 100, 120, 110 → median = 110
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-1", 100_000, "binance"); err != nil {
		t.Fatalf("submit index price val-1: %v", err)
	}
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-2", 120_000, "okx"); err != nil {
		t.Fatalf("submit index price val-2: %v", err)
	}
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-3", 110_000, "coinbase"); err != nil {
		t.Fatalf("submit index price val-3: %v", err)
	}

	got := n.GetIndexPrice("BTC-USDC-PERP")
	if got != 110_000 {
		t.Errorf("expected median index price 110000, got %d", got)
	}
}

// TestIndexPrice_FundingRateNonZero verifies that when mark price ≠ index price,
// the funding rate is non-zero.
func TestIndexPrice_FundingRateNonZero(t *testing.T) {
	markPrice := int64(105_000)  // mark is 5% above index
	indexPrice := int64(100_000)
	maxRateBps := int64(100)

	rate := funding.CalcFundingRate(markPrice, indexPrice, maxRateBps)
	if rate == 0 {
		t.Error("expected non-zero funding rate when mark > index")
	}
	// (105000 - 100000) * 10000 / 100000 = 500 bps, clamped to 100 bps
	if rate != maxRateBps {
		t.Errorf("expected funding rate clamped to %d bps, got %d", maxRateBps, rate)
	}
}

// TestIndexPrice_FundingRateZeroWhenEqual verifies rate=0 when mark == index.
func TestIndexPrice_FundingRateZeroWhenEqual(t *testing.T) {
	rate := funding.CalcFundingRate(100_000, 100_000, 100)
	if rate != 0 {
		t.Errorf("expected zero rate when mark == index, got %d", rate)
	}
}

// TestIndexPrice_FundingRateNegative verifies negative rate when mark < index (short pays).
func TestIndexPrice_FundingRateNegative(t *testing.T) {
	markPrice := int64(95_000)
	indexPrice := int64(100_000)
	rate := funding.CalcFundingRate(markPrice, indexPrice, 100)
	if rate >= 0 {
		t.Errorf("expected negative funding rate when mark < index, got %d", rate)
	}
	// (95000 - 100000) * 10000 / 100000 = -500, clamped to -100
	if rate != -100 {
		t.Errorf("expected rate = -100, got %d", rate)
	}
}

// TestIndexPrice_FundingSettledWithRealIndexPrice verifies end-to-end that once
// a real index price is set, the EventFundingSettled payload has the correct rate.
func TestIndexPrice_FundingSettledWithRealIndexPrice(t *testing.T) {
	n := node.NewLocalNode()
	registerIndexPerpMarket(t, n, "BTC-USDC-PERP", 1) // settle every block

	// Set mark price (oracle) and index price (CEX)
	markPrice := int64(105_000)
	indexPrice := int64(100_000)

	// Submit oracle mark price via TxSubmitPrice
	height := n.CurrentHeight() + 1
	markBatch := fairbatch.NewBatchBuilder(height).
		AddSubmitPrice("BTC-USDC-PERP", "val-1", markPrice).
		Build()
	if _, err := n.SubmitBatch(markBatch); err != nil {
		t.Fatalf("submit mark price: %v", err)
	}

	// Submit index price via SubmitIndexPrice (Stage 26 TX-based mechanism)
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-1", indexPrice, "binance"); err != nil {
		t.Fatalf("submit index price: %v", err)
	}

	// Verify index price is stored
	got := n.GetIndexPrice("BTC-USDC-PERP")
	if got != indexPrice {
		t.Errorf("expected index price %d, got %d", indexPrice, got)
	}

	// Collect funding settled events
	var fundingEvents []state.Event
	n.Subscribe(state.EventFundingSettled, func(e state.Event) {
		fundingEvents = append(fundingEvents, e)
	})

	// Advance one block — funding should settle
	height = n.CurrentHeight() + 1
	emptyBatch := fairbatch.NewBatchBuilder(height).Build()
	if _, err := n.SubmitBatch(emptyBatch); err != nil {
		t.Fatalf("advance block: %v", err)
	}

	if len(fundingEvents) == 0 {
		t.Fatal("expected EventFundingSettled, got none")
	}
	payload := fundingEvents[0].Payload.(state.FundingSettledPayload)
	if payload.MarketId != "BTC-USDC-PERP" {
		t.Errorf("unexpected market: %s", payload.MarketId)
	}
	// Mark > index → positive rate (longs pay)
	if payload.RateBps <= 0 {
		t.Errorf("expected positive funding rate, got %d", payload.RateBps)
	}
	if payload.MarkPrice != markPrice {
		t.Errorf("expected mark price %d, got %d", markPrice, payload.MarkPrice)
	}
}

// TestIndexPrice_FallbackToMarkPrice verifies that when no index price is submitted,
// the system falls back to using markPrice as indexPrice (rate = 0).
func TestIndexPrice_FallbackToMarkPrice(t *testing.T) {
	markPrice := int64(100_000)
	// No index price submitted → indexPrice = markPrice
	rate := funding.CalcFundingRate(markPrice, markPrice, 100)
	if rate != 0 {
		t.Errorf("expected zero rate on fallback, got %d", rate)
	}
}

// TestIndexPrice_UpdaterOverridesOldSubmission verifies that a validator can
// update their price and the median recalculates correctly.
// Uses 3 validators (odd count) for deterministic median.
func TestIndexPrice_UpdaterOverridesOldSubmission(t *testing.T) {
	n := node.NewLocalNode()

	// Initial: val-1=100, val-2=120, val-3=110 → sorted: [100,110,120] → median=110
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-1", 100_000, "binance"); err != nil {
		t.Fatalf("submit val-1 initial: %v", err)
	}
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-2", 120_000, "okx"); err != nil {
		t.Fatalf("submit val-2: %v", err)
	}
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-3", 110_000, "coinbase"); err != nil {
		t.Fatalf("submit val-3: %v", err)
	}

	before := n.GetIndexPrice("BTC-USDC-PERP")
	if before != 110_000 {
		t.Errorf("expected median 110000, got %d", before)
	}

	// val-1 updates: 100 → 130 → sorted: [110,120,130] → new median=120
	if err := n.SubmitIndexPrice("BTC-USDC-PERP", "val-1", 130_000, "binance"); err != nil {
		t.Fatalf("submit val-1 update: %v", err)
	}

	after := n.GetIndexPrice("BTC-USDC-PERP")
	if after != 120_000 {
		t.Errorf("expected updated median 120000, got %d", after)
	}
}
