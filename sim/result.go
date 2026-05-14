package sim

import (
	"math"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Result captures the full outcome of a simulation run.
type Result struct {
	// Per-event records.
	Fills       []exchange.TradingEvent
	EquityCurve []float64 // equity sampled on every tick

	// Account summary.
	InitialBalance float64
	FinalBalance   float64
	FinalEquity    float64

	// Performance.
	NetPnL      float64 // realised PnL minus fees
	TotalFees   float64
	NumFills    int
	WinRate     float64 // fraction of fills with positive realised PnL
	MaxDrawdown float64 // peak-to-trough fraction (0.05 = 5%)
	SharpeRatio float64 // annualised Sharpe (risk-free rate = 0)

	// Strategy.
	FinalState string
}

// ReturnPct returns the percentage return relative to the initial balance.
func (r Result) ReturnPct() float64 {
	if r.InitialBalance == 0 {
		return 0
	}
	return (r.FinalEquity - r.InitialBalance) / r.InitialBalance * 100
}

// ─── internal constructors ────────────────────────────────────────────────────

func buildResult(ex *simExchange, curve []float64, finalState string) Result {
	totalFees, totalPnL := 0.0, 0.0
	wins, losses := 0, 0
	for _, f := range ex.fills {
		totalFees += f.Fee
		totalPnL += f.RealizedPnL
		if f.RealizedPnL > 0 {
			wins++
		} else if f.RealizedPnL < 0 {
			losses++
		}
	}
	winRate := 0.0
	if wins+losses > 0 {
		winRate = float64(wins) / float64(wins+losses)
	}
	mid := ex.currentMid()
	finalEquity := ex.balance + ex.position*mid

	return Result{
		Fills:          ex.fills,
		EquityCurve:    curve,
		InitialBalance: ex.cfg.InitialBalance,
		FinalBalance:   ex.balance,
		FinalEquity:    finalEquity,
		NetPnL:         totalPnL - totalFees,
		TotalFees:      totalFees,
		NumFills:       len(ex.fills),
		WinRate:        winRate,
		MaxDrawdown:    maxDrawdown(curve),
		SharpeRatio:    sharpe(curve),
		FinalState:     finalState,
	}
}

func maxDrawdown(curve []float64) float64 {
	if len(curve) == 0 {
		return 0
	}
	peak, dd := curve[0], 0.0
	for _, v := range curve {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			if d := (peak - v) / peak; d > dd {
				dd = d
			}
		}
	}
	return dd
}

func sharpe(curve []float64) float64 {
	if len(curve) < 2 {
		return 0
	}
	returns := make([]float64, len(curve)-1)
	for i := 1; i < len(curve); i++ {
		if curve[i-1] != 0 {
			returns[i-1] = (curve[i] - curve[i-1]) / curve[i-1]
		}
	}
	n := float64(len(returns))
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= n
	variance := 0.0
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	if n > 1 {
		variance /= n - 1
	}
	if stddev := math.Sqrt(variance); stddev > 0 {
		return mean / stddev * math.Sqrt(n)
	}
	return 0
}
