package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEClient represents one connected Server-Sent Events subscriber.
type SSEClient struct {
	ch     chan string
	done   chan struct{}
	topics map[string]bool
}

func newSSEClient(topics ...string) *SSEClient {
	tm := make(map[string]bool, len(topics))
	for _, t := range topics {
		tm[t] = true
	}
	return &SSEClient{
		ch:     make(chan string, 64),
		done:   make(chan struct{}),
		topics: tm,
	}
}

func (c *SSEClient) close() {
	close(c.done)
}

// StreamHub manages all active SSE connections and broadcasts events to them.
type StreamHub struct {
	mu      sync.RWMutex
	clients map[*SSEClient]struct{}
}

func newStreamHub() *StreamHub {
	return &StreamHub{clients: make(map[*SSEClient]struct{})}
}

func (h *StreamHub) add(c *SSEClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *StreamHub) remove(c *SSEClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// Broadcast sends a JSON-encoded SSE message to all clients subscribed to topic.
func (h *StreamHub) Broadcast(topic string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", topic, data)

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.topics["*"] || c.topics[topic] {
			select {
			case c.ch <- msg:
			default: // drop if client is slow
			}
		}
	}
}

// ServeSSE handles an SSE HTTP connection for the given topics.
// It blocks until the client disconnects or ctx is cancelled.
func (h *StreamHub) ServeSSE(w http.ResponseWriter, r *http.Request, topics ...string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := newSSEClient(topics...)
	h.add(client)
	defer func() {
		h.remove(client)
		client.close()
	}()

	// Send a heartbeat comment every 15s to keep the connection alive.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-client.ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
