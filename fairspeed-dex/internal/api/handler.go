package api

import (
	"encoding/json"
	"net/http"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
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
		KYCStatus:       string(acc.KYCStatus),
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

// handleComplianceReport handles GET /reports/trades
// Returns all trade executions in compliance-grade format (MiCA/FATF).
func (s *Server) handleComplianceReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	trades := s.node.AllTrades()
	records := compliance.ReportTrades(trades)
	writeJSON(w, http.StatusOK, records)
}

// handleKYCStatus handles GET /reports/kyc/{accountId}
func (s *Server) handleKYCStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accountId := pathSuffix(r.URL.Path, "/reports/kyc/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	status := s.node.GetKYCStatus(accountId)
	writeJSON(w, http.StatusOK, KYCStatusResponse{
		AccountId: accountId,
		KYCStatus: string(status),
	})
}

// handleGetMarkets handles GET /markets
// Returns all known markets with type, status, and current mark price.
func (s *Server) handleGetMarkets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	perpMarkets := s.node.AppState.AllPerpMarkets()
	seen := make(map[string]bool)
	var resp []MarketResponse

	// PERP markets
	for marketId, cfg := range perpMarkets {
		seen[marketId] = true
		markPrice := s.node.GetMarkPrice(marketId)
		status := "ACTIVE"
		if s.node.GetMarketStatus(marketId) == clob.MarketStatusHalted {
			status = "HALTED"
		}
		history := s.node.GetFundingHistory(marketId)
		var rateBps int64
		if len(history) > 0 {
			rateBps = history[len(history)-1].RateBps
		}
		_ = cfg
		resp = append(resp, MarketResponse{
			MarketId:       marketId,
			Type:           "PERP",
			Status:         status,
			MarkPrice:      markPrice,
			FundingRateBps: rateBps,
		})
	}

	// SPOT markets (from orderbooks that aren't PERP)
	s.node.AppState.EachMarket(func(marketId string, info *clob.MarketInfo) {
		if seen[marketId] {
			return
		}
		markPrice := s.node.GetMarkPrice(marketId)
		status := "ACTIVE"
		if info.Status == clob.MarketStatusHalted {
			status = "HALTED"
		}
		resp = append(resp, MarketResponse{
			MarketId:  marketId,
			Type:      "SPOT",
			Status:    status,
			MarkPrice: markPrice,
		})
	})

	if resp == nil {
		resp = []MarketResponse{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetMarkPrice handles GET /markprice/{marketId}
func (s *Server) handleGetMarkPrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	marketId := pathSuffix(r.URL.Path, "/markprice/")
	if marketId == "" {
		writeError(w, http.StatusBadRequest, "marketId required")
		return
	}
	markPrice := s.node.GetMarkPrice(marketId)
	var rateBps int64
	if history := s.node.GetFundingHistory(marketId); len(history) > 0 {
		rateBps = history[len(history)-1].RateBps
	}
	writeJSON(w, http.StatusOK, MarkPriceResponse{
		MarketId:       marketId,
		MarkPrice:      markPrice,
		FundingRateBps: rateBps,
		BlockHeight:    s.node.CurrentHeight(),
	})
}

// handleGetBalances handles GET /balances/{accountId}
func (s *Server) handleGetBalances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accountId := pathSuffix(r.URL.Path, "/balances/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	balances := s.node.AppState.AllBalancesForAccount(accountId)
	resp := make([]BalanceResponse, 0, len(balances))
	for _, b := range balances {
		resp = append(resp, BalanceResponse{
			AssetId:   b.AssetId,
			Available: b.Available,
			Reserved:  b.Reserved,
			Total:     b.Available + b.Reserved,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetPositions handles GET /positions/{accountId}
func (s *Server) handleGetPositions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accountId := pathSuffix(r.URL.Path, "/positions/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	all := s.node.AllPositionsForAccount(accountId)
	resp := make([]PositionResponse, 0, len(all))
	for _, pos := range all {
		if pos.NetQuantity == 0 {
			continue
		}
		markPrice := s.node.GetMarkPrice(pos.MarketId)
		pnl := pos.UnrealizedPnL(markPrice)
		side := "LONG"
		if pos.NetQuantity < 0 {
			side = "SHORT"
		}
		resp = append(resp, PositionResponse{
			MarketId:        pos.MarketId,
			NetQuantity:     pos.NetQuantity,
			AvgEntryPrice:   pos.AvgEntryPrice,
			AllocatedMargin: pos.AllocatedMargin,
			UnrealizedPnL:   pnl,
			MarkPrice:       markPrice,
			Side:            side,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAccounts routes POST /accounts to create and GET /accounts (no suffix) to list.
func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateAccount(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleCreateAccount handles POST /accounts
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	nextHeight := s.node.CurrentHeight() + 1
	batch := fairbatch.NewBatchBuilder(nextHeight).
		AddCreateAccount(req.OwnerAddress, req.RootPublicKey, req.WithdrawalPublicKey).
		Build()
	if _, err := s.node.SubmitBatch(batch); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Find the newly created account by owner address
	acc := s.node.AppState.FindAccountByOwner(req.OwnerAddress)
	if acc == nil {
		writeError(w, http.StatusInternalServerError, "account created but not found")
		return
	}
	writeJSON(w, http.StatusCreated, AccountResponse{
		AccountId:       acc.AccountId,
		OwnerAddress:    acc.OwnerAddress,
		Status:          string(acc.Status),
		KYCStatus:       string(acc.KYCStatus),
		AccountSequence: acc.AccountSequence,
	})
}

// handleCreateSession handles POST /sessions
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AccountId == "" {
		writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	markets := req.AllowedMarkets
	if len(markets) == 0 {
		markets = []string{"*"}
	}
	maxAmt := req.MaxOrderAmount
	if maxAmt <= 0 {
		maxAmt = 1_000_000_000
	}
	opts := account.SessionOptions{
		AllowedMarkets: markets,
		MaxOrderAmount: maxAmt,
	}
	if req.ExpireAfterBlocks > 0 {
		opts.ExpiresAtBlockHeight = s.node.CurrentHeight() + req.ExpireAfterBlocks
	}
	nextHeight := s.node.CurrentHeight() + 1
	batch := fairbatch.NewBatchBuilder(nextHeight).
		AddCreateSession(req.AccountId, opts).
		Build()
	// Capture the session ID synchronously via the EventBus (fires during batch processing).
	var sessionId string
	s.node.Subscribe(state.EventSessionCreated, func(e state.Event) {
		if p, ok := e.Payload.(state.SessionCreatedPayload); ok && p.AccountId == req.AccountId {
			sessionId = p.SessionId
		}
	})
	if _, err := s.node.SubmitBatch(batch); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if sessionId == "" {
		writeError(w, http.StatusInternalServerError, "session created but ID not found")
		return
	}
	sess, ok := s.node.GetSession(sessionId)
	if !ok {
		writeError(w, http.StatusInternalServerError, "session not found after creation")
		return
	}
	mktList := make([]string, 0, len(sess.AllowedMarkets))
	for m := range sess.AllowedMarkets {
		mktList = append(mktList, m)
	}
	writeJSON(w, http.StatusCreated, SessionResponse{
		SessionId:        sess.SessionId,
		AccountId:        sess.AccountId,
		AllowedMarkets:   mktList,
		SessionPublicKey: sess.SessionPublicKey,
	})
}
