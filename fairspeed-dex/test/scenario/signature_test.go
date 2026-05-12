package scenario_test

// Signature verification tests.
//
// Verifies that:
//   1. Orders signed with the correct session key are accepted
//   2. Orders with a wrong signature are rejected
//   3. Orders with a missing signature are rejected when a key is configured
//   4. Sessions without a public key skip verification (bootstrap / test mode)

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// bootstrapWithKey creates a single-node with Alice having a real ed25519 session key.
// Returns node, aliceId, aliceSess, alicePrivKey.
func bootstrapWithKey(t *testing.T) (*node.LocalNode, string, string, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	// Block 1: create Alice's account.
	b1 := fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice@sig-test.com", "alice-root", "alice-withdraw").
		Build()
	if _, err := n.SubmitBatch(b1); err != nil {
		t.Fatalf("block 1: %v", err)
	}
	var aliceId string
	for _, acc := range n.AppState.Accounts {
		if acc.OwnerAddress == "alice@sig-test.com" {
			aliceId = acc.AccountId
		}
	}

	// Block 2: create session WITH public key.
	opts := account.SessionOptions{
		SessionPublicKey: account.PublicKeyHex(pub),
		AllowedMarkets:   []string{"BTC-USDC"},
		MaxOrderAmount:   1000,
	}
	b2 := fairbatch.NewBatchBuilder(2).AddCreateSession(aliceId, opts).Build()
	if _, err := n.SubmitBatch(b2); err != nil {
		t.Fatalf("block 2: %v", err)
	}
	var aliceSess string
	for _, sess := range n.AppState.Sessions {
		if sess.AccountId == aliceId {
			aliceSess = sess.SessionId
		}
	}

	// Block 3: deposit USDC.
	b3 := fairbatch.NewBatchBuilder(3).AddDeposit(aliceId, "USDC", 100_000).Build()
	if _, err := n.SubmitBatch(b3); err != nil {
		t.Fatalf("block 3: %v", err)
	}

	return n, aliceId, aliceSess, priv
}

// submitSignedOrder builds a BUY order, signs its TxHash, and submits it.
// TxHash excludes the Signature field, so we can compute it first, sign it,
// then embed the signature back into the order — no circular dependency.
func submitSignedOrder(t *testing.T, n *node.LocalNode, aliceId, aliceSess string,
	priv ed25519.PrivateKey, price, qty, height int64) (fairbatch.FairBatch, string) {
	t.Helper()
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC",
		clob.OrderSideBuy, price, qty, clob.TimeInForceGtc, height)

	// Step 1: compute TxHash from order content (Signature is excluded from hash).
	unsignedBatch := fairbatch.NewBatchBuilder(height).AddSubmitOrder(order).Build()
	txHash := unsignedBatch.Transactions[0].TxHash

	// Step 2: sign the TxHash.
	order.Signature = account.SignMessage(priv, txHash)

	// Step 3: rebuild batch — TxHash is identical since Signature is excluded.
	signedBatch := fairbatch.NewBatchBuilder(height).AddSubmitOrder(order).Build()
	return signedBatch, aliceId
}

// -------------------------------------------------------------------------
// Scenario 1: Valid signature — order accepted
// -------------------------------------------------------------------------
func TestSignature_ValidKey_OrderAccepted(t *testing.T) {
	n, aliceId, aliceSess, priv := bootstrapWithKey(t)

	batch, _ := submitSignedOrder(t, n, aliceId, aliceSess, priv, 10_000, 1, 4)
	result, err := n.SubmitBatch(batch)
	if err != nil {
		t.Fatalf("SubmitBatch: %v", err)
	}
	if result.TxCount != 1 {
		t.Errorf("expected 1 tx, got %d", result.TxCount)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: Wrong signature — order rejected
// -------------------------------------------------------------------------
func TestSignature_WrongKey_OrderRejected(t *testing.T) {
	n, aliceId, aliceSess, _ := bootstrapWithKey(t)

	// Sign with a DIFFERENT key.
	_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)

	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC",
		clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)

	// Get the correct TxHash.
	tmpBatch := fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build()
	txHash := tmpBatch.Transactions[0].TxHash

	// Sign with wrong key.
	order.Signature = account.SignMessage(wrongPriv, txHash)

	batch := fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build()
	_, err := n.SubmitBatch(batch)
	if err == nil {
		t.Error("expected rejection for wrong signature, got nil error")
	}
}

// -------------------------------------------------------------------------
// Scenario 3: Missing signature — order rejected when key is configured
// -------------------------------------------------------------------------
func TestSignature_MissingSignature_Rejected(t *testing.T) {
	n, aliceId, aliceSess, _ := bootstrapWithKey(t)

	// Order with empty Signature.
	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC",
		clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
	// order.Signature is "" by default.

	batch := fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build()
	_, err := n.SubmitBatch(batch)
	if err == nil {
		t.Error("expected rejection for missing signature, got nil error")
	}
}

// -------------------------------------------------------------------------
// Scenario 4: No public key on session — verification skipped (bootstrap mode)
// -------------------------------------------------------------------------
func TestSignature_NoPublicKey_SkipsVerification(t *testing.T) {
	// Use the standard bootstrapNode (no public key on session).
	n, aliceId, _, aliceSess, _ := bootstrapNode(t)

	order := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC",
		clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 4)
	// No signature — but session has no public key, so verification is skipped.

	batch := fairbatch.NewBatchBuilder(4).AddSubmitOrder(order).Build()
	_, err := n.SubmitBatch(batch)
	if err != nil {
		t.Errorf("expected pass for unsigned order on unsigned session, got: %v", err)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: VerifyOrderSignature unit test
// -------------------------------------------------------------------------
func TestSignature_VerifyOrderSignature_Unit(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubHex := account.PublicKeyHex(pub)
	txHash := "abc123deadbeef"

	sig := account.SignMessage(priv, txHash)

	// Valid.
	if err := account.VerifyOrderSignature(txHash, sig, pubHex); err != nil {
		t.Errorf("valid sig rejected: %v", err)
	}

	// Tampered message.
	if err := account.VerifyOrderSignature("tampered", sig, pubHex); err == nil {
		t.Error("expected rejection for tampered message")
	}

	// No public key → skip.
	if err := account.VerifyOrderSignature(txHash, "", ""); err != nil {
		t.Errorf("empty key should skip, got: %v", err)
	}

	// Public key set but no signature.
	if err := account.VerifyOrderSignature(txHash, "", pubHex); err == nil {
		t.Error("expected rejection: key configured but signature missing")
	}
}
