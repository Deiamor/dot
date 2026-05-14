package sim

import (
	"context"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Run executes strategy against scenario and returns a Result.
// Blocks until the scenario's ticks are exhausted or ctx is cancelled.
// Equivalent to RunContext(context.Background(), strategy, scenario).
func Run(strategy core.Strategy, scenario Scenario) Result {
	return RunContext(context.Background(), strategy, scenario)
}

// RunContext is like Run but accepts a context for timeout control.
func RunContext(ctx context.Context, strategy core.Strategy, scenario Scenario) Result {
	ex := newSimExchange(scenario.Symbol, scenario)

	// Drain tick events into equity curve concurrently with the engine.
	curve := make([]float64, 0, len(scenario.Ticks))
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range ex.Events() {
			if ev.Kind == exchange.EventKindTick && ev.Equity > 0 {
				curve = append(curve, ev.Equity)
			}
		}
	}()

	eng, err := core.NewEngine(strategy, ex, scenario.Symbol, time.Nanosecond, nil, nil)
	if err != nil {
		ex.shutdown()
		<-drained
		return Result{InitialBalance: scenario.Config.InitialBalance, FinalState: "ERROR"}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eng.Run(runCtx) //nolint — expected to stop via ErrTerminal or ctx

	// Engine is stopped: no more emit calls possible, safe to close channel.
	ex.shutdown()
	<-drained

	ex.mu.Lock()
	r := buildResult(ex, curve, eng.CurrentState())
	ex.mu.Unlock()
	return r
}
