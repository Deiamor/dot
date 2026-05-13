package risk

import "sync"

// MarginMode distinguishes cross-margin from isolated-margin positions.
type MarginMode string

const (
	MarginModeCross    MarginMode = "CROSS"
	MarginModeIsolated MarginMode = "ISOLATED"
)

// NetPosition tracks cumulative net exposure for one (account, market) pair.
// Positive NetQuantity = net long; negative = net short.
// AvgEntryPrice, AllocatedMargin, and AccruedFunding are only meaningful for
// PERP markets; they are zero for SPOT positions.
type NetPosition struct {
	AccountId       string
	MarketId        string
	NetQuantity     int64      // signed: long(+) short(-)
	AvgEntryPrice   int64      // weighted average entry price
	AllocatedMargin int64      // margin currently reserved for this position
	MarginMode      MarginMode // CROSS (default) | ISOLATED
	AccruedFunding  int64      // cumulative funding: positive = owe, negative = receive
}

// UnrealizedPnL computes mark-price-based P&L using integer arithmetic.
// Long:  (markPrice - avgEntry) * netQty
// Short: (avgEntry - markPrice) * (-netQty)
func (p NetPosition) UnrealizedPnL(markPrice int64) int64 {
	if p.NetQuantity == 0 || p.AvgEntryPrice == 0 {
		return 0
	}
	if p.NetQuantity > 0 {
		return (markPrice - p.AvgEntryPrice) * p.NetQuantity
	}
	return (p.AvgEntryPrice - markPrice) * (-p.NetQuantity)
}

// LiquidationPrice returns the mark price at which maintenance margin is breached.
// Long:  avgEntry - (allocatedMargin - maintMargin) / netQty
// Short: avgEntry + (allocatedMargin - maintMargin) / (-netQty)
// Returns 0 when the position is flat or has no allocated margin.
func (p NetPosition) LiquidationPrice(maintenanceMarginBps int64) int64 {
	if p.NetQuantity == 0 || p.AllocatedMargin == 0 || p.AvgEntryPrice == 0 {
		return 0
	}
	absQty := p.NetQuantity
	if absQty < 0 {
		absQty = -absQty
	}
	// Divide before the second multiplication to avoid int64 overflow on large positions.
	maintMargin := (absQty * p.AvgEntryPrice / 10_000) * maintenanceMarginBps
	margin := p.AllocatedMargin - maintMargin
	if margin <= 0 {
		return 0
	}
	if p.NetQuantity > 0 {
		return p.AvgEntryPrice - margin/p.NetQuantity
	}
	return p.AvgEntryPrice + margin/(-p.NetQuantity)
}

type PositionTracker struct {
	mu        sync.RWMutex
	positions map[string]*NetPosition // key: accountId+":"+marketId
}

func NewPositionTracker() *PositionTracker {
	return &PositionTracker{positions: make(map[string]*NetPosition)}
}

func positionKey(accountId, marketId string) string {
	return accountId + ":" + marketId
}

// Get returns a copy of the current net position (zero-value if not found).
func (pt *PositionTracker) Get(accountId, marketId string) NetPosition {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if p := pt.positions[positionKey(accountId, marketId)]; p != nil {
		return *p
	}
	return NetPosition{AccountId: accountId, MarketId: marketId, MarginMode: MarginModeCross}
}

// ApplyTrade updates positions for both sides of a matched trade (quantity only).
func (pt *PositionTracker) ApplyTrade(buyerAccountId, sellerAccountId, marketId string, qty int64) {
	pt.ApplyTradeWithPrice(buyerAccountId, sellerAccountId, marketId, qty, 0)
}

// ApplyTradeWithPrice updates positions and, when price > 0, recalculates AvgEntryPrice.
// This is the preferred call for perpetual settlements.
func (pt *PositionTracker) ApplyTradeWithPrice(buyerAccountId, sellerAccountId, marketId string, qty, price int64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.adjust(buyerAccountId, marketId, qty, price, true)
	pt.adjust(sellerAccountId, marketId, -qty, price, false)
}

func (pt *PositionTracker) adjust(accountId, marketId string, delta int64, price int64, isBuyer bool) {
	key := positionKey(accountId, marketId)
	p := pt.positions[key]
	if p == nil {
		p = &NetPosition{AccountId: accountId, MarketId: marketId, MarginMode: MarginModeCross}
		pt.positions[key] = p
	}

	if price > 0 {
		// Update AvgEntryPrice: only when increasing position (same direction as delta).
		// Reducing a position does not change entry price.
		increasing := (p.NetQuantity >= 0 && delta > 0) || (p.NetQuantity <= 0 && delta < 0)
		if increasing {
			oldAbs := p.NetQuantity
			if oldAbs < 0 {
				oldAbs = -oldAbs
			}
			addQty := delta
			if addQty < 0 {
				addQty = -addQty
			}
			newAbs := oldAbs + addQty
			if newAbs > 0 {
				p.AvgEntryPrice = (oldAbs*p.AvgEntryPrice + addQty*price) / newAbs
			}
		}
		_ = isBuyer // reserved for future use (isolated margin accounting)
	}

	p.NetQuantity += delta
}

// AdjustMargin adds delta (positive = add, negative = remove) to the position's
// AllocatedMargin. Creates the position record if it doesn't exist yet.
func (pt *PositionTracker) AdjustMargin(accountId, marketId string, delta int64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	key := positionKey(accountId, marketId)
	p := pt.positions[key]
	if p == nil {
		p = &NetPosition{AccountId: accountId, MarketId: marketId, MarginMode: MarginModeCross}
		pt.positions[key] = p
	}
	p.AllocatedMargin += delta
}

// SetMarginMode updates the margin mode for a position.
func (pt *PositionTracker) SetMarginMode(accountId, marketId string, mode MarginMode) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	key := positionKey(accountId, marketId)
	p := pt.positions[key]
	if p == nil {
		p = &NetPosition{AccountId: accountId, MarketId: marketId}
		pt.positions[key] = p
	}
	p.MarginMode = mode
}

// ForceClose zeroes out a position (used by the liquidation engine).
func (pt *PositionTracker) ForceClose(accountId, marketId string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	key := positionKey(accountId, marketId)
	if p := pt.positions[key]; p != nil {
		p.NetQuantity = 0
		p.AvgEntryPrice = 0
		p.AllocatedMargin = 0
		p.AccruedFunding = 0
	}
}

// AllPositions returns a snapshot of all non-zero positions.
func (pt *PositionTracker) AllPositions() []NetPosition {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	out := make([]NetPosition, 0, len(pt.positions))
	for _, p := range pt.positions {
		if p.NetQuantity != 0 {
			out = append(out, *p)
		}
	}
	return out
}
