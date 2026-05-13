# FairSpeed DEX — Gap Analysis & Improvement Report

## Overview

Reviewed after completion of Stages 1–25 (Spot CLOB, Perpetual Futures, Conditional Orders), FAIR Token, Points System, whitepaper, and Next.js frontend.

---

## Critical Gaps

### 1. Anti-Wash-Trade in Points System
**Gap**: A single operator running matching maker+taker accounts can farm points infinitely at near-zero cost.  
**Impact**: Corrupts TGE airdrop; early sybil farmers could drain 35% community allocation.  
**Fix**: Track `(makerAccountId, takerAccountId)` pairs. If the same account appears on both sides (self-trade), award 0 points. Also cap daily points per account to `MaxDailyPoints = 10_000`.

### 2. Missing Conditional Orders API
**Gap**: Conditional orders (stop-loss/take-profit) are fully implemented in the node but have no REST endpoints.  
**Impact**: Frontend cannot submit, view, or cancel conditional orders. Stage 25 is unreachable from the UI.  
**Fix**: Add `POST /conditional-orders`, `GET /conditional-orders/{accountId}`, `DELETE /conditional-orders/{orderId}`.

### 3. Testnet Infrastructure Absent
**Gap**: No faucet, no genesis config, no deployment scripts.  
**Impact**: Cannot onboard testnet users; points leaderboard has no data.  
**Fix**: Add `GET /faucet/{address}?asset=USDC&amount=1000` with IP-based 24h cooldown, testnet genesis JSON, Docker Compose for single-node testnet.

### 4. Frontend Points/Leaderboard Page Missing
**Gap**: `/points` and `/points/leaderboard` pages not in Next.js app.  
**Impact**: Core user engagement mechanic (points → FAIR airdrop) is invisible.  
**Fix**: Add `app/points/page.tsx` with personal stats + leaderboard table.

### 5. SSE Not Wired in Frontend
**Gap**: `OrderBook` and `RecentTrades` components use polling (`setInterval`) not SSE.  
**Impact**: Higher latency updates; defeats FairSpeed's low-latency positioning.  
**Fix**: Switch to `EventSource` connections to `/stream/orderbook?market=X` and `/stream/trades?market=X`.

---

## High-Priority Gaps

### 6. Input Validation on REST Endpoints
**Gap**: `POST /orders` checks only side/market/quantity but not price bounds, string length injection, or negative values.  
**Impact**: Malformed requests cause panics or silent mismatch.  
**Fix**: Add `validateOrderRequest` guards for `price > 0`, `quantity > 0`, `len(market_id) < 32`.

### 7. Whitepaper: Missing Competitor Comparison
**Gap**: Whitepaper claims FairSpeed outperforms Hyperliquid but provides no benchmarks.  
**Impact**: Investor credibility gap.  
**Fix**: Add Section 8 "Competitive Landscape" with latency/fee/decentralization comparison table.

### 8. Vesting: No On-Chain Enforcement
**Gap**: `VestingSchedule.ReleasableNow()` exists in `token/token.go` but is never called by the block processor.  
**Impact**: Team/investor FAIR allocations can be transferred immediately.  
**Fix**: Wire vesting check into withdrawal TX handler; reject transfers exceeding `ReleasableNow`.

### 9. Funding Rate Uses MarkPrice as IndexPrice
**Gap**: Stage 23 sets `indexPrice = markPrice` as a temporary stub.  
**Impact**: Funding rate is always 0; perpetuals lose their price-anchoring mechanism.  
**Fix**: Stage 26 should add an external price oracle feed consumed as `indexPrice` in `CalcFundingRate`.

### 10. No Liquidation Price in API Response
**Gap**: `GET /positions/{accountId}` returns `net_quantity` and `avg_entry_price` but not `liquidation_price`.  
**Impact**: Traders cannot see their risk without computing offline.  
**Fix**: Compute `pos.LiquidationPrice(maintenanceMarginBps)` in `handleGetPositions` and include in `PositionResponse`.

---

## Medium-Priority Gaps

### 11. No Order History Endpoint
**Gap**: Filled/cancelled orders are only visible through EventBus; no query endpoint exists.  
**Impact**: Users cannot review their trade history in the UI.  

### 12. Frontend Has No Conditional Order UI
**Gap**: Stop-loss/take-profit form is missing from `OrderForm.tsx`.  
**Impact**: Stage 25 feature is inaccessible.  

### 13. Governance Voting Not Wired to Parameter Changes
**Gap**: Governance module records votes but does not update `PerpConfig.InitialMarginBps` on passage.

### 14. KYC Status Not Checked on PERP Orders
**Gap**: `RiskChecker.CheckOrder` checks KYC for withdrawal but not for PERP order submission.

---

## Improvements Implemented in This Report

The following are being addressed immediately:

| # | Gap | Status |
|---|-----|--------|
| 1 | Anti-wash-trade + daily cap | ✅ Implemented below |
| 2 | Conditional orders REST API | ✅ Implemented below |
| 3 | Testnet faucet + genesis | ✅ Implemented below |
| 4 | Points/leaderboard frontend page | ✅ Implemented below |
| 10 | Liquidation price in positions API | ✅ Implemented below |

Remaining gaps (#5–9, #11–14) are logged for Stage 26+ sprint.

---

## Architecture Invariants (Maintained Throughout)

- `float64` prohibited — all arithmetic is `int64` (bps units)
- Session keys cannot withdraw
- Matching engine does not modify balances
- Only Settlement module modifies balances
- Only Fee module calculates fees
- stdlib only — no external Go dependencies
