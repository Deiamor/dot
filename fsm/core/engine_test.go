package core_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// ─── mock exchange ─────────────────────────────────────────────────────────────

type mockExchange struct {
	mid         float64
	placeErr    error
	cancelErr   error
	ordersPlaced atomic.Int64
}

func (m *mockExchange) MarketSnapshot(_ context.Context, sym string) (exchange.MarketSnapshot, error) {
	return exchange.MarketSnapshot{Symbol: sym, Mid: m.mid, Bid: m.mid - 1, Ask: m.mid + 1}, nil
}
func (m *mockExchange) OrderBook(_ context.Context, sym string, _ int) (exchange.OrderBook, error) {
	return exchange.OrderBook{Symbol: sym}, nil
}
func (m *mockExchange) Position(_ context.Context, sym string) (exchange.Position, error) {
	return exchange.Position{Symbol: sym}, nil
}
func (m *mockExchange) Balances(_ context.Context) ([]exchange.Balance, error) { return nil, nil }
func (m *mockExchange) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	return nil, nil
}
func (m *mockExchange) PlaceOrder(_ context.Context, _ exchange.PlaceOrderRequest) (exchange.Order, error) {
	m.ordersPlaced.Add(1)
	return exchange.Order{}, m.placeErr
}
func (m *mockExchange) CancelOrder(_ context.Context, _, _ string) error { return m.cancelErr }
func (m *mockExchange) CancelAllOrders(_ context.Context, _ string) error { return m.cancelErr }

// ─── minimal strategy helpers ─────────────────────────────────────────────────

type simpleStrategy struct {
	name        string
	initial     string
	states      []core.State
	transitions []core.Transition
}

func (s *simpleStrategy) Name() string                  { return s.name }
func (s *simpleStrategy) Initial() string               { return s.initial }
func (s *simpleStrategy) States() []core.State          { return s.states }
func (s *simpleStrategy) Transitions() []core.Transition { return s.transitions }

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ─── NewEngine validation ─────────────────────────────────────────────────────

func TestNewEngine_DuplicateState_Error(t *testing.T) {
	s := &simpleStrategy{
		name:    "dup",
		initial: "A",
		states: []core.State{
			{Name: "A"},
			{Name: "A"},
		},
	}
	_, err := core.NewEngine(s, &mockExchange{mid: 100}, "SYM", time.Second, nil, newLogger())
	if err == nil {
		t.Fatal("expected error for duplicate state")
	}
}

func TestNewEngine_UnknownInitialState_Error(t *testing.T) {
	s := &simpleStrategy{
		name:    "bad-init",
		initial: "NOPE",
		states:  []core.State{{Name: "A"}},
	}
	_, err := core.NewEngine(s, &mockExchange{mid: 100}, "SYM", time.Second, nil, newLogger())
	if err == nil {
		t.Fatal("expected error for unknown initial state")
	}
}

func TestNewEngine_TransitionToUnknownState_Error(t *testing.T) {
	s := &simpleStrategy{
		name:    "bad-tr",
		initial: "A",
		states:  []core.State{{Name: "A"}},
		transitions: []core.Transition{
			{From: "A", To: "GHOST"},
		},
	}
	_, err := core.NewEngine(s, &mockExchange{mid: 100}, "SYM", time.Second, nil, newLogger())
	if err == nil {
		t.Fatal("expected error for transition to unknown state")
	}
}

func TestNewEngine_Valid(t *testing.T) {
	s := &simpleStrategy{
		name:    "ok",
		initial: "A",
		states:  []core.State{{Name: "A"}},
	}
	eng, err := core.NewEngine(s, &mockExchange{mid: 100}, "SYM", time.Second, nil, newLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.CurrentState() != "" {
		// CurrentState is empty before Run
	}
}

// ─── State transitions ────────────────────────────────────────────────────────

func TestEngine_Transition_FiresOnGuard(t *testing.T) {
	var entered atomic.Int64
	s := &simpleStrategy{
		name:    "tr-test",
		initial: "A",
		states: []core.State{
			{Name: "A"},
			{
				Name: "B",
				OnEnter: func(_ *core.Context) error {
					entered.Add(1)
					return nil
				},
			},
		},
		transitions: []core.Transition{
			{From: "A", To: "B", Guard: func(_ *core.Context) bool { return true }},
		},
	}
	ex := &mockExchange{mid: 1000}
	eng, err := core.NewEngine(s, ex, "SYM", 10*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint // expected to cancel

	if entered.Load() == 0 {
		t.Fatal("expected B.OnEnter to be called")
	}
}

func TestEngine_KillSwitch_TerminatesAfterTerminalState(t *testing.T) {
	// A → B (terminal, no outgoing transitions). Engine should stop.
	reached := make(chan struct{})
	s := &simpleStrategy{
		name:    "terminal",
		initial: "A",
		states: []core.State{
			{Name: "A"},
			{
				Name: "B",
				OnEnter: func(_ *core.Context) error {
					close(reached)
					return nil
				},
			},
		},
		transitions: []core.Transition{
			{From: "A", To: "B", Guard: func(_ *core.Context) bool { return true }},
			// No transition FROM B → terminal state
		},
	}
	ex := &mockExchange{mid: 500}
	eng, err := core.NewEngine(s, ex, "SYM", 10*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()

	select {
	case <-reached:
	case <-time.After(500*time.Millisecond):
		t.Fatal("B.OnEnter never called")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(500*time.Millisecond):
		t.Fatal("engine did not terminate after reaching terminal state")
	}
}

func TestEngine_OnTick_CalledEachInterval(t *testing.T) {
	var ticks atomic.Int64
	// "B" is a dead-end; the never-firing guard keeps the engine in "A".
	s := &simpleStrategy{
		name:    "tick-test",
		initial: "A",
		states: []core.State{
			{
				Name: "A",
				OnTick: func(_ *core.Context) error {
					ticks.Add(1)
					return nil
				},
			},
			{Name: "B"},
		},
		transitions: []core.Transition{
			// Guard never fires → engine stays in A indefinitely.
			{From: "A", To: "B", Guard: func(_ *core.Context) bool { return false }},
		},
	}
	ex := &mockExchange{mid: 100}
	eng, err := core.NewEngine(s, ex, "SYM", 20*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if ticks.Load() < 5 {
		t.Fatalf("expected at least 5 ticks in 500ms at 20ms interval, got %d", ticks.Load())
	}
}

func TestEngine_OnExit_CalledOnTransition(t *testing.T) {
	var exited atomic.Int64
	s := &simpleStrategy{
		name:    "exit-test",
		initial: "A",
		states: []core.State{
			{
				Name: "A",
				OnExit: func(_ *core.Context) error {
					exited.Add(1)
					return nil
				},
			},
			{Name: "B"},
		},
		transitions: []core.Transition{
			{From: "A", To: "B", Guard: func(_ *core.Context) bool { return true }},
		},
	}
	ex := &mockExchange{mid: 100}
	eng, err := core.NewEngine(s, ex, "SYM", 10*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if exited.Load() == 0 {
		t.Fatal("expected A.OnExit to be called on transition")
	}
}

func TestEngine_WildcardTransition_MatchesAnyState(t *testing.T) {
	var flatEntered atomic.Int64
	s := &simpleStrategy{
		name:    "wildcard",
		initial: "QUOTING",
		states: []core.State{
			{Name: "QUOTING"},
			{
				Name: "FLAT",
				OnEnter: func(_ *core.Context) error {
					flatEntered.Add(1)
					return nil
				},
			},
		},
		transitions: []core.Transition{
			// Wildcard: any → FLAT
			{From: "*", To: "FLAT", Guard: func(_ *core.Context) bool { return true }},
		},
	}
	ex := &mockExchange{mid: 100}
	eng, err := core.NewEngine(s, ex, "SYM", 10*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if flatEntered.Load() == 0 {
		t.Fatal("expected FLAT.OnEnter to be called via wildcard transition")
	}
}

// ─── Context fields ───────────────────────────────────────────────────────────

func TestContext_GoCtx_Available(t *testing.T) {
	var gotCtx context.Context
	s := &simpleStrategy{
		name:    "ctx-test",
		initial: "A",
		states: []core.State{
			{
				Name: "A",
				OnTick: func(fsmCtx *core.Context) error {
					gotCtx = fsmCtx.GoCtx
					return nil
				},
			},
		},
	}
	ex := &mockExchange{mid: 100}
	eng, err := core.NewEngine(s, ex, "SYM", 10*time.Millisecond, nil, newLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	eng.Run(ctx) //nolint

	if gotCtx == nil {
		t.Fatal("expected GoCtx to be populated in Context")
	}
}

// ─── Param helper ─────────────────────────────────────────────────────────────

func TestParam_ExistingKey_ReturnsValue(t *testing.T) {
	ctx := &core.Context{Params: map[string]any{"x": 42}}
	got := core.Param(ctx, "x", 0)
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestParam_MissingKey_ReturnsDefault(t *testing.T) {
	ctx := &core.Context{Params: map[string]any{}}
	got := core.Param(ctx, "missing", 99)
	if got != 99 {
		t.Fatalf("expected default 99, got %d", got)
	}
}

func TestParam_WrongType_ReturnsDefault(t *testing.T) {
	ctx := &core.Context{Params: map[string]any{"x": "not-int"}}
	got := core.Param(ctx, "x", 7)
	if got != 7 {
		t.Fatalf("expected default 7 for wrong type, got %d", got)
	}
}
