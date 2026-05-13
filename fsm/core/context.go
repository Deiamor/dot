package core

import (
	"context"
	"log/slog"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Context carries live market state and execution facilities
// to every State handler and Transition guard.
//
// Context is re-populated by the Engine before each tick;
// handlers must not cache it across ticks.
type Context struct {
	// Symbol is the trading pair, e.g. "BTC-USDC-PERP".
	Symbol string

	// Market is a snapshot of current market conditions.
	Market exchange.MarketSnapshot

	// Position is the current open position for Symbol.
	Position exchange.Position

	// OpenOrders lists all resting orders for Symbol.
	OpenOrders []exchange.Order

	// Tick is the engine's current tick counter (starts at 0).
	Tick int64

	// Elapsed is time since the strategy started.
	Elapsed time.Duration

	// Params holds strategy-specific configuration values.
	// Strategies may read any key; unknown keys are silently ignored.
	Params map[string]any

	// State is the current state name (read-only; set by Engine).
	State string

	// Exchange provides order execution and market data queries.
	Exchange exchange.Exchange

	// Log is a structured logger scoped to this strategy instance.
	Log *slog.Logger

	// GoCtx is the engine's cancellation context.
	// Use it when calling ctx.Exchange methods that require a context.Context.
	GoCtx context.Context
}

// Param retrieves a typed parameter value, returning def if absent or wrong type.
func Param[T any](ctx *Context, key string, def T) T {
	if v, ok := ctx.Params[key]; ok {
		if typed, ok := v.(T); ok {
			return typed
		}
	}
	return def
}
