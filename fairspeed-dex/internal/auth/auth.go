package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const (
	nonceTTL = 10 * time.Minute
	// Domain is embedded in SIWE messages.
	Domain = "fairspeed.dex"
	// ChainID is the Ethereum chain ID included in SIWE messages.
	ChainID = 1
)

type nonceEntry struct {
	address   string // lower-cased Ethereum address
	expiresAt time.Time
}

// NonceStore issues single-use challenges for SIWE wallet authentication.
// Thread-safe.
type NonceStore struct {
	mu     sync.Mutex
	nonces map[string]*nonceEntry
}

// NewNonceStore creates a ready-to-use NonceStore.
func NewNonceStore() *NonceStore {
	return &NonceStore{nonces: make(map[string]*nonceEntry)}
}

// Issue creates a new nonce bound to address and returns it.
// Expired nonces are pruned on each call.
func (s *NonceStore) Issue(address string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.nonces {
		if now.After(e.expiresAt) {
			delete(s.nonces, k)
		}
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	nonce := hex.EncodeToString(buf)
	s.nonces[nonce] = &nonceEntry{
		address:   strings.ToLower(address),
		expiresAt: now.Add(nonceTTL),
	}
	return nonce
}

// Consume validates nonce+address and removes it (prevents replay).
// Returns an error if the nonce is unknown, expired, or bound to a different address.
func (s *NonceStore) Consume(nonce, address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.nonces[nonce]
	if !ok {
		return fmt.Errorf("unknown or already-used nonce")
	}
	delete(s.nonces, nonce)
	if time.Now().After(e.expiresAt) {
		return fmt.Errorf("nonce expired")
	}
	if e.address != strings.ToLower(address) {
		return fmt.Errorf("nonce was issued for a different address")
	}
	return nil
}

// BuildSIWEMessage constructs an EIP-4361 sign-in message.
// address must be the EIP-55 checksummed (or lower-cased) Ethereum address.
// issuedAt must be an RFC3339 UTC timestamp.
func BuildSIWEMessage(address, nonce, issuedAt string) string {
	return fmt.Sprintf(
		"%s wants you to sign in with your Ethereum account:\n%s\n\nSign in to FairSpeed DEX\n\nURI: https://%s\nVersion: 1\nChain ID: %d\nNonce: %s\nIssued At: %s",
		Domain, address, Domain, ChainID, nonce, issuedAt,
	)
}

// RecoverAddress recovers the Ethereum address from an EIP-191 personal_sign signature.
// message is the plain-text SIWE message (as built by BuildSIWEMessage).
// sigHex is the 65-byte hex signature [R(32)|S(32)|V(1)] produced by personal_sign.
// Returns the lower-cased "0x…" address.
func RecoverAddress(message, sigHex string) (string, error) {
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(sigHex, "0x"))
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes, got %d", len(sigBytes))
	}

	hash := personalSignHash(message)

	// Convert Ethereum [R|S|V] → secp256k1 compact [recoverByte|R|S].
	// recoverByte = 27 (V=0 or 27 → recovery=0) or 28 (V=1 or 28 → recovery=1).
	compact := make([]byte, 65)
	v := sigBytes[64]
	if v < 27 {
		v += 27
	}
	compact[0] = v
	copy(compact[1:33], sigBytes[0:32])
	copy(compact[33:65], sigBytes[32:64])

	pubKey, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", fmt.Errorf("recover public key: %w", err)
	}

	// Derive Ethereum address: keccak256(uncompressed_pub[1:])[12:]
	uncompressed := pubKey.SerializeUncompressed() // [04 | x(32) | y(32)]
	addrHash := keccak256(uncompressed[1:])
	return "0x" + hex.EncodeToString(addrHash[12:]), nil
}

// keccak256 returns the Keccak-256 hash of data.
func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// personalSignHash returns keccak256("\x19Ethereum Signed Message:\n<len><message>").
func personalSignHash(message string) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	return keccak256([]byte(prefix + message))
}
