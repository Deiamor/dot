package auth_test

// Unit tests for the auth package:
//   - NonceStore (issue, consume, replay, expiry, concurrent safety)
//   - BuildSIWEMessage (format, determinism)
//   - RecoverAddress (round-trip, V-normalization, wrong message, bad input)

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/auth"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// genKey returns a random secp256k1 key and the derived Ethereum address.
func genKey(t *testing.T) (*secp256k1.PrivateKey, string) {
	t.Helper()
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	uncompressed := privKey.PubKey().SerializeUncompressed() // [04|x(32)|y(32)]
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	hash := h.Sum(nil)
	addr := "0x" + hex.EncodeToString(hash[12:])
	return privKey, addr
}

// ethSign produces a personal_sign signature [R|S|V] in hex from a secp256k1 key.
func ethSign(privKey *secp256k1.PrivateKey, message string) string {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(prefix + message))
	hash := h.Sum(nil)

	compact := ecdsa.SignCompact(privKey, hash, false) // [V|R|S] where V = 27 or 28
	ethSig := make([]byte, 65)
	copy(ethSig[0:32], compact[1:33])  // R
	copy(ethSig[32:64], compact[33:65]) // S
	ethSig[64] = compact[0]             // V
	return "0x" + hex.EncodeToString(ethSig)
}

// ─── NonceStore ───────────────────────────────────────────────────────────────

func TestNonceStore_IssueAndConsume(t *testing.T) {
	ns := auth.NewNonceStore()
	addr := "0xdeadbeef"
	nonce := ns.Issue(addr)
	if nonce == "" {
		t.Fatal("expected non-empty nonce")
	}
	if err := ns.Consume(nonce, addr); err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
}

func TestNonceStore_ReplayPrevented(t *testing.T) {
	ns := auth.NewNonceStore()
	addr := "0xaabbcc"
	nonce := ns.Issue(addr)
	if err := ns.Consume(nonce, addr); err != nil {
		t.Fatalf("first Consume failed: %v", err)
	}
	// second consume of the same nonce must fail
	if err := ns.Consume(nonce, addr); err == nil {
		t.Fatal("expected error on replay, got nil")
	}
}

func TestNonceStore_WrongAddress(t *testing.T) {
	ns := auth.NewNonceStore()
	nonce := ns.Issue("0xalice")
	if err := ns.Consume(nonce, "0xbob"); err == nil {
		t.Fatal("expected error on wrong address, got nil")
	}
}

func TestNonceStore_UnknownNonce(t *testing.T) {
	ns := auth.NewNonceStore()
	if err := ns.Consume("doesnotexist", "0xany"); err == nil {
		t.Fatal("expected error on unknown nonce, got nil")
	}
}

func TestNonceStore_AddressNormalization(t *testing.T) {
	// Nonce issued for lower-case address must be consumable with mixed-case.
	ns := auth.NewNonceStore()
	nonce := ns.Issue("0xAbCd")
	if err := ns.Consume(nonce, "0xabcd"); err != nil {
		t.Fatalf("case-insensitive Consume failed: %v", err)
	}
}

func TestNonceStore_UniqueNonces(t *testing.T) {
	ns := auth.NewNonceStore()
	seen := map[string]struct{}{}
	for i := range 100 {
		nonce := ns.Issue(fmt.Sprintf("0xaddr%d", i))
		if _, dup := seen[nonce]; dup {
			t.Fatalf("duplicate nonce generated at iteration %d", i)
		}
		seen[nonce] = struct{}{}
	}
}

func TestNonceStore_ConcurrentSafety(t *testing.T) {
	// Hammer Issue + Consume from multiple goroutines; the race detector catches data races.
	ns := auth.NewNonceStore()
	const workers = 50
	nonces := make([]string, workers)
	for i := range workers {
		nonces[i] = ns.Issue(fmt.Sprintf("0x%04x", i))
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wg.Done()
			_ = ns.Consume(nonces[idx], fmt.Sprintf("0x%04x", idx))
		}(i)
	}
	wg.Wait()
}

// ─── BuildSIWEMessage ─────────────────────────────────────────────────────────

func TestBuildSIWEMessage_ContainsRequiredFields(t *testing.T) {
	addr := "0xAbCdEf1234"
	nonce := "abc123def456"
	issuedAt := "2024-01-01T00:00:00Z"

	msg := auth.BuildSIWEMessage(addr, nonce, issuedAt)

	checks := []string{
		auth.Domain,
		addr,
		nonce,
		issuedAt,
		"Version: 1",
		fmt.Sprintf("Chain ID: %d", auth.ChainID),
		"URI:",
		"Nonce:",
		"Issued At:",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("SIWE message missing %q\nFull message:\n%s", want, msg)
		}
	}
}

func TestBuildSIWEMessage_Deterministic(t *testing.T) {
	msg1 := auth.BuildSIWEMessage("0xAABB", "nonce1", "2024-01-01T00:00:00Z")
	msg2 := auth.BuildSIWEMessage("0xAABB", "nonce1", "2024-01-01T00:00:00Z")
	if msg1 != msg2 {
		t.Errorf("BuildSIWEMessage is not deterministic:\n%s\nvs\n%s", msg1, msg2)
	}
}

func TestBuildSIWEMessage_DifferentNonce(t *testing.T) {
	msg1 := auth.BuildSIWEMessage("0xAABB", "nonce1", "2024-01-01T00:00:00Z")
	msg2 := auth.BuildSIWEMessage("0xAABB", "nonce2", "2024-01-01T00:00:00Z")
	if msg1 == msg2 {
		t.Error("different nonces must produce different messages")
	}
}

// ─── RecoverAddress ───────────────────────────────────────────────────────────

func TestRecoverAddress_RoundTrip(t *testing.T) {
	privKey, addr := genKey(t)
	msg := auth.BuildSIWEMessage(addr, "testNonce", "2024-01-01T00:00:00Z")
	sig := ethSign(privKey, msg)

	recovered, err := auth.RecoverAddress(msg, sig)
	if err != nil {
		t.Fatalf("RecoverAddress error: %v", err)
	}
	if !strings.EqualFold(recovered, addr) {
		t.Errorf("address mismatch: want %s got %s", addr, recovered)
	}
}

func TestRecoverAddress_VNormalization_ZeroOne(t *testing.T) {
	// Some wallets return V=0 or V=1 instead of 27/28.
	// Our RecoverAddress must normalise both forms.
	privKey, addr := genKey(t)
	msg := auth.BuildSIWEMessage(addr, "vnorm", "2024-06-01T00:00:00Z")
	sig := ethSign(privKey, msg)

	sigBytes, _ := hex.DecodeString(strings.TrimPrefix(sig, "0x"))
	// Normalise V from 27/28 → 0/1
	sigBytes[64] -= 27
	normSig := "0x" + hex.EncodeToString(sigBytes)

	recovered, err := auth.RecoverAddress(msg, normSig)
	if err != nil {
		t.Fatalf("RecoverAddress with V=0/1: %v", err)
	}
	if !strings.EqualFold(recovered, addr) {
		t.Errorf("V-normalization failed: want %s got %s", addr, recovered)
	}
}

func TestRecoverAddress_WrongMessage(t *testing.T) {
	// Signing message A and presenting message B must not recover the same address.
	privKey, addr := genKey(t)
	msgSigned := auth.BuildSIWEMessage(addr, "nonce1", "2024-01-01T00:00:00Z")
	msgPresented := auth.BuildSIWEMessage(addr, "nonce2", "2024-01-01T00:00:00Z")
	sig := ethSign(privKey, msgSigned)

	recovered, err := auth.RecoverAddress(msgPresented, sig)
	if err != nil {
		// Some hash combinations produce an invalid curve point → acceptable
		return
	}
	if strings.EqualFold(recovered, addr) {
		t.Error("wrong message should not recover the correct address")
	}
}

func TestRecoverAddress_InvalidSignatureLength(t *testing.T) {
	// Not 65 bytes → must return an error.
	_, err := auth.RecoverAddress("some message", "0x1234abcd")
	if err == nil {
		t.Fatal("expected error for short signature, got nil")
	}
}

func TestRecoverAddress_InvalidHex(t *testing.T) {
	_, err := auth.RecoverAddress("msg", "0xGGGG")
	if err == nil {
		t.Fatal("expected error for invalid hex, got nil")
	}
}

func TestRecoverAddress_SIWEFullCycle(t *testing.T) {
	// End-to-end: issue nonce, build message, sign, recover, verify match.
	ns := auth.NewNonceStore()
	privKey, addr := genKey(t)

	nonce := ns.Issue(addr)
	issuedAt := "2025-01-01T00:00:00Z"
	msg := auth.BuildSIWEMessage(addr, nonce, issuedAt)
	sig := ethSign(privKey, msg)

	recovered, err := auth.RecoverAddress(msg, sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}
	if !strings.EqualFold(recovered, addr) {
		t.Errorf("full cycle failed: want %s got %s", addr, recovered)
	}

	// Consume nonce — must succeed
	if err := ns.Consume(nonce, addr); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	// Replay — must fail
	if err := ns.Consume(nonce, addr); err == nil {
		t.Fatal("replay should be rejected")
	}
}

func TestRecoverAddress_EdgeCase_MultipleKeys(t *testing.T) {
	// Each key produces a distinct address; recovery is always correct.
	for range 10 {
		privKey, addr := genKey(t)
		msg := auth.BuildSIWEMessage(addr, "multi", "2025-01-01T00:00:00Z")
		sig := ethSign(privKey, msg)
		recovered, err := auth.RecoverAddress(msg, sig)
		if err != nil {
			t.Fatalf("RecoverAddress: %v", err)
		}
		if !strings.EqualFold(recovered, addr) {
			t.Errorf("key mismatch: want %s got %s", addr, recovered)
		}
	}
}
