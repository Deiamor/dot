package node

import (
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
)

type BlockProposal struct {
	ProposedBlock LocalBlock
	ProposedAt    int64
}

func ProposeBlock(height int64, batch fairbatch.FairBatch, parentHash string) BlockProposal {
	return BlockProposal{
		ProposedBlock: NewLocalBlock(height, batch, parentHash),
		ProposedAt:    time.Now().UnixNano(),
	}
}
