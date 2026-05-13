package backtest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/backtest"
)

// makeSnap builds a MarketSnapshot with the given mid price and timestamp.
// Bid and Ask are set symmetrically around mid.
func makeSnap(mid float64, t time.Time) exchange.MarketSnapshot {
	return exchange.MarketSnapshot{
		Symbol:    "BTCUSDT",
		Mid:       mid,
		Bid:       mid - 0.5,
		Ask:       mid + 0.5,
		Timestamp: t,
	}
}

// defaultBacktestCfg returns a Config with 100k balance and no fees.
func defaultBacktestCfg() backtest.Config {
	return backtest.Config{
		InitialBalance: 100_000,
		TakerFeeBps:    0,
		MakerFeeBps:    0,
	}
}

// base is an arbitrary starting time for deterministic snapshot timestamps.
var base = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// TestBacktest_ReturnsSnapshotsInOrder verifies that successive MarketSnapshot
// calls return the pre-loaded snapshots in insertion order.
func TestBacktest_ReturnsSnapshotsInOrder(t *testing.T) {
	mids := []float64{40_000, 40_500, 41_000}
	snaps := make([]exchange.MarketSnapshot, len(mids))
	for i, m := range mids {
		snaps[i] = makeSnap(m, base.Add(time.Duration(i)*time.Minute))
	}

	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	for i, want := range mids {
		snap, err := ex.MarketSnapshot(ctx, "BTCUSDT")
		if err != nil {
			t.Fatalf("snapshot[%d]: unexpected error: %v", i, err)
		}
		if snap.Mid != want {
			t.Errorf("snapshot[%d]: Mid: got %g want %g", i, snap.Mid, want)
		}
	}
}

// TestBacktest_ErrTerminalAfterLastSnapshot verifies that calling MarketSnapshot
// after all snapshots have been consumed returns an error wrapping ErrTerminal.
func TestBacktest_ErrTerminalAfterLastSnapshot(t *testing.T) {
	snaps := []exchange.MarketSnapshot{makeSnap(40_000, base)}
	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	// Consume the only snapshot.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("first snapshot: unexpected error: %v", err)
	}

	// Next call should return ErrTerminal.
	_, err := ex.MarketSnapshot(ctx, "BTCUSDT")
	if err == nil {
		t.Fatal("expected error after last snapshot, got nil")
	}
	if !errors.Is(err, exchange.ErrTerminal) {
		t.Errorf("expected errors.Is(err, ErrTerminal)=true, got %v", err)
	}
}

// TestBacktest_MarketBuy_FillsAtCurrentMid verifies that a market BUY order
// fills at the mid price of the most recently consumed snapshot.
func TestBacktest_MarketBuy_FillsAtCurrentMid(t *testing.T) {
	snaps := []exchange.MarketSnapshot{makeSnap(40_000, base)}
	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	// Advance to snapshot at mid=40000.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("MarketSnapshot: %v", err)
	}

	_, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Market,
		Qty:    1,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	pos, err := ex.Position(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if pos.AvgEntryPrice < 39_900 || pos.AvgEntryPrice > 40_100 {
		t.Errorf("AvgEntryPrice: want ~40000, got %g", pos.AvgEntryPrice)
	}
	if pos.Qty != 1 {
		t.Errorf("Qty: want 1, got %g", pos.Qty)
	}
}

// TestBacktest_LimitFill_OnNextTick places a BUY LIMIT at 40100 and verifies
// it fills when the next snapshot has mid=40000 (40000 <= 40100 triggers fill).
func TestBacktest_LimitFill_OnNextTick(t *testing.T) {
	snaps := []exchange.MarketSnapshot{
		makeSnap(40_500, base),                         // first tick to prime cursor
		makeSnap(40_000, base.Add(time.Minute)),        // second tick triggers fill
	}
	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	// Consume first snapshot to set current mid.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("first MarketSnapshot: %v", err)
	}

	// Place a BUY LIMIT at 40100 (above current mid=40000, will fill next tick).
	if _, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Limit,
		Price:  40_100,
		Qty:    1,
	}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	orders, _ := ex.OpenOrders(ctx, "BTCUSDT")
	if len(orders) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(orders))
	}

	// Second snapshot at mid=40000: 40000 <= 40100 → triggers fill.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("second MarketSnapshot: %v", err)
	}

	orders, err := ex.OpenOrders(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders: %v", err)
	}
	if len(orders) != 0 {
		t.Errorf("expected limit order to be filled, got %d open orders", len(orders))
	}

	stats := ex.Stats()
	if stats.NumFills != 1 {
		t.Errorf("NumFills: want 1, got %d", stats.NumFills)
	}
}

// TestBacktest_PnL_LongPosition buys at mid=40000, advances to mid=41000,
// closes the position, and verifies TotalPnL > 0.
func TestBacktest_PnL_LongPosition(t *testing.T) {
	snaps := []exchange.MarketSnapshot{
		makeSnap(40_000, base),
		makeSnap(41_000, base.Add(time.Minute)),
	}
	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	// Prime at 40000.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("first snap: %v", err)
	}

	// Buy 1 BTC at 40000.
	if _, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Buy, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	// Advance to 41000.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("second snap: %v", err)
	}

	// Close at 41000.
	if _, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("sell: %v", err)
	}

	stats := ex.Stats()
	if stats.TotalPnL <= 0 {
		t.Errorf("TotalPnL: want > 0, got %g", stats.TotalPnL)
	}
}

// TestBacktest_Stats_Correct runs through 5 snapshots performing one buy and
// one sell and checks that NumFills == 2.
func TestBacktest_Stats_Correct(t *testing.T) {
	snaps := make([]exchange.MarketSnapshot, 5)
	for i := range snaps {
		snaps[i] = makeSnap(40_000+float64(i)*100, base.Add(time.Duration(i)*time.Minute))
	}

	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	// Consume first snapshot.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("snap[0]: %v", err)
	}

	// Buy at snapshot 1.
	if _, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Buy, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	// Advance through snapshots 1-3.
	for i := 1; i <= 3; i++ {
		if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
			t.Fatalf("snap[%d]: %v", i, err)
		}
	}

	// Sell at snapshot 4.
	if _, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("sell: %v", err)
	}

	// Consume last snapshot.
	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("snap[4]: %v", err)
	}

	stats := ex.Stats()
	if stats.NumFills != 2 {
		t.Errorf("NumFills: want 2, got %d", stats.NumFills)
	}
}

// TestBacktest_Progress verifies that Progress() returns the correct cursor and
// total snapshot count as snapshots are consumed.
func TestBacktest_Progress(t *testing.T) {
	snaps := []exchange.MarketSnapshot{
		makeSnap(40_000, base),
		makeSnap(40_500, base.Add(time.Minute)),
		makeSnap(41_000, base.Add(2*time.Minute)),
	}
	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	cursor, total := ex.Progress()
	if cursor != 0 || total != 3 {
		t.Errorf("initial Progress: got (%d, %d) want (0, 3)", cursor, total)
	}

	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("snap[0]: %v", err)
	}
	cursor, total = ex.Progress()
	if cursor != 1 || total != 3 {
		t.Errorf("after snap[0]: got (%d, %d) want (1, 3)", cursor, total)
	}

	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("snap[1]: %v", err)
	}
	cursor, total = ex.Progress()
	if cursor != 2 || total != 3 {
		t.Errorf("after snap[1]: got (%d, %d) want (2, 3)", cursor, total)
	}
}

// TestBacktest_CancelAllOrders places a limit order and cancels it, verifying
// OpenOrders is empty afterwards.
func TestBacktest_CancelAllOrders(t *testing.T) {
	snaps := []exchange.MarketSnapshot{makeSnap(40_000, base)}
	ex := backtest.New(defaultBacktestCfg(), snaps)
	ctx := context.Background()

	if _, err := ex.MarketSnapshot(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("MarketSnapshot: %v", err)
	}

	if _, err := ex.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Limit,
		Price:  39_000, // below current mid — won't fill immediately
		Qty:    1,
	}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	orders, _ := ex.OpenOrders(ctx, "BTCUSDT")
	if len(orders) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(orders))
	}

	if err := ex.CancelAllOrders(ctx, "BTCUSDT"); err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}

	orders, err := ex.OpenOrders(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders: %v", err)
	}
	if len(orders) != 0 {
		t.Errorf("expected 0 open orders after CancelAllOrders, got %d", len(orders))
	}
}
