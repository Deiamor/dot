package clob

// MarketType distinguishes spot from perpetual futures markets.
type MarketType string

const (
	MarketTypeSpot MarketType = "SPOT"
	MarketTypePerp MarketType = "PERP"
)

// PerpConfig holds perpetual-specific parameters for a market.
// All margin values are in basis points (1 bps = 0.01%).
type PerpConfig struct {
	InitialMarginBps      int64 // e.g. 1000 = 10% → max 10x leverage
	MaintenanceMarginBps  int64 // e.g.  500 = 5%  → liquidation threshold
	MaxLeverage           int64 // e.g. 20
	FundingIntervalBlocks int64 // blocks between funding settlements
	MaxFundingRateBps     int64 // per-epoch rate clamp, e.g. 100 = 1%
}
