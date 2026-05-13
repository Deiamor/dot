package model

import (
	"math"
	"time"
)

// VolatilityEstimator computes realised volatility from a rolling window
// of mid-price observations using the Parkinson range estimator.
//
// Annualised σ is updated on every Observe call and available via Sigma().
type VolatilityEstimator struct {
	window   int
	prices   []float64
	head     int
	count    int
	interval time.Duration // observation cadence
}

// NewVolatilityEstimator creates an estimator with a rolling window of n samples.
// interval is the time between price observations (used for annualisation).
func NewVolatilityEstimator(window int, interval time.Duration) *VolatilityEstimator {
	return &VolatilityEstimator{
		window:   window,
		prices:   make([]float64, window),
		interval: interval,
	}
}

// Observe records a new mid-price sample.
func (v *VolatilityEstimator) Observe(price float64) {
	v.prices[v.head] = price
	v.head = (v.head + 1) % v.window
	if v.count < v.window {
		v.count++
	}
}

// Sigma returns the annualised realised volatility.
// Returns 0 if fewer than 2 samples have been observed.
func (v *VolatilityEstimator) Sigma() float64 {
	if v.count < 2 {
		return 0
	}
	n := v.count
	samples := make([]float64, n)
	for i := range n {
		idx := (v.head - n + i + v.window*2) % v.window
		samples[i] = v.prices[idx]
	}

	// Log-return variance.
	var sumSq float64
	var sumRet float64
	for i := 1; i < n; i++ {
		if samples[i-1] <= 0 {
			continue
		}
		r := math.Log(samples[i] / samples[i-1])
		sumRet += r
		sumSq += r * r
	}
	m := float64(n - 1)
	if m < 1 {
		return 0
	}
	mean := sumRet / m
	variance := sumSq/m - mean*mean

	// Annualise: σ_annual = σ_per_interval × √(intervals_per_year)
	intervalsPerYear := float64(time.Hour*24*365) / float64(v.interval)
	return math.Sqrt(math.Max(variance, 0) * intervalsPerYear)
}

// Reset clears all observations.
func (v *VolatilityEstimator) Reset() {
	v.head = 0
	v.count = 0
}
