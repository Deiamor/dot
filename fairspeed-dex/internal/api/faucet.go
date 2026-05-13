package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
)

// FaucetConfig configures the testnet faucet.
type FaucetConfig struct {
	Enabled         bool
	CooldownSeconds int64
	// AssetAmounts maps asset_id → amount to drip per request.
	AssetAmounts map[string]int64
}

// DefaultFaucetConfig returns testnet-ready drip amounts.
func DefaultFaucetConfig() FaucetConfig {
	return FaucetConfig{
		Enabled:         true,
		CooldownSeconds: 86_400, // 24 hours
		AssetAmounts: map[string]int64{
			"USDC": 10_000_000_000, // 10,000 USDC (6 decimals)
			"BTC":  100_000_000,    // 1 BTC (8 decimals)
			"ETH":  1_000_000_000,  // 1 ETH (9 decimals, simplified)
		},
	}
}

// faucetState tracks IP → last drip timestamp.
type faucetState struct {
	mu   sync.Mutex
	last map[string]time.Time // IP → last drip time
}

func newFaucetState() *faucetState {
	return &faucetState{last: make(map[string]time.Time)}
}

// allow returns true if the IP can drip now (cooldown elapsed).
func (f *faucetState) allow(ip string, cooldown time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	last, ok := f.last[ip]
	if !ok || time.Since(last) >= cooldown {
		f.last[ip] = time.Now()
		return true
	}
	return false
}

// handleFaucet handles GET /faucet/{accountId}?asset=USDC
func (s *Server) handleFaucet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.faucetCfg.Enabled {
		writeError(w, http.StatusForbidden, "faucet not enabled on this network")
		return
	}

	accountId := pathSuffix(r.URL.Path, "/faucet/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}

	// Verify account exists
	if _, ok := s.node.AppState.GetAccount(accountId); !ok {
		writeError(w, http.StatusNotFound, "account not found: "+accountId)
		return
	}

	// IP-based anti-sybil cooldown
	clientIP := remoteIP(r)
	cooldown := time.Duration(s.faucetCfg.CooldownSeconds) * time.Second
	if !s.faucetState.allow(clientIP, cooldown) {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("faucet cooldown: retry after %d seconds", s.faucetCfg.CooldownSeconds))
		return
	}

	// Drip requested asset or all configured assets
	assetId := strings.ToUpper(r.URL.Query().Get("asset"))
	type result struct {
		AssetId string `json:"asset_id"`
		Amount  int64  `json:"amount"`
		TxId    string `json:"tx_id"`
	}
	var results []result

	drip := func(aid string, amount int64) {
		nextHeight := s.node.AppState.CurrentHeight() + 1
		batch := fairbatch.NewBatchBuilder(nextHeight).AddDeposit(accountId, aid, amount).Build()
		_, err := s.node.SubmitBatch(batch)
		if err == nil {
			results = append(results, result{
				AssetId: aid,
				Amount:  amount,
				TxId:    fmt.Sprintf("faucet-%s-%d", aid, nextHeight),
			})
		}
	}

	if assetId != "" {
		if amount, ok := s.faucetCfg.AssetAmounts[assetId]; ok {
			drip(assetId, amount)
		} else {
			writeError(w, http.StatusBadRequest, "asset not supported by faucet: "+assetId)
			return
		}
	} else {
		for aid, amount := range s.faucetCfg.AssetAmounts {
			drip(aid, amount)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"account_id": accountId,
		"status":     "ok",
		"drips":      results,
	})
}

// remoteIP extracts the client IP from the request, preferring X-Forwarded-For.
func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	// Strip port from RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		addr = addr[:idx]
	}
	return addr
}
