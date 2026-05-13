// Package primitives provides ready-to-use FSM Strategy implementations
// that can be used standalone or composed into larger strategies.
package primitives

import (
	"fmt"
	"math"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// TWAP states.
const (
	TWAPStateIdle     = "IDLE"
	TWAPStateExecuting = "EXECUTING"
	TWAPStateDone     = "DONE"
)

// TWAPConfig controls the time-weighted average price execution strategy.
type TWAPConfig struct {
	// Symbol is the trading pair (e.g. "BTC-USDC-PERP").
	Symbol string

	// Side is BUY or SELL.
	Side exchange.Side

	// TotalQty is the total base asset quantity to execute.
	TotalQty float64

	// Slices is the number of child orders to split TotalQty into.
	// Each slice fires on a separate tick.
	Slices int

	// Duration is the total wall-clock time over which to execute.
	// The engine TickInterval should divide this evenly.
	Duration time.Duration

	// LimitPriceBps, when > 0, adds a limit-price fence:
	//   BUY:  limit = mid × (1 + LimitPriceBps/10_000)
	//   SELL: limit = mid × (1 − LimitPriceBps/10_000)
	// Set to 0 to use market orders.
	LimitPriceBps float64
}

// TWAPStrategy executes a TWAP order as a declarative FSM.
// It implements core.Strategy.
//
// State graph:
//
//	IDLE → EXECUTING → DONE (terminal)
type TWAPStrategy struct {
	cfg     TWAPConfig
	sent    int     // slices placed so far
	filled  float64 // cumulative filled quantity (informational)
}

// NewTWAP creates a TWAPStrategy ready to pass to core.NewEngine.
func NewTWAP(cfg TWAPConfig) (*TWAPStrategy, error) {
	if cfg.TotalQty <= 0 {
		return nil, fmt.Errorf("TotalQty must be > 0")
	}
	if cfg.Slices <= 0 {
		return nil, fmt.Errorf("Slices must be > 0")
	}
	if cfg.Duration <= 0 {
		return nil, fmt.Errorf("Duration must be > 0")
	}
	return &TWAPStrategy{cfg: cfg}, nil
}

// Name implements core.Strategy.
func (t *TWAPStrategy) Name() string { return "twap" }

// Initial implements core.Strategy.
func (t *TWAPStrategy) Initial() string { return TWAPStateIdle }

// States implements core.Strategy.
func (t *TWAPStrategy) States() []core.State {
	return []core.State{
		{
			Name: TWAPStateIdle,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("TWAP idle",
					"symbol", t.cfg.Symbol,
					"side", t.cfg.Side,
					"total_qty", t.cfg.TotalQty,
					"slices", t.cfg.Slices,
					"duration", t.cfg.Duration,
				)
				return nil
			},
		},
		{
			Name:   TWAPStateExecuting,
			OnEnter: t.placeSlice,
			OnTick:  t.placeSlice,
		},
		{
			Name: TWAPStateDone,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("TWAP complete", "placed_slices", t.sent, "symbol", t.cfg.Symbol)
				return nil
			},
		},
	}
}

// Transitions implements core.Strategy.
func (t *TWAPStrategy) Transitions() []core.Transition {
	return []core.Transition{
		// IDLE → EXECUTING when market price is available.
		{
			From:  TWAPStateIdle,
			To:    TWAPStateExecuting,
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid > 0 },
		},
		// EXECUTING → DONE when all slices have been placed.
		{
			From:  TWAPStateExecuting,
			To:    TWAPStateDone,
			Guard: func(_ *core.Context) bool { return t.sent >= t.cfg.Slices },
		},
	}
}

// FilledQty returns the cumulative quantity filled so far.
// This is informational only (based on fills seen in OpenOrders, not confirmed fills).
func (t *TWAPStrategy) SentSlices() int { return t.sent }

// ─── internal ─────────────────────────────────────────────────────────────────

func (t *TWAPStrategy) placeSlice(ctx *core.Context) error {
	if t.sent >= t.cfg.Slices {
		return nil
	}

	sliceQty := t.sliceQty()

	var orderType exchange.OrderType
	var price float64

	if t.cfg.LimitPriceBps > 0 {
		orderType = exchange.Limit
		price = t.limitPrice(ctx.Market.Mid)
	} else {
		orderType = exchange.Market
	}

	req := exchange.PlaceOrderRequest{
		Symbol: t.cfg.Symbol,
		Side:   t.cfg.Side,
		Type:   orderType,
		Price:  price,
		Qty:    sliceQty,
	}

	_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, req)
	if err != nil {
		ctx.Log.Error("TWAP slice failed", "slice", t.sent+1, "err", err)
		return nil // non-fatal; retry next tick
	}

	t.sent++
	ctx.Log.Info("TWAP slice placed",
		"slice", t.sent,
		"of", t.cfg.Slices,
		"qty", sliceQty,
		"type", orderType,
		"price", fmt.Sprintf("%.4f", price),
	)
	return nil
}

func (t *TWAPStrategy) sliceQty() float64 {
	remaining := t.cfg.TotalQty - float64(t.sent)*t.perSliceQty()
	return math.Min(t.perSliceQty(), remaining)
}

func (t *TWAPStrategy) perSliceQty() float64 {
	return t.cfg.TotalQty / float64(t.cfg.Slices)
}

func (t *TWAPStrategy) limitPrice(mid float64) float64 {
	bps := t.cfg.LimitPriceBps / 10_000
	if t.cfg.Side == exchange.Buy {
		return mid * (1 + bps)
	}
	return mid * (1 - bps)
}
