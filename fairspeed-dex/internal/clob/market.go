package clob

// MarketStatus represents the operational state of a trading market.
type MarketStatus string

const (
	MarketStatusActive MarketStatus = "ACTIVE"
	MarketStatusHalted MarketStatus = "HALTED"
)

// MarketInfo holds the current state of a market including halt information.
type MarketInfo struct {
	MarketId       string
	Status         MarketStatus
	HaltReason     string
	HaltedAtHeight int64
	Type           MarketType  // SPOT (default) | PERP
	PerpConfig     *PerpConfig // non-nil only for PERP markets
}
