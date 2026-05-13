package api

import (
	"encoding/json"
	"net/http"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
)

// handleGetAccount handles GET /accounts/{accountId}
func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accountId := pathSuffix(r.URL.Path, "/accounts/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	acc, ok := s.node.AppState.GetAccount(accountId)
	if !ok {
		writeError(w, http.StatusNotFound, "account not found: "+accountId)
		return
	}
	writeJSON(w, http.StatusOK, AccountResponse{
		AccountId:       acc.AccountId,
		OwnerAddress:    acc.OwnerAddress,
		Status:          string(acc.Status),
		AccountSequence: acc.AccountSequence,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

// handleSubmitOrder handles POST /orders
func (s *Server) handleSubmitOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req SubmitOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := validateOrderRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	side := clob.OrderSideBuy
	if req.Side == "SELL" {
		side = clob.OrderSideSell
	}
	tif := clob.TimeInForceGtc
	switch req.TimeInForce {
	case "IOC":
		tif = clob.TimeInForceIoc
	case "FOK":
		tif = clob.TimeInForceFok
	}

	nextHeight := s.node.CurrentHeight() + 1
	order := clob.NewLimitOrder(req.AccountId, req.SessionId, req.MarketId,
		side, req.Price, req.Quantity, tif, nextHeight)
	order.ClientOrderId = req.ClientOrderId
	order.AccountSequence = s.node.GetAccountSequence(req.AccountId)

	s.node.NotifyOrderReceived(order.OrderId, req.AccountId, req.SessionId,
		req.MarketId, req.Side, "", req.Price, req.Quantity)

	result, err := s.node.SubmitOrderImmediate(order)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Broadcast orderbook update to SSE clients.
	if ob, ok := s.node.GetOrderBook(req.MarketId); ok {
		s.hub.Broadcast("orderbook", s.buildOrderBookResponse(ob, req.MarketId))
	}
	for _, t := range result.Trades {
		s.hub.Broadcast("trade", tradeToResponse(t))
	}

	resp := OrderResponse{
		OrderId:     order.OrderId,
		Status:      string(order.Status),
		MarketId:    order.MarketId,
		Side:        string(order.Side),
		Price:       order.Price,
		Quantity:    order.Quantity,
		Remaining:   order.RemainingQuantity,
		BlockHeight: result.Height,
		TradeCount:  result.TradeCount,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleGetOrderBook handles GET /orderbook/{marketId}
func (s *Server) handleGetOrderBook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	marketId := pathSuffix(r.URL.Path, "/orderbook/")
	if marketId == "" {
		writeError(w, http.StatusBadRequest, "marketId required")
		return
	}

	ob, ok := s.node.GetOrderBook(marketId)
	if !ok {
		writeJSON(w, http.StatusOK, OrderBookResponse{
			MarketId: marketId,
			Bids:     []PriceLevelResponse{},
			Asks:     []PriceLevelResponse{},
		})
		return
	}
	writeJSON(w, http.StatusOK, s.buildOrderBookResponse(ob, marketId))
}

// handleGetTrades handles GET /trades/{marketId}
func (s *Server) handleGetTrades(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	marketId := pathSuffix(r.URL.Path, "/trades/")
	if marketId == "" {
		writeError(w, http.StatusBadRequest, "marketId required")
		return
	}

	trades := s.node.TradesForMarket(marketId)
	resp := make([]TradeResponse, len(trades))
	for i, t := range trades {
		resp[i] = tradeToResponse(t)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleStreamOrderBook handles GET /stream/orderbook?market=BTC-USDC (SSE)
func (s *Server) handleStreamOrderBook(w http.ResponseWriter, r *http.Request) {
	marketId := r.URL.Query().Get("market")
	if marketId == "" {
		marketId = "*"
	}
	// Send current snapshot immediately on connect.
	if marketId != "*" {
		if ob, ok := s.node.GetOrderBook(marketId); ok {
			snap, _ := json.Marshal(s.buildOrderBookResponse(ob, marketId))
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			_, _ = w.Write([]byte("event: orderbook\ndata: " + string(snap) + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
	s.hub.ServeSSE(w, r, "orderbook")
}

// handleStreamTrades handles GET /stream/trades (SSE)
func (s *Server) handleStreamTrades(w http.ResponseWriter, r *http.Request) {
	s.hub.ServeSSE(w, r, "trade")
}

func (s *Server) buildOrderBookResponse(ob *clob.OrderBook, marketId string) OrderBookResponse {
	bids := ob.SortedBidPrices()
	asks := ob.SortedAskPrices()

	bidLevels := make([]PriceLevelResponse, 0, len(bids))
	for _, p := range bids {
		if pl := ob.Bids[p]; pl != nil && !pl.IsEmpty() {
			bidLevels = append(bidLevels, PriceLevelResponse{
				Price:    p,
				Quantity: pl.TotalQuantity,
				Orders:   len(pl.OrderQueue),
			})
		}
	}
	askLevels := make([]PriceLevelResponse, 0, len(asks))
	for _, p := range asks {
		if pl := ob.Asks[p]; pl != nil && !pl.IsEmpty() {
			askLevels = append(askLevels, PriceLevelResponse{
				Price:    p,
				Quantity: pl.TotalQuantity,
				Orders:   len(pl.OrderQueue),
			})
		}
	}
	return OrderBookResponse{MarketId: marketId, Bids: bidLevels, Asks: askLevels}
}

func validateOrderRequest(req SubmitOrderRequest) error {
	if req.AccountId == "" {
		return errorf("account_id required")
	}
	if req.SessionId == "" {
		return errorf("session_id required")
	}
	if req.MarketId == "" {
		return errorf("market_id required")
	}
	if req.Side != "BUY" && req.Side != "SELL" {
		return errorf("side must be BUY or SELL")
	}
	if req.Price <= 0 {
		return errorf("price must be > 0")
	}
	if req.Quantity <= 0 {
		return errorf("quantity must be > 0")
	}
	return nil
}

type apiError string

func (e apiError) Error() string { return string(e) }

func errorf(msg string) error { return apiError(msg) }

func pathSuffix(path, prefix string) string {
	if len(path) > len(prefix) {
		return path[len(prefix):]
	}
	return ""
}
