package account

// PendingWithdrawal represents a large withdrawal that is queued pending a timelock delay.
type PendingWithdrawal struct {
	WithdrawalId  string
	AccountId     string
	AssetId       string
	Amount        int64
	ReadyAtHeight int64
}
