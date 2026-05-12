package simulation

import "sync"

// ProposerRotation implements round-robin block proposer selection.
// In a real Appchain, this maps to CometBFT's ValidatorSet.
type ProposerRotation struct {
	mu       sync.Mutex
	nodeIds  []string
	current  int
	blockNum int64
}

func NewProposerRotation(nodeIds []string) *ProposerRotation {
	ids := make([]string, len(nodeIds))
	copy(ids, nodeIds)
	return &ProposerRotation{nodeIds: ids}
}

// Current returns the proposer for the current block.
func (pr *ProposerRotation) Current() string {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.nodeIds[pr.current%len(pr.nodeIds)]
}

// Advance moves to the next block proposer.
func (pr *ProposerRotation) Advance() string {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.blockNum++
	pr.current = int(pr.blockNum) % len(pr.nodeIds)
	return pr.nodeIds[pr.current]
}

// BlockNum returns how many blocks have been proposed.
func (pr *ProposerRotation) BlockNum() int64 {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.blockNum
}
