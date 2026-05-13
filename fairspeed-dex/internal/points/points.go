package points

const (
	PointsPerNotional    int64 = 1 // 1 point per $1 notional
	PerpBoostNumerator   int64 = 2 // PERP trades: 2x
	PerpBoostDenominator int64 = 1
	MakerBoostNum        int64 = 3 // maker orders: 1.5x (3/2)
	MakerBoostDen        int64 = 2
	ReferralPercent      int64 = 10 // 10% of referee's points
	EarlyBirdMultiplier  int64 = 2  // first 10,000 wallets: 2x
	EarlyBirdLimit       int64 = 10_000
)

// TradePoints calculates points for a single trade.
// notional = price * qty, isPerp and isMaker determine bonuses.
func TradePoints(notional int64, isPerp, isMaker bool) int64 {
	pts := notional * PointsPerNotional

	// Apply PERP boost (2x)
	if isPerp {
		pts = pts * PerpBoostNumerator / PerpBoostDenominator
	}

	// Apply maker boost (1.5x = 3/2)
	if isMaker {
		pts = pts * MakerBoostNum / MakerBoostDen
	}

	return pts
}

// AccountPoints holds the points balance for a single account.
type AccountPoints struct {
	AccountId      string
	TotalPoints    int64
	TradePoints    int64
	ReferralPoints int64
	IsEarlyBird    bool
	ReferrerId     string // accountId of referrer (empty if none)
}
