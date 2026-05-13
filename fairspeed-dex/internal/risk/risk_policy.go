package risk

type RiskPolicy struct {
	MaxOrderQuantity         int64
	MinOrderQuantity         int64
	MaxDailyVolumePerSession int64
	MaxPriceDivergenceBps    int64
	// MaxPositionSize is the absolute net position limit per (account, market).
	// 0 means unlimited.
	MaxPositionSize int64
	// RequireKYC enforces that the trader's account has KYCStatus == APPROVED or EXEMPT
	// before an order is accepted. Set false for test / bootstrap environments.
	RequireKYC bool
	// RequirePerpKYC enforces KYC for PERP market orders independent of RequireKYC.
	// Enables stricter compliance requirements for leverage products.
	RequirePerpKYC bool
	// AMLSingleTradeLimitNotional fires an AML alert when a single trade's notional
	// (price × quantity) exceeds this value. 0 means disabled.
	AMLSingleTradeLimitNotional int64
	// MaxOrdersPerBlock is the maximum number of orders a single account may submit
	// in one block. 0 means unlimited.
	MaxOrdersPerBlock int64
	// WithdrawTimelockThreshold is the minimum amount above which a withdrawal
	// must go through a timelock delay. 0 means all withdrawals are instant.
	WithdrawTimelockThreshold int64
	// WithdrawTimelockBlocks is the number of blocks a large withdrawal must wait
	// before it is auto-finalized. 0 means instant even for large amounts.
	WithdrawTimelockBlocks int64
}

var DefaultRiskPolicy = RiskPolicy{
	MaxOrderQuantity:            1_000_000,
	MinOrderQuantity:            1,
	MaxDailyVolumePerSession:    100_000_000,
	MaxPositionSize:             0,    // unlimited
	RequireKYC:                  false, // disabled in test/bootstrap
	AMLSingleTradeLimitNotional: 0,    // disabled
	MaxOrdersPerBlock:           0,    // unlimited by default
	WithdrawTimelockThreshold:   0,    // no timelock by default
	WithdrawTimelockBlocks:      0,
}
