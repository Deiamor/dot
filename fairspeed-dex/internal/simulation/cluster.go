package simulation

import (
	"fmt"
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// NodeCluster manages a set of LocalNodes with a shared proposer rotation
// and an underlying NetworkSimulator for gossip propagation.
type NodeCluster struct {
	net      *NetworkSimulator
	proposer *ProposerRotation
	nodeIds  []string
}

// NewNodeCluster creates N nodes named "node-0", "node-1", ... "node-(N-1)".
// All nodes start with zero state and must be bootstrapped by the caller.
func NewNodeCluster(n int) *NodeCluster {
	net := NewNetworkSimulator()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("node-%d", i)
		ids[i] = id
		ln := node.NewLocalNode()
		net.AddNode(id, ln)
	}
	return &NodeCluster{
		net:      net,
		proposer: NewProposerRotation(ids),
		nodeIds:  ids,
	}
}

// Node returns the LocalNode for the given id.
func (c *NodeCluster) Node(id string) *node.LocalNode {
	n, _ := c.net.GetNode(id)
	return n
}

// NodeAt returns the LocalNode at index i.
func (c *NodeCluster) NodeAt(i int) *node.LocalNode {
	return c.Node(c.nodeIds[i])
}

// NodeIds returns all node IDs.
func (c *NodeCluster) NodeIds() []string { return c.nodeIds }

// SetDelay sets the one-way gossip delay between two nodes (ms).
func (c *NodeCluster) SetDelay(from, to string, delayMs int64) {
	c.net.SetDelay(from, to, delayMs)
}

// SetUniformDelay sets the same gossip delay from every node to every other node.
func (c *NodeCluster) SetUniformDelay(delayMs int64) {
	for _, from := range c.nodeIds {
		for _, to := range c.nodeIds {
			if from != to {
				c.net.SetDelay(from, to, delayMs)
			}
		}
	}
}

// ProposeBlock has the current proposer process a batch and propagate it to peers.
// Returns the proposer ID, the block result, and a WaitGroup for propagation.
func (c *NodeCluster) ProposeBlock(batch fairbatch.FairBatch) (proposerId string, wg *sync.WaitGroup, err error) {
	proposerId = c.proposer.Current()
	proposer := c.Node(proposerId)
	if _, err = proposer.SubmitBatch(batch); err != nil {
		return proposerId, nil, fmt.Errorf("proposer %s: %w", proposerId, err)
	}
	wg = c.net.Propagate(proposerId, batch)
	c.proposer.Advance()
	return proposerId, wg, nil
}

// ProposeBlockSync is ProposeBlock that waits for all gossip to settle.
func (c *NodeCluster) ProposeBlockSync(batch fairbatch.FairBatch) (string, error) {
	id, wg, err := c.ProposeBlock(batch)
	if err != nil {
		return id, err
	}
	wg.Wait()
	return id, nil
}

// BootstrapAll runs the same batch on every node directly (no gossip delay).
// Use for initial account/deposit setup that must be identical on all nodes.
func (c *NodeCluster) BootstrapAll(batch fairbatch.FairBatch) error {
	for _, id := range c.nodeIds {
		n := c.Node(id)
		if _, err := n.SubmitBatch(batch); err != nil {
			return fmt.Errorf("bootstrap %s: %w", id, err)
		}
	}
	return nil
}

// RegisterAssetAll registers an asset on every node.
func (c *NodeCluster) RegisterAssetAll(a asset.Asset) {
	for _, id := range c.nodeIds {
		c.Node(id).RegisterAsset(a)
	}
}

// OrderBookState captures the L2 state of a single market for comparison.
type OrderBookState struct {
	BidPrices []int64
	AskPrices []int64
	BidQtys   []int64
	AskQtys   []int64
}

// SnapshotOrderBook reads the current L2 state for a market from a node.
func SnapshotOrderBook(n *node.LocalNode, marketId string) OrderBookState {
	ob, ok := n.GetOrderBook(marketId)
	if !ok {
		return OrderBookState{}
	}
	bids := ob.SortedBidPrices()
	asks := ob.SortedAskPrices()
	state := OrderBookState{
		BidPrices: bids,
		AskPrices: asks,
		BidQtys:   make([]int64, len(bids)),
		AskQtys:   make([]int64, len(asks)),
	}
	for i, p := range bids {
		if pl := ob.Bids[p]; pl != nil {
			state.BidQtys[i] = pl.TotalQuantity
		}
	}
	for i, p := range asks {
		if pl := ob.Asks[p]; pl != nil {
			state.AskQtys[i] = pl.TotalQuantity
		}
	}
	return state
}

// OrderBookStatesEqual returns true if two snapshots are identical.
func OrderBookStatesEqual(a, b OrderBookState) bool {
	if len(a.BidPrices) != len(b.BidPrices) || len(a.AskPrices) != len(b.AskPrices) {
		return false
	}
	for i := range a.BidPrices {
		if a.BidPrices[i] != b.BidPrices[i] || a.BidQtys[i] != b.BidQtys[i] {
			return false
		}
	}
	for i := range a.AskPrices {
		if a.AskPrices[i] != b.AskPrices[i] || a.AskQtys[i] != b.AskQtys[i] {
			return false
		}
	}
	return true
}

// BootstrapCluster is a convenience function that creates accounts, sessions,
// and deposits on every node and returns the IDs.
func BootstrapCluster(c *NodeCluster, aliceAddr, bobAddr string) (aliceId, bobId, aliceSess, bobSess string, err error) {
	c.RegisterAssetAll(asset.BTC)
	c.RegisterAssetAll(asset.USDC)

	// Block 1: create accounts on all nodes.
	b1 := fairbatch.NewBatchBuilder(1).
		AddCreateAccount(aliceAddr, "alice-root", "alice-withdraw").
		AddCreateAccount(bobAddr, "bob-root", "bob-withdraw").
		Build()
	if err = c.BootstrapAll(b1); err != nil {
		return
	}

	// Collect account IDs from node-0.
	n0 := c.NodeAt(0)
	for _, acc := range n0.AppState.Accounts {
		if acc.OwnerAddress == aliceAddr {
			aliceId = acc.AccountId
		} else if acc.OwnerAddress == bobAddr {
			bobId = acc.AccountId
		}
	}
	if aliceId == "" || bobId == "" {
		err = fmt.Errorf("accounts not found after bootstrap")
		return
	}

	// Block 2: create sessions.
	opts := account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 1000}
	b2 := fairbatch.NewBatchBuilder(2).
		AddCreateSession(aliceId, opts).
		AddCreateSession(bobId, opts).
		Build()
	if err = c.BootstrapAll(b2); err != nil {
		return
	}

	// Collect session IDs from node-0.
	for _, sess := range n0.AppState.Sessions {
		if sess.AccountId == aliceId {
			aliceSess = sess.SessionId
		} else if sess.AccountId == bobId {
			bobSess = sess.SessionId
		}
	}

	// Block 3: deposits.
	b3 := fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 100_000).
		AddDeposit(bobId, "BTC", 100).
		Build()
	err = c.BootstrapAll(b3)
	return
}

// buildOrder is a helper for creating test orders.
func BuildOrder(accountId, sessionId, marketId string, side clob.OrderSide,
	price, qty, blockHeight int64) clob.Order {
	return clob.NewLimitOrder(accountId, sessionId, marketId, side, price, qty,
		clob.TimeInForceGtc, blockHeight)
}
