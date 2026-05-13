package token

const (
	TotalSupply int64 = 1_000_000_000_000_000 // 1B FAIR * 1e6 decimals
	Decimals          = 6
	AssetId           = "FAIR"

	// Allocation buckets (in base units)
	CommunityAlloc int64 = 350_000_000_000_000 // 35%
	TeamAlloc      int64 = 200_000_000_000_000 // 20%
	InvestorAlloc  int64 = 150_000_000_000_000 // 15%
	TreasuryAlloc  int64 = 200_000_000_000_000 // 20%
	ValidatorAlloc int64 = 100_000_000_000_000 // 10%
)

// FeeDiscountBps returns the trading fee discount in bps for a given FAIR balance.
// Tiers: 0 FAIR=0bps, 1K=10bps, 10K=20bps, 100K=30bps, 1M=50bps
// Balances are in base units (1 FAIR = 1_000_000 base units).
func FeeDiscountBps(fairBalance int64) int64 {
	const (
		tier1K   = 1_000 * 1_000_000   // 1K FAIR in base units
		tier10K  = 10_000 * 1_000_000  // 10K FAIR in base units
		tier100K = 100_000 * 1_000_000 // 100K FAIR in base units
		tier1M   = 1_000_000 * 1_000_000 // 1M FAIR in base units
	)
	switch {
	case fairBalance >= tier1M:
		return 50
	case fairBalance >= tier100K:
		return 30
	case fairBalance >= tier10K:
		return 20
	case fairBalance >= tier1K:
		return 10
	default:
		return 0
	}
}

// VestingSchedule describes a token allocation with cliff and linear vesting.
type VestingSchedule struct {
	TotalAmount   int64
	CliffBlocks   int64
	VestingBlocks int64 // total vesting duration after cliff
	StartBlock    int64
	Released      int64
}

// Vested returns how much is unlocked at the given block height.
func (v VestingSchedule) Vested(blockHeight int64) int64 {
	elapsed := blockHeight - v.StartBlock
	if elapsed < v.CliffBlocks {
		return 0
	}
	// After cliff: linear vesting over VestingBlocks
	vestElapsed := elapsed - v.CliffBlocks
	if vestElapsed >= v.VestingBlocks {
		return v.TotalAmount
	}
	if v.VestingBlocks == 0 {
		return v.TotalAmount
	}
	return v.TotalAmount * vestElapsed / v.VestingBlocks
}

// ReleasableNow returns how much can be released at blockHeight (Vested - Released).
func (v VestingSchedule) ReleasableNow(blockHeight int64) int64 {
	vested := v.Vested(blockHeight)
	releasable := vested - v.Released
	if releasable < 0 {
		return 0
	}
	return releasable
}
