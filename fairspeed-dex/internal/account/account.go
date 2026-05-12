package account

import (
	"crypto/sha256"
	"fmt"
)

type AccountStatus string

const (
	AccountStatusActive   AccountStatus = "ACTIVE"
	AccountStatusDisabled AccountStatus = "DISABLED"
)

type NativeAccount struct {
	AccountId           string
	OwnerAddress        string
	RootPublicKey       string
	WithdrawalPublicKey string
	AccountSequence     uint64
	Status              AccountStatus
}

// NewNativeAccount creates an account with a deterministic ID derived from seed
// (the transaction hash). All nodes processing the same tx will produce the same ID.
func NewNativeAccount(ownerAddress, rootPublicKey, withdrawalPublicKey, seed string) NativeAccount {
	return NativeAccount{
		AccountId:           seedID("acc", seed),
		OwnerAddress:        ownerAddress,
		RootPublicKey:       rootPublicKey,
		WithdrawalPublicKey: withdrawalPublicKey,
		AccountSequence:     0,
		Status:              AccountStatusActive,
	}
}

// seedID produces a deterministic, prefix-tagged ID from an arbitrary seed string.
func seedID(prefix, seed string) string {
	h := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s-%x", prefix, h[:8])
}
