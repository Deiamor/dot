package primitives_test

import (
	"context"
	"log/slog"
	"math"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/primitives"
)

// ─── mock exchange ─────────────────────────────────────────────────────────────

type mockEx struct {
	mid          float64
	ordersPlaced atomic.Int64
	lastOrder    exchange.PlaceOrderRequest
}

func (m *mockEx) MarketSnapshot(_ context.Context, sym string) (exchange.MarketSnapshot, error) {
	return exchange.MarketSnapshot{Symbol: sym, Mid: m.mid, Bid: m.mid - 1, Ask: m.mid + 1}, nil
}
func (m *mockEx) OrderBook(_ context.Context, sym string, _ int) (exchange.OrderBook, error) {
	return exchange.OrderBook{Symbol: sym}, nil
}
func (m *mockEx) Position(_ context.Context, sym string) (exchange.Position, error) {
	return exchange.Position{Symbol: sym}, nil
}
func (m *mockEx) Balances(_ context.Context) ([]exchange.Balance, error) { return nil, nil }
func (m *mockEx) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	return nil, nil
}
func (m *mockEx) PlaceOrder(_ context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	m.ordersPlaced.Add(1)
	m.lastOrder = req
	return exchange.Order{ID: "mock"}, nil
}
func (m *mockEx) CancelOrder(_ context.Context, _, _ string) error    { return nil }
func (m *mockEx) CancelAllOrders(_ context.Context, _ string) error   { return nil }

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ─── NewTWAP validation ───────────────────────────────────────────────────────

func TestNewTWAP_ZeroQty_Error(t *testing.T) {
	_, err := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol: "BTC", Side: exchange.Buy, TotalQty: 0, Slices: 5, Duration: time.Minute,
	})
	if err == nil {
		t.Fatal("expected error for TotalQty=0")
	}
}

func TestNewTWAP_ZeroSlices_Error(t *testing.T) {
	_, err := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol: "BTC", Side: exchange.Buy, TotalQty: 1, Slices: 0, Duration: time.Minute,
	})
	if err == nil {
		t.Fatal("expected error for Slices=0")
	}
}

func TestNewTWAP_ZeroDuration_Error(t *testing.T) {
	_, err := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol: "BTC", Side: exchange.Buy, TotalQty: 1, Slices: 5, Duration: 0,
	})
	if err == nil {
		t.Fatal("expected error for Duration=0")
	}
}

func TestNewTWAP_Valid(t *testing.T) {
	_, err := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol: "BTC", Side: exchange.Buy, TotalQty: 10, Slices: 5, Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── Execution ────────────────────────────────────────────────────────────────

func TestTWAP_PlacesNSlices(t *testing.T) {
	const slices = 4
	ex := &mockEx{mid: 1000}
	tw, err := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol:   "BTC",
		Side:     exchange.Buy,
		TotalQty: 4.0,
		Slices:   slices,
		Duration: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(tw, ex, "BTC", 30*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	eng.Run(ctx) //nolint

	if tw.SentSlices() != slices {
		t.Fatalf("expected %d slices placed, got %d", slices, tw.SentSlices())
	}
	if ex.ordersPlaced.Load() != int64(slices) {
		t.Fatalf("expected %d PlaceOrder calls, got %d", slices, ex.ordersPlaced.Load())
	}
}

func TestTWAP_MarketOrder_WhenNoBpsFence(t *testing.T) {
	ex := &mockEx{mid: 1000}
	tw, _ := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol:        "BTC",
		Side:          exchange.Buy,
		TotalQty:      1.0,
		Slices:        1,
		Duration:      100 * time.Millisecond,
		LimitPriceBps: 0, // no fence → Market
	})

	eng, _ := core.NewEngine(tw, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if ex.lastOrder.Type != exchange.Market {
		t.Fatalf("expected Market order, got %s", ex.lastOrder.Type)
	}
}

func TestTWAP_LimitOrder_WhenBpsFenceSet(t *testing.T) {
	ex := &mockEx{mid: 1000}
	tw, _ := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol:        "BTC",
		Side:          exchange.Buy,
		TotalQty:      1.0,
		Slices:        1,
		Duration:      100 * time.Millisecond,
		LimitPriceBps: 10, // 10 bps above mid
	})

	eng, _ := core.NewEngine(tw, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if ex.lastOrder.Type != exchange.Limit {
		t.Fatalf("expected Limit order, got %s", ex.lastOrder.Type)
	}
	// Price should be mid * (1 + 10/10_000) ≈ 1001.0
	want := 1000.0 * (1 + 10.0/10_000)
	if math.Abs(ex.lastOrder.Price-want) > 1e-6 {
		t.Fatalf("expected limit price %g, got %g", want, ex.lastOrder.Price)
	}
}

func TestTWAP_SentSlices_InitiallyZero(t *testing.T) {
	tw, _ := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol: "BTC", Side: exchange.Buy, TotalQty: 10, Slices: 5, Duration: time.Minute,
	})
	if tw.SentSlices() != 0 {
		t.Fatal("expected SentSlices=0 before execution")
	}
}

func TestTWAP_CorrectQtyPerSlice(t *testing.T) {
	ex := &mockEx{mid: 500}
	tw, _ := primitives.NewTWAP(primitives.TWAPConfig{
		Symbol:   "ETH",
		Side:     exchange.Sell,
		TotalQty: 3.0,
		Slices:   3,
		Duration: 200 * time.Millisecond,
	})
	eng, _ := core.NewEngine(tw, ex, "ETH", 20*time.Millisecond, nil, newLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	eng.Run(ctx) //nolint

	// Last placed order should have qty = 3/3 = 1.0.
	if ex.lastOrder.Qty != 1.0 {
		t.Fatalf("expected slice qty 1.0, got %g", ex.lastOrder.Qty)
	}
}
