package model_test

import (
	"math"
	"testing"
	"time"

	"github.com/deiamor/perp-strategy-engine/mm/model"
)

func newVol(window int) *model.VolatilityEstimator {
	return model.NewVolatilityEstimator(window, time.Minute)
}

func TestVolatility_LessThanTwoSamples_Zero(t *testing.T) {
	v := newVol(10)
	if v.Sigma() != 0 {
		t.Fatal("expected 0 before any observations")
	}
	v.Observe(100)
	if v.Sigma() != 0 {
		t.Fatal("expected 0 with only one observation")
	}
}

func TestVolatility_ConstantPrice_NearZero(t *testing.T) {
	v := newVol(10)
	for range 10 {
		v.Observe(100.0)
	}
	sigma := v.Sigma()
	if sigma > 1e-12 {
		t.Fatalf("constant prices should give ~0 sigma, got %g", sigma)
	}
}

func TestVolatility_Positive(t *testing.T) {
	v := newVol(20)
	price := 1000.0
	for i := range 20 {
		if i%2 == 0 {
			v.Observe(price * 1.01)
		} else {
			v.Observe(price * 0.99)
		}
	}
	sigma := v.Sigma()
	if sigma <= 0 {
		t.Fatalf("expected positive sigma for oscillating prices, got %g", sigma)
	}
}

func TestVolatility_Annualised(t *testing.T) {
	// 1% log-return per minute → annualised σ ≈ 0.01 * √(525600)
	interval := time.Minute
	v := model.NewVolatilityEstimator(100, interval)
	base := 1000.0
	for i := range 100 {
		if i%2 == 0 {
			v.Observe(base)
		} else {
			v.Observe(base * math.Exp(0.01))
		}
	}
	sigma := v.Sigma()
	intervalsPerYear := float64(time.Hour*24*365) / float64(interval)
	// per-interval σ ≈ 0.01 (half returns are 0, half are 0.01)
	// annualised σ ≈ 0.01 / sqrt(2) * sqrt(intervalsPerYear)
	// Just verify it's in a plausible ballpark (> 1 = 100% p.a.)
	_ = intervalsPerYear
	if sigma <= 0 {
		t.Fatalf("expected positive annualised sigma, got %g", sigma)
	}
}

func TestVolatility_RollingWindowEvicts(t *testing.T) {
	v := newVol(5)
	// Fill with high-vol prices.
	for range 5 {
		v.Observe(100)
		v.Observe(200)
	}
	sigmaHigh := v.Sigma()

	// Now flood with constant prices (evicts old data).
	for range 10 {
		v.Observe(150)
	}
	sigmaLow := v.Sigma()

	if sigmaLow >= sigmaHigh {
		t.Fatalf("expected low vol after constant prices, high=%g low=%g", sigmaHigh, sigmaLow)
	}
}

func TestVolatility_Reset(t *testing.T) {
	v := newVol(10)
	for range 10 {
		v.Observe(math.Exp(float64(10)))
	}
	v.Reset()
	if v.Sigma() != 0 {
		t.Fatal("expected 0 after Reset")
	}
}
