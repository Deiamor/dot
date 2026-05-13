package state

type EventType string

const (
	EventAccountCreated   EventType = "ACCOUNT_CREATED"
	EventSessionCreated   EventType = "SESSION_CREATED"
	EventDepositSimulated EventType = "DEPOSIT_SIMULATED"

	// Order lifecycle events (in order of occurrence)
	EventOrderReceived  EventType = "ORDER_RECEIVED"  // order arrived at the node (pre-block)
	EventOrderIncluded  EventType = "ORDER_INCLUDED"  // order included in a block batch
	EventOrderSubmitted EventType = "ORDER_SUBMITTED" // order processed by block (resting or immediate)
	EventOrderRejected  EventType = "ORDER_REJECTED"  // order rejected (risk, session, or FOK)
	EventOrderCancelled EventType = "ORDER_CANCELLED"
	EventOrderExpired   EventType = "ORDER_EXPIRED"
	EventTradeExecuted  EventType = "TRADE_EXECUTED"
	EventBalanceUpdated EventType = "BALANCE_UPDATED"
	EventFeeCharged     EventType = "FEE_CHARGED"
	EventBlockProcessed EventType = "BLOCK_PROCESSED"
	EventWithdrawal     EventType = "WITHDRAWAL"

	EventAll EventType = "*"
)

type Event struct {
	Type        EventType
	BlockHeight int64
	Payload     any
}

type AccountCreatedPayload struct {
	AccountId    string
	OwnerAddress string
}

type SessionCreatedPayload struct {
	SessionId string
	AccountId string
}

type DepositSimulatedPayload struct {
	AccountId string
	AssetId   string
	Amount    int64
}

type OrderReceivedPayload struct {
	OrderId   string
	AccountId string
	SessionId string
	MarketId  string
	Side      string
	Price     int64
	Quantity  int64
	TxHash    string
}

type OrderIncludedPayload struct {
	OrderId     string
	AccountId   string
	MarketId    string
	BlockHeight int64
	TxHash      string
}

type OrderRejectedPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
	Reason    string
}

type OrderSubmittedPayload struct {
	OrderId   string
	AccountId string
	MarketId  string
	Status    string
}

type OrderCancelledPayload struct {
	OrderId   string
	AccountId string
}

type OrderExpiredPayload struct {
	OrderId   string
	AccountId string
}

type TradeExecutedPayload struct {
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
}

type BalanceUpdatedPayload struct {
	AccountId    string
	AssetId      string
	NewAvailable int64
	NewReserved  int64
}

type FeeChargedPayload struct {
	AccountId string
	TradeId   string
	AssetId   string
	Amount    int64
	FeeType   string
}

type BlockProcessedPayload struct {
	BlockHeight int64
	TxCount     int
	TradeCount  int
}

type WithdrawalPayload struct {
	AccountId string
	AssetId   string
	Amount    int64
}
