package api

import (
	"github.com/byunghee1994/fairspeed-dex/internal/settlement"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// SubmitOrderRequest is the JSON body for POST /orders.
type SubmitOrderRequest struct {
	AccountId     string `json:"account_id"`
	SessionId     string `json:"session_id"`
	MarketId      string `json:"market_id"`
	Side          string `json:"side"`
	Price         int64  `json:"price"`
	Quantity      int64  `json:"quantity"`
	TimeInForce   string `json:"time_in_force"`
	ClientOrderId string `json:"client_order_id,omitempty"`
}

// OrderResponse is returned after a successful order submission.
type OrderResponse struct {
	OrderId     string `json:"order_id"`
	Status      string `json:"status"`
	MarketId    string `json:"market_id"`
	Side        string `json:"side"`
	Price       int64  `json:"price"`
	Quantity    int64  `json:"quantity"`
	Remaining   int64  `json:"remaining_quantity"`
	BlockHeight int64  `json:"block_height"`
	TradeCount  int    `json:"trade_count"`
}

// ErrorResponse wraps error details.
type ErrorResponse struct {
	Error string `json:"error"`
}

// PriceLevelResponse is one row of the L2 orderbook.
type PriceLevelResponse struct {
	Price    int64 `json:"price"`
	Quantity int64 `json:"quantity"`
	Orders   int   `json:"orders"`
}

// OrderBookResponse is the full L2 snapshot.
type OrderBookResponse struct {
	MarketId string               `json:"market_id"`
	Bids     []PriceLevelResponse `json:"bids"`
	Asks     []PriceLevelResponse `json:"asks"`
}

// AccountResponse is returned by GET /accounts/{accountId}.
type AccountResponse struct {
	AccountId       string `json:"account_id"`
	OwnerAddress    string `json:"owner_address"`
	Status          string `json:"status"`
	KYCStatus       string `json:"kyc_status"`
	AccountSequence uint64 `json:"account_sequence"`
}

// KYCStatusResponse is returned by GET /reports/kyc/{accountId}.
type KYCStatusResponse struct {
	AccountId string `json:"account_id"`
	KYCStatus string `json:"kyc_status"`
}

// TradeResponse is a single executed trade.
type TradeResponse struct {
	TradeId        string `json:"trade_id"`
	MarketId       string `json:"market_id"`
	MakerOrderId   string `json:"maker_order_id"`
	TakerOrderId   string `json:"taker_order_id"`
	Price          int64  `json:"price"`
	Quantity       int64  `json:"quantity"`
	MakerFeeAmount int64  `json:"maker_fee"`
	TakerFeeAmount int64  `json:"taker_fee"`
	BlockHeight    int64  `json:"block_height"`
}

func tradeToResponse(t settlement.TradeExecution) TradeResponse {
	return TradeResponse{
		TradeId:        t.TradeId,
		MarketId:       t.MarketId,
		MakerOrderId:   t.MakerOrderId,
		TakerOrderId:   t.TakerOrderId,
		Price:          t.Price,
		Quantity:       t.Quantity,
		MakerFeeAmount: t.MakerFeeAmount,
		TakerFeeAmount: t.TakerFeeAmount,
		BlockHeight:    t.BlockHeight,
	}
}

func tradePayloadToResponse(p state.TradeExecutedPayload) TradeResponse {
	return TradeResponse{
		TradeId:        p.TradeId,
		MarketId:       p.MarketId,
		MakerOrderId:   p.MakerOrderId,
		TakerOrderId:   p.TakerOrderId,
		Price:          p.Price,
		Quantity:       p.Quantity,
		MakerFeeAmount: p.MakerFeeAmount,
		TakerFeeAmount: p.TakerFeeAmount,
	}
}
