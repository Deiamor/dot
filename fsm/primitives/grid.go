package primitives

import (
	"fmt"
	"math"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Grid states.
const (
	GridStateIdle    = "IDLE"
	GridStateActive  = "ACTIVE"
	GridStateStopped = "STOPPED"
)

// GridConfig controls the grid market-making strategy.
type GridConfig struct {
	// Symbol is the trading pair (e.g. "BTC-USDC-PERP").
	Symbol string

	// GridLevels is the number of levels on each side.
	// e.g. 5 → 5 buy orders + 5 sell orders = 10 total.
	GridLevels int

	// GridSpacingBps is the price spacing between adjacent levels in basis points.
	// e.g. 20 = 0.2% spacing.
	GridSpacingBps float64

	// OrderQty is the quantity per grid level.
	OrderQty float64

	// MaxPosition is the maximum absolute position before pausing new orders on that side.
	MaxPosition float64
}

// GridStrategy implements a grid market-making strategy as a declarative FSM.
// It implements core.Strategy.
//
// State graph:
//
//	IDLE → ACTIVE ←→ (runs until kill)
//	   * → STOPPED (when kill channel closed)
type GridStrategy struct {
	cfg        GridConfig
	kill       <-chan struct{}
	initialMid float64
	prevOrders []exchange.Order // orders from the previous tick (for fill detection)
}

// NewGrid creates a GridStrategy ready to pass to core.NewEngine.
func NewGrid(cfg GridConfig, kill <-chan struct{}) (*GridStrategy, error) {
	if cfg.GridLevels <= 0 {
		return nil, fmt.Errorf("GridLevels must be > 0")
	}
	if cfg.GridSpacingBps <= 0 {
		return nil, fmt.Errorf("GridSpacingBps must be > 0")
	}
	if cfg.OrderQty <= 0 {
		return nil, fmt.Errorf("OrderQty must be > 0")
	}
	if cfg.MaxPosition < 0 {
		return nil, fmt.Errorf("MaxPosition must be >= 0")
	}
	if kill == nil {
		return nil, fmt.Errorf("kill channel must not be nil")
	}
	return &GridStrategy{cfg: cfg, kill: kill}, nil
}

// Name implements core.Strategy.
func (g *GridStrategy) Name() string { return "grid" }

// Initial implements core.Strategy.
func (g *GridStrategy) Initial() string { return GridStateIdle }

// States implements core.Strategy.
func (g *GridStrategy) States() []core.State {
	return []core.State{
		{
			Name: GridStateIdle,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("Grid idle",
					"symbol", g.cfg.Symbol,
					"levels", g.cfg.GridLevels,
					"spacing_bps", g.cfg.GridSpacingBps,
					"order_qty", g.cfg.OrderQty,
				)
				return nil
			},
		},
		{
			Name:    GridStateActive,
			OnEnter: g.onActiveEnter,
			OnTick:  g.onActiveTick,
			OnExit:  g.onActiveExit,
		},
		{
			Name: GridStateStopped,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("Grid stopped", "symbol", g.cfg.Symbol)
				return nil
			},
		},
	}
}

// Transitions implements core.Strategy.
func (g *GridStrategy) Transitions() []core.Transition {
	return []core.Transition{
		// * → STOPPED when kill channel is closed (highest priority).
		{
			From: "*",
			To:   GridStateStopped,
			Guard: func(_ *core.Context) bool {
				select {
				case <-g.kill:
					return true
				default:
					return false
				}
			},
		},
		// IDLE → ACTIVE when market price is available.
		{
			From:  GridStateIdle,
			To:    GridStateActive,
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid > 0 },
		},
	}
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (g *GridStrategy) onActiveEnter(ctx *core.Context) error {
	g.initialMid = ctx.Market.Mid
	ctx.Log.Info("Grid active: placing initial grid",
		"mid", fmt.Sprintf("%.4f", g.initialMid),
		"levels", g.cfg.GridLevels,
	)
	return g.placeGrid(ctx, g.initialMid)
}

func (g *GridStrategy) onActiveTick(ctx *core.Context) error {
	// Detect fills by comparing current open orders to previous tick's orders.
	fills := g.detectFills(ctx.OpenOrders)
	for _, filled := range fills {
		if err := g.respondToFill(ctx, filled); err != nil {
			ctx.Log.Warn("Grid fill response failed", "order_id", filled.ID, "err", err)
		}
	}

	// Update prevOrders for next tick's fill detection.
	g.prevOrders = make([]exchange.Order, len(ctx.OpenOrders))
	copy(g.prevOrders, ctx.OpenOrders)

	// Check if mid has drifted too far from initial grid centre; re-grid if so.
	if g.initialMid > 0 {
		spacing := g.cfg.GridSpacingBps / 10_000
		driftThreshold := g.initialMid * spacing * float64(g.cfg.GridLevels) * 2
		if math.Abs(ctx.Market.Mid-g.initialMid) > driftThreshold {
			ctx.Log.Info("Grid drift detected: cancelling and re-gridding",
				"initial_mid", fmt.Sprintf("%.4f", g.initialMid),
				"current_mid", fmt.Sprintf("%.4f", ctx.Market.Mid),
				"threshold", fmt.Sprintf("%.4f", driftThreshold),
			)
			if err := ctx.Exchange.CancelAllOrders(ctx.GoCtx, g.cfg.Symbol); err != nil {
				ctx.Log.Warn("Grid cancel all failed during re-grid", "err", err)
			}
			g.initialMid = ctx.Market.Mid
			g.prevOrders = nil
			return g.placeGrid(ctx, g.initialMid)
		}
	}

	return nil
}

func (g *GridStrategy) onActiveExit(ctx *core.Context) error {
	ctx.Log.Info("Grid exiting: cancelling all orders", "symbol", g.cfg.Symbol)
	if err := ctx.Exchange.CancelAllOrders(ctx.GoCtx, g.cfg.Symbol); err != nil {
		ctx.Log.Warn("Grid cancel all on exit failed", "err", err)
	}
	return nil
}

// placeGrid places buy levels below mid and sell levels above mid.
func (g *GridStrategy) placeGrid(ctx *core.Context, mid float64) error {
	spacing := g.cfg.GridSpacingBps / 10_000
	pos := ctx.Position.Qty

	for i := 1; i <= g.cfg.GridLevels; i++ {
		// Buy level at mid * (1 - i*spacing).
		if g.cfg.MaxPosition == 0 || math.Abs(pos) < g.cfg.MaxPosition {
			buyPrice := mid * (1 - float64(i)*spacing)
			req := exchange.PlaceOrderRequest{
				Symbol: g.cfg.Symbol,
				Side:   exchange.Buy,
				Type:   exchange.Limit,
				Price:  buyPrice,
				Qty:    g.cfg.OrderQty,
			}
			if _, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, req); err != nil {
				ctx.Log.Warn("Grid buy order failed", "level", i, "price", buyPrice, "err", err)
			} else {
				ctx.Log.Info("Grid buy order placed", "level", i, "price", fmt.Sprintf("%.4f", buyPrice))
			}
		}

		// Sell level at mid * (1 + i*spacing).
		if g.cfg.MaxPosition == 0 || math.Abs(pos) < g.cfg.MaxPosition {
			sellPrice := mid * (1 + float64(i)*spacing)
			req := exchange.PlaceOrderRequest{
				Symbol: g.cfg.Symbol,
				Side:   exchange.Sell,
				Type:   exchange.Limit,
				Price:  sellPrice,
				Qty:    g.cfg.OrderQty,
			}
			if _, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, req); err != nil {
				ctx.Log.Warn("Grid sell order failed", "level", i, "price", sellPrice, "err", err)
			} else {
				ctx.Log.Info("Grid sell order placed", "level", i, "price", fmt.Sprintf("%.4f", sellPrice))
			}
		}
	}
	return nil
}

// detectFills compares current open orders against previous tick's orders.
// An order from prevOrders that no longer appears in currentOrders is considered filled.
func (g *GridStrategy) detectFills(currentOrders []exchange.Order) []exchange.Order {
	if len(g.prevOrders) == 0 {
		return nil
	}
	currentIDs := make(map[string]struct{}, len(currentOrders))
	for _, o := range currentOrders {
		currentIDs[o.ID] = struct{}{}
	}
	var fills []exchange.Order
	for _, prev := range g.prevOrders {
		if _, stillOpen := currentIDs[prev.ID]; !stillOpen {
			fills = append(fills, prev)
		}
	}
	return fills
}

// respondToFill places the opposite-side order after a fill.
func (g *GridStrategy) respondToFill(ctx *core.Context, filled exchange.Order) error {
	spacing := g.cfg.GridSpacingBps / 10_000
	pos := ctx.Position.Qty

	var counterSide exchange.Side
	var counterPrice float64

	if filled.Side == exchange.Buy {
		// A buy filled at price P → place sell at P + spacing.
		counterSide = exchange.Sell
		counterPrice = filled.Price * (1 + spacing)
		// Check max position before placing sell.
		if g.cfg.MaxPosition > 0 && math.Abs(pos) >= g.cfg.MaxPosition {
			ctx.Log.Info("Grid: max position reached, skipping counter sell",
				"position", pos, "max", g.cfg.MaxPosition)
			return nil
		}
	} else {
		// A sell filled at price P → place buy at P - spacing.
		counterSide = exchange.Buy
		counterPrice = filled.Price * (1 - spacing)
		// Check max position before placing buy.
		if g.cfg.MaxPosition > 0 && math.Abs(pos) >= g.cfg.MaxPosition {
			ctx.Log.Info("Grid: max position reached, skipping counter buy",
				"position", pos, "max", g.cfg.MaxPosition)
			return nil
		}
	}

	req := exchange.PlaceOrderRequest{
		Symbol: g.cfg.Symbol,
		Side:   counterSide,
		Type:   exchange.Limit,
		Price:  counterPrice,
		Qty:    g.cfg.OrderQty,
	}
	_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, req)
	if err != nil {
		return fmt.Errorf("place counter order: %w", err)
	}
	ctx.Log.Info("Grid fill response",
		"filled_side", filled.Side,
		"filled_price", fmt.Sprintf("%.4f", filled.Price),
		"counter_side", counterSide,
		"counter_price", fmt.Sprintf("%.4f", counterPrice),
	)
	return nil
}
