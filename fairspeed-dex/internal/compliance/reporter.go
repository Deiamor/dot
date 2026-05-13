package compliance

import "github.com/byunghee1994/fairspeed-dex/internal/settlement"

// TradeRecord is a compliance-grade representation of a trade suitable for
// regulatory reporting (MiCA Article 76, FATF Travel Rule, etc.).
type TradeRecord struct {
	TradeId        string `json:"trade_id"`
	MarketId       string `json:"market_id"`
	BlockHeight    int64  `json:"block_height"`
	Price          int64  `json:"price"`
	Quantity       int64  `json:"quantity"`
	Notional       int64  `json:"notional"`
	MakerAccountId string `json:"maker_account_id"`
	TakerAccountId string `json:"taker_account_id"`
	MakerFee       int64  `json:"maker_fee"`
	TakerFee       int64  `json:"taker_fee"`
	TotalFee       int64  `json:"total_fee"`
}

// ReportTrades converts raw settlement executions to compliance trade records.
// This is a pure function — no external dependencies, trivially testable.
func ReportTrades(trades []settlement.TradeExecution) []TradeRecord {
	out := make([]TradeRecord, 0, len(trades))
	for _, t := range trades {
		out = append(out, TradeRecord{
			TradeId:        t.TradeId,
			MarketId:       t.MarketId,
			BlockHeight:    t.BlockHeight,
			Price:          t.Price,
			Quantity:       t.Quantity,
			Notional:       t.Price * t.Quantity,
			MakerAccountId: t.MakerAccountId,
			TakerAccountId: t.TakerAccountId,
			MakerFee:       t.MakerFeeAmount,
			TakerFee:       t.TakerFeeAmount,
			TotalFee:       t.MakerFeeAmount + t.TakerFeeAmount,
		})
	}
	return out
}
