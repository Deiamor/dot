package scenario_test

// Stage 6: Open validator set + slashing tests.
//
// Verifies:
//  1. TxBondValidator adds validator to active set with correct stake
//  2. Multiple validators: TotalStake = sum of individual stakes
//  3. TxUnbondValidator removes validator from active set
//  4. DOUBLE_SIGN slash reduces stake by 5% (500 bps)
//  5. FRONT_RUN slash reduces stake by 1% (100 bps)
//  6. Slash below MinBondAmount forces validator to SLASHED/removed from active set

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// bootstrapValidatorNode creates a LocalNode pre-loaded with Alice and Bob
// trading accounts (same as bootstrapNode) but also sets up validators.
func bootstrapValidatorNode(t *testing.T) *node.LocalNode {
	t.Helper()
	n, _, _, _, _ := bootstrapNode(t)
	return n
}

const (
	val1Id     = "val-0000000000000001"
	val2Id     = "val-0000000000000002"
	val1Moniker = "alpha"
	val2Moniker = "beta"
	val1PubKey  = "aabbcc"
	val2PubKey  = "ddeeff"
)

// -------------------------------------------------------------------------
// Scenario 1: Bond validator → appears in active set
// -------------------------------------------------------------------------
func TestValidator_Bond_AddsToActiveSet(t *testing.T) {
	n := bootstrapValidatorNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator(val1Id, val1Moniker, val1PubKey, 10_000).
		Build())
	if err != nil {
		t.Fatalf("bond validator: %v", err)
	}

	active := n.ActiveValidatorSet()
	if len(active) != 1 {
		t.Fatalf("expected 1 active validator, got %d", len(active))
	}
	if active[0].ValidatorId != val1Id {
		t.Errorf("validator id: want %s got %s", val1Id, active[0].ValidatorId)
	}
	if active[0].Stake != 10_000 {
		t.Errorf("stake: want 10000 got %d", active[0].Stake)
	}
	if active[0].Status != validator.StatusBonded {
		t.Errorf("status: want BONDED got %s", active[0].Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Multiple validators — TotalStake = sum
// -------------------------------------------------------------------------
func TestValidator_TotalStake_IsSum(t *testing.T) {
	n := bootstrapValidatorNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator(val1Id, val1Moniker, val1PubKey, 10_000).
		AddBondValidator(val2Id, val2Moniker, val2PubKey, 25_000).
		Build())
	if err != nil {
		t.Fatalf("bond validators: %v", err)
	}

	total := n.TotalValidatorStake()
	if total != 35_000 {
		t.Errorf("total stake: want 35000 got %d", total)
	}
	if len(n.ActiveValidatorSet()) != 2 {
		t.Errorf("expected 2 active validators, got %d", len(n.ActiveValidatorSet()))
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Unbond → removed from active set
// -------------------------------------------------------------------------
func TestValidator_Unbond_RemovesFromActiveSet(t *testing.T) {
	n := bootstrapValidatorNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator(val1Id, val1Moniker, val1PubKey, 10_000).
		AddBondValidator(val2Id, val2Moniker, val2PubKey, 20_000).
		Build())

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddUnbondValidator(val1Id).
		Build())
	if err != nil {
		t.Fatalf("unbond: %v", err)
	}

	active := n.ActiveValidatorSet()
	if len(active) != 1 {
		t.Fatalf("expected 1 active validator after unbond, got %d", len(active))
	}
	if active[0].ValidatorId != val2Id {
		t.Errorf("remaining validator: want %s got %s", val2Id, active[0].ValidatorId)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: DOUBLE_SIGN slash reduces stake by 5%
// -------------------------------------------------------------------------
func TestValidator_Slash_DoubleSign_FivePercent(t *testing.T) {
	n := bootstrapValidatorNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator(val1Id, val1Moniker, val1PubKey, 100_000).
		Build())

	var slashEvent state.ValidatorSlashedPayload
	n.Subscribe(state.EventValidatorSlashed, func(e state.Event) {
		slashEvent = e.Payload.(state.ValidatorSlashedPayload)
	})

	slashed, err := n.SlashValidator(val1Id, "DOUBLE_SIGN")
	if err != nil {
		t.Fatalf("slash: %v", err)
	}

	// 5% of 100_000 = 5_000
	if slashed != 5_000 {
		t.Errorf("slashed amount: want 5000 got %d", slashed)
	}
	remaining := n.GetValidatorStake(val1Id)
	if remaining != 95_000 {
		t.Errorf("remaining stake: want 95000 got %d", remaining)
	}
	if slashEvent.ValidatorId != val1Id {
		t.Error("expected ValidatorSlashed event for val1")
	}
	if slashEvent.Reason != "DOUBLE_SIGN" {
		t.Errorf("slash reason: want DOUBLE_SIGN got %s", slashEvent.Reason)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: FRONT_RUN slash reduces stake by 1%
// -------------------------------------------------------------------------
func TestValidator_Slash_FrontRun_OnePercent(t *testing.T) {
	n := bootstrapValidatorNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator(val1Id, val1Moniker, val1PubKey, 100_000).
		Build())

	slashed, err := n.SlashValidator(val1Id, "FRONT_RUN")
	if err != nil {
		t.Fatalf("slash: %v", err)
	}

	// 1% of 100_000 = 1_000
	if slashed != 1_000 {
		t.Errorf("slashed amount: want 1000 got %d", slashed)
	}
	remaining := n.GetValidatorStake(val1Id)
	if remaining != 99_000 {
		t.Errorf("remaining stake: want 99000 got %d", remaining)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Slash below MinBondAmount forces validator out of active set
// -------------------------------------------------------------------------
func TestValidator_Slash_BelowMin_ForcesOut(t *testing.T) {
	n := bootstrapValidatorNode(t)

	// Bond with exactly MinBondAmount (1_000) — any double-sign slash removes it.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddBondValidator(val1Id, val1Moniker, val1PubKey, 1_000).
		Build())

	var unbondedId string
	n.Subscribe(state.EventValidatorUnbonded, func(e state.Event) {
		p := e.Payload.(state.ValidatorUnbondedPayload)
		unbondedId = p.ValidatorId
	})

	_, err := n.SlashValidator(val1Id, "DOUBLE_SIGN")
	if err != nil {
		t.Fatalf("slash: %v", err)
	}

	// Should be removed from active set.
	if len(n.ActiveValidatorSet()) != 0 {
		t.Errorf("expected empty active set after slash-out, got %d", len(n.ActiveValidatorSet()))
	}
	if unbondedId != val1Id {
		t.Errorf("expected ValidatorUnbonded event for %s, got %q", val1Id, unbondedId)
	}
}
