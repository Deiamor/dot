package clob

import (
	"crypto/rand"
	"fmt"
)

type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
)

type TimeInForce string

const (
	TimeInForceGtc TimeInForce = "GTC"
	TimeInForceIoc TimeInForce = "IOC"
	TimeInForceFok TimeInForce = "FOK"
)

type OrderStatus string

const (
	OrderStatusOpen            OrderStatus = "OPEN"
	OrderStatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderStatusFilled          OrderStatus = "FILLED"
	OrderStatusCancelled       OrderStatus = "CANCELLED"
	OrderStatusRejected        OrderStatus = "REJECTED"
	OrderStatusExpired         OrderStatus = "EXPIRED"
)

type Order struct {
	OrderId             string
	AccountId           string
	SessionId           string
	MarketId            string
	Side                OrderSide
	OrderType           OrderType
	Price               int64
	Quantity            int64
	RemainingQuantity   int64
	TimeInForce         TimeInForce
	ClientOrderId       string
	AccountSequence     uint64
	ExpireBlockHeight   int64
	CreatedBlockHeight  int64
	Signature           string
	Status              OrderStatus
}

func NewLimitOrder(accountId, sessionId, marketId string, side OrderSide, price, qty int64, tif TimeInForce, blockHeight int64) Order {
	return Order{
		OrderId:            newOrderID(),
		AccountId:          accountId,
		SessionId:          sessionId,
		MarketId:           marketId,
		Side:               side,
		OrderType:          OrderTypeLimit,
		Price:              price,
		Quantity:           qty,
		RemainingQuantity:  qty,
		TimeInForce:        tif,
		CreatedBlockHeight: blockHeight,
		Status:             OrderStatusOpen,
	}
}

func newOrderID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("ord-%x", b)
}

func (o Order) IsBuy() bool    { return o.Side == OrderSideBuy }
func (o Order) IsSell() bool   { return o.Side == OrderSideSell }
func (o Order) IsFilled() bool { return o.RemainingQuantity == 0 }
func (o Order) NotionalValue() int64 { return o.Price * o.Quantity }
