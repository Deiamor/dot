package risk

type RiskPolicy struct {
	MaxOrderQuantity        int64
	MinOrderQuantity        int64
	MaxDailyVolumePerSession int64
	MaxPriceDivergenceBps   int64
}

var DefaultRiskPolicy = RiskPolicy{
	MaxOrderQuantity:        1_000_000,
	MinOrderQuantity:        1,
	MaxDailyVolumePerSession: 100_000_000,
}
