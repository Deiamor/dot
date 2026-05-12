package api

import (
	"context"
	"net/http"

	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// Server exposes a LocalNode over HTTP REST + SSE streaming.
type Server struct {
	node *node.LocalNode
	mux  *http.ServeMux
	srv  *http.Server
	hub  *StreamHub
}

// NewServer creates a new API server bound to addr.
func NewServer(n *node.LocalNode, addr string) *Server {
	s := &Server{
		node: n,
		mux:  http.NewServeMux(),
		hub:  newStreamHub(),
	}
	s.srv = &http.Server{Addr: addr, Handler: s.mux}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/orders", s.handleSubmitOrder)
	s.mux.HandleFunc("/orderbook/", s.handleGetOrderBook)
	s.mux.HandleFunc("/trades/", s.handleGetTrades)
	s.mux.HandleFunc("/stream/orderbook", s.handleStreamOrderBook)
	s.mux.HandleFunc("/stream/trades", s.handleStreamTrades)
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
