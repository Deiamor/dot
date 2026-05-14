// Package backtest provides an exchange that replays historical MarketSnapshots
// through a strategy — no network calls, deterministic results.
//
// MarketSnapshot advances the cursor on each call.
// When all snapshots have been consumed, it returns exchange.ErrTerminal,
// which causes the FSM engine to stop cleanly.
package backtest

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

// Config controls the backtest exchange.
type Config struct {
	// InitialBalance is the starting USDT balance.
	InitialBalance float64

	// TakerFeeBps is the taker fee for market orders (basis points).
	TakerFeeBps float64

	// MakerFeeBps is the maker fee for limit order fills.
	MakerFeeBps float64

	// EventBuf is the events channel buffer size (default 256).
	EventBuf int
}

// Exchange replays a fixed slice of MarketSnapshots and maintains a virtual account.
// It implements exchange.Exchange and exchange.EventEmitter.
type Exchange struct {
	cfg       Config
	snapshots []exchange.MarketSnapshot
	cursor    int

	mu       sync.Mutex
	balance  float64
	position float64
	avgEntry float64
	pending  []exchange.Order
	fills    []exchange.TradingEvent

	nextID atomic.Int64
	events chan exchange.TradingEvent
}

// New creates a BacktestExchange from pre-loaded snapshots.
// Use LoadSnapshots to produce the slice from a downloaded kline file.
func New(cfg Config, snapshots []exchange.MarketSnapshot) *Exchange {
	if cfg.EventBuf <= 0 {
		cfg.EventBuf = 256
	}
	return &Exchange{
		cfg:       cfg,
		snapshots: snapshots,
		balance:   cfg.InitialBalance,
		events:    make(chan exchange.TradingEvent, cfg.EventBuf),
	}
}

// Events returns a read-only channel of TradingEvents.
// Implements exchange.EventEmitter.
func (e *Exchange) Events() <-chan exchange.TradingEvent { return e.events }

// Close closes the events channel. Call once after the engine has stopped to
// unblock any goroutine draining Events().
func (e *Exchange) Close() { close(e.events) }

// Progress returns (cursor, total) snapshot counts.
func (e *Exchange) Progress() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cursor, len(e.snapshots)
}

// Stats returns a summary of the backtest session so far.
func (e *Exchange) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats()
}

// ─── exchange.Exchange interface ──────────────────────────────────────────────

func (e *Exchange) MarketSnapshot(_ context.Context, _ string) (exchange.MarketSnapshot, error) {
	e.mu.Lock()
	if e.cursor >= len(e.snapshots) {
		e.mu.Unlock()
		return exchange.MarketSnapshot{}, fmt.Errorf("%w: replayed %d snapshots", exchange.ErrTerminal, len(e.snapshots))
	}
	snap := e.snapshots[e.cursor]
	e.cursor++
	e.checkLimitFills(snap.Mid, snap.Symbol)
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

func (e *Exchange) OrderBook(_ context.Context, symbol string, _ int) (exchange.OrderBook, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cursor == 0 || e.cursor > len(e.snapshots) {
		return exchange.OrderBook{Symbol: symbol}, nil
	}
	snap := e.snapshots[e.cursor-1]
	return exchange.OrderBook{
		Symbol: symbol,
		Bids:   []exchange.PriceLevel{{Price: snap.Bid, Size: 1}},
		Asks:   []exchange.PriceLevel{{Price: snap.Ask, Size: 1}},
	}, nil
}

func (e *Exchange) Position(_ context.Context, symbol string) (exchange.Position, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	mid := e.currentMid()
	return exchange.Position{
		Symbol:        symbol,
		Qty:           e.position,
		AvgEntryPrice: e.avgEntry,
		UnrealizedPnL: e.unrealisedPnL(mid),
	}, nil
}

func (e *Exchange) Balances(_ context.Context) ([]exchange.Balance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []exchange.Balance{{Asset: "USDT", Available: e.balance, Total: e.balance}}, nil
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
		CreatedAt:    e.currentTime(),
	}
	if req.Type == exchange.Market {
		e.fillOrder(o, e.currentMid(), true)
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
		triggered := (o.Side == exchange.Buy && mid <= o.Price) ||
			(o.Side == exchange.Sell && mid >= o.Price)
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

	switch o.Side {
	case exchange.Buy:
		if e.position < 0 {
			closeQty := math.Min(qty, math.Abs(e.position))
			realised = (e.avgEntry - fillPrice) * closeQty
			e.balance -= closeQty * fillPrice
			e.position += closeQty
			qty -= closeQty
		}
		if qty > 0 {
			e.balance -= qty * fillPrice
			newQty := e.position + qty
			if newQty != 0 {
				e.avgEntry = (e.position*e.avgEntry + qty*fillPrice) / newQty
			}
			e.position = newQty
		}
	case exchange.Sell:
		if e.position > 0 {
			closeQty := math.Min(qty, e.position)
			realised = (fillPrice - e.avgEntry) * closeQty
			e.balance += closeQty * fillPrice
			e.position -= closeQty
			qty -= closeQty
		}
		if qty > 0 {
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
		Time:        e.currentTime(),
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

func (e *Exchange) currentMid() float64 {
	if e.cursor > 0 && e.cursor <= len(e.snapshots) {
		return e.snapshots[e.cursor-1].Mid
	}
	return 0
}

func (e *Exchange) currentTime() time.Time {
	if e.cursor > 0 && e.cursor <= len(e.snapshots) {
		return e.snapshots[e.cursor-1].Timestamp
	}
	return time.Now()
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
	totalPnL, totalFees := 0.0, 0.0
	wins, losses := 0, 0
	for _, f := range e.fills {
		if f.Kind == exchange.EventKindFill {
			totalPnL += f.RealizedPnL
			totalFees += f.Fee
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
	mid := e.currentMid()
	return Stats{
		InitialBalance: e.cfg.InitialBalance,
		FinalBalance:   e.balance,
		TotalPnL:       totalPnL,
		TotalFees:      totalFees,
		NetPnL:         totalPnL - totalFees,
		NumFills:       len(e.fills),
		WinRate:        winRate,
		FinalPosition:  e.position,
		FinalEquity:    e.equity(mid),
		SnapshotsTotal: len(e.snapshots),
		SnapshotsDone:  e.cursor,
	}
}

func (e *Exchange) emit(ev exchange.TradingEvent) {
	select {
	case e.events <- ev:
	default:
	}
}

// Stats summarises a completed or in-progress backtest.
type Stats struct {
	InitialBalance float64
	FinalBalance   float64
	TotalPnL       float64
	TotalFees      float64
	NetPnL         float64
	NumFills       int
	WinRate        float64
	FinalPosition  float64
	FinalEquity    float64
	SnapshotsTotal int
	SnapshotsDone  int
}

func (s Stats) ReturnPct() float64 {
	if s.InitialBalance == 0 {
		return 0
	}
	return (s.FinalEquity - s.InitialBalance) / s.InitialBalance * 100
}
