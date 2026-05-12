package evidence

import "sync"

// OrderLifecycleStep records a single state transition for an order.
type OrderLifecycleStep struct {
	EventType   string
	BlockHeight int64
	Detail      string
}

// OrderLifecycle tracks the full lifecycle of a single order.
type OrderLifecycle struct {
	OrderId   string
	AccountId string
	MarketId  string
	Steps     []OrderLifecycleStep
}

func (l *OrderLifecycle) AddStep(eventType string, blockHeight int64, detail string) {
	l.Steps = append(l.Steps, OrderLifecycleStep{
		EventType:   eventType,
		BlockHeight: blockHeight,
		Detail:      detail,
	})
}

// IsComplete returns true when the lifecycle has reached a terminal state.
func (l *OrderLifecycle) IsComplete() bool {
	for _, s := range l.Steps {
		switch s.EventType {
		case "ORDER_FILLED", "ORDER_CANCELLED", "ORDER_REJECTED", "ORDER_EXPIRED":
			return true
		}
	}
	return false
}

// FinalStatus returns the terminal status string, or empty if not yet terminal.
func (l *OrderLifecycle) FinalStatus() string {
	for i := len(l.Steps) - 1; i >= 0; i-- {
		s := l.Steps[i]
		switch s.EventType {
		case "ORDER_FILLED", "ORDER_CANCELLED", "ORDER_REJECTED", "ORDER_EXPIRED", "TRADE_EXECUTED":
			return s.EventType
		}
	}
	return ""
}

// LifecycleTracker subscribes to EventBus events and builds per-order lifecycles.
// It implements state.Handler-compatible logic but is decoupled via a callback interface.
type LifecycleTracker struct {
	mu         sync.RWMutex
	lifecycles map[string]*OrderLifecycle
}

func NewLifecycleTracker() *LifecycleTracker {
	return &LifecycleTracker{
		lifecycles: make(map[string]*OrderLifecycle),
	}
}

// HandleEvent processes a single state.Event (passed as raw fields to avoid import cycle).
// eventType, orderId, accountId, marketId, blockHeight, detail must be extracted by caller.
func (t *LifecycleTracker) RecordStep(orderId, accountId, marketId, eventType string, blockHeight int64, detail string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	lc, ok := t.lifecycles[orderId]
	if !ok {
		lc = &OrderLifecycle{
			OrderId:   orderId,
			AccountId: accountId,
			MarketId:  marketId,
		}
		t.lifecycles[orderId] = lc
	}
	lc.AddStep(eventType, blockHeight, detail)
}

func (t *LifecycleTracker) GetLifecycle(orderId string) (*OrderLifecycle, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	lc, ok := t.lifecycles[orderId]
	return lc, ok
}

func (t *LifecycleTracker) AllLifecycles() []*OrderLifecycle {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*OrderLifecycle, 0, len(t.lifecycles))
	for _, lc := range t.lifecycles {
		result = append(result, lc)
	}
	return result
}
