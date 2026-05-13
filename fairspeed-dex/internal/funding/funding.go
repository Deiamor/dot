package funding

// FundingEpoch records one funding settlement for a perpetual market.
type FundingEpoch struct {
	MarketId    string
	RateBps     int64 // signed: positive = longs pay shorts, negative = shorts pay longs
	MarkPrice   int64
	IndexPrice  int64
	BlockHeight int64
}

// CalcFundingRate computes the per-epoch funding rate in basis points.
// Formula: (markPrice - indexPrice) * 10_000 / indexPrice
// Result is clamped to [-maxRateBps, +maxRateBps].
// Returns 0 when indexPrice == 0.
func CalcFundingRate(markPrice, indexPrice, maxRateBps int64) int64 {
	if indexPrice == 0 {
		return 0
	}
	rate := (markPrice - indexPrice) * 10_000 / indexPrice
	if rate > maxRateBps {
		return maxRateBps
	}
	if rate < -maxRateBps {
		return -maxRateBps
	}
	return rate
}

// FundingPayment computes the funding payment for one position.
// Positive result = position owes payment (longs when rate > 0).
// Negative result = position receives payment (shorts when rate > 0).
// Formula: netQty * avgEntryPrice * rateBps / 10_000
func FundingPayment(netQty, avgEntryPrice, rateBps int64) int64 {
	return netQty * avgEntryPrice * rateBps / 10_000
}
