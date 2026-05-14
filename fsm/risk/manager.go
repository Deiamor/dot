// Package risk provides a middleware exchange that enforces live risk limits.
// When a limit is breached, the kill channel is closed to stop the strategy.
package risk

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// Sentinel errors returned by Manager.
var (
	ErrPositionLimitExceeded = errors.New("risk: position limit exceeded")
	ErrKilled                = errors.New("risk: manager killed")
)

// Config defines all risk limits. A zero value for any field disables that limit.
type Config struct {
	// MaxPositionQty is the maximum absolute position in base units.
	// 0 = no limit.
	MaxPositionQty float64

	// MaxDailyLossUSDT triggers a kill when cumulative daily fees/losses exceed this.
	// 0 = no limit.
	MaxDailyLossUSDT float64

	// MaxDrawdownPct triggers a kill when drawdown from equity peak exceeds
	// this percentage (e.g. 10 = 10%). 0 = no limit.
	MaxDrawdownPct float64

	// MinEquityUSDT triggers an emergency CancelAllOrders + kill when equity
	// drops below this. 0 = no limit.
	MinEquityUSDT float64
}

// Manager wraps exchange.Exchange with live risk enforcement.
// When a limit is breached, it closes the kill channel to stop the strategy.
type Manager struct {
	exchange.Exchange // embed inner exchange

	cfg  Config
	kill chan<- struct{} // closed to signal strategy stop

	mu         sync.Mutex
	peakEquity float64
	dailyLoss  float64
	dailyDate  string // YYYY-MM-DD of last reset
	killed     bool
	killOnce   sync.Once
}

// New wraps inner with risk checks. kill is closed when any limit is breached.
func New(cfg Config, inner exchange.Exchange, kill chan<- struct{}) *Manager {
	return &Manager{
		Exchange: inner,
		cfg:      cfg,
		kill:     kill,
	}
}

// MarketSnapshot fetches the live snapshot then checks drawdown and min-equity limits.
func (m *Manager) MarketSnapshot(ctx context.Context, symbol string) (exchange.MarketSnapshot, error) {
	snap, err := m.Exchange.MarketSnapshot(ctx, symbol)
	if err != nil {
		return snap, err
	}

	// Fetch position and balances to compute equity.
	pos, posErr := m.Exchange.Position(ctx, symbol)
	balances, balErr := m.Exchange.Balances(ctx)
	if posErr != nil || balErr != nil {
		// Non-fatal — skip equity checks this tick.
		return snap, nil
	}

	// equity = USDT balance + position * markPrice
	var balance float64
	for _, b := range balances {
		if b.Asset == "USDT" {
			balance = b.Total
			break
		}
	}
	markPrice := snap.MarkPrice
	if markPrice == 0 {
		markPrice = snap.Mid
	}
	equity := balance + pos.Qty*markPrice

	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset daily loss at midnight UTC.
	today := time.Now().UTC().Format("2006-01-02")
	if m.dailyDate != today {
		m.dailyLoss = 0
		m.dailyDate = today
	}

	// Update peak equity.
	if equity > m.peakEquity {
		m.peakEquity = equity
	}

	// Check MaxDrawdownPct: (peakEquity - equity) / peakEquity * 100 > cfg.MaxDrawdownPct → kill
	if m.cfg.MaxDrawdownPct > 0 && m.peakEquity > 0 {
		drawdownPct := (m.peakEquity - equity) / m.peakEquity * 100
		if drawdownPct > m.cfg.MaxDrawdownPct {
			m.triggerKill("max drawdown exceeded")
		}
	}

	// Check MinEquityUSDT — emergency cancel all orders then kill.
	if m.cfg.MinEquityUSDT > 0 && equity < m.cfg.MinEquityUSDT {
		// Cancel all orders (best-effort; ignore errors).
		_ = m.Exchange.CancelAllOrders(ctx, symbol)
		m.triggerKill("equity below minimum")
	}

	return snap, nil
}

// PlaceOrder checks risk limits before delegating to the inner exchange.
func (m *Manager) PlaceOrder(ctx context.Context, req exchange.PlaceOrderRequest) (exchange.Order, error) {
	m.mu.Lock()
	killed := m.killed
	m.mu.Unlock()

	if killed {
		return exchange.Order{}, ErrKilled
	}

	// Check MaxPositionQty limit.
	if m.cfg.MaxPositionQty > 0 {
		pos, err := m.Exchange.Position(ctx, req.Symbol)
		if err != nil {
			return exchange.Order{}, err
		}

		var delta float64
		if req.Side == exchange.Buy {
			delta = req.Qty
		} else {
			delta = -req.Qty
		}
		netPos := pos.Qty + delta
		if math.Abs(netPos) > m.cfg.MaxPositionQty {
			return exchange.Order{}, ErrPositionLimitExceeded
		}
	}

	// Delegate to inner exchange.
	order, err := m.Exchange.PlaceOrder(ctx, req)
	if err != nil {
		return order, err
	}

	// Track fill cost as fee proxy for daily loss (conservative).
	// Since exchange.Order has no Fee field, the fee is tracked via TrackFee.
	// Here we track 0 by default; callers should call TrackFee with actual fees.

	return order, nil
}

// TrackFee records a fee amount toward the daily loss limit and triggers a kill
// if MaxDailyLossUSDT is exceeded. Call this after receiving a fill event.
func (m *Manager) TrackFee(fee float64) {
	if fee <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")
	if m.dailyDate != today {
		m.dailyLoss = 0
		m.dailyDate = today
	}
	m.dailyLoss += fee
	if m.cfg.MaxDailyLossUSDT > 0 && m.dailyLoss > m.cfg.MaxDailyLossUSDT {
		m.triggerKill("daily loss limit exceeded")
	}
}

// triggerKill closes the kill channel exactly once and logs the reason.
// Must be called with m.mu held.
func (m *Manager) triggerKill(reason string) {
	m.killOnce.Do(func() {
		m.killed = true
		slog.Default().Warn("risk manager kill triggered", "reason", reason)
		close(m.kill)
	})
}
