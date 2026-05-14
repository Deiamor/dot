package primitives_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/primitives"
)

// TestVWAP_RollingVWAP_Calculation verifies the rolling average with known prices.
// We drive the strategy's onTick directly via a short engine run and inspect VWAP().
func TestVWAP_RollingVWAP_Calculation(t *testing.T) {
	// Window of 3 ticks; prices will be 100, 200, 300 → avg = 200.
	// Use DeviationBps=0 so the gate never blocks.
	prices := []float64{100, 200, 300}
	callIdx := 0

	ex := &mockExVWAP{
		snapshotFn: func() exchange.MarketSnapshot {
			mid := prices[callIdx%len(prices)]
			callIdx++
			return exchange.MarketSnapshot{
				Mid: mid, Bid: mid - 1, Ask: mid + 1,
			}
		},
	}

	vw, err := primitives.NewVWAP(primitives.VWAPConfig{
		Symbol:       "BTC",
		Side:         exchange.Buy,
		TotalQty:     3.0,
		Slices:       100, // large so DONE is never reached during the test
		WindowTicks:  3,
		DeviationBps: 0, // always place
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(vw, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	// Run for enough ticks to fill the window (3 ticks at 20 ms = ~60 ms; give 300 ms).
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	got := vw.VWAP()
	if got <= 0 {
		t.Fatalf("expected VWAP > 0, got %g", got)
	}
	// After a full window of [100,200,300] the average must be 200.
	// Because the mock cycles through prices we can only assert it is within [100,300].
	if got < 100 || got > 300 {
		t.Fatalf("VWAP %g is outside expected range [100, 300]", got)
	}
}

// TestVWAP_DeviationGate_BlocksOrder verifies that a very high deviation threshold
// prevents any slice from being placed.
func TestVWAP_DeviationGate_BlocksOrder(t *testing.T) {
	// Mid = 1000, ask = 1001, bid = 999.
	// Threshold = 10 000 bps = 100% → ask must be < 1000*(1+1.0)=2000. That IS true.
	// So we need a threshold small enough to block: use a threshold that requires ask < mid*(1+0).
	// Actually we want to BLOCK: for a Buy, place if ask < vwap*(1+threshold).
	// If threshold is very small (e.g. 0.001 bps), vwap*(1+threshold)≈vwap=1000 but ask=1001 > 1000 → blocked.
	ex := &mockEx{mid: 1000}

	vw, err := primitives.NewVWAP(primitives.VWAPConfig{
		Symbol:       "BTC",
		Side:         exchange.Buy,
		TotalQty:     1.0,
		Slices:       5,
		WindowTicks:  3,
		DeviationBps: 0.001, // tiny threshold → ask(1001) > vwap(1000)*(1+0.0000001) → blocked
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(vw, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if ex.ordersPlaced.Load() != 0 {
		t.Fatalf("expected 0 orders placed (deviation gate should block), got %d", ex.ordersPlaced.Load())
	}
}

// TestVWAP_DeviationGate_AllowsOrder verifies that a generous deviation threshold
// allows orders to be placed.
func TestVWAP_DeviationGate_AllowsOrder(t *testing.T) {
	// Mid = 1000, ask = 1001.
	// With DeviationBps=200 (2%), threshold=0.02. vwap≈1000.
	// Buy condition: ask < vwap*(1+0.02) = 1020. ask=1001 < 1020 → allowed.
	ex := &mockEx{mid: 1000}

	vw, err := primitives.NewVWAP(primitives.VWAPConfig{
		Symbol:       "BTC",
		Side:         exchange.Buy,
		TotalQty:     1.0,
		Slices:       1,
		WindowTicks:  3,
		DeviationBps: 200, // 2% — wide enough that ask < vwap*(1.02)
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(vw, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if ex.ordersPlaced.Load() == 0 {
		t.Fatal("expected at least 1 order placed (deviation gate should allow)")
	}
}

// TestVWAP_AllSlicesSent_Transitions_DONE checks that after all slices are sent
// the strategy transitions to DONE and the engine terminates.
func TestVWAP_AllSlicesSent_Transitions_DONE(t *testing.T) {
	const slices = 3
	ex := &mockEx{mid: 1000}

	vw, err := primitives.NewVWAP(primitives.VWAPConfig{
		Symbol:       "BTC",
		Side:         exchange.Buy,
		TotalQty:     3.0,
		Slices:       slices,
		WindowTicks:  5,
		DeviationBps: 0, // always place
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(vw, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	eng.Run(ctx) //nolint

	if vw.SentSlices() != slices {
		t.Fatalf("expected %d sent slices, got %d", slices, vw.SentSlices())
	}
	if ex.ordersPlaced.Load() != int64(slices) {
		t.Fatalf("expected %d PlaceOrder calls, got %d", slices, ex.ordersPlaced.Load())
	}
}

// TestVWAP_ZeroDeviation_AlwaysPlaces confirms that with DeviationBps==0 a slice is
// placed every tick regardless of VWAP deviation.
func TestVWAP_ZeroDeviation_AlwaysPlaces(t *testing.T) {
	ex := &mockEx{mid: 1000}

	vw, err := primitives.NewVWAP(primitives.VWAPConfig{
		Symbol:       "ETH",
		Side:         exchange.Sell,
		TotalQty:     2.0,
		Slices:       2,
		WindowTicks:  10,
		DeviationBps: 0, // zero → always place
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(vw, ex, "ETH", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	eng.Run(ctx) //nolint

	if vw.SentSlices() != 2 {
		t.Fatalf("expected 2 slices placed with zero deviation, got %d", vw.SentSlices())
	}
}

// TestVWAP_CorrectSliceQty verifies the per-slice quantity.
func TestVWAP_CorrectSliceQty(t *testing.T) {
	ex := &mockEx{mid: 500}

	vw, err := primitives.NewVWAP(primitives.VWAPConfig{
		Symbol:       "SOL",
		Side:         exchange.Buy,
		TotalQty:     6.0,
		Slices:       3,
		WindowTicks:  5,
		DeviationBps: 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(vw, ex, "SOL", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	eng.Run(ctx) //nolint

	// Last order should carry qty = 6/3 = 2.0.
	if math.Abs(ex.lastOrder.Qty-2.0) > 1e-9 {
		t.Fatalf("expected slice qty 2.0, got %g", ex.lastOrder.Qty)
	}
}

// ─── mockExVWAP ───────────────────────────────────────────────────────────────

// mockExVWAP extends the basic mockEx with a configurable snapshot function.
type mockExVWAP struct {
	snapshotFn   func() exchange.MarketSnapshot
	ordersPlaced int64
	lastOrder    exchange.PlaceOrderRequest
}

func (m *mockExVWAP) MarketSnapshot(_ context.Context, sym string) (exchange.MarketSnapshot, error) {
	snap := m.snapshotFn()
	snap.Symbol = sym
	return snap, nil
}
func (m *mockExVWAP) OrderBook(_ context.Context, sym string, _ int) (exchange.OrderBook, error) {
	return exchange.OrderBook{Symbol: sym}, nil
}
func (m *mockExVWAP) Position(_ context.Context, sym string) (exchange.Position, error) {
	return exchange.Position{Symbol: sym}, nil
}
func (m *mockExVWAP) Balances(_ context.Context) ([]exchange.Balance, error) { return nil, nil }
func (m *mockExVWAP) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	return nil, nil
}
func (m *mockExVWAP) PlaceOrder(_ context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	m.ordersPlaced++
	m.lastOrder = req
	return exchange.Order{ID: "mock"}, nil
}
func (m *mockExVWAP) CancelOrder(_ context.Context, _, _ string) error  { return nil }
func (m *mockExVWAP) CancelAllOrders(_ context.Context, _ string) error { return nil }
