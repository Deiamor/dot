// Package validator manages the open validator set and stake-based slashing.
//
// Validators bond native stake to join the active set. Slashing reduces
// stake when evidence of Byzantine behaviour (double-sign, front-run) is
// confirmed. Validators whose stake falls below MinBondAmount are
// automatically moved to UNBONDED and removed from the active set.
package validator

import (
	"crypto/rand"
	"fmt"
)

// ValidatorStatus reflects the bonding lifecycle of a validator.
type ValidatorStatus string

const (
	StatusBonded   ValidatorStatus = "BONDED"
	StatusUnbonding ValidatorStatus = "UNBONDING"
	StatusUnbonded  ValidatorStatus = "UNBONDED"
	StatusSlashed   ValidatorStatus = "SLASHED"
)

// MinBondAmount is the minimum stake required to remain in the active set.
const MinBondAmount int64 = 1_000

// DefaultUnbondingPeriod is the number of blocks a validator must wait after
// requesting to unbond before their stake is fully released and their status
// transitions to UNBONDED. 21 blocks mirrors common Cosmos-SDK convention.
const DefaultUnbondingPeriod int64 = 21

// Validator represents a node that participates in block proposal and voting.
type Validator struct {
	ValidatorId     string
	Moniker         string
	PubKey          string          // ed25519 public key (hex)
	Stake           int64           // total bonded stake (native token units)
	Status          ValidatorStatus
	UnbondingHeight int64           // block at which UNBONDING → UNBONDED; 0 when not unbonding
}

// NewValidator creates a Validator in BONDED state.
func NewValidator(moniker, pubKey string, stake int64) Validator {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return Validator{
		ValidatorId: fmt.Sprintf("val-%x", b),
		Moniker:     moniker,
		PubKey:      pubKey,
		Stake:       stake,
		Status:      StatusBonded,
	}
}
