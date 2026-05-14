// Package primitives provides ready-to-use FSM Strategy implementations
// that can be used standalone or composed into larger strategies.
package primitives

import (
	"fmt"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// VWAP states.
const (
	VWAPStateIdle      = "IDLE"
	VWAPStateExecuting = "EXECUTING"
	VWAPStateDone      = "DONE"
)

// VWAPConfig controls the VWAP execution strategy.
type VWAPConfig struct {
	// Symbol is the trading pair (e.g. "BTC-USDC-PERP").
	Symbol string

	// Side is BUY or SELL.
	Side exchange.Side

	// TotalQty is the total base asset quantity to execute.
	TotalQty float64

	// Slices is the number of child orders to split TotalQty into.
	Slices int

	// WindowTicks is the rolling window size for the VWAP calculation (e.g. 20).
	WindowTicks int

	// DeviationBps is the minimum deviation from VWAP in basis points required
	// to trigger a slice order (e.g. 10 = 0.1%).
	// Set to 0 to always place regardless of deviation (same as TWAP).
	DeviationBps float64

	// LimitPriceBps, when > 0, places limit orders at bid (for buys) or ask (for sells).
	// When 0, falls through to bid/ask directly.
	LimitPriceBps float64
}

// VWAPStrategy executes a VWAP (time-weighted approximation) order as a declarative FSM.
// It implements core.Strategy.
//
// State graph:
//
//	IDLE → EXECUTING → DONE (terminal)
type VWAPStrategy struct {
	cfg        VWAPConfig
	sentSlices int
	window     []float64 // rolling mid-price window
	vwap       float64   // current rolling average
}

// NewVWAP creates a VWAPStrategy ready to pass to core.NewEngine.
func NewVWAP(cfg VWAPConfig) (*VWAPStrategy, error) {
	if cfg.TotalQty <= 0 {
		return nil, fmt.Errorf("TotalQty must be > 0")
	}
	if cfg.Slices <= 0 {
		return nil, fmt.Errorf("Slices must be > 0")
	}
	if cfg.WindowTicks <= 0 {
		return nil, fmt.Errorf("WindowTicks must be > 0")
	}
	return &VWAPStrategy{cfg: cfg}, nil
}

// Name implements core.Strategy.
func (v *VWAPStrategy) Name() string { return "vwap" }

// Initial implements core.Strategy.
func (v *VWAPStrategy) Initial() string { return VWAPStateIdle }

// SentSlices returns the number of slices placed so far.
func (v *VWAPStrategy) SentSlices() int { return v.sentSlices }

// VWAP returns the current rolling VWAP estimate.
func (v *VWAPStrategy) VWAP() float64 { return v.vwap }

// States implements core.Strategy.
func (v *VWAPStrategy) States() []core.State {
	return []core.State{
		{
			Name: VWAPStateIdle,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("VWAP idle",
					"symbol", v.cfg.Symbol,
					"side", v.cfg.Side,
					"total_qty", v.cfg.TotalQty,
					"slices", v.cfg.Slices,
					"window_ticks", v.cfg.WindowTicks,
					"deviation_bps", v.cfg.DeviationBps,
				)
				return nil
			},
		},
		{
			Name:   VWAPStateExecuting,
			OnTick: v.onTick,
		},
		{
			Name: VWAPStateDone,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("VWAP complete",
					"placed_slices", v.sentSlices,
					"symbol", v.cfg.Symbol,
					"final_vwap", fmt.Sprintf("%.4f", v.vwap),
				)
				return nil
			},
		},
	}
}

// Transitions implements core.Strategy.
func (v *VWAPStrategy) Transitions() []core.Transition {
	return []core.Transition{
		// IDLE → EXECUTING when market price is available.
		{
			From:  VWAPStateIdle,
			To:    VWAPStateExecuting,
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid > 0 },
		},
		// EXECUTING → DONE when all slices have been placed.
		{
			From:  VWAPStateExecuting,
			To:    VWAPStateDone,
			Guard: func(_ *core.Context) bool { return v.sentSlices >= v.cfg.Slices },
		},
	}
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (v *VWAPStrategy) onTick(ctx *core.Context) error {
	if v.sentSlices >= v.cfg.Slices {
		return nil
	}

	// Update rolling VWAP window.
	v.updateVWAP(ctx.Market.Mid)

	// Check deviation gate.
	if !v.deviationMet(ctx.Market) {
		ctx.Log.Info("VWAP deviation gate: skipping slice",
			"vwap", fmt.Sprintf("%.4f", v.vwap),
			"mid", fmt.Sprintf("%.4f", ctx.Market.Mid),
		)
		return nil
	}

	// Cancel any existing open orders before placing the next slice.
	if err := ctx.Exchange.CancelAllOrders(ctx.GoCtx, v.cfg.Symbol); err != nil {
		ctx.Log.Warn("VWAP cancel orders failed", "err", err)
	}

	sliceQty := v.sliceQty()
	price := v.limitPrice(ctx.Market)

	req := exchange.PlaceOrderRequest{
		Symbol: v.cfg.Symbol,
		Side:   v.cfg.Side,
		Type:   exchange.Limit,
		Price:  price,
		Qty:    sliceQty,
	}

	_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, req)
	if err != nil {
		ctx.Log.Error("VWAP slice failed", "slice", v.sentSlices+1, "err", err)
		return nil // non-fatal; retry next tick
	}

	v.sentSlices++
	ctx.Log.Info("VWAP slice placed",
		"slice", v.sentSlices,
		"of", v.cfg.Slices,
		"qty", sliceQty,
		"price", fmt.Sprintf("%.4f", price),
		"vwap", fmt.Sprintf("%.4f", v.vwap),
	)
	return nil
}

// updateVWAP adds a price to the rolling window and recalculates the average.
func (v *VWAPStrategy) updateVWAP(mid float64) {
	v.window = append(v.window, mid)
	if len(v.window) > v.cfg.WindowTicks {
		v.window = v.window[len(v.window)-v.cfg.WindowTicks:]
	}

	// Compute simple average (time-weighted approximation of VWAP).
	sum := 0.0
	for _, p := range v.window {
		sum += p
	}
	v.vwap = sum / float64(len(v.window))
}

// deviationMet returns true when the current price deviates from VWAP enough
// to justify placing a slice, or when DeviationBps == 0.
func (v *VWAPStrategy) deviationMet(snap exchange.MarketSnapshot) bool {
	if v.cfg.DeviationBps == 0 {
		return true
	}
	if v.vwap == 0 {
		return false
	}
	threshold := v.cfg.DeviationBps / 10_000

	if v.cfg.Side == exchange.Buy {
		// Buy when ask is below vwap * (1 + threshold) — price is favourable.
		return snap.Ask < v.vwap*(1+threshold)
	}
	// Sell when bid is above vwap * (1 - threshold) — price is favourable.
	return snap.Bid > v.vwap*(1-threshold)
}

func (v *VWAPStrategy) sliceQty() float64 {
	perSlice := v.cfg.TotalQty / float64(v.cfg.Slices)
	remaining := v.cfg.TotalQty - float64(v.sentSlices)*perSlice
	if remaining < perSlice {
		return remaining
	}
	return perSlice
}

// limitPrice returns the limit order price: bid for buys, ask for sells.
// If LimitPriceBps > 0, the price is adjusted by that many bps from mid.
func (v *VWAPStrategy) limitPrice(snap exchange.MarketSnapshot) float64 {
	if v.cfg.LimitPriceBps > 0 {
		bps := v.cfg.LimitPriceBps / 10_000
		if v.cfg.Side == exchange.Buy {
			return snap.Mid * (1 + bps)
		}
		return snap.Mid * (1 - bps)
	}
	if v.cfg.Side == exchange.Buy {
		return snap.Bid
	}
	return snap.Ask
}
