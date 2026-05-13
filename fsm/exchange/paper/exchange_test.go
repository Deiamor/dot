package paper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/paper"
)

// mockReal is a minimal exchange.Exchange that returns a fixed MarketSnapshot.
// The Mid can be overridden per-call via nextMid.
type mockReal struct {
	snaps []exchange.MarketSnapshot
	idx   int
}

func newMockReal(mids ...float64) *mockReal {
	snaps := make([]exchange.MarketSnapshot, len(mids))
	for i, m := range mids {
		snaps[i] = exchange.MarketSnapshot{
			Symbol:    "BTCUSDT",
			Mid:       m,
			Bid:       m - 1,
			Ask:       m + 1,
			Timestamp: time.Now(),
		}
	}
	return &mockReal{snaps: snaps}
}

func (m *mockReal) MarketSnapshot(_ context.Context, _ string) (exchange.MarketSnapshot, error) {
	if m.idx >= len(m.snaps) {
		return m.snaps[len(m.snaps)-1], nil
	}
	s := m.snaps[m.idx]
	m.idx++
	return s, nil
}

func (m *mockReal) OrderBook(_ context.Context, symbol string, _ int) (exchange.OrderBook, error) {
	return exchange.OrderBook{Symbol: symbol}, nil
}
func (m *mockReal) Position(_ context.Context, symbol string) (exchange.Position, error) {
	return exchange.Position{Symbol: symbol}, nil
}
func (m *mockReal) Balances(_ context.Context) ([]exchange.Balance, error) { return nil, nil }
func (m *mockReal) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	return nil, nil
}
func (m *mockReal) PlaceOrder(_ context.Context, _ exchange.PlaceOrderRequest) (exchange.Order, error) {
	return exchange.Order{}, nil
}
func (m *mockReal) CancelOrder(_ context.Context, _, _ string) error  { return nil }
func (m *mockReal) CancelAllOrders(_ context.Context, _ string) error { return nil }

// defaultCfg returns a Config with no fees and no slippage for simple balance math.
func defaultCfg() paper.Config {
	return paper.Config{
		InitialBalance: 100_000,
		SlippageBps:    0,
		TakerFeeBps:    0,
		MakerFeeBps:    0,
	}
}

// primeExchange calls MarketSnapshot once so lastMid is populated.
func primeExchange(t *testing.T, ex *paper.Exchange, symbol string) exchange.MarketSnapshot {
	t.Helper()
	snap, err := ex.MarketSnapshot(context.Background(), symbol)
	if err != nil {
		t.Fatalf("primeExchange: MarketSnapshot: %v", err)
	}
	return snap
}

// TestPaper_InitialBalance verifies that Balances() returns the configured
// initial USDT balance before any orders are placed.
func TestPaper_InitialBalance(t *testing.T) {
	cfg := defaultCfg()
	cfg.InitialBalance = 50_000
	ex := paper.New(cfg, newMockReal(50_000))

	bals, err := ex.Balances(context.Background())
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	if len(bals) != 1 {
		t.Fatalf("expected 1 balance, got %d", len(bals))
	}
	if bals[0].Asset != "USDT" {
		t.Errorf("Asset: got %s want USDT", bals[0].Asset)
	}
	if bals[0].Available != 50_000 {
		t.Errorf("Available: got %g want 50000", bals[0].Available)
	}
}

// TestPaper_MarketBuy_DeductsBalance checks that buying 1 BTC at mid=50000
// deducts ~50000 USDT from the balance.
func TestPaper_MarketBuy_DeductsBalance(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000))
	primeExchange(t, ex, "BTCUSDT")

	_, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Market,
		Qty:    1,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	bals, err := ex.Balances(context.Background())
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	// balance should have dropped by roughly 50000 (no slippage, no fees)
	remaining := bals[0].Available
	if remaining >= 100_000 {
		t.Errorf("balance should have decreased after buy, got %g", remaining)
	}
	if remaining < 40_000 {
		t.Errorf("balance dropped too far: got %g", remaining)
	}
}

// TestPaper_MarketSell_AddsBalance verifies that selling after buying correctly
// closes the position and records realized PnL.
//
// The paper exchange uses margin-style accounting: the notional cost is debited
// on a long entry and only the realized PnL is credited on close. After a
// round-trip at the same price the balance reflects
//   initial - notional - buy_fee + realised - sell_fee
// with realised = 0 at flat exit. The Stats TotalPnL (net of fees) should be
// approximately −40 (two 0.04 % × 50 000 fills) and the position should be flat.
func TestPaper_MarketSell_AddsBalance(t *testing.T) {
	// Use a tiny fee to reflect reality but keep math simple.
	cfg := paper.Config{InitialBalance: 100_000, TakerFeeBps: 4}
	ex := paper.New(cfg, newMockReal(50_000, 50_000))

	primeExchange(t, ex, "BTCUSDT")

	// Buy 1 BTC at 50000.
	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Buy, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	// Advance the snapshot so lastMid is still 50000.
	primeExchange(t, ex, "BTCUSDT")

	// Sell 1 BTC at 50000 to close the long.
	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("sell: %v", err)
	}

	// Position must be flat after the round-trip.
	pos, err := ex.Position(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if !pos.IsFlat() {
		t.Errorf("expected flat position after round-trip, got Qty=%g", pos.Qty)
	}

	// TotalPnL (net of fees) should be close to −40
	// (two fees of 0.04 % × 50 000 = 20 each, zero gross PnL).
	stats := ex.Stats()
	if stats.NumFills != 2 {
		t.Errorf("NumFills: want 2, got %d", stats.NumFills)
	}
	if stats.TotalPnL > 0 {
		t.Errorf("TotalPnL: want ≤ 0 for flat exit, got %g", stats.TotalPnL)
	}
	if stats.TotalPnL < -100 {
		t.Errorf("TotalPnL: unreasonably negative for round-trip, got %g", stats.TotalPnL)
	}
}

// TestPaper_LongPnL_ClosePositive buys at 50000 and sells at 51000; realised
// PnL should be approximately +1000.
func TestPaper_LongPnL_ClosePositive(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000, 51_000))

	// Snapshot at 50000 → prime lastMid.
	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Buy, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("buy: %v", err)
	}

	// Advance to 51000.
	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("sell: %v", err)
	}

	stats := ex.Stats()
	if stats.TotalPnL < 900 || stats.TotalPnL > 1100 {
		t.Errorf("TotalPnL: want ~1000, got %g", stats.TotalPnL)
	}
}

// TestPaper_ShortPnL_ClosePositive sells short at 50000 and buys back at 49000;
// realised PnL should be approximately +1000.
func TestPaper_ShortPnL_ClosePositive(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000, 49_000))

	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Sell, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("short sell: %v", err)
	}

	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT", Side: exchange.Buy, Type: exchange.Market, Qty: 1,
	}); err != nil {
		t.Fatalf("buy back: %v", err)
	}

	stats := ex.Stats()
	if stats.TotalPnL < 900 || stats.TotalPnL > 1100 {
		t.Errorf("TotalPnL: want ~1000, got %g", stats.TotalPnL)
	}
}

// TestPaper_LimitOrder_FillsWhenPriceCrosses places a BUY LIMIT at 51000
// (above the current mid=50000) and verifies it fills when MarketSnapshot
// returns mid=50000 (which satisfies mid <= limitPrice).
//
// The paper exchange triggers a BUY limit fill when mid <= limitPrice,
// so a limit price above mid fills on the very next snapshot tick.
func TestPaper_LimitOrder_FillsWhenPriceCrosses(t *testing.T) {
	// First snapshot primes lastMid=50000 (used for any immediate market fill checks).
	// Second snapshot at mid=50000 triggers limit fill (50000 <= 51000).
	ex := paper.New(defaultCfg(), newMockReal(50_000, 50_000))

	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Limit,
		Price:  51_000,
		Qty:    1,
	}); err != nil {
		t.Fatalf("PlaceOrder limit: %v", err)
	}

	// Order is pending — not yet filled.
	orders, err := ex.OpenOrders(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 pending order before tick, got %d", len(orders))
	}

	// Advance snapshot: mid=50000 ≤ 51000 → triggers fill.
	primeExchange(t, ex, "BTCUSDT")

	orders, err = ex.OpenOrders(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders after tick: %v", err)
	}
	if len(orders) != 0 {
		t.Errorf("expected 0 open orders after limit fill, got %d", len(orders))
	}

	stats := ex.Stats()
	if stats.NumFills != 1 {
		t.Errorf("NumFills: want 1, got %d", stats.NumFills)
	}
}

// TestPaper_LimitOrder_NoFillBelowLimit places a BUY LIMIT at 49000 when
// mid=50000. Since 50000 > 49000, the order should NOT fill.
func TestPaper_LimitOrder_NoFillBelowLimit(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000, 50_000))

	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Limit,
		Price:  49_000,
		Qty:    1,
	}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	// Advance snapshot at mid=50000 — 50000 > 49000, so no fill.
	primeExchange(t, ex, "BTCUSDT")

	orders, err := ex.OpenOrders(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders: %v", err)
	}
	if len(orders) != 1 {
		t.Errorf("expected order still open, got %d open orders", len(orders))
	}
}

// TestPaper_CancelAllOrders places two limit orders and cancels them all.
func TestPaper_CancelAllOrders(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000))

	primeExchange(t, ex, "BTCUSDT")

	for range 2 {
		if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
			Symbol: "BTCUSDT",
			Side:   exchange.Buy,
			Type:   exchange.Limit,
			Price:  49_000,
			Qty:    0.1,
		}); err != nil {
			t.Fatalf("PlaceOrder: %v", err)
		}
	}

	orders, _ := ex.OpenOrders(context.Background(), "BTCUSDT")
	if len(orders) != 2 {
		t.Fatalf("expected 2 open orders, got %d", len(orders))
	}

	if err := ex.CancelAllOrders(context.Background(), "BTCUSDT"); err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}

	orders, err := ex.OpenOrders(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenOrders: %v", err)
	}
	if len(orders) != 0 {
		t.Errorf("expected 0 open orders after CancelAllOrders, got %d", len(orders))
	}
}

// TestPaper_Position_Flat_Initially verifies that Position().IsFlat() is true
// before any orders are placed.
func TestPaper_Position_Flat_Initially(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000))

	pos, err := ex.Position(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if !pos.IsFlat() {
		t.Errorf("expected flat position initially, got Qty=%g", pos.Qty)
	}
}

// TestPaper_Events_EmittedOnFill verifies that placing a market order emits an
// EventKindFill event on the Events() channel.
func TestPaper_Events_EmittedOnFill(t *testing.T) {
	cfg := paper.Config{InitialBalance: 100_000, EventBuf: 16}
	ex := paper.New(cfg, newMockReal(50_000, 50_000))

	primeExchange(t, ex, "BTCUSDT")

	if _, err := ex.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTCUSDT",
		Side:   exchange.Buy,
		Type:   exchange.Market,
		Qty:    1,
	}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	events := ex.Events()

	// Drain events looking for a fill event.
	var got *exchange.TradingEvent
	timeout := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case ev := <-events:
			if ev.Kind == exchange.EventKindFill {
				ev := ev
				got = &ev
				break drain
			}
		case <-timeout:
			break drain
		}
	}

	if got == nil {
		t.Fatal("expected EventKindFill event, got none")
	}
	if got.Symbol != "BTCUSDT" {
		t.Errorf("event Symbol: got %s want BTCUSDT", got.Symbol)
	}
	if got.Side != exchange.Buy {
		t.Errorf("event Side: got %s want BUY", got.Side)
	}
	if got.Qty != 1 {
		t.Errorf("event Qty: got %g want 1", got.Qty)
	}
}

// TestPaper_ImplementsEventEmitter verifies that *paper.Exchange satisfies the
// exchange.EventEmitter interface via a compile-time assertion.
func TestPaper_ImplementsEventEmitter(t *testing.T) {
	ex := paper.New(defaultCfg(), newMockReal(50_000))
	var _ exchange.EventEmitter = ex
	_ = errors.New // suppress unused import if needed
}
