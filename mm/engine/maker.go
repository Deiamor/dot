// Package engine runs the funding-aware market making strategy
// as an FSM with four states: IDLE → QUOTING → SKEWING → PAUSED.
//
// State graph:
//
//	IDLE      ─── market open ──────────────────► QUOTING
//	QUOTING   ─── |inventory| > threshold ──────► SKEWING
//	QUOTING   ─── funding cost > spread revenue ► PAUSED
//	QUOTING   ─── volatility spike ─────────────► PAUSED
//	SKEWING   ─── inventory back in range ───────► QUOTING
//	SKEWING   ─── funding cost > spread revenue ► PAUSED
//	PAUSED    ─── condition cleared ─────────────► QUOTING
//	* (any)   ─── kill switch ───────────────────► FLAT
//	FLAT      ─── (terminal) ── position closed, engine stops
package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/mm/model"
)

// Config controls the MM engine behaviour.
type Config struct {
	// Model parameters for the A-S + funding model.
	Model model.Params

	// OrderSize is the base quantity per quote pair (in base asset units).
	OrderSize float64

	// InventoryThreshold triggers SKEWING when |inventory| exceeds this.
	// Expressed as a fraction of MaxInventory (e.g. 0.5 = 50%).
	InventoryThreshold float64

	// PauseFundingThreshold pauses quoting when the expected funding cost
	// per epoch exceeds this fraction of the expected spread revenue.
	PauseFundingThreshold float64

	// PauseVolatilityMult pauses quoting when realised σ exceeds
	// the calibrated σ by this multiple (e.g. 2.0 = double).
	PauseVolatilityMult float64

	// VolWindow is the rolling window size for the vol estimator (ticks).
	VolWindow int

	// TickInterval mirrors the engine interval for vol annualisation.
	TickInterval time.Duration
}

// States
const (
	StateIdle    = "IDLE"
	StateQuoting = "QUOTING"
	StateSkewing = "SKEWING"
	StatePaused  = "PAUSED"
	StateFlat    = "FLAT"
)

// MakerStrategy is the FSM definition for the funding-aware MM engine.
// It implements core.Strategy.
type MakerStrategy struct {
	cfg    Config
	m      *model.Model
	volEst *model.VolatilityEstimator
	kill   <-chan struct{} // close to trigger immediate flat
}

// New creates a MakerStrategy ready to pass to core.NewEngine.
// kill is an optional channel; close it to immediately flatten the position.
func New(cfg Config, kill <-chan struct{}) (*MakerStrategy, error) {
	m, err := model.New(cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	return &MakerStrategy{
		cfg:    cfg,
		m:      m,
		volEst: model.NewVolatilityEstimator(cfg.VolWindow, cfg.TickInterval),
		kill:   kill,
	}, nil
}

// ─── core.Strategy interface ─────────────────────────────────────────────────

func (s *MakerStrategy) Name() string    { return "funding-aware-mm" }
func (s *MakerStrategy) Initial() string { return StateIdle }

func (s *MakerStrategy) States() []core.State {
	return []core.State{
		{
			Name:  StateIdle,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Info("MM engine idle, waiting for market")
				return nil
			},
		},
		{
			Name:   StateQuoting,
			OnEnter: s.onEnterQuoting,
			OnTick:  s.onTickQuoting,
			OnExit:  s.cancelAllOrders,
		},
		{
			Name:   StateSkewing,
			OnEnter: s.onEnterSkewing,
			OnTick:  s.onTickSkewing,
			OnExit:  s.cancelAllOrders,
		},
		{
			Name:  StatePaused,
			OnEnter: func(ctx *core.Context) error {
				ctx.Log.Warn("MM paused: unfavourable conditions")
				return s.cancelAllOrders(ctx)
			},
		},
		{
			Name:   StateFlat,
			OnEnter: s.onEnterFlat,
		},
	}
}

func (s *MakerStrategy) Transitions() []core.Transition {
	return []core.Transition{
		// Kill switch: any → FLAT (highest priority)
		{
			From:  "*",
			To:    StateFlat,
			Guard: s.killSwitchFired,
			Action: func(ctx *core.Context) error {
				ctx.Log.Warn("kill switch activated")
				return nil
			},
		},
		// IDLE → QUOTING when market is live
		{
			From:  StateIdle,
			To:    StateQuoting,
			Guard: func(ctx *core.Context) bool { return ctx.Market.Mid > 0 },
		},
		// QUOTING → SKEWING when inventory exceeds threshold
		{
			From:  StateQuoting,
			To:    StateSkewing,
			Guard: s.inventoryExceedsThreshold,
		},
		// QUOTING → PAUSED when conditions unfavourable
		{
			From:  StateQuoting,
			To:    StatePaused,
			Guard: s.conditionsUnfavourable,
		},
		// SKEWING → QUOTING when inventory returns to range
		{
			From:  StateSkewing,
			To:    StateQuoting,
			Guard: func(ctx *core.Context) bool { return !s.inventoryExceedsThreshold(ctx) },
		},
		// SKEWING → PAUSED
		{
			From:  StateSkewing,
			To:    StatePaused,
			Guard: s.conditionsUnfavourable,
		},
		// PAUSED → QUOTING when conditions recover
		{
			From:  StatePaused,
			To:    StateQuoting,
			Guard: func(ctx *core.Context) bool { return !s.conditionsUnfavourable(ctx) },
		},
	}
}

// ─── state handlers ───────────────────────────────────────────────────────────

func (s *MakerStrategy) onEnterQuoting(ctx *core.Context) error {
	ctx.Log.Info("entering QUOTING state")
	return s.refreshQuotes(ctx, 1.0)
}

func (s *MakerStrategy) onTickQuoting(ctx *core.Context) error {
	s.volEst.Observe(ctx.Market.Mid)
	s.m.UpdateSigma(s.volEst.Sigma())
	return s.refreshQuotes(ctx, 1.0)
}

func (s *MakerStrategy) onEnterSkewing(ctx *core.Context) error {
	ctx.Log.Warn("inventory threshold breached, entering SKEWING",
		"inventory", ctx.Position.Qty,
		"max", s.cfg.Model.MaxInventory)
	return s.refreshQuotes(ctx, s.skewFactor(ctx))
}

func (s *MakerStrategy) onTickSkewing(ctx *core.Context) error {
	s.volEst.Observe(ctx.Market.Mid)
	s.m.UpdateSigma(s.volEst.Sigma())
	return s.refreshQuotes(ctx, s.skewFactor(ctx))
}

func (s *MakerStrategy) onEnterFlat(ctx *core.Context) error {
	ctx.Log.Warn("flattening position")
	if err := s.cancelAllOrders(ctx); err != nil {
		ctx.Log.Error("cancel orders on flat", "err", err)
	}
	if !ctx.Position.IsFlat() {
		qty := math.Abs(ctx.Position.Qty)
		side := exchange.Sell
		if ctx.Position.IsShort() {
			side = exchange.Buy
		}
		_, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, exchange.PlaceOrderRequest{
			Symbol:     ctx.Symbol,
			Side:       side,
			Type:       exchange.Market,
			Qty:        qty,
			ReduceOnly: true,
		})
		return err
	}
	return nil
}

// ─── quote helpers ────────────────────────────────────────────────────────────

func (s *MakerStrategy) refreshQuotes(ctx *core.Context, sizeFactor float64) error {
	if err := s.cancelAllOrders(ctx); err != nil {
		return err
	}

	elapsed := ctx.Elapsed.Hours() / 8760 // convert to years
	bid, ask := s.m.Quotes(
		ctx.Market.Mid,
		ctx.Position.Qty,
		ctx.Market.FundingRate,
		elapsed,
	)

	ctx.Log.Info("quotes",
		"bid", fmt.Sprintf("%.4f", bid),
		"ask", fmt.Sprintf("%.4f", ask),
		"spread_bps", fmt.Sprintf("%.2f", (ask-bid)/ctx.Market.Mid*10000),
		"inventory", ctx.Position.Qty,
		"funding_rate", ctx.Market.FundingRate,
	)

	bidQty := s.m.SkewedQty(s.cfg.OrderSize*sizeFactor, ctx.Position.Qty, +1)
	askQty := s.m.SkewedQty(s.cfg.OrderSize*sizeFactor, ctx.Position.Qty, -1)

	if bidQty > 0 {
		if _, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, exchange.PlaceOrderRequest{
			Symbol: ctx.Symbol, Side: exchange.Buy,
			Type: exchange.Limit, Price: bid, Qty: bidQty,
		}); err != nil {
			ctx.Log.Error("place bid", "err", err)
		}
	}
	if askQty > 0 {
		if _, err := ctx.Exchange.PlaceOrder(ctx.GoCtx, exchange.PlaceOrderRequest{
			Symbol: ctx.Symbol, Side: exchange.Sell,
			Type: exchange.Limit, Price: ask, Qty: askQty,
		}); err != nil {
			ctx.Log.Error("place ask", "err", err)
		}
	}
	return nil
}

func (s *MakerStrategy) cancelAllOrders(ctx *core.Context) error {
	return ctx.Exchange.CancelAllOrders(ctx.GoCtx, ctx.Symbol)
}

// ─── guards ───────────────────────────────────────────────────────────────────

func (s *MakerStrategy) killSwitchFired(_ *core.Context) bool {
	if s.kill == nil {
		return false
	}
	select {
	case <-s.kill:
		return true
	default:
		return false
	}
}

func (s *MakerStrategy) inventoryExceedsThreshold(ctx *core.Context) bool {
	if s.cfg.Model.MaxInventory <= 0 {
		return false
	}
	return math.Abs(ctx.Position.Qty) > s.cfg.Model.MaxInventory*s.cfg.InventoryThreshold
}

func (s *MakerStrategy) conditionsUnfavourable(ctx *core.Context) bool {
	// Pause when expected funding cost > fraction of spread revenue.
	elapsed := ctx.Elapsed.Hours() / 8760
	halfSpread := s.m.HalfSpread(elapsed)
	expectedFundingCost := math.Abs(ctx.Market.FundingRate * ctx.Market.Mid * s.cfg.OrderSize)
	expectedSpreadRevenue := halfSpread * s.cfg.OrderSize

	if expectedSpreadRevenue > 0 &&
		expectedFundingCost/expectedSpreadRevenue > s.cfg.PauseFundingThreshold {
		return true
	}

	// Pause on volatility spike.
	currentSigma := s.volEst.Sigma()
	if currentSigma > 0 && s.cfg.Model.Sigma > 0 &&
		currentSigma > s.cfg.Model.Sigma*s.cfg.PauseVolatilityMult {
		return true
	}
	return false
}

func (s *MakerStrategy) skewFactor(ctx *core.Context) float64 {
	if s.cfg.Model.MaxInventory <= 0 {
		return 1.0
	}
	// Scale order size down on the side that increases inventory.
	ratio := math.Abs(ctx.Position.Qty) / s.cfg.Model.MaxInventory
	return math.Max(0.1, 1.0-ratio)
}
