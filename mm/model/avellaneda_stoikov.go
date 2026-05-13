// Package model implements the funding-aware optimal market making model
// for perpetual DEXes, based on:
//
//	"Funding-Aware Optimal Market Making for Perpetual DEXs"
//	arXiv:2605.06405
//
// The classical Avellaneda-Stoikov (2008) model is extended to incorporate
// perpetual funding payments as a stochastic cost/revenue that shifts the
// market maker's reservation price and optimal spread.
//
// # Core equations
//
// Reservation price (inventory + funding adjusted):
//
//	r = s - q·γ·σ²·(T−t) − α·f·q·Δt
//
// where:
//   - s         = current mid price
//   - q         = signed inventory (+ long, − short)
//   - γ         = risk aversion coefficient
//   - σ         = realized volatility (annualised)
//   - T−t       = remaining horizon (years)
//   - α         = funding sensitivity [0, 1]
//   - f         = current funding rate (per epoch)
//   - Δt        = funding epoch duration (years)
//
// Optimal half-spread:
//
//	δ = γ·σ²·(T−t)/2 + (1/γ)·ln(1 + γ/κ)
//
// where κ = order arrival intensity (fills per unit time).
//
// Optimal quotes:
//
//	bid = r − δ
//	ask = r + δ
package model

import (
	"fmt"
	"math"
)

// Params contains the calibrated model parameters.
// All fields must be positive unless noted.
type Params struct {
	// Gamma is risk aversion [0.01, 2.0].
	// Higher γ → wider spreads, stronger inventory skew.
	Gamma float64

	// Kappa is the order arrival intensity (fills per second).
	// Estimate from historical fill rate on the target venue.
	Kappa float64

	// Sigma is realised volatility, annualised (e.g. 0.80 = 80% p.a.).
	// Updated continuously by the VolatilityEstimator.
	Sigma float64

	// Horizon is the strategy time horizon in years (e.g. 1/365 = 1 day).
	Horizon float64

	// Alpha is the funding sensitivity coefficient [0, 1].
	// 0 = ignore funding (classical A-S); 1 = full funding adjustment.
	Alpha float64

	// FundingEpoch is the duration of one funding epoch in years
	// (e.g. 1/365/3 for 8-hour epochs on most perp DEXes).
	FundingEpoch float64

	// MaxInventory caps the absolute position in base units.
	// Orders that would push |q| above this are scaled down.
	MaxInventory float64

	// MinSpread is a floor on the quoted half-spread to cover execution costs.
	// Expressed as a fraction of mid price (e.g. 0.0001 = 1 bps).
	MinSpread float64
}

// Validate returns an error if any required parameter is invalid.
func (p Params) Validate() error {
	if p.Gamma <= 0 {
		return fmt.Errorf("Gamma must be > 0, got %g", p.Gamma)
	}
	if p.Kappa <= 0 {
		return fmt.Errorf("Kappa must be > 0, got %g", p.Kappa)
	}
	if p.Sigma <= 0 {
		return fmt.Errorf("Sigma must be > 0, got %g", p.Sigma)
	}
	if p.Horizon <= 0 {
		return fmt.Errorf("Horizon must be > 0, got %g", p.Horizon)
	}
	if p.FundingEpoch <= 0 {
		return fmt.Errorf("FundingEpoch must be > 0, got %g", p.FundingEpoch)
	}
	return nil
}

// Model is a calibrated instance of the funding-aware A-S model.
type Model struct {
	p Params
}

// New creates a Model with the given parameters.
func New(p Params) (*Model, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Model{p: p}, nil
}

// Quotes computes the optimal bid and ask prices given current market conditions.
//
// Parameters:
//   - mid         current mid price
//   - inventory   signed position in base asset (+ long, − short)
//   - fundingRate current funding rate per epoch (e.g. 0.0001 = 0.01%)
//   - elapsed     time elapsed since strategy start, in years
//
// Returns bid < ask. Both are positive if inputs are well-formed.
func (m *Model) Quotes(mid, inventory, fundingRate, elapsed float64) (bid, ask float64) {
	remaining := m.p.Horizon - elapsed
	if remaining <= 0 {
		remaining = 1e-9
	}

	r := m.reservationPrice(mid, inventory, fundingRate, remaining)
	delta := m.halfSpread(remaining)

	// Apply MinSpread floor.
	minDelta := mid * m.p.MinSpread
	if delta < minDelta {
		delta = minDelta
	}

	return r - delta, r + delta
}

// ReservationPrice returns the skewed mid price, without spread.
// Useful for monitoring inventory bias independently of spread.
func (m *Model) ReservationPrice(mid, inventory, fundingRate, elapsed float64) float64 {
	remaining := m.p.Horizon - elapsed
	if remaining <= 0 {
		remaining = 1e-9
	}
	return m.reservationPrice(mid, inventory, fundingRate, remaining)
}

// HalfSpread returns the optimal half-spread for the given remaining horizon.
func (m *Model) HalfSpread(elapsed float64) float64 {
	remaining := m.p.Horizon - elapsed
	if remaining <= 0 {
		remaining = 1e-9
	}
	return m.halfSpread(remaining)
}

// InventorySkew returns the price skew caused by inventory risk alone.
// Positive = bid pushed down (long inventory, want to sell).
// Negative = ask pushed up  (short inventory, want to buy).
func (m *Model) InventorySkew(inventory float64, elapsed float64) float64 {
	remaining := m.p.Horizon - elapsed
	if remaining <= 0 {
		remaining = 1e-9
	}
	return inventory * m.p.Gamma * m.p.Sigma * m.p.Sigma * remaining
}

// FundingSkew returns the price adjustment from funding payments alone.
func (m *Model) FundingSkew(inventory, fundingRate float64) float64 {
	return m.p.Alpha * fundingRate * inventory * m.p.FundingEpoch
}

// SkewedQty adjusts an order quantity to avoid breaching MaxInventory.
// Returns the reduced quantity (may be 0 if already at limit).
func (m *Model) SkewedQty(qty, inventory float64, side float64) float64 {
	if m.p.MaxInventory <= 0 {
		return qty
	}
	// side = +1 for buy, -1 for sell
	projected := inventory + side*qty
	if math.Abs(projected) <= m.p.MaxInventory {
		return qty
	}
	allowed := m.p.MaxInventory - math.Abs(inventory)
	if allowed < 0 {
		return 0
	}
	return allowed
}

// UpdateSigma replaces the volatility estimate.
// Call this periodically with fresh realised-vol estimates.
func (m *Model) UpdateSigma(sigma float64) {
	if sigma > 0 {
		m.p.Sigma = sigma
	}
}

// Params returns a copy of the current model parameters.
func (m *Model) Params() Params { return m.p }

// ─── internal ─────────────────────────────────────────────────────────────────

// reservationPrice implements:
//
//	r = s - q·γ·σ²·(T−t) − α·f·q·Δt
func (m *Model) reservationPrice(mid, inventory, fundingRate, remaining float64) float64 {
	inventoryTerm := inventory * m.p.Gamma * m.p.Sigma * m.p.Sigma * remaining
	fundingTerm := m.p.Alpha * fundingRate * inventory * m.p.FundingEpoch
	return mid - inventoryTerm - fundingTerm
}

// halfSpread implements:
//
//	δ = γ·σ²·(T−t)/2 + (1/γ)·ln(1 + γ/κ)
func (m *Model) halfSpread(remaining float64) float64 {
	volatilityTerm := m.p.Gamma * m.p.Sigma * m.p.Sigma * remaining / 2
	arrivalTerm := (1 / m.p.Gamma) * math.Log(1+m.p.Gamma/m.p.Kappa)
	return volatilityTerm + arrivalTerm
}
