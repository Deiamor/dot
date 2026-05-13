package validator

import "fmt"

// ValidatorStore is the persistence interface the keeper uses.
// AppState implements this.
type ValidatorStore interface {
	GetValidator(validatorId string) (Validator, bool)
	SetValidator(v Validator)
	AllValidators() []Validator
}

// EventPublisher is the subset of EventBus the keeper needs.
type EventPublisher interface {
	PublishValidatorBonded(validatorId, moniker string, stake int64, blockHeight int64)
	PublishValidatorSlashed(validatorId string, reason string, slashed, remaining int64, blockHeight int64)
	PublishValidatorUnbonded(validatorId string, blockHeight int64)
}

// SlashPolicy holds slashing fractions in basis points (1 bps = 0.01%).
type SlashPolicy struct {
	DoubleSignSlashBps int64 // default 500 = 5%
	FrontRunSlashBps   int64 // default 100 = 1%
}

var DefaultSlashPolicy = SlashPolicy{
	DoubleSignSlashBps: 500,
	FrontRunSlashBps:   100,
}

// ValidatorKeeper manages the active validator set and slashing logic.
type ValidatorKeeper struct {
	store  ValidatorStore
	bus    EventPublisher
	policy SlashPolicy
}

func NewValidatorKeeper(store ValidatorStore, bus EventPublisher) *ValidatorKeeper {
	return &ValidatorKeeper{store: store, bus: bus, policy: DefaultSlashPolicy}
}

func NewValidatorKeeperWithPolicy(store ValidatorStore, bus EventPublisher, policy SlashPolicy) *ValidatorKeeper {
	return &ValidatorKeeper{store: store, bus: bus, policy: policy}
}

// BondValidator adds a new validator or increases the stake of an existing one.
func (k *ValidatorKeeper) BondValidator(validatorId, moniker, pubKey string, stake int64, blockHeight int64) error {
	if stake < MinBondAmount {
		return fmt.Errorf("stake %d is below minimum %d", stake, MinBondAmount)
	}

	v, exists := k.store.GetValidator(validatorId)
	if !exists {
		v = Validator{
			ValidatorId: validatorId,
			Moniker:     moniker,
			PubKey:      pubKey,
			Stake:       stake,
			Status:      StatusBonded,
		}
	} else {
		v.Stake += stake
		v.Status = StatusBonded
	}
	k.store.SetValidator(v)
	k.bus.PublishValidatorBonded(v.ValidatorId, v.Moniker, v.Stake, blockHeight)
	return nil
}

// UnbondValidator transitions a validator to UNBONDING/UNBONDED state.
func (k *ValidatorKeeper) UnbondValidator(validatorId string, blockHeight int64) error {
	v, ok := k.store.GetValidator(validatorId)
	if !ok {
		return fmt.Errorf("validator %s not found", validatorId)
	}
	v.Status = StatusUnbonded
	k.store.SetValidator(v)
	k.bus.PublishValidatorUnbonded(validatorId, blockHeight)
	return nil
}

// SlashDoubleSign slashes a validator by DoubleSignSlashBps for double-sign evidence.
// If remaining stake < MinBondAmount the validator is forced-unbonded.
func (k *ValidatorKeeper) SlashDoubleSign(validatorId string, blockHeight int64) (int64, error) {
	return k.slash(validatorId, k.policy.DoubleSignSlashBps, "DOUBLE_SIGN", blockHeight)
}

// SlashFrontRun slashes a validator by FrontRunSlashBps for front-run evidence.
func (k *ValidatorKeeper) SlashFrontRun(validatorId string, blockHeight int64) (int64, error) {
	return k.slash(validatorId, k.policy.FrontRunSlashBps, "FRONT_RUN", blockHeight)
}

func (k *ValidatorKeeper) slash(validatorId string, bps int64, reason string, blockHeight int64) (int64, error) {
	v, ok := k.store.GetValidator(validatorId)
	if !ok {
		return 0, fmt.Errorf("validator %s not found", validatorId)
	}
	if v.Status == StatusUnbonded {
		return 0, fmt.Errorf("validator %s is already unbonded", validatorId)
	}

	slashed := v.Stake * bps / 10_000
	if slashed == 0 {
		slashed = 1 // minimum 1 unit slash
	}
	v.Stake -= slashed

	if v.Stake < MinBondAmount {
		v.Stake = 0
		v.Status = StatusSlashed
		k.bus.PublishValidatorSlashed(validatorId, reason, slashed, 0, blockHeight)
		k.bus.PublishValidatorUnbonded(validatorId, blockHeight)
	} else {
		v.Status = StatusSlashed
		k.bus.PublishValidatorSlashed(validatorId, reason, slashed, v.Stake, blockHeight)
	}
	k.store.SetValidator(v)
	return slashed, nil
}

// ActiveSet returns all BONDED validators sorted by descending stake.
func (k *ValidatorKeeper) ActiveSet() []Validator {
	all := k.store.AllValidators()
	var bonded []Validator
	for _, v := range all {
		if v.Status == StatusBonded {
			bonded = append(bonded, v)
		}
	}
	// Simple insertion sort — validator sets are small.
	for i := 1; i < len(bonded); i++ {
		for j := i; j > 0 && bonded[j].Stake > bonded[j-1].Stake; j-- {
			bonded[j], bonded[j-1] = bonded[j-1], bonded[j]
		}
	}
	return bonded
}

// TotalStake returns the sum of stake across all BONDED validators.
func (k *ValidatorKeeper) TotalStake() int64 {
	var total int64
	for _, v := range k.store.AllValidators() {
		if v.Status == StatusBonded {
			total += v.Stake
		}
	}
	return total
}

// GetValidator returns the validator by ID.
func (k *ValidatorKeeper) GetValidator(validatorId string) (Validator, bool) {
	return k.store.GetValidator(validatorId)
}
