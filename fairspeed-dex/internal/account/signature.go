package account

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
)

// VerifyOrderSignature verifies that the order's Signature field was produced
// by the ed25519 private key corresponding to the session's public key.
//
// The signed message is the order's TxHash (deterministic, content-addressed).
// Returns nil if the signature is valid, or an error if:
//   - the session has no public key configured (unauthenticated session)
//   - the public key bytes are malformed
//   - the signature does not verify
//
// Sessions created without a SessionPublicKey skip verification — useful for
// internal bootstrapping and tests, but should be disallowed in production
// by the RiskChecker policy.
func VerifyOrderSignature(txHash, signature, sessionPublicKey string) error {
	if sessionPublicKey == "" {
		// No key registered → skip verification (test / bootstrap mode).
		return nil
	}
	if signature == "" {
		return fmt.Errorf("missing signature")
	}

	pubKeyBytes, err := hex.DecodeString(sessionPublicKey)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(pubKeyBytes))
	}

	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	msg := []byte(txHash)
	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), msg, sigBytes) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// SignMessage signs a message (typically a TxHash) with an ed25519 private key
// and returns the hex-encoded signature.
// Callers (wallets, test helpers) use this to produce Signature fields.
func SignMessage(privateKey ed25519.PrivateKey, message string) string {
	sig := ed25519.Sign(privateKey, []byte(message))
	return hex.EncodeToString(sig)
}

// PublicKeyHex returns the hex-encoded ed25519 public key for storage in SessionPublicKey.
func PublicKeyHex(pub ed25519.PublicKey) string {
	return hex.EncodeToString(pub)
}
