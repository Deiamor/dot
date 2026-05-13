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

// KYCTier represents the depth of identity verification completed.
// Higher tiers unlock larger trade limits.
type KYCTier int

const (
	KYCTierNone KYCTier = 0 // no verification (PENDING / REVOKED)
	KYCTier1    KYCTier = 1 // basic — name + ID document
	KYCTier2    KYCTier = 2 // enhanced — address + source of funds
	KYCTier3    KYCTier = 3 // institutional / accredited investor
)

// Jurisdiction tags the regulatory regime applicable to an account.
type Jurisdiction string

const (
	JurisdictionDefault Jurisdiction = "DEFAULT" // fallback when not specified
	JurisdictionUS      Jurisdiction = "US"
	JurisdictionEU      Jurisdiction = "EU"
	JurisdictionAPAC    Jurisdiction = "APAC"
)

type NativeAccount struct {
	AccountId           string
	OwnerAddress        string
	RootPublicKey       string
	WithdrawalPublicKey string
	AccountSequence     uint64
	Status              AccountStatus
	KYCStatus           KYCStatus
	KYCTier             KYCTier
	Jurisdiction        Jurisdiction
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
		KYCTier:             KYCTierNone,
		Jurisdiction:        JurisdictionDefault,
	}
}

// seedID produces a deterministic, prefix-tagged ID from an arbitrary seed string.
func seedID(prefix, seed string) string {
	h := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s-%x", prefix, h[:8])
}
