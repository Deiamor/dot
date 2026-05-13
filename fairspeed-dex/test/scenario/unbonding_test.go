package scenario_test

// Stage 11: Unbonding period tests.
//
// Verifies the 21-block delayed unbonding mechanism:
//
//  1. UnbondValidator transitions status to UNBONDING (not UNBONDED immediately)
//  2. UNBONDING validator is excluded from the active set
//  3. After UnbondingPeriod blocks the validator moves to UNBONDED
//  4. UNBONDED validators emit EventValidatorUnbonded at completion
//  5. Slashed validators skip the unbonding delay (slashing is immediate)
//  6. Double-unbond request is rejected

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/validator"
)

// bondedNode returns a node with one bonded validator at height 1.
func bondedNode(t *testing.T) (*node.LocalNode, string) {
	t.Helper()
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddBondValidator("val-ub01", "unbond-test", "aabb", 50_000).Build())
	if err != nil {
		t.Fatalf("bond validator: %v", err)
	}
	return n, "val-ub01"
}

// advanceBlocks submits empty blocks from startHeight up to and including endHeight.
func advanceBlocks(t *testing.T, n *node.LocalNode, startHeight, endHeight int64) {
	t.Helper()
	for h := startHeight; h <= endHeight; h++ {
		if _, err := n.SubmitBatch(fairbatch.NewBatchBuilder(h).Build()); err != nil {
			t.Fatalf("advance to block %d: %v", h, err)
		}
	}
}

// -------------------------------------------------------------------------
// Scenario 1: UnbondValidator → UNBONDING (not UNBONDED immediately)
// -------------------------------------------------------------------------
func TestUnbonding_InitialStatus_IsUnbonding(t *testing.T) {
	n, valId := bondedNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddUnbondValidator(valId).Build())
	if err != nil {
		t.Fatalf("unbond: %v", err)
	}

	v, ok := n.GetValidatorInfo(valId)
	if !ok {
		t.Fatal("validator not found")
	}
	if v.Status != validator.StatusUnbonding {
		t.Errorf("status after unbond request: want UNBONDING got %s", v.Status)
	}
	// Not yet unbonded — UnbondingHeight should be set.
	if v.UnbondingHeight == 0 {
		t.Error("UnbondingHeight should be set after unbond request")
	}
}

// -------------------------------------------------------------------------
// Scenario 2: UNBONDING validator is excluded from the active set
// -------------------------------------------------------------------------
func TestUnbonding_NotInActiveSet(t *testing.T) {
	n, valId := bondedNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddUnbondValidator(valId).Build())

	active := n.ActiveValidatorSet()
	for _, v := range active {
		if v.ValidatorId == valId {
			t.Errorf("UNBONDING validator %s should not be in active set", valId)
		}
	}
}

// -------------------------------------------------------------------------
// Scenario 3: After UnbondingPeriod blocks → status UNBONDED
// -------------------------------------------------------------------------
func TestUnbonding_CompletesAfterDelay(t *testing.T) {
	n, valId := bondedNode(t)

	const unbondHeight = int64(2)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(unbondHeight).
		AddUnbondValidator(valId).Build())

	v, _ := n.GetValidatorInfo(valId)
	completionHeight := v.UnbondingHeight // = unbondHeight + 21

	// Advance to one block before completion — should still be UNBONDING.
	advanceBlocks(t, n, unbondHeight+1, completionHeight-1)
	v, _ = n.GetValidatorInfo(valId)
	if v.Status != validator.StatusUnbonding {
		t.Errorf("should still be UNBONDING at height %d, got %s",
			completionHeight-1, v.Status)
	}

	// Advance to completion height — should now be UNBONDED.
	advanceBlocks(t, n, completionHeight, completionHeight)
	v, _ = n.GetValidatorInfo(valId)
	if v.Status != validator.StatusUnbonded {
		t.Errorf("should be UNBONDED at height %d, got %s", completionHeight, v.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: EventValidatorUnbonded fires at completion (not at request)
// -------------------------------------------------------------------------
func TestUnbonding_EventFiredAtCompletion(t *testing.T) {
	n, valId := bondedNode(t)

	const unbondHeight = int64(2)
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(unbondHeight).
		AddUnbondValidator(valId).Build())

	v, _ := n.GetValidatorInfo(valId)
	completionHeight := v.UnbondingHeight

	var unbondedId string
	n.Subscribe(state.EventValidatorUnbonded, func(e state.Event) {
		unbondedId = e.Payload.(state.ValidatorUnbondedPayload).ValidatorId
	})

	// No event before completion.
	advanceBlocks(t, n, unbondHeight+1, completionHeight-1)
	if unbondedId != "" {
		t.Errorf("EventValidatorUnbonded fired too early at height %d", completionHeight-1)
	}

	// Event fires at completion block.
	advanceBlocks(t, n, completionHeight, completionHeight)
	if unbondedId != valId {
		t.Errorf("EventValidatorUnbonded: want %s got %q", valId, unbondedId)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Slashing is immediate — no unbonding delay
// -------------------------------------------------------------------------
func TestUnbonding_SlashIsImmediate(t *testing.T) {
	n, valId := bondedNode(t)

	slashed, err := n.SlashValidator(valId, "DOUBLE_SIGN")
	if err != nil {
		t.Fatalf("slash: %v", err)
	}
	if slashed <= 0 {
		t.Fatalf("expected positive slash amount, got %d", slashed)
	}

	// Slashed validator should not be UNBONDING — it's SLASHED immediately.
	v, _ := n.GetValidatorInfo(valId)
	if v.Status == validator.StatusUnbonding {
		t.Error("slashed validator should not enter UNBONDING state")
	}
	if v.Status != validator.StatusSlashed {
		t.Errorf("slashed validator status: want SLASHED got %s", v.Status)
	}
}

// -------------------------------------------------------------------------
// Scenario 6: Double-unbond request is rejected
// -------------------------------------------------------------------------
func TestUnbonding_DoubleUnbondRejected(t *testing.T) {
	n, valId := bondedNode(t)

	// First unbond succeeds.
	res, err := n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddUnbondValidator(valId).Build())
	if err != nil {
		t.Fatalf("first unbond: %v", err)
	}
	_ = res

	// Second unbond should fail (already UNBONDING).
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddUnbondValidator(valId).Build())
	if err == nil {
		t.Error("expected error on double-unbond, got nil")
	}
}
