package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Engine executes a Strategy in a periodic tick loop.
//
// On every tick the Engine:
//  1. Refreshes market data into Context
//  2. Calls ctx.State.OnTick
//  3. Evaluates Transitions in declaration order
//  4. If a Guard fires: calls OnExit → Transition.Action → OnEnter
type Engine struct {
	strategy Strategy
	ex       exchange.Exchange
	symbol   string
	interval time.Duration
	params   map[string]any
	log      *slog.Logger

	current  string
	stateMap map[string]State
	tick     int64
	started  time.Time
}

// NewEngine creates an Engine for the given Strategy.
//
// symbol is the trading pair (e.g. "BTC-USDC-PERP").
// interval controls how often the tick loop fires.
// params are passed through to every Context.
func NewEngine(
	s Strategy,
	ex exchange.Exchange,
	symbol string,
	interval time.Duration,
	params map[string]any,
	log *slog.Logger,
) (*Engine, error) {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		strategy: s,
		ex:       ex,
		symbol:   symbol,
		interval: interval,
		params:   params,
		log:      log.With("strategy", s.Name(), "symbol", symbol),
		stateMap: make(map[string]State),
	}
	for _, st := range s.States() {
		if _, dup := e.stateMap[st.Name]; dup {
			return nil, fmt.Errorf("duplicate state %q in strategy %q", st.Name, s.Name())
		}
		e.stateMap[st.Name] = st
	}
	if _, ok := e.stateMap[s.Initial()]; !ok {
		return nil, fmt.Errorf("initial state %q not found in strategy %q", s.Initial(), s.Name())
	}
	for _, tr := range s.Transitions() {
		if tr.From != "*" {
			if _, ok := e.stateMap[tr.From]; !ok {
				return nil, fmt.Errorf("transition from unknown state %q", tr.From)
			}
		}
		if _, ok := e.stateMap[tr.To]; !ok {
			return nil, fmt.Errorf("transition to unknown state %q", tr.To)
		}
	}
	return e, nil
}

// Run starts the tick loop, blocking until ctx is cancelled or a terminal
// state (no outgoing transitions) is reached.
func (e *Engine) Run(ctx context.Context) error {
	e.current = e.strategy.Initial()
	e.started = time.Now()
	e.log.Info("engine started", "initial_state", e.current)

	if err := e.enter(ctx, e.current); err != nil {
		return fmt.Errorf("enter initial state: %w", err)
	}

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.log.Info("engine stopped by context", "state", e.current)
			return ctx.Err()
		case <-ticker.C:
			if err := e.doTick(ctx); err != nil {
				return err
			}
		}
	}
}

// CurrentState returns the name of the active state.
func (e *Engine) CurrentState() string { return e.current }

// ─── internal ────────────────────────────────────────────────────────────────

func (e *Engine) buildContext(goCtx context.Context) (*Context, error) {
	snap, err := e.ex.MarketSnapshot(goCtx, e.symbol)
	if err != nil {
		return nil, fmt.Errorf("market snapshot: %w", err)
	}
	pos, err := e.ex.Position(goCtx, e.symbol)
	if err != nil {
		return nil, fmt.Errorf("position: %w", err)
	}
	orders, err := e.ex.OpenOrders(goCtx, e.symbol)
	if err != nil {
		return nil, fmt.Errorf("open orders: %w", err)
	}
	return &Context{
		Symbol:     e.symbol,
		Market:     snap,
		Position:   pos,
		OpenOrders: orders,
		Tick:       e.tick,
		Elapsed:    time.Since(e.started),
		Params:     e.params,
		State:      e.current,
		Exchange:   e.ex,
		Log:        e.log.With("state", e.current, "tick", e.tick),
		GoCtx:      goCtx,
	}, nil
}

func (e *Engine) doTick(goCtx context.Context) error {
	e.tick++

	fsmCtx, err := e.buildContext(goCtx)
	if err != nil {
		if errors.Is(err, exchange.ErrTerminal) {
			e.log.Info("exchange signalled terminal (backtest complete)", "reason", err)
			return fmt.Errorf("terminal: %w", context.Canceled)
		}
		e.log.Warn("context refresh failed", "err", err)
		return nil // transient; keep running
	}

	// Call OnTick for current state.
	st := e.stateMap[e.current]
	if st.OnTick != nil {
		if err := st.OnTick(fsmCtx); err != nil {
			e.log.Error("OnTick error", "state", e.current, "err", err)
		}
	}

	// Evaluate transitions.
	for _, tr := range e.strategy.Transitions() {
		if tr.From != "*" && tr.From != e.current {
			continue
		}
		if tr.To == e.current {
			continue
		}
		if tr.Guard == nil || tr.Guard(fsmCtx) {
			if err := e.transition(goCtx, fsmCtx, tr); err != nil {
				return fmt.Errorf("transition %s→%s: %w", tr.From, tr.To, err)
			}
			break
		}
	}

	// Check terminal state: no outgoing transitions → done.
	hasExit := false
	for _, tr := range e.strategy.Transitions() {
		if tr.From == e.current || tr.From == "*" {
			hasExit = true
			break
		}
	}
	if !hasExit {
		e.log.Info("reached terminal state", "state", e.current)
		return fmt.Errorf("terminal: %w", context.Canceled)
	}
	return nil
}

func (e *Engine) enter(goCtx context.Context, name string) error {
	st, ok := e.stateMap[name]
	if !ok {
		return fmt.Errorf("unknown state %q", name)
	}
	if st.OnEnter != nil {
		fsmCtx, _ := e.buildContext(goCtx)
		if fsmCtx != nil {
			if err := st.OnEnter(fsmCtx); err != nil {
				return fmt.Errorf("OnEnter %q: %w", name, err)
			}
		}
	}
	return nil
}

func (e *Engine) transition(goCtx context.Context, fsmCtx *Context, tr Transition) error {
	e.log.Info("transition", "from", e.current, "to", tr.To)

	// OnExit current.
	cur := e.stateMap[e.current]
	if cur.OnExit != nil {
		if err := cur.OnExit(fsmCtx); err != nil {
			e.log.Warn("OnExit error", "state", e.current, "err", err)
		}
	}

	// Transition action.
	if tr.Action != nil {
		if err := tr.Action(fsmCtx); err != nil {
			return fmt.Errorf("transition action: %w", err)
		}
	}

	e.current = tr.To

	// OnEnter next.
	return e.enter(goCtx, tr.To)
}
