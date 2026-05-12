package fee

type FeeCalculator struct {
	Policy FeeBps
}

func NewFeeCalculator(policy FeeBps) *FeeCalculator {
	return &FeeCalculator{Policy: policy}
}

func (c *FeeCalculator) CalcMakerFee(notional int64) int64 {
	return notional * c.Policy.MakerBps / 10_000
}

func (c *FeeCalculator) CalcTakerFee(notional int64) int64 {
	return notional * c.Policy.TakerBps / 10_000
}

// CalcFees returns (makerFee, takerFee) for a trade.
// notional = price * qty
func (c *FeeCalculator) CalcFees(price, qty int64) (makerFee, takerFee int64) {
	notional := price * qty
	return c.CalcMakerFee(notional), c.CalcTakerFee(notional)
}
