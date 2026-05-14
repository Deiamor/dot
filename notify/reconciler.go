package notify

import (
	"context"
	"fmt"
	"math"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Reconciler checks for position discrepancy between two views.
// Use case: on bot restart, local paper state may differ from real exchange state.
type Reconciler struct {
	notifier  Notifier
	threshold float64 // abs qty difference that triggers an alert
}

// NewReconciler creates a new Reconciler with the given alert threshold.
func NewReconciler(notifier Notifier, threshold float64) *Reconciler {
	return &Reconciler{
		notifier:  notifier,
		threshold: threshold,
	}
}

// Check compares localPos with exchangePos and sends an alert if they differ
// beyond the configured threshold.
// Returns the discrepancy quantity (exchangePos.Qty - localPos.Qty).
func (r *Reconciler) Check(ctx context.Context, localPos, exchangePos exchange.Position) (discrepancy float64, err error) {
	discrepancy = exchangePos.Qty - localPos.Qty

	if math.Abs(discrepancy) <= r.threshold {
		return discrepancy, nil
	}

	msg := fmt.Sprintf(
		"⚠️ Position discrepancy for %s: local=%.8g exchange=%.8g diff=%+.8g",
		exchangePos.Symbol, localPos.Qty, exchangePos.Qty, discrepancy,
	)
	if sendErr := r.notifier.Send(ctx, msg); sendErr != nil {
		return discrepancy, fmt.Errorf("reconciler: send alert: %w", sendErr)
	}

	return discrepancy, nil
}
