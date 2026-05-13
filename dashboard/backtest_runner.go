package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/backtest"
	mmengine "github.com/deiamor/perp-strategy-engine/mm/engine"
	"github.com/deiamor/perp-strategy-engine/mm/model"
)

// BacktestConfig is sent from the browser to POST /api/backtest.
type BacktestConfig struct {
	DatasetPath    string  `json:"datasetPath"`
	InitialBalance float64 `json:"initialBalance"`
	Gamma          float64 `json:"gamma"`
	Kappa          float64 `json:"kappa"`
	Sigma          float64 `json:"sigma"`
	Alpha          float64 `json:"alpha"`
	OrderSize      float64 `json:"orderSize"`
	MaxInventory   float64 `json:"maxInventory"`
}

// BacktestResult is returned to the browser after a backtest run.
type BacktestResult struct {
	NetPnL      float64   `json:"netPnl"`
	ReturnPct   float64   `json:"returnPct"`
	TotalFees   float64   `json:"totalFees"`
	NumFills    int       `json:"numFills"`
	WinRate     float64   `json:"winRate"`
	FinalEquity float64   `json:"finalEquity"`
	EquityCurve []float64 `json:"equityCurve"`
}

// RunBacktest loads snapshots, runs the MM strategy through a BacktestExchange,
// and returns the result. It is called synchronously from the HTTP handler.
func RunBacktest(cfg BacktestConfig, symbol string) (*BacktestResult, error) {
	if cfg.InitialBalance <= 0 {
		cfg.InitialBalance = 10_000
	}

	// Apply sensible defaults for any zero-valued params.
	if cfg.Gamma <= 0 {
		cfg.Gamma = 0.1
	}
	if cfg.Kappa <= 0 {
		cfg.Kappa = 1.5
	}
	if cfg.Sigma <= 0 {
		cfg.Sigma = 0.80
	}
	if cfg.Alpha <= 0 {
		cfg.Alpha = 1.0
	}
	if cfg.OrderSize <= 0 {
		cfg.OrderSize = 0.001
	}
	if cfg.MaxInventory <= 0 {
		cfg.MaxInventory = 0.1
	}

	snapshots, err := backtest.LoadSnapshots(cfg.DatasetPath, symbol)
	if err != nil {
		return nil, fmt.Errorf("load snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}

	ex := backtest.New(backtest.Config{
		InitialBalance: cfg.InitialBalance,
		TakerFeeBps:    4, // 0.04%
		MakerFeeBps:    2, // 0.02%
	}, snapshots)

	strategy, err := mmengine.New(mmengine.Config{
		Model: model.Params{
			Gamma:        cfg.Gamma,
			Kappa:        cfg.Kappa,
			Sigma:        cfg.Sigma,
			Horizon:      1.0 / 365,
			Alpha:        cfg.Alpha,
			FundingEpoch: 1.0 / 365 / 3,
			MaxInventory: cfg.MaxInventory,
			MinSpread:    0.0001,
		},
		OrderSize:             cfg.OrderSize,
		InventoryThreshold:    0.5,
		PauseFundingThreshold: 2.0,
		PauseVolatilityMult:   2.0,
		VolWindow:             50,
		TickInterval:          time.Minute,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("strategy: %w", err)
	}

	// Drain events into equity curve while engine runs.
	equityCurve := make([]float64, 0, len(snapshots))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ex.Events() {
			if ev.Kind == exchange.EventKindTick && ev.Equity > 0 {
				equityCurve = append(equityCurve, ev.Equity)
			}
		}
	}()

	eng, err := core.NewEngine(strategy, ex, symbol, time.Nanosecond, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	eng.Run(ctx) //nolint — stops when ErrTerminal

	// Close events channel so the drain goroutine exits.
	// The backtest exchange's events channel is unbuffered-drained: just wait.
	<-done

	stats := ex.Stats()
	return &BacktestResult{
		NetPnL:      stats.NetPnL,
		ReturnPct:   stats.ReturnPct(),
		TotalFees:   stats.TotalFees,
		NumFills:    stats.NumFills,
		WinRate:     stats.WinRate,
		FinalEquity: stats.FinalEquity,
		EquityCurve: equityCurve,
	}, nil
}
