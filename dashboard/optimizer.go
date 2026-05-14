package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"github.com/deiamor/perp-strategy-engine/mm/optimizer"
)

// OptimizeRequest is posted from the browser.
type OptimizeRequest struct {
	DatasetPath    string             `json:"datasetPath"`
	InitialBalance float64            `json:"initialBalance"`
	MaxResults     int                `json:"maxResults"` // top N to return (default 20)
	Grid           optimizer.ParamGrid `json:"grid"`
}

// handleOptimize runs a grid-search backtest and returns the top N results.
// POST /api/optimize  body: OptimizeRequest JSON
func (s *Server) handleOptimize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req OptimizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.DatasetPath == "" {
		http.Error(w, "datasetPath required", http.StatusBadRequest)
		return
	}

	if req.InitialBalance <= 0 {
		req.InitialBalance = 10_000
	}
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 20
	}

	// Infer symbol from dataset filename.
	info, err := parseDatasetFilename(filepath.Base(req.DatasetPath), req.DatasetPath)
	symbol := "BTCUSDT"
	if err == nil {
		symbol = info.Symbol
	}

	// Use a sensible timeout for the optimize run.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	_ = ctx // optimizer.Optimize is synchronous; ctx is honoured by inner backtest runs

	results, err := optimizer.Optimize(optimizer.Config{
		DatasetPath:    req.DatasetPath,
		Symbol:         symbol,
		InitialBalance: req.InitialBalance,
		Grid:           req.Grid,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Trim to top N.
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	writeJSON(w, results)
}
