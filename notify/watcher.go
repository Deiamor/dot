package notify

import (
	"context"
	"fmt"
	"math"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Watcher watches a TradingEvent channel and sends alerts for notable events.
type Watcher struct {
	notifier Notifier
	symbol   string

	// FillAlertMinPnL sends an alert when |RealizedPnL| > this (0 = all fills).
	FillAlertMinPnL float64
	// EquityAlertPct sends an alert when equity changes > this % in one tick (0 = off).
	EquityAlertPct float64

	prevEquity float64
}

// NewWatcher creates a new Watcher for the given symbol.
func NewWatcher(notifier Notifier, symbol string) *Watcher {
	return &Watcher{
		notifier: notifier,
		symbol:   symbol,
	}
}

// Watch reads from ch until it's closed or ctx is done.
// Runs in caller's goroutine (call with go watcher.Watch(ctx, ch)).
func (w *Watcher) Watch(ctx context.Context, ch <-chan exchange.TradingEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			w.handle(ctx, ev)
		}
	}
}

func (w *Watcher) handle(ctx context.Context, ev exchange.TradingEvent) {
	switch ev.Kind {
	case exchange.EventKindFill:
		w.handleFill(ctx, ev)
	case exchange.EventKindTick:
		w.handleTick(ctx, ev)
	}
}

func (w *Watcher) handleFill(ctx context.Context, ev exchange.TradingEvent) {
	if w.FillAlertMinPnL > 0 && math.Abs(ev.RealizedPnL) <= w.FillAlertMinPnL {
		return
	}
	msg := fmt.Sprintf(
		"⚡ Fill: %s %s %.8g @ %.2f | PnL: %+.4f | Fee: %.4f",
		w.symbol, ev.Side, ev.Qty, ev.Price, ev.RealizedPnL, ev.Fee,
	)
	_ = w.notifier.Send(ctx, msg)
}

func (w *Watcher) handleTick(ctx context.Context, ev exchange.TradingEvent) {
	// Check for kill/error events (State contains terminal information).
	if ev.State == "KILL" || ev.State == "ERROR" {
		_ = w.notifier.Send(ctx, "🔴 Strategy stopped")
		return
	}

	// Check for large equity changes.
	if w.EquityAlertPct > 0 && w.prevEquity != 0 {
		pct := (ev.Equity - w.prevEquity) / w.prevEquity * 100
		if math.Abs(pct) > w.EquityAlertPct {
			msg := fmt.Sprintf("⚠️ Equity spike: %.2f (%+.1f%%)", ev.Equity, pct)
			_ = w.notifier.Send(ctx, msg)
		}
	}
	w.prevEquity = ev.Equity
}
