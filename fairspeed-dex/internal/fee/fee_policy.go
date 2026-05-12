package fee

type FeeBps struct {
	MakerBps int64
	TakerBps int64
}

var DefaultFeePolicy = FeeBps{
	MakerBps: 2,
	TakerBps: 5,
}

type FeeType string

const (
	FeeTypeMaker FeeType = "MAKER"
	FeeTypeTaker FeeType = "TAKER"
)
