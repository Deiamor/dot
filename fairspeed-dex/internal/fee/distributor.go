package fee

// ValidatorRewardAccountId returns the virtual account ID for a validator's rewards.
// This account is credited when fees are distributed; validators call Withdraw to claim.
func ValidatorRewardAccountId(validatorId string) string {
	return "val-reward:" + validatorId
}

// DistributionShare computes one validator's share of the total fee pool.
// Uses integer arithmetic: share = (totalFees * validatorStake) / totalStake.
// Returns 0 if totalStake is 0.
func DistributionShare(totalFees, validatorStake, totalStake int64) int64 {
	if totalStake == 0 || totalFees == 0 {
		return 0
	}
	return totalFees * validatorStake / totalStake
}
