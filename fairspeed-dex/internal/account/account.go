package account

import (
	"crypto/rand"
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

func NewNativeAccount(ownerAddress, rootPublicKey, withdrawalPublicKey string) NativeAccount {
	return NativeAccount{
		AccountId:           newID("acc"),
		OwnerAddress:        ownerAddress,
		RootPublicKey:       rootPublicKey,
		WithdrawalPublicKey: withdrawalPublicKey,
		AccountSequence:     0,
		Status:              AccountStatusActive,
	}
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}
