// Package optimizer performs a grid search over MM strategy parameters
// against a historical dataset, ranking results by Sharpe ratio.
package optimizer

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/backtest"
	mmengine "github.com/deiamor/perp-strategy-engine/mm/engine"
	"github.com/deiamor/perp-strategy-engine/mm/model"
)

// ParamGrid defines the search space. Each field is a list of values to try.
// The optimizer tests every combination (cartesian product).
type ParamGrid struct {
	Gamma     []float64 `json:"gamma"`
	Kappa     []float64 `json:"kappa"`
	Sigma     []float64 `json:"sigma"`
	Alpha     []float64 `json:"alpha"`
	OrderSize []float64 `json:"orderSize"`
}

// DefaultGrid returns a sensible starting search space.
func DefaultGrid() ParamGrid {
	return ParamGrid{
		Gamma:     []float64{0.05, 0.1, 0.2},
		Kappa:     []float64{1.0, 1.5, 2.0},
		Sigma:     []float64{0.5, 0.8, 1.2},
		Alpha:     []float64{0.5, 1.0},
		OrderSize: []float64{0.001},
	}
}

// RunResult holds the outcome of one parameter combination.
type RunResult struct {
	Params      model.Params `json:"params"`
	OrderSize   float64      `json:"orderSize"`
	Sharpe      float64      `json:"sharpe"`
	NetPnL      float64      `json:"netPnl"`
	ReturnPct   float64      `json:"returnPct"`
	MaxDrawdown float64      `json:"maxDrawdown"`
	WinRate     float64      `json:"winRate"`
	NumFills    int          `json:"numFills"`
}

// Config controls the optimization run.
type Config struct {
	DatasetPath    string
	Symbol         string
	InitialBalance float64
	Grid           ParamGrid
	MaxWorkers     int // parallel backtests (default: runtime.NumCPU())
}

// combo holds one parameter combination to evaluate.
type combo struct {
	params    model.Params
	orderSize float64
}

// cartesian produces every combination of parameter values in the grid.
func cartesian(grid ParamGrid) []combo {
	gammas := nonEmpty(grid.Gamma, []float64{0.1})
	kappas := nonEmpty(grid.Kappa, []float64{1.5})
	sigmas := nonEmpty(grid.Sigma, []float64{0.8})
	alphas := nonEmpty(grid.Alpha, []float64{1.0})
	sizes := nonEmpty(grid.OrderSize, []float64{0.001})

	var out []combo
	for _, g := range gammas {
		for _, k := range kappas {
			for _, s := range sigmas {
				for _, a := range alphas {
					for _, sz := range sizes {
						out = append(out, combo{
							params: model.Params{
								Gamma:        g,
								Kappa:        k,
								Sigma:        s,
								Horizon:      1.0 / 365,
								Alpha:        a,
								FundingEpoch: 1.0 / 365 / 3,
								MaxInventory: 0.1,
								MinSpread:    0.0001,
							},
							orderSize: sz,
						})
					}
				}
			}
		}
	}
	return out
}

func nonEmpty(vals []float64, def []float64) []float64 {
	if len(vals) == 0 {
		return def
	}
	return vals
}

// Optimize runs all parameter combinations concurrently and returns results
// sorted by Sharpe ratio descending.
func Optimize(cfg Config) ([]RunResult, error) {
	if cfg.InitialBalance <= 0 {
		cfg.InitialBalance = 10_000
	}
	workers := cfg.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	// Load snapshots once; share the slice across all goroutines (read-only).
	snapshots, err := backtest.LoadSnapshots(cfg.DatasetPath, cfg.Symbol)
	if err != nil {
		return nil, fmt.Errorf("load snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}

	combos := cartesian(cfg.Grid)

	// Semaphore to cap concurrency.
	sem := make(chan struct{}, workers)

	var (
		mu      sync.Mutex
		results []RunResult
		wg      sync.WaitGroup
	)

	for _, c := range combos {
		c := c // capture
		wg.Add(1)
		sem <- struct{}{} // acquire
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release

			r, err := runSingle(cfg.InitialBalance, cfg.Symbol, snapshots, c)
			if err != nil {
				// Skip failed runs silently.
				return
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Sort by Sharpe descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Sharpe > results[j].Sharpe
	})
	return results, nil
}

// runSingle executes one backtest for a given parameter combo and returns its result.
func runSingle(initialBalance float64, symbol string, snapshots []exchange.MarketSnapshot, c combo) (RunResult, error) {
	// Use a large event buffer so we can drain it after eng.Run returns without
	// a separate goroutine. The backtest exchange never closes its events channel,
	// so ranging over it in a goroutine would deadlock.
	eventBuf := len(snapshots) + 64
	ex := backtest.New(backtest.Config{
		InitialBalance: initialBalance,
		TakerFeeBps:    4,
		MakerFeeBps:    2,
		EventBuf:       eventBuf,
	}, snapshots)

	strategy, err := mmengine.New(mmengine.Config{
		Model:                 c.params,
		OrderSize:             c.orderSize,
		InventoryThreshold:    0.5,
		PauseFundingThreshold: 2.0,
		PauseVolatilityMult:   2.0,
		VolWindow:             50,
		TickInterval:          time.Minute,
	}, nil)
	if err != nil {
		return RunResult{}, fmt.Errorf("strategy: %w", err)
	}

	eng, err := core.NewEngine(strategy, ex, symbol, time.Nanosecond, nil, nil)
	if err != nil {
		return RunResult{}, fmt.Errorf("engine: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	eng.Run(ctx)

	// Drain the buffered events channel non-blocking to collect the equity curve.
	// The backtest exchange never closes its events channel, so we use select+default.
	equityCurve := make([]float64, 0, len(snapshots))
	for {
		select {
		case ev, ok := <-ex.Events():
			if !ok {
				goto drained
			}
			if ev.Kind == exchange.EventKindTick && ev.Equity > 0 {
				equityCurve = append(equityCurve, ev.Equity)
			}
		default:
			goto drained
		}
	}
drained:

	stats := ex.Stats()
	sharpe := computeSharpe(equityCurve)
	maxDD := computeMaxDrawdown(equityCurve)

	return RunResult{
		Params:      c.params,
		OrderSize:   c.orderSize,
		Sharpe:      sharpe,
		NetPnL:      stats.NetPnL,
		ReturnPct:   stats.ReturnPct(),
		MaxDrawdown: maxDD,
		WinRate:     stats.WinRate,
		NumFills:    stats.NumFills,
	}, nil
}

// computeSharpe computes the annualised Sharpe ratio (risk-free rate = 0).
func computeSharpe(curve []float64) float64 {
	n := len(curve)
	if n < 2 {
		return 0
	}
	returns := make([]float64, n-1)
	for i := 1; i < n; i++ {
		prev := curve[i-1]
		if prev == 0 {
			returns[i-1] = 0
		} else {
			returns[i-1] = (curve[i] - prev) / prev
		}
	}
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(returns) - 1)
	stddev := math.Sqrt(variance)
	if stddev == 0 {
		return 0
	}
	return mean / stddev * math.Sqrt(float64(n))
}

// computeMaxDrawdown returns the maximum drawdown as a fraction (0–1).
func computeMaxDrawdown(curve []float64) float64 {
	if len(curve) == 0 {
		return 0
	}
	peak := curve[0]
	maxDD := 0.0
	for _, v := range curve {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := (peak - v) / peak
			if dd > maxDD {
				maxDD = dd
			}
		}
	}
	return maxDD
}
