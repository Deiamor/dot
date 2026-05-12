package evidence

import (
	"sync"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// ByzantineProposer wraps a set of LocalNodes and simulates a malicious
// proposer that deliberately shuffles transactions out of FairBatch order,
// or sends different batches to different peers.
type ByzantineProposer struct {
	mu       sync.Mutex
	proposer *node.LocalNode
	peers    map[string]*node.LocalNode
}

func NewByzantineProposer(proposer *node.LocalNode, peers map[string]*node.LocalNode) *ByzantineProposer {
	return &ByzantineProposer{proposer: proposer, peers: peers}
}

// ProposeShuffled executes the batch on the proposer node but sends a
// REVERSED transaction ordering to all peers. This is the classic front-run
// attack: the proposer puts its own order first regardless of FairBatch hash.
//
// Returns the honest batch (for hash comparison) and the tampered batches
// seen by each peer, keyed by peer ID.
func (b *ByzantineProposer) ProposeShuffled(honest fairbatch.FairBatch) (tampered fairbatch.FairBatch, peerBatches map[string]fairbatch.FairBatch, err error) {
	// Execute the honest batch on the proposer node.
	if _, err = b.proposer.SubmitBatch(honest); err != nil {
		return
	}

	// Build a tampered batch: reverse the tx ordering (worst case shuffle).
	// Pre-compute TxHashes so the BatchHash reflects the reversed (non-canonical) order.
	txs := make([]fairbatch.Transaction, len(honest.Transactions))
	copy(txs, honest.Transactions)
	for i := range txs {
		txs[i].TxHash = fairbatch.ComputeTxHash(txs[i], honest.BlockHeight)
	}
	for i, j := 0, len(txs)-1; i < j; i, j = i+1, j-1 {
		txs[i], txs[j] = txs[j], txs[i]
	}
	// BatchHash is computed over the REVERSED ordering — not canonical.
	tamperedHash := fairbatch.ComputeBatchHash(txs, honest.BlockHeight)
	tampered = fairbatch.FairBatch{
		BatchId:      honest.BatchId + "-tampered",
		BlockHeight:  honest.BlockHeight,
		Transactions: txs,
		BatchHash:    tamperedHash, // reflects reversed ordering
		Timestamp:    honest.Timestamp,
	}

	// Deliver tampered batch to each peer.
	peerBatches = make(map[string]fairbatch.FairBatch, len(b.peers))
	for id, peer := range b.peers {
		_, _ = peer.SubmitBatch(tampered)
		peerBatches[id] = tampered
	}
	return
}

// ProposeConflicting sends `batchA` to the first half of peers and `batchB`
// to the second half, simulating a double-sign attack. The proposer itself
// executes `batchA`.
func (b *ByzantineProposer) ProposeConflicting(batchA, batchB fairbatch.FairBatch) error {
	if _, err := b.proposer.SubmitBatch(batchA); err != nil {
		return err
	}
	i := 0
	for _, peer := range b.peers {
		if i%2 == 0 {
			_, _ = peer.SubmitBatch(batchA)
		} else {
			_, _ = peer.SubmitBatch(batchB)
		}
		i++
	}
	return nil
}
