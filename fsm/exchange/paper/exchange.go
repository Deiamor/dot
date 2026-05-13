// Package paper provides a paper trading exchange that replays live market data
// with a virtual account — no real orders are ever placed.
//
// Limit orders are filled when the mid price crosses the limit price.
// Market orders fill immediately at mid ± slippage.
// P&L is tracked in real-time; equity = USDT balance + unrealised position value.
package paper

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Config controls the paper exchange behaviour.
type Config struct {
	// InitialBalance is the starting USDT balance.
	InitialBalance float64

	// SlippageBps is the one-way fill slippage in basis points (e.g. 1 = 0.01%).
	// Market orders fill at mid ± slippage. Limit orders fill at their stated price.
	SlippageBps float64

	// TakerFeeBps is the taker fee in basis points (e.g. 4 = 0.04%).
	TakerFeeBps float64

	// MakerFeeBps is the maker fee applied to limit order fills.
	MakerFeeBps float64

	// EventBuf is the channel buffer size for TradingEvents (default 256).
	EventBuf int
}

// Exchange is a paper trading exchange wrapping a real exchange for market data.
// It implements exchange.Exchange and exchange.EventEmitter.
type Exchange struct {
	cfg  Config
	real exchange.Exchange

	mu       sync.Mutex
	balance  float64          // USDT
	position float64          // signed base qty
	avgEntry float64          // weighted average entry price
	pending  []exchange.Order // resting limit orders
	fills    []exchange.TradingEvent
	lastMid  float64

	nextID atomic.Int64
	events chan exchange.TradingEvent
}

// New creates a paper Exchange backed by real for market data.
func New(cfg Config, real exchange.Exchange) *Exchange {
	if cfg.EventBuf <= 0 {
		cfg.EventBuf = 256
	}
	return &Exchange{
		cfg:     cfg,
		real:    real,
		balance: cfg.InitialBalance,
		events:  make(chan exchange.TradingEvent, cfg.EventBuf),
	}
}

// Events returns a read-only channel of TradingEvents (fills + ticks).
// Implements exchange.EventEmitter.
func (e *Exchange) Events() <-chan exchange.TradingEvent { return e.events }

// Stats returns a summary of the paper trading session.
func (e *Exchange) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats()
}

// ─── exchange.Exchange interface ──────────────────────────────────────────────

func (e *Exchange) MarketSnapshot(ctx context.Context, symbol string) (exchange.MarketSnapshot, error) {
	snap, err := e.real.MarketSnapshot(ctx, symbol)
	if err != nil {
		return snap, err
	}

	e.mu.Lock()
	e.lastMid = snap.Mid
	e.checkLimitFills(snap.Mid, symbol)
	equity := e.equity(snap.Mid)
	pos := e.position
	e.mu.Unlock()

	e.emit(exchange.TradingEvent{
		Kind:     exchange.EventKindTick,
		Time:     snap.Timestamp,
		Snapshot: snap,
		Position: pos,
		Equity:   equity,
	})
	return snap, nil
}

func (e *Exchange) OrderBook(ctx context.Context, symbol string, depth int) (exchange.OrderBook, error) {
	return e.real.OrderBook(ctx, symbol, depth)
}

func (e *Exchange) Position(_ context.Context, symbol string) (exchange.Position, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return exchange.Position{
		Symbol:        symbol,
		Qty:           e.position,
		AvgEntryPrice: e.avgEntry,
		UnrealizedPnL: e.unrealisedPnL(e.lastMid),
	}, nil
}

func (e *Exchange) Balances(_ context.Context) ([]exchange.Balance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []exchange.Balance{{
		Asset:     "USDT",
		Available: e.balance,
		Total:     e.balance,
	}}, nil
}

func (e *Exchange) OpenOrders(_ context.Context, symbol string) ([]exchange.Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]exchange.Order, 0, len(e.pending))
	for _, o := range e.pending {
		if o.Symbol == symbol {
			out = append(out, o)
		}
	}
	return out, nil
}

func (e *Exchange) PlaceOrder(_ context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	id := strconv.FormatInt(e.nextID.Add(1), 10)
	o := exchange.Order{
		ID:           id,
		Symbol:       req.Symbol,
		Side:         req.Side,
		Type:         req.Type,
		Price:        req.Price,
		Qty:          req.Qty,
		RemainingQty: req.Qty,
		Status:       exchange.StatusOpen,
		CreatedAt:    time.Now(),
	}

	if req.Type == exchange.Market {
		e.fillOrder(o, e.marketFillPrice(req.Side), true)
		o.Status = exchange.StatusFilled
		o.FilledQty = req.Qty
		o.RemainingQty = 0
	} else {
		e.pending = append(e.pending, o)
	}
	return o, nil
}

func (e *Exchange) CancelOrder(_ context.Context, _, orderID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	filtered := e.pending[:0]
	for _, o := range e.pending {
		if o.ID != orderID {
			filtered = append(filtered, o)
		}
	}
	e.pending = filtered
	return nil
}

func (e *Exchange) CancelAllOrders(_ context.Context, symbol string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	filtered := e.pending[:0]
	for _, o := range e.pending {
		if o.Symbol != symbol {
			filtered = append(filtered, o)
		}
	}
	e.pending = filtered
	return nil
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (e *Exchange) checkLimitFills(mid float64, symbol string) {
	remaining := e.pending[:0]
	for _, o := range e.pending {
		if o.Symbol != symbol {
			remaining = append(remaining, o)
			continue
		}
		triggered := false
		switch o.Side {
		case exchange.Buy:
			triggered = mid <= o.Price
		case exchange.Sell:
			triggered = mid >= o.Price
		}
		if triggered {
			e.fillOrder(o, o.Price, false)
		} else {
			remaining = append(remaining, o)
		}
	}
	e.pending = remaining
}

func (e *Exchange) fillOrder(o exchange.Order, fillPrice float64, taker bool) {
	feeBps := e.cfg.MakerFeeBps
	if taker {
		feeBps = e.cfg.TakerFeeBps
	}
	fee := fillPrice * o.Qty * feeBps / 10_000

	realised := 0.0
	qty := o.Qty

	// Balance model: equity = balance + position * markPrice (signed position).
	// Opening long:  balance -= cost        (pay notional)
	// Closing long:  balance += proceeds    (receive notional)
	// Opening short: balance += proceeds    (receive notional from short sale)
	// Closing short: balance -= cost        (pay to buy back)
	switch o.Side {
	case exchange.Buy:
		if e.position < 0 {
			// Closing (partial or full) short: pay fillPrice to buy back.
			closeQty := math.Min(qty, math.Abs(e.position))
			realised = (e.avgEntry - fillPrice) * closeQty
			e.balance -= closeQty * fillPrice
			e.position += closeQty
			qty -= closeQty
		}
		if qty > 0 {
			// Opening new long: pay notional.
			e.balance -= qty * fillPrice
			newQty := e.position + qty
			if newQty != 0 {
				e.avgEntry = (e.position*e.avgEntry + qty*fillPrice) / newQty
			}
			e.position = newQty
		}
	case exchange.Sell:
		if e.position > 0 {
			// Closing (partial or full) long: receive fillPrice as proceeds.
			closeQty := math.Min(qty, e.position)
			realised = (fillPrice - e.avgEntry) * closeQty
			e.balance += closeQty * fillPrice
			e.position -= closeQty
			qty -= closeQty
		}
		if qty > 0 {
			// Opening new short: receive notional from short sale.
			e.balance += qty * fillPrice
			newQty := e.position - qty
			if newQty != 0 {
				e.avgEntry = (math.Abs(e.position)*e.avgEntry + qty*fillPrice) / math.Abs(newQty)
			}
			e.position = newQty
		}
	}
	e.balance -= fee

	ev := exchange.TradingEvent{
		Kind:        exchange.EventKindFill,
		Time:        time.Now(),
		Symbol:      o.Symbol,
		Side:        o.Side,
		Price:       fillPrice,
		Qty:         o.Qty,
		Fee:         fee,
		RealizedPnL: realised,
	}
	e.fills = append(e.fills, ev)
	e.emit(ev)
}

func (e *Exchange) marketFillPrice(side exchange.Side) float64 {
	slippage := e.cfg.SlippageBps / 10_000
	if side == exchange.Buy {
		return e.lastMid * (1 + slippage)
	}
	return e.lastMid * (1 - slippage)
}

func (e *Exchange) unrealisedPnL(mid float64) float64 {
	if e.position == 0 || e.avgEntry == 0 {
		return 0
	}
	if e.position > 0 {
		return (mid - e.avgEntry) * e.position
	}
	return (e.avgEntry - mid) * math.Abs(e.position)
}

func (e *Exchange) equity(mid float64) float64 {
	return e.balance + e.position*mid
}

func (e *Exchange) stats() Stats {
	totalPnL := 0.0
	wins, losses := 0, 0
	for _, f := range e.fills {
		if f.Kind == exchange.EventKindFill {
			totalPnL += f.RealizedPnL - f.Fee
			if f.RealizedPnL > 0 {
				wins++
			} else if f.RealizedPnL < 0 {
				losses++
			}
		}
	}
	winRate := 0.0
	if wins+losses > 0 {
		winRate = float64(wins) / float64(wins+losses)
	}
	return Stats{
		InitialBalance: e.cfg.InitialBalance,
		CurrentBalance: e.balance,
		TotalPnL:       totalPnL,
		NumFills:       len(e.fills),
		WinRate:        winRate,
		Position:       e.position,
		AvgEntry:       e.avgEntry,
	}
}

func (e *Exchange) emit(ev exchange.TradingEvent) {
	select {
	case e.events <- ev:
	default:
		// Drop if buffer full — non-blocking.
	}
}

// Stats summarises a paper trading session.
type Stats struct {
	InitialBalance float64
	CurrentBalance float64
	TotalPnL       float64
	NumFills       int
	WinRate        float64
	Position       float64
	AvgEntry       float64
}

func (s Stats) String() string {
	return fmt.Sprintf(
		"balance=%.2f pnl=%.2f fills=%d winRate=%.1f%% position=%.6f",
		s.CurrentBalance, s.TotalPnL, s.NumFills, s.WinRate*100, s.Position,
	)
}
