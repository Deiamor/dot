package scenario_test

// Auth scenario + edge case tests.
//
// Tests run against an httptest.Server wrapping a real LocalNode.
// Covers: GET /auth/nonce, POST /auth/connect, GET /height
//
// Scenarios:
//   1. Happy-path: new wallet connects, account + session created
//   2. Idempotent connect: same wallet connects twice → same accountId, new session
//   3. Invalid signature → 401
//   4. Nonce replay attack → 401
//   5. Address mismatch (signed by different key) → 401
//   6. Missing required fields → 400
//   7. GET /height returns monotonic block height
//   8. Session created with Ed25519 session public key
//   9. Nonce is unique per request (entropy test)
//  10. Concurrent connect requests are safe (race detector)

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/api"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpEcdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// newAuthTestServer creates a minimal LocalNode with no pre-built accounts,
// suitable for testing the auth endpoints from scratch.
func newAuthTestServer(t *testing.T) (*httptest.Server, *node.LocalNode) {
	t.Helper()
	n := node.NewLocalNode()
	n.RegisterAsset(asset.USDC)
	n.RegisterAsset(asset.BTC)
	srv := api.NewServer(n, "")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, n
}

// genEthKey returns a secp256k1 private key and its Ethereum address (lower-cased).
func genEthKey(t *testing.T) (*secp.PrivateKey, string) {
	t.Helper()
	privKey, err := secp.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	uncompressed := privKey.PubKey().SerializeUncompressed()
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	hash := h.Sum(nil)
	return privKey, "0x" + hex.EncodeToString(hash[12:])
}

// ethPersonalSign signs a message with the secp256k1 key, returning Ethereum [R|S|V] hex.
func ethPersonalSign(privKey *secp.PrivateKey, message string) string {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(prefix + message))
	hash := h.Sum(nil)
	compact := secpEcdsa.SignCompact(privKey, hash, false) // [V|R|S]
	ethSig := make([]byte, 65)
	copy(ethSig[0:32], compact[1:33])
	copy(ethSig[32:64], compact[33:65])
	ethSig[64] = compact[0]
	return "0x" + hex.EncodeToString(ethSig)
}

// genEd25519Hex returns a fresh Ed25519 public key as hex (simulating Web Crypto output).
func genEd25519Hex(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return hex.EncodeToString(pub)
}

// nonceResponse fetches GET /auth/nonce?address=addr and returns the JSON body.
func fetchNonce(t *testing.T, ts *httptest.Server, addr string) map[string]string {
	t.Helper()
	resp, err := http.Get(ts.URL + "/auth/nonce?address=" + addr)
	if err != nil {
		t.Fatalf("GET /auth/nonce: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /auth/nonce status %d: %s", resp.StatusCode, body)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode nonce response: %v", err)
	}
	return result
}

// doConnect POSTs to /auth/connect and returns (statusCode, responseBody).
func doConnect(t *testing.T, ts *httptest.Server, payload map[string]string) (int, map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	resp, err := http.Post(ts.URL+"/auth/connect", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST /auth/connect: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// fullAuth performs the complete SIWE auth flow and returns the connect response.
func fullAuth(t *testing.T, ts *httptest.Server, privKey *secp.PrivateKey, addr, sessionPubKey string) map[string]string {
	t.Helper()
	nonceResp := fetchNonce(t, ts, addr)
	nonce := nonceResp["nonce"]
	issuedAt := nonceResp["issued_at"]
	message := nonceResp["message"]
	sig := ethPersonalSign(privKey, message)

	status, body := doConnect(t, ts, map[string]string{
		"address":            addr,
		"signature":          sig,
		"nonce":              nonce,
		"issued_at":          issuedAt,
		"session_public_key": sessionPubKey,
	})
	if status != http.StatusOK {
		t.Fatalf("auth connect failed (status %d): %v", status, body)
	}
	return body
}

// ─── Scenario 1: Happy path ──────────────────────────────────────────────────

func TestAuth_HappyPath_NewAccount(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	privKey, addr := genEthKey(t)
	sessKey := genEd25519Hex(t)

	resp := fullAuth(t, ts, privKey, addr, sessKey)

	if resp["account_id"] == "" {
		t.Error("expected non-empty account_id")
	}
	if resp["session_id"] == "" {
		t.Error("expected non-empty session_id")
	}
	if !strings.EqualFold(resp["wallet_address"], addr) {
		t.Errorf("wallet_address: want %s got %s", addr, resp["wallet_address"])
	}
	if resp["session_public_key"] != sessKey {
		t.Errorf("session_public_key mismatch: want %s got %s", sessKey, resp["session_public_key"])
	}
}

// ─── Scenario 2: Idempotent connect ──────────────────────────────────────────

func TestAuth_IdempotentConnect_SameAccountDifferentSession(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	privKey, addr := genEthKey(t)
	sessKey1 := genEd25519Hex(t)
	sessKey2 := genEd25519Hex(t)

	resp1 := fullAuth(t, ts, privKey, addr, sessKey1)
	resp2 := fullAuth(t, ts, privKey, addr, sessKey2)

	if resp1["account_id"] != resp2["account_id"] {
		t.Errorf("same wallet must map to same account: %s vs %s", resp1["account_id"], resp2["account_id"])
	}
	if resp1["session_id"] == resp2["session_id"] {
		t.Error("each connect must create a new session")
	}
}

// ─── Scenario 3: Invalid signature ───────────────────────────────────────────

func TestAuth_InvalidSignature(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	privKey, addr := genEthKey(t)

	nonceResp := fetchNonce(t, ts, addr)
	nonce := nonceResp["nonce"]
	issuedAt := nonceResp["issued_at"]

	// Use a garbage signature (all zeros), not a valid ECDSA output
	badSig := "0x" + strings.Repeat("00", 65)
	status, _ := doConnect(t, ts, map[string]string{
		"address":   addr,
		"signature": badSig,
		"nonce":     nonce,
		"issued_at": issuedAt,
	})
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", status)
	}
	_ = privKey
}

func TestAuth_CorruptedSignature_WrongLength(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	_, addr := genEthKey(t)
	nonceResp := fetchNonce(t, ts, addr)

	status, _ := doConnect(t, ts, map[string]string{
		"address":   addr,
		"signature": "0xdeadbeef", // not 65 bytes
		"nonce":     nonceResp["nonce"],
		"issued_at": nonceResp["issued_at"],
	})
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 for short sig, got %d", status)
	}
}

// ─── Scenario 4: Nonce replay ─────────────────────────────────────────────────

func TestAuth_NonceReplay(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	privKey, addr := genEthKey(t)
	sessKey := genEd25519Hex(t)

	nonceResp := fetchNonce(t, ts, addr)
	nonce := nonceResp["nonce"]
	issuedAt := nonceResp["issued_at"]
	message := nonceResp["message"]
	sig := ethPersonalSign(privKey, message)

	payload := map[string]string{
		"address":            addr,
		"signature":          sig,
		"nonce":              nonce,
		"issued_at":          issuedAt,
		"session_public_key": sessKey,
	}

	// First connect must succeed
	status1, _ := doConnect(t, ts, payload)
	if status1 != http.StatusOK {
		t.Fatalf("first connect failed: %d", status1)
	}

	// Second connect with the same nonce must fail
	status2, _ := doConnect(t, ts, payload)
	if status2 != http.StatusUnauthorized {
		t.Errorf("replay should return 401, got %d", status2)
	}
}

// ─── Scenario 5: Address mismatch ────────────────────────────────────────────

func TestAuth_AddressMismatch(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	privKeyA, addrA := genEthKey(t)
	_, addrB := genEthKey(t)

	// Fetch nonce for address A, sign with key A, but claim address B
	nonceResp := fetchNonce(t, ts, addrA)
	nonce := nonceResp["nonce"]
	issuedAt := nonceResp["issued_at"]
	message := nonceResp["message"]
	sig := ethPersonalSign(privKeyA, message) // signed as A

	status, _ := doConnect(t, ts, map[string]string{
		"address":   addrB, // claiming to be B
		"signature": sig,
		"nonce":     nonce,
		"issued_at": issuedAt,
	})
	if status != http.StatusUnauthorized {
		t.Errorf("address mismatch should return 401, got %d", status)
	}
}

// ─── Scenario 6: Missing fields ───────────────────────────────────────────────

func TestAuth_MissingFields(t *testing.T) {
	ts, _ := newAuthTestServer(t)

	cases := []struct {
		name    string
		payload map[string]string
	}{
		{"no_address", map[string]string{"signature": "0x" + strings.Repeat("ab", 65), "nonce": "n", "issued_at": "t"}},
		{"no_signature", map[string]string{"address": "0xabc", "nonce": "n", "issued_at": "t"}},
		{"no_nonce", map[string]string{"address": "0xabc", "signature": "0x" + strings.Repeat("ab", 65), "issued_at": "t"}},
		{"no_issued_at", map[string]string{"address": "0xabc", "signature": "0x" + strings.Repeat("ab", 65), "nonce": "n"}},
		{"empty_body", map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := doConnect(t, ts, tc.payload)
			if status != http.StatusBadRequest && status != http.StatusUnauthorized {
				t.Errorf("[%s] expected 400 or 401, got %d", tc.name, status)
			}
		})
	}
}

func TestAuth_Nonce_MissingAddress(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp, err := http.Get(ts.URL + "/auth/nonce")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// ─── Scenario 7: GET /height ──────────────────────────────────────────────────

func TestHeight_ReturnsBlockHeight(t *testing.T) {
	ts, n := newAuthTestServer(t)

	initialHeight := n.CurrentHeight()

	resp, err := http.Get(ts.URL + "/height")
	if err != nil {
		t.Fatalf("GET /height: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode height: %v", err)
	}
	if body["height"] != initialHeight {
		t.Errorf("want height %d, got %d", initialHeight, body["height"])
	}
}

func TestHeight_IncreasesAfterAuth(t *testing.T) {
	ts, n := newAuthTestServer(t)
	privKey, addr := genEthKey(t)

	h0 := n.CurrentHeight()
	_ = fullAuth(t, ts, privKey, addr, "")
	h1 := n.CurrentHeight()

	if h1 <= h0 {
		t.Errorf("height should increase after auth (account+session creation): h0=%d h1=%d", h0, h1)
	}
}

// ─── Scenario 8: Session key stored ──────────────────────────────────────────

func TestAuth_SessionPublicKey_Stored(t *testing.T) {
	ts, n := newAuthTestServer(t)
	privKey, addr := genEthKey(t)
	sessionPubKey := genEd25519Hex(t)

	resp := fullAuth(t, ts, privKey, addr, sessionPubKey)
	sessionId := resp["session_id"]

	sess, ok := n.GetSession(sessionId)
	if !ok {
		t.Fatalf("session %s not found after auth", sessionId)
	}
	if sess.SessionPublicKey != sessionPubKey {
		t.Errorf("session public key mismatch: want %s got %s", sessionPubKey, sess.SessionPublicKey)
	}
}

func TestAuth_NoSessionKey_SessionStillCreated(t *testing.T) {
	// session_public_key is optional; omitting it creates an unauthenticated session
	ts, n := newAuthTestServer(t)
	privKey, addr := genEthKey(t)

	resp := fullAuth(t, ts, privKey, addr, "") // empty session key
	sess, ok := n.GetSession(resp["session_id"])
	if !ok {
		t.Fatal("session not found")
	}
	if sess.SessionPublicKey != "" {
		t.Errorf("expected empty session public key, got %s", sess.SessionPublicKey)
	}
}

// ─── Scenario 9: Nonce uniqueness ────────────────────────────────────────────

func TestAuth_NonceUniqueness(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	_, addr := genEthKey(t)

	seen := map[string]struct{}{}
	for i := range 20 {
		nr := fetchNonce(t, ts, addr)
		nonce := nr["nonce"]
		if _, dup := seen[nonce]; dup {
			t.Fatalf("duplicate nonce at iteration %d: %s", i, nonce)
		}
		seen[nonce] = struct{}{}
	}
}

// ─── Scenario 10: Concurrent connect safety ───────────────────────────────────

func TestAuth_ConcurrentConnect_RaceDetector(t *testing.T) {
	// 10 different wallets connect concurrently.
	// Each must get a unique accountId and sessionId.
	ts, _ := newAuthTestServer(t)
	const n = 10

	type result struct {
		accountId string
		sessionId string
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			privKey, addr := genEthKey(t)
			resp := fullAuth(t, ts, privKey, addr, "")
			results[idx] = result{resp["account_id"], resp["session_id"]}
		}(i)
	}
	wg.Wait()

	accounts := map[string]struct{}{}
	sessions := map[string]struct{}{}
	for _, r := range results {
		if r.accountId == "" {
			t.Error("empty accountId in concurrent connect")
		}
		if r.sessionId == "" {
			t.Error("empty sessionId in concurrent connect")
		}
		accounts[r.accountId] = struct{}{}
		sessions[r.sessionId] = struct{}{}
	}
	if len(accounts) != n {
		t.Errorf("expected %d unique accounts, got %d", n, len(accounts))
	}
	if len(sessions) != n {
		t.Errorf("expected %d unique sessions, got %d", n, len(sessions))
	}
}

// ─── Edge case: wrong-address nonce consumption ───────────────────────────────

func TestAuth_NonceIssuedForDifferentAddress(t *testing.T) {
	// Fetch nonce for address A, but submit with address B (different key).
	// Backend: recovered address will be A (from the message), not B.
	ts, _ := newAuthTestServer(t)
	privKeyA, addrA := genEthKey(t)
	privKeyB, addrB := genEthKey(t)

	// Nonce for A, message for A
	nrA := fetchNonce(t, ts, addrA)
	nrB := fetchNonce(t, ts, addrB)

	// B signs A's message (wrong)
	sigBSigningAMsg := ethPersonalSign(privKeyB, nrA["message"])

	// Submit: address=A, signed with B's key → recovered address ≠ A → 401
	status, _ := doConnect(t, ts, map[string]string{
		"address":   addrA,
		"signature": sigBSigningAMsg,
		"nonce":     nrA["nonce"],
		"issued_at": nrA["issued_at"],
	})
	if status != http.StatusUnauthorized {
		t.Errorf("wrong signer should return 401, got %d", status)
	}

	// A signs B's message → nonce was issued for B, address consumed would check "addrB"
	sigASigningBMsg := ethPersonalSign(privKeyA, nrB["message"])
	status2, _ := doConnect(t, ts, map[string]string{
		"address":   addrB,
		"signature": sigASigningBMsg,
		"nonce":     nrB["nonce"],
		"issued_at": nrB["issued_at"],
	})
	if status2 != http.StatusUnauthorized {
		t.Errorf("key-message mismatch should return 401, got %d", status2)
	}
}

// ─── Edge case: account created with correct address binding ─────────────────

func TestAuth_AccountOwnerAddress_IsLowercased(t *testing.T) {
	ts, n := newAuthTestServer(t)
	privKey, addr := genEthKey(t)

	resp := fullAuth(t, ts, privKey, addr, "")
	acc := n.AppState.FindAccountByOwner(strings.ToLower(addr))
	if acc == nil {
		t.Fatal("account not found by lower-cased owner address")
	}
	if acc.AccountId != resp["account_id"] {
		t.Errorf("account ID mismatch: %s vs %s", acc.AccountId, resp["account_id"])
	}
}
