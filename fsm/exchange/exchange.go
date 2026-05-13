// Package exchange defines the interface between execution strategies
// and underlying perp DEXes or CEXes.
//
// Implement Exchange for any venue (HyperLiquid, dYdX, Aevo, …).
// All methods accept a context for timeout/cancellation propagation.
package exchange

import (
	"context"
	"time"
)

// Side is order direction.
type Side string

const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

// OrderType classifies execution semantics.
type OrderType string

const (
	Limit  OrderType = "LIMIT"
	Market OrderType = "MARKET"
)

// OrderStatus is the lifecycle state of a resting order.
type OrderStatus string

const (
	StatusOpen      OrderStatus = "OPEN"
	StatusFilled    OrderStatus = "FILLED"
	StatusCancelled OrderStatus = "CANCELLED"
	StatusRejected  OrderStatus = "REJECTED"
)

// Order represents a single resting or historical order.
type Order struct {
	ID            string
	Symbol        string
	Side          Side
	Type          OrderType
	Price         float64
	Qty           float64
	FilledQty     float64
	RemainingQty  float64
	Status        OrderStatus
	CreatedAt     time.Time
}

// Position is the current open position for one symbol.
type Position struct {
	Symbol        string
	Qty           float64  // signed: + = long, - = short
	AvgEntryPrice float64
	UnrealizedPnL float64
	Leverage      float64
}

// IsFlat returns true when there is no open position.
func (p Position) IsFlat() bool { return p.Qty == 0 }

// IsLong returns true for net long.
func (p Position) IsLong() bool { return p.Qty > 0 }

// IsShort returns true for net short.
func (p Position) IsShort() bool { return p.Qty < 0 }

// MarketSnapshot captures a point-in-time view of the market.
type MarketSnapshot struct {
	Symbol      string
	Bid         float64
	Ask         float64
	Mid         float64
	MarkPrice   float64
	IndexPrice  float64
	FundingRate float64  // current epoch rate, e.g. 0.0001 = 0.01%
	Volume24h   float64
	Timestamp   time.Time
}

// Spread returns ask - bid.
func (m MarketSnapshot) Spread() float64 { return m.Ask - m.Bid }

// OrderBook is a depth snapshot.
type OrderBook struct {
	Symbol string
	Bids   []PriceLevel
	Asks   []PriceLevel
}

// PriceLevel is a single row in the order book.
type PriceLevel struct {
	Price float64
	Size  float64
}

// Balance is the available collateral for one asset.
type Balance struct {
	Asset     string
	Available float64
	Reserved  float64
	Total     float64
}

// PlaceOrderRequest parameterises order submission.
type PlaceOrderRequest struct {
	Symbol     string
	Side       Side
	Type       OrderType
	Price      float64   // ignored for Market orders
	Qty        float64
	ReduceOnly bool
	ClientID   string    // optional idempotency key
}

// Exchange abstracts a perp DEX or CEX for strategy execution.
// All implementations must be safe for concurrent use.
type Exchange interface {
	// ── Market data ──────────────────────────────────────────────────

	// MarketSnapshot returns live prices and funding for symbol.
	MarketSnapshot(ctx context.Context, symbol string) (MarketSnapshot, error)

	// OrderBook returns the current depth snapshot.
	OrderBook(ctx context.Context, symbol string, depth int) (OrderBook, error)

	// ── Account state ────────────────────────────────────────────────

	// Position returns the current open position for symbol.
	Position(ctx context.Context, symbol string) (Position, error)

	// Balances returns all collateral balances.
	Balances(ctx context.Context) ([]Balance, error)

	// OpenOrders lists all resting orders for symbol.
	OpenOrders(ctx context.Context, symbol string) ([]Order, error)

	// ── Execution ────────────────────────────────────────────────────

	// PlaceOrder submits a new order and returns it with the exchange-assigned ID.
	PlaceOrder(ctx context.Context, req PlaceOrderRequest) (Order, error)

	// CancelOrder cancels a single order by its exchange ID.
	CancelOrder(ctx context.Context, symbol, orderID string) error

	// CancelAllOrders cancels every resting order for symbol.
	CancelAllOrders(ctx context.Context, symbol string) error
}
