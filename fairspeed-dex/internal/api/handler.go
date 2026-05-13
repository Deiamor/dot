package api

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/compliance"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/token"
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

const (
	maxIDLen     = 64   // max length for account/session/market IDs
	maxNotional  = 1_000_000_000_000 // 1 trillion — sane upper bound
)

func validateOrderRequest(req SubmitOrderRequest) error {
	if req.AccountId == "" {
		return errorf("account_id required")
	}
	if len(req.AccountId) > maxIDLen {
		return errorf("account_id too long")
	}
	if req.SessionId == "" {
		return errorf("session_id required")
	}
	if len(req.SessionId) > maxIDLen {
		return errorf("session_id too long")
	}
	if req.MarketId == "" {
		return errorf("market_id required")
	}
	if len(req.MarketId) > maxIDLen {
		return errorf("market_id too long")
	}
	if req.Side != "BUY" && req.Side != "SELL" {
		return errorf("side must be BUY or SELL")
	}
	if req.Price <= 0 {
		return errorf("price must be > 0")
	}
	if req.Price > maxNotional {
		return errorf("price exceeds maximum allowed value")
	}
	if req.Quantity <= 0 {
		return errorf("quantity must be > 0")
	}
	if req.Quantity > maxNotional {
		return errorf("quantity exceeds maximum allowed value")
	}
	// Overflow guard: price * quantity must not overflow int64
	if req.Price > maxNotional/req.Quantity {
		return errorf("notional value (price × quantity) exceeds maximum")
	}
	tif := req.TimeInForce
	if tif != "" && tif != "GTC" && tif != "FOK" && tif != "IOC" {
		return errorf("time_in_force must be GTC, FOK, or IOC")
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
		var liqPrice int64
		if cfg, ok := s.node.GetPerpConfig(pos.MarketId); ok {
			liqPrice = pos.LiquidationPrice(cfg.MaintenanceMarginBps)
		}
		resp = append(resp, PositionResponse{
			MarketId:         pos.MarketId,
			NetQuantity:      pos.NetQuantity,
			AvgEntryPrice:    pos.AvgEntryPrice,
			AllocatedMargin:  pos.AllocatedMargin,
			UnrealizedPnL:    pnl,
			MarkPrice:        markPrice,
			LiquidationPrice: liqPrice,
			Side:             side,
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
	if req.OwnerAddress == "" {
		writeError(w, http.StatusBadRequest, "owner_address required")
		return
	}
	if len(req.OwnerAddress) > 128 {
		writeError(w, http.StatusBadRequest, "owner_address too long")
		return
	}
	if req.RootPublicKey == "" {
		writeError(w, http.StatusBadRequest, "root_public_key required")
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

// handleGetPoints handles GET /points/{accountId}
func (s *Server) handleGetPoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accountId := pathSuffix(r.URL.Path, "/points/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}

	ap := s.node.GetAccountPoints(accountId)
	var totalPoints, tradePoints, referralPoints int64
	var isEarlyBird bool
	if ap != nil {
		totalPoints = ap.TotalPoints
		tradePoints = ap.TradePoints
		referralPoints = ap.ReferralPoints
		isEarlyBird = ap.IsEarlyBird
	}
	fairEst := s.node.TGEAllocation(accountId, token.CommunityAlloc)
	writeJSON(w, http.StatusOK, PointsResponse{
		AccountId:      accountId,
		TotalPoints:    totalPoints,
		TradePoints:    tradePoints,
		ReferralPoints: referralPoints,
		IsEarlyBird:    isEarlyBird,
		FAIREstimate:   fairEst,
	})
}

// handleGetLeaderboard handles GET /points/leaderboard
func (s *Server) handleGetLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	top := s.node.PointsLeaderboard(100)
	resp := make([]LeaderboardEntry, len(top))
	for i, ap := range top {
		fairEst := s.node.TGEAllocation(ap.AccountId, token.CommunityAlloc)
		resp[i] = LeaderboardEntry{
			Rank:         i + 1,
			AccountId:    ap.AccountId,
			TotalPoints:  ap.TotalPoints,
			FAIREstimate: fairEst,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleReferral handles POST /referral
func (s *Server) handleReferral(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req ReferralRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AccountId == "" {
		writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	if req.ReferrerId == "" {
		writeError(w, http.StatusBadRequest, "referrer_id required")
		return
	}
	s.node.SetReferrer(req.AccountId, req.ReferrerId)
	writeJSON(w, http.StatusOK, map[string]string{
		"account_id":  req.AccountId,
		"referrer_id": req.ReferrerId,
		"status":      "ok",
	})
}

// handlePoints routes /points/ and /points/leaderboard
func (s *Server) handlePoints(w http.ResponseWriter, r *http.Request) {
	suffix := pathSuffix(r.URL.Path, "/points/")
	if strings.EqualFold(suffix, "leaderboard") {
		s.handleGetLeaderboard(w, r)
		return
	}
	s.handleGetPoints(w, r)
}

// handleOrderHistory handles GET /orders/{accountId}/history
func (s *Server) handleOrderHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// path: /orders/{accountId}/history
	rest := pathSuffix(r.URL.Path, "/orders/")
	accountId := strings.TrimSuffix(rest, "/history")
	if accountId == "" || accountId == rest {
		writeError(w, http.StatusBadRequest, "path must be /orders/{accountId}/history")
		return
	}
	limit := 100
	orders := s.node.GetOrderHistory(accountId, limit)
	resp := make([]OrderHistoryResponse, len(orders))
	for i, o := range orders {
		resp[i] = OrderHistoryResponse{
			OrderId:            o.OrderId,
			MarketId:           o.MarketId,
			Side:               string(o.Side),
			Price:              o.Price,
			Quantity:           o.Quantity,
			RemainingQuantity:  o.RemainingQuantity,
			FilledQuantity:     o.Quantity - o.RemainingQuantity,
			Status:             string(o.Status),
			TimeInForce:        string(o.TimeInForce),
			CreatedBlockHeight: o.CreatedBlockHeight,
			ClientOrderId:      o.ClientOrderId,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOpenOrders handles GET /orders/{accountId}/open
func (s *Server) handleOpenOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := pathSuffix(r.URL.Path, "/orders/")
	accountId := strings.TrimSuffix(rest, "/open")
	if accountId == "" || accountId == rest {
		writeError(w, http.StatusBadRequest, "path must be /orders/{accountId}/open")
		return
	}
	orders := s.node.AllOpenOrdersForAccount(accountId)
	resp := make([]OrderHistoryResponse, len(orders))
	for i, o := range orders {
		resp[i] = OrderHistoryResponse{
			OrderId:            o.OrderId,
			MarketId:           o.MarketId,
			Side:               string(o.Side),
			Price:              o.Price,
			Quantity:           o.Quantity,
			RemainingQuantity:  o.RemainingQuantity,
			FilledQuantity:     o.Quantity - o.RemainingQuantity,
			Status:             string(o.Status),
			TimeInForce:        string(o.TimeInForce),
			CreatedBlockHeight: o.CreatedBlockHeight,
			ClientOrderId:      o.ClientOrderId,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOrdersRouter dispatches /orders/{accountId}/history and /orders/{accountId}/open.
func (s *Server) handleOrdersRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasSuffix(path, "/history") {
		s.handleOrderHistory(w, r)
		return
	}
	if strings.HasSuffix(path, "/open") {
		s.handleOpenOrders(w, r)
		return
	}
	// Fall back to existing POST /orders handler.
	s.handleSubmitOrder(w, r)
}

// handleConditionalOrders routes POST /conditional-orders and GET /conditional-orders/{accountId}.
func (s *Server) handleConditionalOrders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleSubmitConditionalOrder(w, r)
	case http.MethodGet:
		s.handleGetConditionalOrders(w, r)
	case http.MethodDelete:
		s.handleCancelConditionalOrder(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleSubmitConditionalOrder(w http.ResponseWriter, r *http.Request) {
	var req SubmitConditionalOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AccountId == "" || req.MarketId == "" || req.Quantity <= 0 || req.TriggerPrice <= 0 {
		writeError(w, http.StatusBadRequest, "account_id, market_id, quantity, trigger_price required")
		return
	}
	side := clob.OrderSideBuy
	if strings.EqualFold(req.Side, "SELL") {
		side = clob.OrderSideSell
	}
	orderType := clob.OrderTypeMarket
	if strings.EqualFold(req.OrderType, "LIMIT") {
		orderType = clob.OrderTypeLimit
	}
	triggerCond := clob.TriggerGTE
	if strings.EqualFold(req.TriggerCondition, "LTE") {
		triggerCond = clob.TriggerLTE
	}
	height := s.node.CurrentHeight()
	expireAt := height + req.ExpireAfterBlocks
	if req.ExpireAfterBlocks <= 0 {
		expireAt = height + 3_600
	}
	o := clob.ConditionalOrder{
		OrderId:           newOrderId(),
		AccountId:         req.AccountId,
		SessionId:         req.SessionId,
		MarketId:          req.MarketId,
		Side:              side,
		OrderType:         orderType,
		Price:             req.Price,
		Quantity:          req.Quantity,
		TriggerPrice:      req.TriggerPrice,
		TriggerCondition:  triggerCond,
		ReduceOnly:        req.ReduceOnly,
		ExpireBlockHeight: expireAt,
		Status:            clob.OrderStatusOpen,
	}
	if err := s.node.SubmitConditionalOrder(o); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, conditionalOrderToResponse(o))
}

func (s *Server) handleGetConditionalOrders(w http.ResponseWriter, r *http.Request) {
	accountId := pathSuffix(r.URL.Path, "/conditional-orders/")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "accountId required")
		return
	}
	orders := s.node.AllConditionalOrdersForAccount(accountId)
	resp := make([]ConditionalOrderResponse, len(orders))
	for i, o := range orders {
		resp[i] = conditionalOrderToResponse(o)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCancelConditionalOrder(w http.ResponseWriter, r *http.Request) {
	orderId := pathSuffix(r.URL.Path, "/conditional-orders/")
	if orderId == "" {
		writeError(w, http.StatusBadRequest, "orderId required")
		return
	}
	accountId := r.URL.Query().Get("account_id")
	if accountId == "" {
		writeError(w, http.StatusBadRequest, "account_id query param required")
		return
	}
	if err := s.node.CancelConditionalOrder(orderId, accountId); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"order_id": orderId, "status": "cancelled"})
}

func conditionalOrderToResponse(o clob.ConditionalOrder) ConditionalOrderResponse {
	return ConditionalOrderResponse{
		OrderId:            o.OrderId,
		AccountId:          o.AccountId,
		MarketId:           o.MarketId,
		Side:               string(o.Side),
		OrderType:          string(o.OrderType),
		Price:              o.Price,
		Quantity:           o.Quantity,
		TriggerPrice:       o.TriggerPrice,
		TriggerCondition:   string(o.TriggerCondition),
		ReduceOnly:         o.ReduceOnly,
		Status:             string(o.Status),
		ExpireBlockHeight:  o.ExpireBlockHeight,
		CreatedBlockHeight: o.CreatedBlockHeight,
	}
}

// newOrderId generates a random order ID using crypto/rand.
func newOrderId() string {
	b := make([]byte, 8)
	if _, err := cryptoRand.Read(b); err != nil {
		return "cond-0000"
	}
	return fmt.Sprintf("cond-%x", b)
}
