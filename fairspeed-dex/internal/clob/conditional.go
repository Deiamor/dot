package clob

// TriggerCondition describes when a conditional order activates.
type TriggerCondition string

const (
	TriggerGTE TriggerCondition = "GTE" // activate when markPrice >= triggerPrice
	TriggerLTE TriggerCondition = "LTE" // activate when markPrice <= triggerPrice
)

// ConditionalOrder is a pending order that activates when a mark-price condition is met.
type ConditionalOrder struct {
	OrderId            string
	AccountId          string
	SessionId          string
	MarketId           string
	Side               OrderSide
	OrderType          OrderType
	Price              int64           // limit price (0 for market orders)
	Quantity           int64
	TriggerPrice       int64
	TriggerCondition   TriggerCondition
	ReduceOnly         bool
	ExpireBlockHeight  int64           // 0 = no expiry
	CreatedBlockHeight int64
	Status             OrderStatus
	AccountSequence    uint64
	Signature          string
}
