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

// MarketResponse describes a single market for GET /markets.
type MarketResponse struct {
	MarketId       string `json:"market_id"`
	Type           string `json:"type"`    // "SPOT" | "PERP"
	Status         string `json:"status"`  // "ACTIVE" | "HALTED"
	MarkPrice      int64  `json:"mark_price"`
	FundingRateBps int64  `json:"funding_rate_bps,omitempty"`
}

// MarkPriceResponse is returned by GET /markprice/{marketId}.
type MarkPriceResponse struct {
	MarketId       string `json:"market_id"`
	MarkPrice      int64  `json:"mark_price"`
	FundingRateBps int64  `json:"funding_rate_bps"`
	BlockHeight    int64  `json:"block_height"`
}

// BalanceResponse is one asset balance for GET /balances/{accountId}.
type BalanceResponse struct {
	AssetId   string `json:"asset_id"`
	Available int64  `json:"available"`
	Reserved  int64  `json:"reserved"`
	Total     int64  `json:"total"`
}

// PositionResponse is one PERP position for GET /positions/{accountId}.
type PositionResponse struct {
	MarketId        string `json:"market_id"`
	NetQuantity     int64  `json:"net_quantity"`
	AvgEntryPrice   int64  `json:"avg_entry_price"`
	AllocatedMargin int64  `json:"allocated_margin"`
	UnrealizedPnL   int64  `json:"unrealized_pnl"`
	MarkPrice       int64  `json:"mark_price"`
	Side            string `json:"side"` // "LONG" | "SHORT" | "FLAT"
}

// CreateAccountRequest is the JSON body for POST /accounts.
type CreateAccountRequest struct {
	OwnerAddress        string `json:"owner_address"`
	RootPublicKey       string `json:"root_public_key"`
	WithdrawalPublicKey string `json:"withdrawal_public_key"`
}

// CreateSessionRequest is the JSON body for POST /sessions.
type CreateSessionRequest struct {
	AccountId      string   `json:"account_id"`
	AllowedMarkets []string `json:"allowed_markets"`
	MaxOrderAmount int64    `json:"max_order_amount"`
	ExpireAfterBlocks int64 `json:"expire_after_blocks"`
}

// SessionResponse is returned after session creation.
type SessionResponse struct {
	SessionId        string   `json:"session_id"`
	AccountId        string   `json:"account_id"`
	AllowedMarkets   []string `json:"allowed_markets"`
	SessionPublicKey string   `json:"session_public_key"`
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
