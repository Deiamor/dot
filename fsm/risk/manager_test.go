package risk_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/risk"
)

// ─── mock exchange ────────────────────────────────────────────────────────────

type mockExchange struct {
	mu sync.Mutex

	snapshot exchange.MarketSnapshot
	position exchange.Position
	balances []exchange.Balance

	// Records
	cancelAllCalled int
	ordersPlaced    []exchange.PlaceOrderRequest

	// Errors to inject.
	snapshotErr error
	positionErr error
	balancesErr error
	placeErr    error
}

func (m *mockExchange) MarketSnapshot(_ context.Context, _ string) (exchange.MarketSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot, m.snapshotErr
}

func (m *mockExchange) OrderBook(_ context.Context, _ string, _ int) (exchange.OrderBook, error) {
	return exchange.OrderBook{}, nil
}

func (m *mockExchange) Position(_ context.Context, _ string) (exchange.Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.position, m.positionErr
}

func (m *mockExchange) Balances(_ context.Context) ([]exchange.Balance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances, m.balancesErr
}

func (m *mockExchange) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	return nil, nil
}

func (m *mockExchange) PlaceOrder(_ context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.placeErr != nil {
		return exchange.Order{}, m.placeErr
	}
	m.ordersPlaced = append(m.ordersPlaced, req)
	return exchange.Order{
		ID:        "mock-order",
		Symbol:    req.Symbol,
		Side:      req.Side,
		Type:      req.Type,
		Price:     req.Price,
		Qty:       req.Qty,
		FilledQty: req.Qty,
		Status:    exchange.StatusFilled,
		CreatedAt: time.Now(),
	}, nil
}

func (m *mockExchange) CancelOrder(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockExchange) CancelAllOrders(_ context.Context, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelAllCalled++
	return nil
}

// helpers

func newMock(balance, posQty, markPrice float64) *mockExchange {
	return &mockExchange{
		snapshot: exchange.MarketSnapshot{
			Symbol:    "BTC-USDT",
			Bid:       markPrice - 1,
			Ask:       markPrice + 1,
			Mid:       markPrice,
			MarkPrice: markPrice,
			Timestamp: time.Now(),
		},
		position: exchange.Position{
			Symbol: "BTC-USDT",
			Qty:    posQty,
		},
		balances: []exchange.Balance{
			{Asset: "USDT", Available: balance, Total: balance},
		},
	}
}

func newKillChan() chan struct{} { return make(chan struct{}) }

func isKilled(kill chan struct{}) bool {
	select {
	case <-kill:
		return true
	default:
		return false
	}
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestRisk_PositionLimit_Blocks(t *testing.T) {
	inner := newMock(10_000, 0, 50_000)
	kill := newKillChan()

	mgr := risk.New(risk.Config{MaxPositionQty: 0.01}, inner, kill)

	_, err := mgr.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTC-USDT",
		Side:   exchange.Buy,
		Type:   exchange.Market,
		Qty:    0.02,
	})
	if !errors.Is(err, risk.ErrPositionLimitExceeded) {
		t.Fatalf("expected ErrPositionLimitExceeded, got %v", err)
	}
	if isKilled(kill) {
		t.Fatal("kill should not be triggered for position limit breach")
	}
}

func TestRisk_PositionLimit_Allows(t *testing.T) {
	inner := newMock(10_000, 0, 50_000)
	kill := newKillChan()

	mgr := risk.New(risk.Config{MaxPositionQty: 0.01}, inner, kill)

	_, err := mgr.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTC-USDT",
		Side:   exchange.Buy,
		Type:   exchange.Market,
		Qty:    0.005,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if isKilled(kill) {
		t.Fatal("kill should not be triggered for an allowed order")
	}
}

func TestRisk_MaxDrawdown_TriggersKill(t *testing.T) {
	// Start with equity = 10_000 (balance=10_000, pos=0).
	// After first snapshot, peak = 10_000.
	// Drop equity to 8_500 (15% drawdown). MaxDrawdownPct=10 → kill.
	inner := newMock(10_000, 0, 50_000)
	kill := newKillChan()

	mgr := risk.New(risk.Config{MaxDrawdownPct: 10}, inner, kill)

	// First call establishes peak equity.
	_, err := mgr.MarketSnapshot(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error on first snapshot: %v", err)
	}
	if isKilled(kill) {
		t.Fatal("should not be killed yet")
	}

	// Drop balance to simulate 15% equity loss.
	inner.mu.Lock()
	inner.balances[0].Total = 8_500
	inner.balances[0].Available = 8_500
	inner.mu.Unlock()

	_, err = mgr.MarketSnapshot(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error on second snapshot: %v", err)
	}

	// Allow a moment for channel close to propagate (it's synchronous, but be safe).
	if !isKilled(kill) {
		t.Fatal("expected kill to be triggered by drawdown breach")
	}
}

func TestRisk_MinEquity_TriggersKill(t *testing.T) {
	inner := newMock(500, 0, 50_000) // equity = 500
	kill := newKillChan()

	mgr := risk.New(risk.Config{MinEquityUSDT: 1_000}, inner, kill)

	_, err := mgr.MarketSnapshot(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !isKilled(kill) {
		t.Fatal("expected kill to be triggered by min equity breach")
	}

	inner.mu.Lock()
	calls := inner.cancelAllCalled
	inner.mu.Unlock()
	if calls == 0 {
		t.Fatal("expected CancelAllOrders to be called on min equity breach")
	}
}

func TestRisk_DailyLoss_TriggersKill(t *testing.T) {
	inner := newMock(10_000, 0, 50_000)
	kill := newKillChan()

	mgr := risk.New(risk.Config{MaxDailyLossUSDT: 50}, inner, kill)

	// Accumulate fees that surpass the daily limit.
	mgr.TrackFee(30)
	if isKilled(kill) {
		t.Fatal("should not be killed yet at 30 USDT loss")
	}

	mgr.TrackFee(25) // total = 55 > 50
	if !isKilled(kill) {
		t.Fatal("expected kill to be triggered by daily loss breach")
	}
}

func TestRisk_KillOnce(t *testing.T) {
	inner := newMock(500, 0, 50_000)
	kill := newKillChan()

	mgr := risk.New(risk.Config{MinEquityUSDT: 1_000, MaxDrawdownPct: 5}, inner, kill)

	// Trigger two conditions simultaneously — channel must only be closed once.
	// If closed twice, the runtime panics.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic (likely double close): %v", r)
		}
	}()

	// First breach.
	_, _ = mgr.MarketSnapshot(context.Background(), "BTC-USDT")
	// Second breach — same condition, should be a no-op.
	_, _ = mgr.MarketSnapshot(context.Background(), "BTC-USDT")

	if !isKilled(kill) {
		t.Fatal("expected kill to be triggered")
	}
}

func TestRisk_Killed_BlocksPlaceOrder(t *testing.T) {
	inner := newMock(500, 0, 50_000)
	kill := newKillChan()

	mgr := risk.New(risk.Config{MinEquityUSDT: 1_000}, inner, kill)

	// Trigger kill via min equity.
	_, _ = mgr.MarketSnapshot(context.Background(), "BTC-USDT")
	if !isKilled(kill) {
		t.Fatal("expected kill after min equity breach")
	}

	// Now PlaceOrder must return ErrKilled.
	_, err := mgr.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{
		Symbol: "BTC-USDT",
		Side:   exchange.Buy,
		Type:   exchange.Market,
		Qty:    0.001,
	})
	if !errors.Is(err, risk.ErrKilled) {
		t.Fatalf("expected ErrKilled after manager killed, got %v", err)
	}

	// Ensure no order was placed on the inner exchange.
	inner.mu.Lock()
	n := len(inner.ordersPlaced)
	inner.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 orders placed after kill, got %d", n)
	}
}
