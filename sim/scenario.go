// Package sim provides a strategy simulation framework for testing FSM strategies
// against synthetic market data without real money or network calls.
//
// Basic usage:
//
//	scenario := sim.NewScenario("BTCUSDT").
//	    Balance(10_000).
//	    Ticks([]float64{50000, 50100, 49900, 50200}).
//	    Build()
//
//	result := sim.Run(myStrategy, scenario)
//	sim.Assert(t, result).MinFills(1).PnLAbove(0)
package sim

import (
	"math"
	"math/rand"
	"time"
)

// Tick is one market data point in a Scenario.
type Tick struct {
	Bid         float64
	Ask         float64
	FundingRate float64
	At          time.Time
}

// Mid returns the midpoint price.
func (t Tick) Mid() float64 { return (t.Bid + t.Ask) / 2 }

// Config holds the virtual account parameters for a Scenario.
type Config struct {
	InitialBalance   float64
	SlippageBps      float64 // market order fill slippage (basis points)
	TakerFeeBps      float64 // taker (market order) fee
	MakerFeeBps      float64 // maker (limit order fill) fee
	DefaultSpreadBps float64 // half-spread used when only mid price is given
}

// Scenario is an immutable market sequence plus virtual account config.
type Scenario struct {
	Symbol string
	Ticks  []Tick
	Config Config
}

// ScenarioBuilder constructs a Scenario with a fluent API.
type ScenarioBuilder struct {
	symbol string
	ticks  []Tick
	cfg    Config
	cursor time.Time
	step   time.Duration
}

// NewScenario begins building a test scenario for the given trading pair symbol.
func NewScenario(symbol string) *ScenarioBuilder {
	return &ScenarioBuilder{
		symbol: symbol,
		cfg: Config{
			InitialBalance:   10_000,
			SlippageBps:      1,
			TakerFeeBps:      4,
			MakerFeeBps:      2,
			DefaultSpreadBps: 1,
		},
		cursor: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		step:   5 * time.Second,
	}
}

// Balance sets the starting virtual USDT balance.
func (b *ScenarioBuilder) Balance(v float64) *ScenarioBuilder { b.cfg.InitialBalance = v; return b }

// SlippageBps sets the market order fill slippage in basis points.
func (b *ScenarioBuilder) SlippageBps(v float64) *ScenarioBuilder {
	b.cfg.SlippageBps = v
	return b
}

// TakerFeeBps sets the taker fee in basis points.
func (b *ScenarioBuilder) TakerFeeBps(v float64) *ScenarioBuilder {
	b.cfg.TakerFeeBps = v
	return b
}

// MakerFeeBps sets the maker fee in basis points.
func (b *ScenarioBuilder) MakerFeeBps(v float64) *ScenarioBuilder {
	b.cfg.MakerFeeBps = v
	return b
}

// SpreadBps sets the default half-spread (in bps) applied when Tick(mid) is used.
func (b *ScenarioBuilder) SpreadBps(v float64) *ScenarioBuilder {
	b.cfg.DefaultSpreadBps = v
	return b
}

// Step sets the time increment between consecutive ticks (default: 5s).
func (b *ScenarioBuilder) Step(d time.Duration) *ScenarioBuilder { b.step = d; return b }

// Tick adds one tick from a mid price; bid/ask are auto-computed from SpreadBps.
func (b *ScenarioBuilder) Tick(mid float64) *ScenarioBuilder {
	half := mid * b.cfg.DefaultSpreadBps / 10_000 / 2
	b.ticks = append(b.ticks, Tick{
		Bid: mid - half,
		Ask: mid + half,
		At:  b.cursor,
	})
	b.cursor = b.cursor.Add(b.step)
	return b
}

// TickFull adds a tick with explicit bid, ask, and funding rate.
func (b *ScenarioBuilder) TickFull(bid, ask, fundingRate float64) *ScenarioBuilder {
	b.ticks = append(b.ticks, Tick{Bid: bid, Ask: ask, FundingRate: fundingRate, At: b.cursor})
	b.cursor = b.cursor.Add(b.step)
	return b
}

// Ticks adds multiple ticks from a slice of mid prices.
func (b *ScenarioBuilder) Ticks(mids []float64) *ScenarioBuilder {
	for _, mid := range mids {
		b.Tick(mid)
	}
	return b
}

// RandomWalk generates n ticks starting at start using a log-normal random walk.
// vol is the per-tick volatility fraction (e.g. 0.001 = 0.1%). seed=0 is non-deterministic.
func (b *ScenarioBuilder) RandomWalk(n int, start, vol float64, seed int64) *ScenarioBuilder {
	src := time.Now().UnixNano()
	if seed != 0 {
		src = seed
	}
	rng := rand.New(rand.NewSource(src))
	price := start
	for i := 0; i < n; i++ {
		price *= math.Exp((rng.Float64()*2 - 1) * vol)
		b.Tick(price)
	}
	return b
}

// Build finalises and returns the Scenario. Panics if no ticks were added.
func (b *ScenarioBuilder) Build() Scenario {
	if len(b.ticks) == 0 {
		panic("sim: scenario has no ticks")
	}
	return Scenario{Symbol: b.symbol, Ticks: b.ticks, Config: b.cfg}
}
