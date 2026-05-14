package sim

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

// simExchange is the deterministic virtual exchange that powers a simulation run.
// It implements exchange.Exchange and exchange.EventEmitter.
// Not exported: callers use Run() which creates it internally.
type simExchange struct {
	symbol string
	cfg    Config
	ticks  []Tick
	cursor int

	mu       sync.Mutex
	balance  float64
	position float64
	avgEntry float64
	pending  []exchange.Order
	fills    []exchange.TradingEvent

	nextID atomic.Int64
	events chan exchange.TradingEvent
}

func newSimExchange(symbol string, s Scenario) *simExchange {
	return &simExchange{
		symbol:  symbol,
		cfg:     s.Config,
		ticks:   s.Ticks,
		balance: s.Config.InitialBalance,
		events:  make(chan exchange.TradingEvent, max(len(s.Ticks)*2, 256)),
	}
}

func (e *simExchange) Events() <-chan exchange.TradingEvent { return e.events }

// shutdown closes the events channel; safe to call once after Run() returns.
func (e *simExchange) shutdown() { close(e.events) }

// ─── exchange.Exchange ────────────────────────────────────────────────────────

func (e *simExchange) MarketSnapshot(_ context.Context, _ string) (exchange.MarketSnapshot, error) {
	e.mu.Lock()
	if e.cursor >= len(e.ticks) {
		e.mu.Unlock()
		return exchange.MarketSnapshot{}, fmt.Errorf("%w: consumed %d ticks", exchange.ErrTerminal, len(e.ticks))
	}
	tick := e.ticks[e.cursor]
	e.cursor++
	mid := (tick.Bid + tick.Ask) / 2
	e.checkLimitFills(mid)
	equity := e.balance + e.position*mid
	pos := e.position
	e.mu.Unlock()

	snap := exchange.MarketSnapshot{
		Symbol:      e.symbol,
		Bid:         tick.Bid,
		Ask:         tick.Ask,
		Mid:         mid,
		FundingRate: tick.FundingRate,
		Timestamp:   tick.At,
	}
	e.emit(exchange.TradingEvent{
		Kind:     exchange.EventKindTick,
		Time:     tick.At,
		Snapshot: snap,
		Position: pos,
		Equity:   equity,
	})
	return snap, nil
}

func (e *simExchange) OrderBook(_ context.Context, _ string, _ int) (exchange.OrderBook, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cursor == 0 || e.cursor > len(e.ticks) {
		return exchange.OrderBook{Symbol: e.symbol}, nil
	}
	tick := e.ticks[e.cursor-1]
	return exchange.OrderBook{
		Symbol: e.symbol,
		Bids:   []exchange.PriceLevel{{Price: tick.Bid, Size: 1}},
		Asks:   []exchange.PriceLevel{{Price: tick.Ask, Size: 1}},
	}, nil
}

func (e *simExchange) Position(_ context.Context, _ string) (exchange.Position, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	mid := e.currentMid()
	return exchange.Position{
		Symbol:        e.symbol,
		Qty:           e.position,
		AvgEntryPrice: e.avgEntry,
		UnrealizedPnL: e.unrealisedPnL(mid),
	}, nil
}

func (e *simExchange) Balances(_ context.Context) ([]exchange.Balance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []exchange.Balance{{
		Asset:     "USDT",
		Available: e.balance,
		Total:     e.balance,
	}}, nil
}

func (e *simExchange) OpenOrders(_ context.Context, _ string) ([]exchange.Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]exchange.Order, len(e.pending))
	copy(out, e.pending)
	return out, nil
}

func (e *simExchange) PlaceOrder(_ context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
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
		e.fillOrder(o, e.marketFillPrice(req.Side), true)
		o.Status = exchange.StatusFilled
		o.FilledQty = req.Qty
		o.RemainingQty = 0
	} else {
		e.pending = append(e.pending, o)
	}
	return o, nil
}

func (e *simExchange) CancelOrder(_ context.Context, _, orderID string) error {
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

func (e *simExchange) CancelAllOrders(_ context.Context, _ string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pending = e.pending[:0]
	return nil
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (e *simExchange) checkLimitFills(mid float64) {
	remaining := e.pending[:0]
	for _, o := range e.pending {
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

func (e *simExchange) fillOrder(o exchange.Order, fillPrice float64, taker bool) {
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

func (e *simExchange) marketFillPrice(side exchange.Side) float64 {
	mid := e.currentMid()
	slip := e.cfg.SlippageBps / 10_000
	if side == exchange.Buy {
		return mid * (1 + slip)
	}
	return mid * (1 - slip)
}

func (e *simExchange) currentMid() float64 {
	if e.cursor > 0 && e.cursor <= len(e.ticks) {
		t := e.ticks[e.cursor-1]
		return (t.Bid + t.Ask) / 2
	}
	return 0
}

func (e *simExchange) currentTime() time.Time {
	if e.cursor > 0 && e.cursor <= len(e.ticks) {
		return e.ticks[e.cursor-1].At
	}
	return time.Now()
}

func (e *simExchange) unrealisedPnL(mid float64) float64 {
	if e.position == 0 {
		return 0
	}
	if e.position > 0 {
		return (mid - e.avgEntry) * e.position
	}
	return (e.avgEntry - mid) * math.Abs(e.position)
}

func (e *simExchange) emit(ev exchange.TradingEvent) {
	select {
	case e.events <- ev:
	default:
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
