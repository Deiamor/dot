package exchange

import (
	"errors"
	"time"
)

// ErrTerminal signals the engine that the exchange has no more data.
// Returned by backtest exchanges when all snapshots have been replayed.
// The engine stops cleanly instead of treating it as a transient error.
var ErrTerminal = errors.New("terminal: no more data")

// EventKind classifies a TradingEvent.
type EventKind string

const (
	EventKindFill EventKind = "fill"
	EventKindTick EventKind = "tick"
)

// TradingEvent is emitted by paper and backtest exchanges on every fill and tick.
type TradingEvent struct {
	Kind EventKind
	Time time.Time

	// Fill fields — populated when Kind == EventKindFill.
	Symbol      string
	Side        Side
	Price       float64
	Qty         float64
	Fee         float64
	RealizedPnL float64

	// Tick fields — populated when Kind == EventKindTick.
	Snapshot MarketSnapshot
	Position float64 // signed: +long / -short
	Equity   float64 // balance + unrealised PnL
	State    string  // current FSM state (injected by the engine, may be "")
}

// EventEmitter is implemented by exchanges that emit TradingEvents.
// Type-assert exchange.Exchange to EventEmitter to subscribe.
//
//	if em, ok := ex.(exchange.EventEmitter); ok {
//	    ch := em.Events()
//	}
type EventEmitter interface {
	Events() <-chan TradingEvent
}
