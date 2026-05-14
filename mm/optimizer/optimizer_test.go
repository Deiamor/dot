package optimizer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/mm/optimizer"
)

// base is a fixed reference time for synthetic snapshot timestamps.
var base = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// makeSyntheticSnapshots builds n snapshots with a simple linear price series
// starting at startPrice and ticking up by step each interval.
func makeSyntheticSnapshots(n int, startPrice, step float64) []exchange.MarketSnapshot {
	snaps := make([]exchange.MarketSnapshot, n)
	for i := range snaps {
		mid := startPrice + float64(i)*step
		halfSpread := mid * 0.00005
		snaps[i] = exchange.MarketSnapshot{
			Symbol:    "BTCUSDT",
			Mid:       mid,
			Bid:       mid - halfSpread,
			Ask:       mid + halfSpread,
			MarkPrice: mid,
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
	}
	return snaps
}

// writeSnapshotsAsKlines writes synthetic snapshots to a temporary JSON file
// in the Binance klines format so LoadSnapshots can read them.
// Each entry: [openTimeMs, open, high, low, close, volume, closeTimeMs,
//
//	quoteVol, numTrades, takerBuyBaseVol, takerBuyQuoteVol, ignore]
func writeSnapshotsAsKlines(t *testing.T, snaps []exchange.MarketSnapshot) string {
	t.Helper()
	type kline [12]interface{}
	records := make([]kline, len(snaps))
	for i, s := range snaps {
		mid := s.Mid
		records[i] = kline{
			float64(s.Timestamp.UnixMilli()), // openTime
			mid,                              // open
			mid + mid*0.001,                  // high
			mid - mid*0.001,                  // low
			mid,                              // close
			1.0,                              // volume
			float64(s.Timestamp.UnixMilli() + 60000), // closeTime
			1.0, 1, 0.5, 0.5, "0",
		}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal klines: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "BTCUSDT_5m_test.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write klines file: %v", err)
	}
	return path
}

// TestOptimizer_DefaultGrid_Builds verifies DefaultGrid produces the expected counts.
func TestOptimizer_DefaultGrid_Builds(t *testing.T) {
	g := optimizer.DefaultGrid()

	if len(g.Gamma) != 3 {
		t.Errorf("Gamma: want 3 values, got %d", len(g.Gamma))
	}
	if len(g.Kappa) != 3 {
		t.Errorf("Kappa: want 3 values, got %d", len(g.Kappa))
	}
	if len(g.Sigma) != 3 {
		t.Errorf("Sigma: want 3 values, got %d", len(g.Sigma))
	}
	if len(g.Alpha) != 2 {
		t.Errorf("Alpha: want 2 values, got %d", len(g.Alpha))
	}
	if len(g.OrderSize) != 1 {
		t.Errorf("OrderSize: want 1 value, got %d", len(g.OrderSize))
	}

	// Total combinations: 3 * 3 * 3 * 2 * 1 = 54
	total := len(g.Gamma) * len(g.Kappa) * len(g.Sigma) * len(g.Alpha) * len(g.OrderSize)
	if total != 54 {
		t.Errorf("DefaultGrid total combos: want 54, got %d", total)
	}
}

// TestOptimizer_SmallGrid runs a tiny 2×2 grid and verifies:
//   - the correct number of results is returned
//   - results are sorted by Sharpe descending
func TestOptimizer_SmallGrid(t *testing.T) {
	snaps := makeSyntheticSnapshots(150, 40_000, 10)
	path := writeSnapshotsAsKlines(t, snaps)

	grid := optimizer.ParamGrid{
		Gamma:     []float64{0.05, 0.2},
		Kappa:     []float64{1.0, 2.0},
		Sigma:     []float64{0.8},
		Alpha:     []float64{1.0},
		OrderSize: []float64{0.001},
	}

	cfg := optimizer.Config{
		DatasetPath:    path,
		Symbol:         "BTCUSDT",
		InitialBalance: 10_000,
		Grid:           grid,
		MaxWorkers:     2,
	}

	results, err := optimizer.Optimize(cfg)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	// 2 Gamma × 2 Kappa × 1 Sigma × 1 Alpha × 1 OrderSize = 4 combos.
	if len(results) != 4 {
		t.Errorf("results count: want 4, got %d", len(results))
	}

	// Verify sorted descending by Sharpe.
	for i := 1; i < len(results); i++ {
		if results[i].Sharpe > results[i-1].Sharpe {
			t.Errorf("results not sorted by Sharpe at index %d: %g > %g",
				i, results[i].Sharpe, results[i-1].Sharpe)
		}
	}
}

// TestOptimizer_ParallelSafe runs with MaxWorkers=4 to verify no data races.
// Run this test with -race to catch any concurrency issues.
func TestOptimizer_ParallelSafe(t *testing.T) {
	snaps := makeSyntheticSnapshots(100, 50_000, 5)
	path := writeSnapshotsAsKlines(t, snaps)

	grid := optimizer.ParamGrid{
		Gamma:     []float64{0.05, 0.1, 0.2},
		Kappa:     []float64{1.0, 1.5},
		Sigma:     []float64{0.8},
		Alpha:     []float64{1.0},
		OrderSize: []float64{0.001},
	}

	cfg := optimizer.Config{
		DatasetPath:    path,
		Symbol:         "BTCUSDT",
		InitialBalance: 10_000,
		Grid:           grid,
		MaxWorkers:     4,
	}

	results, err := optimizer.Optimize(cfg)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	// 3 × 2 × 1 × 1 × 1 = 6 combos.
	if len(results) != 6 {
		t.Errorf("results count: want 6, got %d", len(results))
	}

	// All results should have valid Params.
	for i, r := range results {
		if r.Params.Gamma <= 0 {
			t.Errorf("results[%d]: Gamma should be > 0, got %g", i, r.Params.Gamma)
		}
		if r.OrderSize <= 0 {
			t.Errorf("results[%d]: OrderSize should be > 0, got %g", i, r.OrderSize)
		}
	}
}
