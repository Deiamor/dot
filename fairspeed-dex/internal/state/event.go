package state

type EventType string

const (
	EventAccountCreated   EventType = "ACCOUNT_CREATED"
	EventSessionCreated   EventType = "SESSION_CREATED"
	EventDepositSimulated EventType = "DEPOSIT_SIMULATED"
	EventOrderSubmitted   EventType = "ORDER_SUBMITTED"
	EventOrderCancelled   EventType = "ORDER_CANCELLED"
	EventOrderExpired     EventType = "ORDER_EXPIRED"
	EventTradeExecuted    EventType = "TRADE_EXECUTED"
	EventBalanceUpdated   EventType = "BALANCE_UPDATED"
	EventFeeCharged       EventType = "FEE_CHARGED"
	EventBlockProcessed   EventType = "BLOCK_PROCESSED"

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
