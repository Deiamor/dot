package model_test

import (
	"math"
	"testing"

	"github.com/deiamor/perp-strategy-engine/mm/model"
)

func newModel(t *testing.T, p model.Params) *model.Model {
	t.Helper()
	m, err := model.New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func baseParams() model.Params {
	return model.Params{
		Gamma:        0.1,
		Kappa:        1.5,
		Sigma:        0.80,
		Horizon:      1.0 / 365,
		Alpha:        1.0,
		FundingEpoch: 1.0 / 365 / 3,
		MaxInventory: 10.0,
		MinSpread:    0.0001,
	}
}

// ─── Params.Validate ──────────────────────────────────────────────────────────

func TestValidate_ZeroGamma(t *testing.T) {
	p := baseParams()
	p.Gamma = 0
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for Gamma=0")
	}
}

func TestValidate_ZeroKappa(t *testing.T) {
	p := baseParams()
	p.Kappa = 0
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for Kappa=0")
	}
}

func TestValidate_ZeroSigma(t *testing.T) {
	p := baseParams()
	p.Sigma = 0
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for Sigma=0")
	}
}

func TestValidate_Valid(t *testing.T) {
	if err := baseParams().Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── Quotes: bid < mid < ask ──────────────────────────────────────────────────

func TestQuotes_BidAskOrdering(t *testing.T) {
	m := newModel(t, baseParams())
	bid, ask := m.Quotes(50000, 0, 0.0001, 0)
	if bid >= ask {
		t.Fatalf("expected bid < ask, got bid=%g ask=%g", bid, ask)
	}
	if bid <= 0 || ask <= 0 {
		t.Fatalf("bid/ask must be positive, got bid=%g ask=%g", bid, ask)
	}
}

func TestQuotes_ZeroInventory_SymmetricAroundReservation(t *testing.T) {
	m := newModel(t, baseParams())
	mid := 50000.0
	bid, ask := m.Quotes(mid, 0, 0, 0)
	// With q=0 and f=0 the reservation price equals mid.
	// Spread should be symmetric.
	bidSpread := mid - bid
	askSpread := ask - mid
	if math.Abs(bidSpread-askSpread) > 1e-6 {
		t.Fatalf("expected symmetric spread, bid_spread=%g ask_spread=%g", bidSpread, askSpread)
	}
}

func TestQuotes_LongInventory_BidBelowMid(t *testing.T) {
	m := newModel(t, baseParams())
	mid := 50000.0
	// Long inventory → MM wants to sell more → reservation price shifts down → bid further from mid.
	bid0, _ := m.Quotes(mid, 0, 0, 0)
	bidL, _ := m.Quotes(mid, 5, 0, 0)
	if bidL >= bid0 {
		t.Fatalf("expected long inventory to push bid down: bid(q=0)=%g bid(q=5)=%g", bid0, bidL)
	}
}

func TestQuotes_PositiveFunding_PushesReservationDown(t *testing.T) {
	// Positive funding rate: longs pay shorts → MM with long position reduces
	// reservation price (wants to sell, reduce long exposure).
	m := newModel(t, baseParams())
	mid := 50000.0
	_, _ = m.Quotes(mid, 1, 0, 0)
	r0 := m.ReservationPrice(mid, 1, 0, 0)
	rF := m.ReservationPrice(mid, 1, 0.01, 0) // 1% positive funding rate
	if rF >= r0 {
		t.Fatalf("positive funding should lower reservation: r0=%g rF=%g", r0, rF)
	}
}

func TestQuotes_MinSpreadFloor(t *testing.T) {
	// With a very short horizon, volatility term → 0, but MinSpread should floor the half-spread.
	p := baseParams()
	p.Horizon = 1e-10
	p.Sigma = 0.01
	p.MinSpread = 0.002 // 20 bps minimum
	m := newModel(t, p)
	mid := 10000.0
	bid, ask := m.Quotes(mid, 0, 0, 0)
	spread := ask - bid
	minSpread := 2 * p.MinSpread * mid
	if spread < minSpread-1e-6 {
		t.Fatalf("spread %g below MinSpread floor %g", spread, minSpread)
	}
}

func TestQuotes_HorizonExhausted_StillValid(t *testing.T) {
	m := newModel(t, baseParams())
	// elapsed > Horizon: model clamps remaining to epsilon.
	bid, ask := m.Quotes(50000, 0, 0, 10.0)
	if bid >= ask {
		t.Fatalf("expected bid < ask even past horizon, got bid=%g ask=%g", bid, ask)
	}
}

// ─── ReservationPrice formula ─────────────────────────────────────────────────

func TestReservationPrice_Formula(t *testing.T) {
	p := baseParams()
	p.Alpha = 1.0
	m := newModel(t, p)
	mid := 1000.0
	q := 2.0
	f := 0.001
	elapsed := 0.0

	remaining := p.Horizon - elapsed
	want := mid - q*p.Gamma*p.Sigma*p.Sigma*remaining - p.Alpha*f*q*p.FundingEpoch
	got := m.ReservationPrice(mid, q, f, elapsed)

	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("ReservationPrice: got=%g want=%g", got, want)
	}
}

// ─── HalfSpread formula ───────────────────────────────────────────────────────

func TestHalfSpread_Formula(t *testing.T) {
	p := baseParams()
	m := newModel(t, p)
	elapsed := 0.0
	remaining := p.Horizon - elapsed

	want := p.Gamma*p.Sigma*p.Sigma*remaining/2 + (1/p.Gamma)*math.Log(1+p.Gamma/p.Kappa)
	got := m.HalfSpread(elapsed)

	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("HalfSpread: got=%g want=%g", got, want)
	}
}

// ─── InventorySkew ────────────────────────────────────────────────────────────

func TestInventorySkew_LongPositive(t *testing.T) {
	m := newModel(t, baseParams())
	skew := m.InventorySkew(5.0, 0)
	if skew <= 0 {
		t.Fatalf("expected positive skew for long inventory, got %g", skew)
	}
}

func TestInventorySkew_ShortNegative(t *testing.T) {
	m := newModel(t, baseParams())
	skew := m.InventorySkew(-5.0, 0)
	if skew >= 0 {
		t.Fatalf("expected negative skew for short inventory, got %g", skew)
	}
}

// ─── FundingSkew ──────────────────────────────────────────────────────────────

func TestFundingSkew_PositiveRate_PositiveInventory(t *testing.T) {
	m := newModel(t, baseParams())
	skew := m.FundingSkew(3.0, 0.001)
	if skew <= 0 {
		t.Fatalf("expected positive funding skew (long pays), got %g", skew)
	}
}

func TestFundingSkew_ZeroAlpha(t *testing.T) {
	p := baseParams()
	p.Alpha = 0
	m := newModel(t, p)
	skew := m.FundingSkew(5.0, 0.01)
	if skew != 0 {
		t.Fatalf("expected zero skew with Alpha=0, got %g", skew)
	}
}

// ─── SkewedQty ───────────────────────────────────────────────────────────────

func TestSkewedQty_WithinLimit_Unchanged(t *testing.T) {
	m := newModel(t, baseParams())
	got := m.SkewedQty(1.0, 3.0, +1) // projected = 4.0, limit = 10.0
	if got != 1.0 {
		t.Fatalf("expected unchanged qty 1.0, got %g", got)
	}
}

func TestSkewedQty_ExceedsLimit_Clamped(t *testing.T) {
	m := newModel(t, baseParams())
	// inventory = 9.5, qty = 2.0, max = 10.0 → allowed = 0.5
	got := m.SkewedQty(2.0, 9.5, +1)
	if math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("expected clamped qty 0.5, got %g", got)
	}
}

func TestSkewedQty_AtLimit_Zero(t *testing.T) {
	m := newModel(t, baseParams())
	got := m.SkewedQty(1.0, 10.0, +1)
	if got != 0 {
		t.Fatalf("expected zero qty at limit, got %g", got)
	}
}

func TestSkewedQty_NoMaxInventory(t *testing.T) {
	p := baseParams()
	p.MaxInventory = 0
	m := newModel(t, p)
	got := m.SkewedQty(1.0, 1000.0, +1) // no limit
	if got != 1.0 {
		t.Fatalf("expected unchanged qty without MaxInventory, got %g", got)
	}
}

// ─── UpdateSigma ─────────────────────────────────────────────────────────────

func TestUpdateSigma_Positive_Updated(t *testing.T) {
	m := newModel(t, baseParams())
	before := m.Params().Sigma
	m.UpdateSigma(before * 2)
	if math.Abs(m.Params().Sigma-before*2) > 1e-9 {
		t.Fatal("UpdateSigma should update sigma")
	}
}

func TestUpdateSigma_Zero_Ignored(t *testing.T) {
	m := newModel(t, baseParams())
	before := m.Params().Sigma
	m.UpdateSigma(0)
	if m.Params().Sigma != before {
		t.Fatal("UpdateSigma(0) should be ignored")
	}
}
