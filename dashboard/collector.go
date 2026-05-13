// Package dashboard provides a real-time web dashboard for the MM bot.
// It aggregates TradingEvents from the active exchange and broadcasts them
// to connected browsers via Server-Sent Events (SSE).
package dashboard

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// EquityPoint is a single point on the equity curve time series.
type EquityPoint struct {
	Time   time.Time `json:"time"`
	Equity float64   `json:"equity"`
}

// FillRecord is a simplified fill for the dashboard.
type FillRecord struct {
	Time        time.Time    `json:"time"`
	Symbol      string       `json:"symbol"`
	Side        exchange.Side `json:"side"`
	Price       float64      `json:"price"`
	Qty         float64      `json:"qty"`
	Fee         float64      `json:"fee"`
	RealizedPnL float64      `json:"realizedPnl"`
}

// TickSummary is sent to the browser on every market tick.
type TickSummary struct {
	Time        time.Time `json:"time"`
	Mid         float64   `json:"mid"`
	Bid         float64   `json:"bid"`
	Ask         float64   `json:"ask"`
	FundingRate float64   `json:"fundingRate"`
	Position    float64   `json:"position"`
	Equity      float64   `json:"equity"`
	State       string    `json:"state"`
	SpreadBps   float64   `json:"spreadBps"`
}

// SSEEvent is a JSON-encoded Server-Sent Events payload.
type SSEEvent struct {
	Type string `json:"type"` // "tick" | "fill" | "state"
	Data any    `json:"data"`
}

// Collector aggregates TradingEvents and fans them out to SSE subscribers.
type Collector struct {
	mu      sync.RWMutex
	equity  []EquityPoint
	fills   []FillRecord
	lastTick TickSummary
	state   string

	subsMu sync.Mutex
	subs   map[int]chan []byte
	nextSub int
}

// NewCollector creates a new Collector.
func NewCollector() *Collector {
	return &Collector{
		subs: make(map[int]chan []byte),
	}
}

// SetState updates the current FSM state label (called by the engine wrapper).
func (c *Collector) SetState(state string) {
	c.mu.Lock()
	c.state = state
	c.mu.Unlock()
	c.broadcast(SSEEvent{Type: "state", Data: map[string]string{"state": state}})
}

// Ingest reads from the exchange Events() channel until it is closed or ctx is done.
// Call in a goroutine.
func (c *Collector) Ingest(ch <-chan exchange.TradingEvent) {
	for ev := range ch {
		switch ev.Kind {
		case exchange.EventKindFill:
			c.recordFill(ev)
		case exchange.EventKindTick:
			c.recordTick(ev)
		}
	}
}

// Subscribe returns a channel of JSON-encoded SSE payloads and an unsubscribe function.
// Each subscriber gets the last 500 equity points on connect.
func (c *Collector) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 128)
	c.subsMu.Lock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = ch
	c.subsMu.Unlock()

	// Send backfill of recent equity + current tick.
	c.mu.RLock()
	equity := c.equity
	lastTick := c.lastTick
	fills := c.fills
	c.mu.RUnlock()

	go func() {
		// Backfill equity curve.
		start := 0
		if len(equity) > 500 {
			start = len(equity) - 500
		}
		for _, pt := range equity[start:] {
			send(ch, SSEEvent{Type: "equity_point", Data: pt})
		}
		// Backfill last 50 fills.
		fstart := 0
		if len(fills) > 50 {
			fstart = len(fills) - 50
		}
		for _, f := range fills[fstart:] {
			send(ch, SSEEvent{Type: "fill", Data: f})
		}
		if lastTick.Mid > 0 {
			send(ch, SSEEvent{Type: "tick", Data: lastTick})
		}
	}()

	unsub := func() {
		c.subsMu.Lock()
		delete(c.subs, id)
		c.subsMu.Unlock()
		close(ch)
	}
	return ch, unsub
}

// Snapshot returns current state, last tick, and recent fills for the REST API.
func (c *Collector) Snapshot() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fstart := 0
	if len(c.fills) > 100 {
		fstart = len(c.fills) - 100
	}
	estart := 0
	if len(c.equity) > 1000 {
		estart = len(c.equity) - 1000
	}
	return map[string]any{
		"state":    c.state,
		"lastTick": c.lastTick,
		"fills":    c.fills[fstart:],
		"equity":   c.equity[estart:],
	}
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (c *Collector) recordFill(ev exchange.TradingEvent) {
	rec := FillRecord{
		Time:        ev.Time,
		Symbol:      ev.Symbol,
		Side:        ev.Side,
		Price:       ev.Price,
		Qty:         ev.Qty,
		Fee:         ev.Fee,
		RealizedPnL: ev.RealizedPnL,
	}
	c.mu.Lock()
	c.fills = append(c.fills, rec)
	c.mu.Unlock()
	c.broadcast(SSEEvent{Type: "fill", Data: rec})
}

func (c *Collector) recordTick(ev exchange.TradingEvent) {
	spread := 0.0
	if ev.Snapshot.Mid > 0 {
		spread = (ev.Snapshot.Ask - ev.Snapshot.Bid) / ev.Snapshot.Mid * 10_000
	}
	c.mu.Lock()
	state := c.state
	tick := TickSummary{
		Time:        ev.Time,
		Mid:         ev.Snapshot.Mid,
		Bid:         ev.Snapshot.Bid,
		Ask:         ev.Snapshot.Ask,
		FundingRate: ev.Snapshot.FundingRate,
		Position:    ev.Position,
		Equity:      ev.Equity,
		State:       state,
		SpreadBps:   spread,
	}
	c.lastTick = tick
	pt := EquityPoint{Time: ev.Time, Equity: ev.Equity}
	c.equity = append(c.equity, pt)
	c.mu.Unlock()

	c.broadcast(SSEEvent{Type: "tick", Data: tick})
	c.broadcast(SSEEvent{Type: "equity_point", Data: pt})
}

func (c *Collector) broadcast(ev SSEEvent) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- raw:
		default:
		}
	}
}

func send(ch chan []byte, ev SSEEvent) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	select {
	case ch <- raw:
	default:
	}
}
