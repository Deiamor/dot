package simulation

import (
	"sync"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// Message is a batch that travels over the simulated network.
type Message struct {
	Batch    fairbatch.FairBatch
	From     string
	To       string
	DelayMs  int64
}

// NetworkLink connects two nodes with a configurable one-way delay.
type NetworkLink struct {
	FromNodeId string
	ToNodeId   string
	DelayMs    int64 // 0 = instant delivery
}

// NetworkSimulator routes batches between named LocalNodes with per-link delays.
// It models the gossip layer: when a proposer finalizes a block, it propagates
// the batch to all peer nodes so they can replay it.
type NetworkSimulator struct {
	mu    sync.Mutex
	nodes map[string]*node.LocalNode
	links map[string]map[string]int64 // from → to → delayMs
}

func NewNetworkSimulator() *NetworkSimulator {
	return &NetworkSimulator{
		nodes: make(map[string]*node.LocalNode),
		links: make(map[string]map[string]int64),
	}
}

// AddNode registers a named LocalNode with the simulator.
func (ns *NetworkSimulator) AddNode(id string, n *node.LocalNode) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.nodes[id] = n
}

// SetDelay configures the one-way delivery delay between two nodes in milliseconds.
func (ns *NetworkSimulator) SetDelay(from, to string, delayMs int64) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	if ns.links[from] == nil {
		ns.links[from] = make(map[string]int64)
	}
	ns.links[from][to] = delayMs
}

// GetNode returns the LocalNode with the given id.
func (ns *NetworkSimulator) GetNode(id string) (*node.LocalNode, bool) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	n, ok := ns.nodes[id]
	return n, ok
}

// Propagate delivers a batch from one node to all peers.
// Delivery to each peer happens after its configured delay (async).
// Returns a WaitGroup that completes when all deliveries are done.
func (ns *NetworkSimulator) Propagate(fromId string, batch fairbatch.FairBatch) *sync.WaitGroup {
	ns.mu.Lock()
	links := ns.links[fromId]
	peers := make(map[string]*node.LocalNode)
	for toId, n := range ns.nodes {
		if toId != fromId {
			peers[toId] = n
		}
	}
	ns.mu.Unlock()

	var wg sync.WaitGroup
	for toId, peer := range peers {
		delay := int64(0)
		if links != nil {
			delay = links[toId]
		}
		wg.Add(1)
		go func(target *node.LocalNode, delayMs int64) {
			defer wg.Done()
			if delayMs > 0 {
				time.Sleep(time.Duration(delayMs) * time.Millisecond)
			}
			// Peer replays the same batch. Block height must already match.
			_, _ = target.SubmitBatch(batch)
		}(peer, delay)
	}
	return &wg
}
