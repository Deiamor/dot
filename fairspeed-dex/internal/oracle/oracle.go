package oracle

import "sort"

// PriceSubmission is a single validator's price update for a market.
type PriceSubmission struct {
	MarketId    string
	ValidatorId string
	Price       int64
	BlockHeight int64
}

// MedianPrice computes the integer median of a non-empty price slice.
// For an even-length slice the lower of the two middle elements is returned
// (consistent, deterministic across all nodes). Returns 0 for an empty slice.
func MedianPrice(prices []int64) int64 {
	if len(prices) == 0 {
		return 0
	}
	sorted := make([]int64, len(prices))
	copy(sorted, prices)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}
