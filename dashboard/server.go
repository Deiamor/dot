package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Server is the dashboard HTTP server.
// It serves the static UI, REST endpoints, SSE events, and the kline download API.
type Server struct {
	collector *Collector
	dataDir   string
	log       *slog.Logger

	mu          sync.Mutex
	activeDownload context.CancelFunc // non-nil while a download is in progress
}

// NewServer creates a dashboard Server.
// collector receives TradingEvents from the active exchange.
// dataDir is where downloaded kline files are stored (e.g. "./data").
func NewServer(collector *Collector, dataDir string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		collector: collector,
		dataDir:   dataDir,
		log:       log.With("component", "dashboard"),
	}
}

// Handler returns the HTTP mux for the dashboard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files.
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("dashboard/static"))))
	mux.HandleFunc("/", s.handleIndex)

	// REST API.
	mux.HandleFunc("/api/snapshot", s.handleSnapshot)
	mux.HandleFunc("/api/datasets", s.handleDatasets)
	mux.HandleFunc("/api/download", s.handleDownload)
	mux.HandleFunc("/api/backtest", s.handleBacktest)
	mux.HandleFunc("/api/optimize", s.handleOptimize)

	// SSE stream.
	mux.HandleFunc("/api/events", s.handleSSE)

	return mux
}

// Run starts the HTTP server on addr (e.g. ":8080") and blocks until ctx is done.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint
	}()
	s.log.Info("dashboard listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ─── handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "dashboard/static/index.html")
}

func (s *Server) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.collector.Snapshot())
}

// handleBacktest runs a synchronous backtest and returns JSON results.
// POST /api/backtest  body: BacktestConfig JSON
func (s *Server) handleBacktest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var cfg BacktestConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if cfg.DatasetPath == "" {
		http.Error(w, "datasetPath required", http.StatusBadRequest)
		return
	}

	// Infer symbol from dataset filename (e.g. "BTCUSDT_5m_...").
	info, err := parseDatasetFilename(filepath.Base(cfg.DatasetPath), cfg.DatasetPath)
	symbol := "BTCUSDT"
	if err == nil {
		symbol = info.Symbol
	}

	result, err := RunBacktest(cfg, symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleDatasets(w http.ResponseWriter, _ *http.Request) {
	datasets, err := ListDatasets(s.dataDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, datasets)
}

// handleDownload starts a kline download and streams progress via SSE.
// POST /api/download  body: DownloadRequest JSON
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Symbol == "" || req.Interval == "" {
		http.Error(w, "symbol and interval required", http.StatusBadRequest)
		return
	}
	if req.StartTime.IsZero() || req.EndTime.IsZero() {
		http.Error(w, "startTime and endTime required", http.StatusBadRequest)
		return
	}

	// Cancel any in-progress download.
	s.mu.Lock()
	if s.activeDownload != nil {
		s.activeDownload()
	}
	dlCtx, dlCancel := context.WithCancel(r.Context())
	s.activeDownload = dlCancel
	s.mu.Unlock()

	// Stream progress as SSE.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, _ := w.(http.Flusher)

	progressCh := DownloadKlines(dlCtx, req, s.dataDir)
	for p := range progressCh {
		var payload map[string]any
		if p.Error != nil {
			payload = map[string]any{"error": p.Error.Error()}
		} else {
			payload = map[string]any{
				"done":      p.Done,
				"total":     p.Total,
				"completed": p.Completed,
				"filePath":  p.FilePath,
			}
		}
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
		if p.Completed || p.Error != nil {
			break
		}
	}

	s.mu.Lock()
	s.activeDownload = nil
	s.mu.Unlock()
}

// handleSSE streams TradingEvents to the browser as Server-Sent Events.
// GET /api/events
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch, unsub := s.collector.Subscribe()
	defer unsub()

	// Ping every 30s to keep connection alive.
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case raw, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v) //nolint
}
