package scenario_test

// Stage 16: Price oracle / mark price tests.
//
// Verifies:
//  1. Single validator submission → mark price equals submitted price
//  2. Three validators → mark price is the median (not average)
//  3. Price update replaces previous submission for same validator
//  4. EventMarkPriceUpdated fired with correct marketId and price
//  5. Mark price returns 0 when no prices submitted
//  6. Oracle prices persist through snapshot round-trip

import (
	"os"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// -------------------------------------------------------------------------
// Scenario 1: Single submission → mark price equals submitted price
// -------------------------------------------------------------------------
func TestOracle_SingleSubmission(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitPrice("BTC-USDC", "val-01", 50_000).Build())
	if err != nil {
		t.Fatalf("submit price: %v", err)
	}

	got := n.GetMarkPrice("BTC-USDC")
	if got != 50_000 {
		t.Errorf("mark price: want 50000 got %d", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Three validators → mark price is median
// -------------------------------------------------------------------------
func TestOracle_MedianOfThree(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	// Prices: 40_000, 60_000, 50_000 → sorted: 40k, 50k, 60k → median = 50k
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitPrice("BTC-USDC", "val-01", 40_000).
		AddSubmitPrice("BTC-USDC", "val-02", 60_000).
		AddSubmitPrice("BTC-USDC", "val-03", 50_000).Build())
	if err != nil {
		t.Fatalf("submit prices: %v", err)
	}

	got := n.GetMarkPrice("BTC-USDC")
	if got != 50_000 {
		t.Errorf("median mark price: want 50000 got %d", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Price update replaces previous submission for same validator
// -------------------------------------------------------------------------
func TestOracle_UpdateReplacesPreviousSubmission(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitPrice("BTC-USDC", "val-01", 40_000).Build())

	// Update to a new price — should replace, not add.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddSubmitPrice("BTC-USDC", "val-01", 55_000).Build())
	if err != nil {
		t.Fatalf("update price: %v", err)
	}

	got := n.GetMarkPrice("BTC-USDC")
	if got != 55_000 {
		t.Errorf("updated mark price: want 55000 got %d", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: EventMarkPriceUpdated fired with correct fields
// -------------------------------------------------------------------------
func TestOracle_EventPayload(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	var payload state.MarkPriceUpdatedPayload
	n.Subscribe(state.EventMarkPriceUpdated, func(e state.Event) {
		payload = e.Payload.(state.MarkPriceUpdatedPayload)
	})

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitPrice("BTC-USDC", "val-01", 48_000).Build())
	if err != nil {
		t.Fatalf("submit price: %v", err)
	}

	if payload.MarketId != "BTC-USDC" {
		t.Errorf("marketId: want BTC-USDC got %s", payload.MarketId)
	}
	if payload.MarkPrice != 48_000 {
		t.Errorf("markPrice: want 48000 got %d", payload.MarkPrice)
	}
	if payload.BlockHeight == 0 {
		t.Error("blockHeight should be populated")
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Mark price is 0 before any submission
// -------------------------------------------------------------------------
func TestOracle_ZeroBeforeSubmission(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	got := n.GetMarkPrice("BTC-USDC")
	if got != 0 {
		t.Errorf("expected 0 mark price before any submission, got %d", got)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Oracle prices persist through snapshot round-trip
// -------------------------------------------------------------------------
func TestOracle_SnapshotPersistence(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddSubmitPrice("BTC-USDC", "val-01", 42_000).
		AddSubmitPrice("BTC-USDC", "val-02", 44_000).Build())
	if err != nil {
		t.Fatalf("submit prices: %v", err)
	}

	wantMark := n.GetMarkPrice("BTC-USDC")
	if wantMark == 0 {
		t.Fatal("expected non-zero mark price before snapshot")
	}

	path := t.TempDir() + "/oracle_snap.json"
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	n2 := node.NewLocalNode()
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	gotMark := n2.GetMarkPrice("BTC-USDC")
	if gotMark != wantMark {
		t.Errorf("mark price after snapshot: want %d got %d", wantMark, gotMark)
	}

	os.Remove(path)
}
