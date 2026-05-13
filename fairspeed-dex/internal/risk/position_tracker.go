package risk

import "sync"

// NetPosition tracks the cumulative net quantity an account holds in a market.
// Positive = net long (bought more than sold), negative = net short.
type NetPosition struct {
	AccountId   string
	MarketId    string
	NetQuantity int64
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
	return NetPosition{AccountId: accountId, MarketId: marketId}
}

// ApplyTrade updates positions for both sides of a matched trade.
// buyerAccountId receives +qty, sellerAccountId receives -qty.
func (pt *PositionTracker) ApplyTrade(buyerAccountId, sellerAccountId, marketId string, qty int64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.adjust(buyerAccountId, marketId, qty)
	pt.adjust(sellerAccountId, marketId, -qty)
}

func (pt *PositionTracker) adjust(accountId, marketId string, delta int64) {
	key := positionKey(accountId, marketId)
	p := pt.positions[key]
	if p == nil {
		p = &NetPosition{AccountId: accountId, MarketId: marketId}
		pt.positions[key] = p
	}
	p.NetQuantity += delta
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
