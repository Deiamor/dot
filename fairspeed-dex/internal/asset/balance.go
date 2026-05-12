package asset

type Balance struct {
	AccountId string
	AssetId   string
	Available int64
	Reserved  int64
}

type BalanceDelta struct {
	AccountId        string
	AssetId          string
	AvailableChange  int64
	ReservedChange   int64
}

func (b Balance) Total() int64 {
	return b.Available + b.Reserved
}

func ApplyDelta(b Balance, d BalanceDelta) Balance {
	return Balance{
		AccountId: b.AccountId,
		AssetId:   b.AssetId,
		Available: b.Available + d.AvailableChange,
		Reserved:  b.Reserved + d.ReservedChange,
	}
}
