package sim_test

import (
	"context"
	"testing"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/sim"
)

// ─── minimal strategies for testing ──────────────────────────────────────────

// buyAndHold buys 1 unit on the first tick, holds until the end.
type buyAndHold struct{ bought bool }

func (s *buyAndHold) Name() string    { return "buy-and-hold" }
func (s *buyAndHold) Initial() string { return "IDLE" }
func (s *buyAndHold) States() []core.State {
	return []core.State{
		{Name: "IDLE"},
		{Name: "HOLDING", OnTick: func(ctx *core.Context) error { return nil }},
	}
}
func (s *buyAndHold) Transitions() []core.Transition {
	return []core.Transition{
		{
			From: "IDLE", To: "HOLDING",
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid > 0 },
			Action: func(ctx *core.Context) error {
				_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, exchange.PlaceOrderRequest{
					Symbol: ctx.Symbol,
					Side:   exchange.Buy,
					Type:   exchange.Market,
					Qty:    1.0,
				})
				return err
			},
		},
	}
}

// sellAtPeak sells 1 unit when price crosses a threshold, then holds DONE (terminal).
type sellAtPeak struct {
	threshold float64
}

func (s *sellAtPeak) Name() string    { return "sell-at-peak" }
func (s *sellAtPeak) Initial() string { return "IDLE" }
func (s *sellAtPeak) States() []core.State {
	return []core.State{
		{Name: "IDLE"},
		{Name: "LONG", OnTick: func(ctx *core.Context) error { return nil }},
		{Name: "DONE"}, // terminal: no outgoing transitions
	}
}
func (s *sellAtPeak) Transitions() []core.Transition {
	return []core.Transition{
		{
			From:  "IDLE",
			To:    "LONG",
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid > 0 },
			Action: func(ctx *core.Context) error {
				_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, exchange.PlaceOrderRequest{
					Symbol: ctx.Symbol, Side: exchange.Buy, Type: exchange.Market, Qty: 0.1,
				})
				return err
			},
		},
		{
			From: "LONG", To: "DONE",
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid >= s.threshold },
			Action: func(ctx *core.Context) error {
				_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, exchange.PlaceOrderRequest{
					Symbol: ctx.Symbol, Side: exchange.Sell, Type: exchange.Market, Qty: 0.1,
				})
				return err
			},
		},
	}
}

// doNothing just runs through all ticks without trading.
type doNothing struct{}

func (s *doNothing) Name() string    { return "do-nothing" }
func (s *doNothing) Initial() string { return "WATCHING" }
func (s *doNothing) States() []core.State {
	return []core.State{{Name: "WATCHING", OnTick: func(ctx *core.Context) error { return nil }}}
}
func (s *doNothing) Transitions() []core.Transition { return nil }

// ─── ScenarioBuilder tests ────────────────────────────────────────────────────

func TestScenario_TickCount(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").
		Ticks([]float64{100, 101, 102, 103, 104}).
		Build()

	if len(sc.Ticks) != 5 {
		t.Errorf("tick count: got %d, want 5", len(sc.Ticks))
	}
}

func TestScenario_MidSpreadAuto(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").
		SpreadBps(10). // 0.1% = 1 bps each side
		Tick(50000).
		Build()

	tick := sc.Ticks[0]
	mid := tick.Mid()
	if mid != 50000 {
		t.Errorf("mid: got %g, want 50000", mid)
	}
	if tick.Bid >= tick.Ask {
		t.Errorf("bid %g >= ask %g", tick.Bid, tick.Ask)
	}
}

func TestScenario_RandomWalk_Deterministic(t *testing.T) {
	build := func() sim.Scenario {
		return sim.NewScenario("BTCUSDT").RandomWalk(20, 50000, 0.001, 42).Build()
	}
	a, b := build(), build()
	for i, ta := range a.Ticks {
		if ta.Mid() != b.Ticks[i].Mid() {
			t.Errorf("tick %d: random walk not deterministic", i)
		}
	}
}

func TestScenario_Build_PanicsOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty scenario")
		}
	}()
	sim.NewScenario("BTCUSDT").Build()
}

// ─── Run / Result tests ───────────────────────────────────────────────────────

func TestRun_NoTrades_BalanceUnchanged(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").Balance(10_000).Ticks([]float64{50000, 50100, 50200}).Build()
	result := sim.Run(&doNothing{}, sc)

	if result.NumFills != 0 {
		t.Errorf("fills: got %d, want 0", result.NumFills)
	}
	if result.FinalBalance != 10_000 {
		t.Errorf("balance: got %g, want 10000", result.FinalBalance)
	}
}

func TestRun_MarketBuy_DeductsBalance(t *testing.T) {
	// Use price=100 so buying qty=1 costs 100, well within balance=10000.
	sc := sim.NewScenario("BTCUSDT").
		Balance(10_000).
		SlippageBps(0).
		TakerFeeBps(0).
		Ticks([]float64{100, 100, 100}).
		Build()

	result := sim.Run(&buyAndHold{}, sc)

	if result.NumFills != 1 {
		t.Fatalf("fills: got %d, want 1", result.NumFills)
	}
	// Bought 1 unit at 100: balance = 9900, equity = 9900 + 1*100 = 10000.
	if result.FinalBalance < 9895 || result.FinalBalance > 9905 {
		t.Errorf("balance after buy: got %.2f, want ≈ 9900", result.FinalBalance)
	}
	if result.FinalEquity < 9_990 || result.FinalEquity > 10_010 {
		t.Errorf("equity: got %.2f, want ≈ 10000", result.FinalEquity)
	}
}

func TestRun_ProfitableTrade(t *testing.T) {
	// Buy at 50000, sell at 55000 → profit = 0.1 * 5000 = 500 (before fees)
	sc := sim.NewScenario("BTCUSDT").
		Balance(10_000).
		SlippageBps(0).
		TakerFeeBps(0).
		Ticks([]float64{50000, 50000, 55000, 55000}).
		Build()

	result := sim.Run(&sellAtPeak{threshold: 55000}, sc)

	if result.NumFills != 2 {
		t.Fatalf("fills: got %d, want 2 (buy + sell)", result.NumFills)
	}
	if result.NetPnL <= 0 {
		t.Errorf("NetPnL: got %.2f, want > 0", result.NetPnL)
	}
}

func TestRun_EquityCurve_Length(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").Ticks([]float64{100, 101, 102, 103, 104}).Build()
	result := sim.Run(&doNothing{}, sc)

	if len(result.EquityCurve) == 0 {
		t.Error("equity curve is empty")
	}
}

func TestRun_MaxDrawdown_Zero_WhenNoLoss(t *testing.T) {
	// Strictly rising prices, no positions → equity flat → drawdown 0.
	prices := make([]float64, 50)
	for i := range prices {
		prices[i] = 50000 + float64(i)*10
	}
	sc := sim.NewScenario("BTCUSDT").Balance(10_000).Ticks(prices).Build()
	result := sim.Run(&doNothing{}, sc)

	if result.MaxDrawdown != 0 {
		t.Errorf("MaxDrawdown: got %g, want 0 (no trades)", result.MaxDrawdown)
	}
}

func TestRun_FinalState(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").
		SlippageBps(0).TakerFeeBps(0).
		Ticks([]float64{50000, 50000, 60000}).
		Build()
	result := sim.Run(&sellAtPeak{threshold: 60000}, sc)

	if result.FinalState != "DONE" {
		t.Errorf("FinalState: got %q, want %q", result.FinalState, "DONE")
	}
}

func TestRunContext_CancelledContext(t *testing.T) {
	// 100 ticks but ctx cancelled immediately — should return early.
	prices := make([]float64, 100)
	for i := range prices {
		prices[i] = 50000
	}
	sc := sim.NewScenario("BTCUSDT").Ticks(prices).Build()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run

	result := sim.RunContext(ctx, &doNothing{}, sc)
	// Should have consumed 0 or very few ticks.
	if len(result.EquityCurve) > 10 {
		t.Errorf("cancelled context: consumed %d ticks, want ≤ 10", len(result.EquityCurve))
	}
}

// ─── Assert tests ─────────────────────────────────────────────────────────────

type mockT struct {
	errors []string
}

func (m *mockT) Errorf(format string, args ...any) {
	m.errors = append(m.errors, "ERROR: "+format)
}
func (m *mockT) Helper() {}

func TestAssert_MinFills_Pass(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").SlippageBps(0).TakerFeeBps(0).
		Ticks([]float64{50000, 50000, 50000}).Build()
	result := sim.Run(&buyAndHold{}, sc)

	mt := &mockT{}
	sim.Assert(mt, result).MinFills(1)
	if len(mt.errors) != 0 {
		t.Errorf("unexpected assertion failure: %v", mt.errors)
	}
}

func TestAssert_MinFills_Fail(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").Ticks([]float64{50000}).Build()
	result := sim.Run(&doNothing{}, sc)

	mt := &mockT{}
	sim.Assert(mt, result).MinFills(1)
	if len(mt.errors) == 0 {
		t.Error("expected assertion failure for 0 fills")
	}
}

func TestAssert_PnLAbove_Pass(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").SlippageBps(0).TakerFeeBps(0).
		Ticks([]float64{50000, 50000, 55000}).Build()
	result := sim.Run(&sellAtPeak{threshold: 55000}, sc)

	mt := &mockT{}
	sim.Assert(mt, result).PnLAbove(0)
	if len(mt.errors) != 0 {
		t.Errorf("unexpected assertion failure: %v", mt.errors)
	}
}

func TestAssert_Custom(t *testing.T) {
	sc := sim.NewScenario("BTCUSDT").Balance(5_000).Ticks([]float64{50000}).Build()
	result := sim.Run(&doNothing{}, sc)

	mt := &mockT{}
	sim.Assert(mt, result).Custom("initial balance 5000", func(r sim.Result) bool {
		return r.InitialBalance == 5_000
	})
	if len(mt.errors) != 0 {
		t.Errorf("unexpected custom assertion failure: %v", mt.errors)
	}
}
