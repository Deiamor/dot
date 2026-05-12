package node

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

type LocalBlock struct {
	Height     int64
	Batch      fairbatch.FairBatch
	ParentHash string
	Hash       string
}

type BlockResult struct {
	Height     int64
	TxCount    int
	TradeCount int
	Trades     []settlement.TradeExecution
	Events     []state.Event
	Error      error
}

func NewLocalBlock(height int64, batch fairbatch.FairBatch, parentHash string) LocalBlock {
	raw := parentHash + strconv.FormatInt(height, 10) + batch.BatchId
	sum := sha256.Sum256([]byte(raw))
	return LocalBlock{
		Height:     height,
		Batch:      batch,
		ParentHash: parentHash,
		Hash:       fmt.Sprintf("%x", sum),
	}
}
