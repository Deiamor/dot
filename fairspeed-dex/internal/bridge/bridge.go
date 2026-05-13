package bridge

// BridgeDeposit represents an external chain deposit being attested by validators.
type BridgeDeposit struct {
	DepositId    string
	AccountId    string // destination account on the DEX
	AssetId      string
	Amount       int64
	SourceChain  string
	AttestedStake int64 // cumulative stake of validators that attested
	Completed    bool  // true once quorum reached and funds credited
}

// QuorumReached returns true when attestedStake exceeds 2/3 of totalStake.
func QuorumReached(attestedStake, totalStake int64) bool {
	if totalStake <= 0 {
		return false
	}
	// Require > 2/3: attestedStake * 3 > totalStake * 2
	return attestedStake*3 > totalStake*2
}
