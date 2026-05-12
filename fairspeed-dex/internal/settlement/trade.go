package settlement

import (
	"crypto/rand"
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

type TradeExecution struct {
	TradeId        string
	MarketId       string
	MakerOrderId   string
	TakerOrderId   string
	MakerAccountId string
	TakerAccountId string
	Price          int64
	Quantity       int64
	MakerFeeAmount int64
	TakerFeeAmount int64
	BlockHeight    int64
}

func NewTradeExecution(result clob.MatchResult, makerFee, takerFee int64) TradeExecution {
	return TradeExecution{
		TradeId:        newTradeID(),
		MarketId:       result.MarketId,
		MakerOrderId:   result.MakerOrderId,
		TakerOrderId:   result.TakerOrderId,
		MakerAccountId: result.MakerAccountId,
		TakerAccountId: result.TakerAccountId,
		Price:          result.Price,
		Quantity:       result.Quantity,
		MakerFeeAmount: makerFee,
		TakerFeeAmount: takerFee,
		BlockHeight:    result.BlockHeight,
	}
}

func (t TradeExecution) NotionalValue() int64 {
	return t.Price * t.Quantity
}

func newTradeID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("trd-%x", b)
}
