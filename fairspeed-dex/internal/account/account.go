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

// KYCStatus tracks compliance verification state for an account.
type KYCStatus string

const (
	KYCStatusPending  KYCStatus = "PENDING"  // awaiting verification
	KYCStatusApproved KYCStatus = "APPROVED" // verified, trading allowed
	KYCStatusRevoked  KYCStatus = "REVOKED"  // previously approved, now revoked
	KYCStatusExempt   KYCStatus = "EXEMPT"   // test / bootstrap — always allowed
)

type NativeAccount struct {
	AccountId           string
	OwnerAddress        string
	RootPublicKey       string
	WithdrawalPublicKey string
	AccountSequence     uint64
	Status              AccountStatus
	KYCStatus           KYCStatus
}

// NewNativeAccount creates an account with a deterministic ID derived from seed
// (the transaction hash). All nodes processing the same tx will produce the same ID.
// New accounts start with KYCStatusPending — they must be approved before trading
// when RequireKYC is enabled on the node's RiskPolicy.
func NewNativeAccount(ownerAddress, rootPublicKey, withdrawalPublicKey, seed string) NativeAccount {
	return NativeAccount{
		AccountId:           seedID("acc", seed),
		OwnerAddress:        ownerAddress,
		RootPublicKey:       rootPublicKey,
		WithdrawalPublicKey: withdrawalPublicKey,
		AccountSequence:     0,
		Status:              AccountStatusActive,
		KYCStatus:           KYCStatusPending,
	}
}

// seedID produces a deterministic, prefix-tagged ID from an arbitrary seed string.
func seedID(prefix, seed string) string {
	h := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s-%x", prefix, h[:8])
}
