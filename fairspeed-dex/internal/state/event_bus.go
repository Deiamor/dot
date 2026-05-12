package state

import "sync"

type Handler func(Event)

type EventBus struct {
	mu       sync.RWMutex
	handlers map[EventType][]Handler
	allHandlers []Handler
}

func NewEventBus() *EventBus {
	return &EventBus{
		handlers: make(map[EventType][]Handler),
	}
}

func (b *EventBus) Subscribe(t EventType, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[t] = append(b.handlers[t], h)
}

func (b *EventBus) SubscribeAll(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allHandlers = append(b.allHandlers, h)
}

func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	specific := b.handlers[e.Type]
	all := b.allHandlers
	b.mu.RUnlock()

	for _, h := range specific {
		h(e)
	}
	for _, h := range all {
		h(e)
	}
}
