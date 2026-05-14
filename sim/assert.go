package sim

import "fmt"

// TB is the subset of testing.TB used by Assertion, allowing use outside of
// Go's test runner (e.g. with custom loggers or CI reporters).
type TB interface {
	Errorf(format string, args ...any)
	Helper()
}

// Assertion chains validation checks on a Result.
// Obtain one with Assert(t, result).
type Assertion struct {
	t      TB
	result Result
}

// Assert begins an assertion chain for result, reporting failures via t.
func Assert(t TB, result Result) *Assertion {
	return &Assertion{t: t, result: result}
}

// MinFills fails if fewer than n fills occurred.
func (a *Assertion) MinFills(n int) *Assertion {
	a.t.Helper()
	if a.result.NumFills < n {
		a.t.Errorf("MinFills: got %d fills, want ≥ %d", a.result.NumFills, n)
	}
	return a
}

// MaxFills fails if more than n fills occurred.
func (a *Assertion) MaxFills(n int) *Assertion {
	a.t.Helper()
	if a.result.NumFills > n {
		a.t.Errorf("MaxFills: got %d fills, want ≤ %d", a.result.NumFills, n)
	}
	return a
}

// NoFills fails if any fills occurred.
func (a *Assertion) NoFills() *Assertion { return a.MaxFills(0) }

// PnLAbove fails if NetPnL ≤ threshold.
func (a *Assertion) PnLAbove(threshold float64) *Assertion {
	a.t.Helper()
	if a.result.NetPnL <= threshold {
		a.t.Errorf("PnLAbove: got NetPnL=%.4f, want > %.4f", a.result.NetPnL, threshold)
	}
	return a
}

// PnLBelow fails if NetPnL ≥ threshold.
func (a *Assertion) PnLBelow(threshold float64) *Assertion {
	a.t.Helper()
	if a.result.NetPnL >= threshold {
		a.t.Errorf("PnLBelow: got NetPnL=%.4f, want < %.4f", a.result.NetPnL, threshold)
	}
	return a
}

// ReturnAbove fails if ReturnPct() ≤ threshold (as a percentage, e.g. 1.0 = 1%).
func (a *Assertion) ReturnAbove(pct float64) *Assertion {
	a.t.Helper()
	if got := a.result.ReturnPct(); got <= pct {
		a.t.Errorf("ReturnAbove: got %.2f%%, want > %.2f%%", got, pct)
	}
	return a
}

// MaxDrawdownBelow fails if MaxDrawdown ≥ threshold (as a fraction, e.g. 0.05 = 5%).
func (a *Assertion) MaxDrawdownBelow(frac float64) *Assertion {
	a.t.Helper()
	if a.result.MaxDrawdown >= frac {
		a.t.Errorf("MaxDrawdownBelow: got %.4f (%.2f%%), want < %.4f",
			a.result.MaxDrawdown, a.result.MaxDrawdown*100, frac)
	}
	return a
}

// WinRateAbove fails if WinRate < threshold (0–1).
func (a *Assertion) WinRateAbove(threshold float64) *Assertion {
	a.t.Helper()
	if a.result.WinRate < threshold {
		a.t.Errorf("WinRateAbove: got %.2f%%, want ≥ %.2f%%",
			a.result.WinRate*100, threshold*100)
	}
	return a
}

// FinalState fails if the strategy did not end in the expected state.
func (a *Assertion) FinalState(want string) *Assertion {
	a.t.Helper()
	if a.result.FinalState != want {
		a.t.Errorf("FinalState: got %q, want %q", a.result.FinalState, want)
	}
	return a
}

// EquityLength fails if fewer than n equity points were recorded.
func (a *Assertion) EquityLength(n int) *Assertion {
	a.t.Helper()
	if len(a.result.EquityCurve) < n {
		a.t.Errorf("EquityLength: got %d points, want ≥ %d", len(a.result.EquityCurve), n)
	}
	return a
}

// Custom runs an arbitrary check fn against the Result, failing with msg if fn returns false.
func (a *Assertion) Custom(msg string, fn func(Result) bool) *Assertion {
	a.t.Helper()
	if !fn(a.result) {
		a.t.Errorf("Custom assertion failed: %s\n  result: %s", msg, formatResult(a.result))
	}
	return a
}

func formatResult(r Result) string {
	return fmt.Sprintf("fills=%d pnl=%.4f return=%.2f%% maxDD=%.2f%% state=%s",
		r.NumFills, r.NetPnL, r.ReturnPct(), r.MaxDrawdown*100, r.FinalState)
}
