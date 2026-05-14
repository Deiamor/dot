package primitives_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/primitives"
)

// ─── grid mock exchange ────────────────────────────────────────────────────────

// gridMockEx is a richer mock for Grid tests: it tracks open orders and supports
// simulated fills by removing orders from its list.
type gridMockEx struct {
	mu           sync.Mutex
	mid          float64
	openOrders   []exchange.Order
	nextID       int
	ordersPlaced atomic.Int64
	placedOrders []exchange.PlaceOrderRequest // all requests ever placed
	position     float64                      // signed qty
}

func (m *gridMockEx) MarketSnapshot(_ context.Context, sym string) (exchange.MarketSnapshot, error) {
	m.mu.Lock()
	mid := m.mid
	m.mu.Unlock()
	return exchange.MarketSnapshot{Symbol: sym, Mid: mid, Bid: mid - 1, Ask: mid + 1}, nil
}

func (m *gridMockEx) OrderBook(_ context.Context, sym string, _ int) (exchange.OrderBook, error) {
	return exchange.OrderBook{Symbol: sym}, nil
}

func (m *gridMockEx) Position(_ context.Context, sym string) (exchange.Position, error) {
	m.mu.Lock()
	qty := m.position
	m.mu.Unlock()
	return exchange.Position{Symbol: sym, Qty: qty}, nil
}

func (m *gridMockEx) Balances(_ context.Context) ([]exchange.Balance, error) { return nil, nil }

func (m *gridMockEx) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]exchange.Order, len(m.openOrders))
	copy(cp, m.openOrders)
	return cp, nil
}

func (m *gridMockEx) PlaceOrder(_ context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("order-%d", m.nextID)
	o := exchange.Order{
		ID:    id,
		Side:  req.Side,
		Price: req.Price,
		Qty:   req.Qty,
		Type:  req.Type,
	}
	m.openOrders = append(m.openOrders, o)
	m.ordersPlaced.Add(1)
	m.placedOrders = append(m.placedOrders, req)
	return o, nil
}

func (m *gridMockEx) CancelOrder(_ context.Context, _, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.openOrders[:0]
	for _, o := range m.openOrders {
		if o.ID != orderID {
			filtered = append(filtered, o)
		}
	}
	m.openOrders = filtered
	return nil
}

func (m *gridMockEx) CancelAllOrders(_ context.Context, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openOrders = nil
	return nil
}

// simulateFill removes the order with the given ID from openOrders, simulating
// an exchange fill.
func (m *gridMockEx) simulateFill(orderID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.openOrders[:0]
	for _, o := range m.openOrders {
		if o.ID != orderID {
			filtered = append(filtered, o)
		}
	}
	m.openOrders = filtered
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestGrid_InitialOrders_PlacedOnEnter verifies that when the strategy enters ACTIVE,
// it places GridLevels buy orders + GridLevels sell orders.
func TestGrid_InitialOrders_PlacedOnEnter(t *testing.T) {
	const levels = 3
	kill := make(chan struct{})
	defer close(kill)

	ex := &gridMockEx{mid: 1000}

	cfg := primitives.GridConfig{
		Symbol:         "BTC",
		GridLevels:     levels,
		GridSpacingBps: 20,
		OrderQty:       0.1,
		MaxPosition:    0, // no limit
	}
	g, err := primitives.NewGrid(cfg, kill)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(g, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	// Run briefly — just long enough to enter ACTIVE and place the initial grid.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	// Expect levels buy orders + levels sell orders = 2*levels total.
	total := ex.ordersPlaced.Load()
	if total < int64(2*levels) {
		t.Fatalf("expected at least %d initial grid orders, got %d", 2*levels, total)
	}

	// Count buy and sell placements.
	var buys, sells int
	ex.mu.Lock()
	for _, req := range ex.placedOrders {
		if req.Side == exchange.Buy {
			buys++
		} else {
			sells++
		}
	}
	ex.mu.Unlock()

	if buys < levels {
		t.Fatalf("expected at least %d buy orders, got %d", levels, buys)
	}
	if sells < levels {
		t.Fatalf("expected at least %d sell orders, got %d", levels, sells)
	}
}

// TestGrid_FillResponse_PlacesOpposite verifies that when a buy order fills, the
// strategy places a sell at price + spacing on the next tick.
func TestGrid_FillResponse_PlacesOpposite(t *testing.T) {
	kill := make(chan struct{})
	defer close(kill)

	ex := &gridMockEx{mid: 1000}

	cfg := primitives.GridConfig{
		Symbol:         "BTC",
		GridLevels:     1,
		GridSpacingBps: 100, // 1%
		OrderQty:       0.1,
		MaxPosition:    0,
	}
	g, err := primitives.NewGrid(cfg, kill)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(g, ex, "BTC", 30*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	// Run for a short time to let initial grid be placed.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	// Find a BUY order and simulate a fill.
	ex.mu.Lock()
	var buyOrder *exchange.Order
	for i := range ex.openOrders {
		if ex.openOrders[i].Side == exchange.Buy {
			cp := ex.openOrders[i]
			buyOrder = &cp
			break
		}
	}
	ex.mu.Unlock()

	if buyOrder == nil {
		t.Skip("no open buy order to fill — test inconclusive at this tick rate")
	}

	filledPrice := buyOrder.Price
	filledID := buyOrder.ID
	ex.simulateFill(filledID)

	// Record how many orders have been placed so far.
	placedBefore := ex.ordersPlaced.Load()

	// Run another tick so the strategy can detect the fill and respond.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel2()
	eng.Run(ctx2) //nolint

	placedAfter := ex.ordersPlaced.Load()
	if placedAfter <= placedBefore {
		t.Fatal("expected at least one counter order placed after fill")
	}

	// The counter order for a buy fill should be a SELL at filledPrice * (1 + spacing).
	spacing := cfg.GridSpacingBps / 10_000
	wantSellPrice := filledPrice * (1 + spacing)

	ex.mu.Lock()
	newOrders := make([]exchange.PlaceOrderRequest, len(ex.placedOrders)-int(placedBefore))
	copy(newOrders, ex.placedOrders[placedBefore:])
	ex.mu.Unlock()

	for _, req := range newOrders {
		if req.Side == exchange.Sell && absF(req.Price-wantSellPrice) < 0.01 {
			return // found the expected counter order
		}
	}
	t.Fatalf("expected SELL counter order near price %.4f, got new orders: %v", wantSellPrice, newOrders)
}

// TestGrid_KillSwitch_Stops verifies that closing the kill channel causes the
// strategy to transition to STOPPED and that no further orders are placed after that.
func TestGrid_KillSwitch_Stops(t *testing.T) {
	kill := make(chan struct{})
	ex := &gridMockEx{mid: 1000}

	cfg := primitives.GridConfig{
		Symbol:         "BTC",
		GridLevels:     2,
		GridSpacingBps: 20,
		OrderQty:       0.1,
		MaxPosition:    0,
	}
	g, err := primitives.NewGrid(cfg, kill)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(g, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() { done <- eng.Run(ctx) }()

	// Let the engine start and enter ACTIVE.
	time.Sleep(100 * time.Millisecond)

	// Close kill — should cause * → STOPPED transition on the next tick.
	close(kill)

	// Give the engine a few ticks to process the kill.
	time.Sleep(100 * time.Millisecond)

	if eng.CurrentState() != "STOPPED" {
		t.Fatalf("expected state STOPPED after kill, got %s", eng.CurrentState())
	}

	// Record order count after kill + transition.
	ordersAtStop := ex.ordersPlaced.Load()

	// Run a bit more and verify no new orders are placed (STOPPED state has no OnTick).
	time.Sleep(100 * time.Millisecond)

	ordersAfter := ex.ordersPlaced.Load()
	if ordersAfter != ordersAtStop {
		t.Fatalf("expected no new orders after STOPPED, but %d were placed", ordersAfter-ordersAtStop)
	}

	cancel() // clean up
	<-done
}

// TestGrid_MaxPosition_StopsBuys verifies that when position >= MaxPosition,
// new buy orders are not placed.
func TestGrid_MaxPosition_StopsBuys(t *testing.T) {
	kill := make(chan struct{})
	defer close(kill)

	// Set position already at MaxPosition so no buys should be placed.
	ex := &gridMockEx{
		mid:      1000,
		position: 5.0, // already at max
	}

	cfg := primitives.GridConfig{
		Symbol:         "BTC",
		GridLevels:     3,
		GridSpacingBps: 20,
		OrderQty:       0.1,
		MaxPosition:    5.0, // abs(position)=5.0 >= MaxPosition=5.0 → no buys
	}
	g, err := primitives.NewGrid(cfg, kill)
	if err != nil {
		t.Fatal(err)
	}

	eng, err := core.NewEngine(g, ex, "BTC", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	ex.mu.Lock()
	defer ex.mu.Unlock()
	for _, req := range ex.placedOrders {
		if req.Side == exchange.Buy {
			t.Fatalf("expected no buy orders when position >= MaxPosition, but got buy at price %.4f", req.Price)
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
