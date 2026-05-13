package api

import (
	"context"
	"net/http"

	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// corsMiddleware adds CORS headers to allow browser access from any origin.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Server exposes a LocalNode over HTTP REST + SSE streaming.
type Server struct {
	node *node.LocalNode
	mux  *http.ServeMux
	srv  *http.Server
	hub  *StreamHub
}

// NewServer creates a new API server bound to addr.
// It subscribes to the node's EventBus so that SSE streams are updated
// even when blocks arrive via CometBFT (read-replica path), not just via
// direct REST calls.
func NewServer(n *node.LocalNode, addr string) *Server {
	s := &Server{
		node: n,
		mux:  http.NewServeMux(),
		hub:  newStreamHub(),
	}
	s.srv = &http.Server{Addr: addr, Handler: corsMiddleware(s.mux)}
	s.registerRoutes()
	s.subscribeEvents()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/orders", s.handleSubmitOrder)
	s.mux.HandleFunc("/orderbook/", s.handleGetOrderBook)
	s.mux.HandleFunc("/trades/", s.handleGetTrades)
	s.mux.HandleFunc("/accounts", s.handleAccounts)
	s.mux.HandleFunc("/accounts/", s.handleGetAccount)
	s.mux.HandleFunc("/sessions", s.handleCreateSession)
	s.mux.HandleFunc("/markets", s.handleGetMarkets)
	s.mux.HandleFunc("/markprice/", s.handleGetMarkPrice)
	s.mux.HandleFunc("/balances/", s.handleGetBalances)
	s.mux.HandleFunc("/positions/", s.handleGetPositions)
	s.mux.HandleFunc("/stream/orderbook", s.handleStreamOrderBook)
	s.mux.HandleFunc("/stream/trades", s.handleStreamTrades)
	s.mux.HandleFunc("/reports/trades", s.handleComplianceReport)
	s.mux.HandleFunc("/reports/kyc/", s.handleKYCStatus)
}

// subscribeEvents wires the node's EventBus to SSE hub broadcasts.
// Replicas (CometBFT block-sync path) trigger these handlers too.
func (s *Server) subscribeEvents() {
	s.node.Subscribe(state.EventTradeExecuted, func(e state.Event) {
		if p, ok := e.Payload.(state.TradeExecutedPayload); ok {
			s.hub.Broadcast("trade", tradePayloadToResponse(p))
		}
	})
	s.node.Subscribe(state.EventOrderSubmitted, func(e state.Event) {
		if p, ok := e.Payload.(state.OrderSubmittedPayload); ok {
			if ob, ok2 := s.node.GetOrderBook(p.MarketId); ok2 {
				s.hub.Broadcast("orderbook", s.buildOrderBookResponse(ob, p.MarketId))
			}
		}
	})
}

// Start begins serving HTTP requests. It is non-blocking.
func (s *Server) Start() error {
	go func() { _ = s.srv.ListenAndServe() }()
	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Handler returns the http.Handler for use with httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return s.mux
}
