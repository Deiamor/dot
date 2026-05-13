package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/auth"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// handleAuthNonce handles GET /auth/nonce?address=0x...
// Issues a one-time SIWE challenge nonce for the given Ethereum address.
func (s *Server) handleAuthNonce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	if address == "" {
		writeError(w, http.StatusBadRequest, "address query parameter required")
		return
	}
	if len(address) > 42 {
		writeError(w, http.StatusBadRequest, "invalid address length")
		return
	}

	nonce := s.nonceStore.Issue(address)
	issuedAt := time.Now().UTC().Format(time.RFC3339)
	message := auth.BuildSIWEMessage(address, nonce, issuedAt)

	writeJSON(w, http.StatusOK, NonceResponse{
		Nonce:    nonce,
		IssuedAt: issuedAt,
		Message:  message,
	})
}

// handleAuthConnect handles POST /auth/connect
// Verifies the SIWE signature and returns DEX account + session credentials.
// If no DEX account exists for the wallet address, one is created automatically.
func (s *Server) handleAuthConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Address == "" || req.Signature == "" || req.Nonce == "" || req.IssuedAt == "" {
		writeError(w, http.StatusBadRequest, "address, signature, nonce, issued_at required")
		return
	}

	// Reconstruct the exact SIWE message the wallet signed.
	message := auth.BuildSIWEMessage(req.Address, req.Nonce, req.IssuedAt)

	// Recover the signer and verify it matches the claimed address.
	recovered, err := auth.RecoverAddress(message, req.Signature)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid signature: "+err.Error())
		return
	}
	if strings.ToLower(recovered) != strings.ToLower(req.Address) {
		writeError(w, http.StatusUnauthorized, "signature mismatch: signed by "+recovered)
		return
	}

	// Consume nonce — prevents replay attacks.
	if err := s.nonceStore.Consume(req.Nonce, req.Address); err != nil {
		writeError(w, http.StatusUnauthorized, "nonce rejected: "+err.Error())
		return
	}

	ownerAddr := strings.ToLower(req.Address)

	// Find existing DEX account or create one bound to this Ethereum address.
	acc := s.node.AppState.FindAccountByOwner(ownerAddr)
	if acc == nil {
		nextHeight := s.node.CurrentHeight() + 1
		batch := fairbatch.NewBatchBuilder(nextHeight).
			AddCreateAccount(ownerAddr, req.Address, req.Address).
			Build()
		if _, err := s.node.SubmitBatch(batch); err != nil {
			writeError(w, http.StatusInternalServerError, "create account: "+err.Error())
			return
		}
		acc = s.node.AppState.FindAccountByOwner(ownerAddr)
		if acc == nil {
			writeError(w, http.StatusInternalServerError, "account creation failed")
			return
		}
	}

	// Create a session with the frontend's Ed25519 session public key.
	opts := account.SessionOptions{
		AllowedMarkets:   []string{"*"},
		MaxOrderAmount:   1_000_000_000,
		SessionPublicKey: req.SessionPublicKey,
	}
	nextHeight := s.node.CurrentHeight() + 1
	batch := fairbatch.NewBatchBuilder(nextHeight).
		AddCreateSession(acc.AccountId, opts).
		Build()

	var sessionId string
	accountId := acc.AccountId
	s.node.Subscribe(state.EventSessionCreated, func(e state.Event) {
		if p, ok := e.Payload.(state.SessionCreatedPayload); ok && p.AccountId == accountId {
			sessionId = p.SessionId
		}
	})

	if _, err := s.node.SubmitBatch(batch); err != nil {
		writeError(w, http.StatusInternalServerError, "create session: "+err.Error())
		return
	}
	if sessionId == "" {
		writeError(w, http.StatusInternalServerError, "session ID not captured after creation")
		return
	}

	writeJSON(w, http.StatusOK, ConnectResponse{
		AccountId:        acc.AccountId,
		SessionId:        sessionId,
		WalletAddress:    req.Address,
		SessionPublicKey: req.SessionPublicKey,
	})
}

// handleHeight handles GET /height — returns the current block height.
func (s *Server) handleHeight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"height": s.node.CurrentHeight()})
}
