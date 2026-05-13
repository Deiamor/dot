package risk

type RiskPolicy struct {
	MaxOrderQuantity         int64
	MinOrderQuantity         int64
	MaxDailyVolumePerSession int64
	MaxPriceDivergenceBps    int64
	// MaxPositionSize is the absolute net position limit per (account, market).
	// 0 means unlimited.
	MaxPositionSize int64
}

var DefaultRiskPolicy = RiskPolicy{
	MaxOrderQuantity:         1_000_000,
	MinOrderQuantity:         1,
	MaxDailyVolumePerSession: 100_000_000,
	MaxPositionSize:          0, // unlimited by default; set per-deployment
}
