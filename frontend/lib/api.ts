const BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'

export interface PriceLevel {
  price: number
  quantity: number
  orders: number
}

export interface OrderBook {
  market_id: string
  bids: PriceLevel[]
  asks: PriceLevel[]
}

export interface Trade {
  trade_id: string
  market_id: string
  price: number
  quantity: number
  block_height: number
}

export interface Market {
  market_id: string
  type: 'SPOT' | 'PERP'
  mark_price: number
  status: string
}

export interface Balance {
  asset_id: string
  available: number
  reserved: number
}

export interface Position {
  market_id: string
  net_quantity: number
  avg_entry_price: number
  allocated_margin: number
  unrealized_pnl: number
  mark_price: number
  liquidation_price: number
  side: string
}

export interface MarkPriceResponse {
  market_id: string
  mark_price: number
  funding_rate_bps: number
}

export interface SubmitOrderRequest {
  account_id: string
  session_id: string
  market_id: string
  side: 'BUY' | 'SELL'
  price: number
  quantity: number
  time_in_force: string
}

export interface OrderResponse {
  order_id: string
  status: string
  trade_count: number
}

export async function getOrderBook(marketId: string): Promise<OrderBook> {
  const res = await fetch(`${BASE}/orderbook/${marketId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch orderbook: ${res.status}`)
  return res.json()
}

export async function getTrades(marketId: string): Promise<Trade[]> {
  const res = await fetch(`${BASE}/trades/${marketId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch trades: ${res.status}`)
  return res.json()
}

export async function getMarkets(): Promise<Market[]> {
  const res = await fetch(`${BASE}/markets`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch markets: ${res.status}`)
  return res.json()
}

export async function getBalances(accountId: string): Promise<Balance[]> {
  const res = await fetch(`${BASE}/balances/${accountId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch balances: ${res.status}`)
  return res.json()
}

export async function getPositions(accountId: string): Promise<Position[]> {
  const res = await fetch(`${BASE}/positions/${accountId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch positions: ${res.status}`)
  return res.json()
}

export async function getMarkPrice(marketId: string): Promise<MarkPriceResponse> {
  const res = await fetch(`${BASE}/markprice/${marketId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch mark price: ${res.status}`)
  return res.json()
}

export async function submitOrder(req: SubmitOrderRequest): Promise<OrderResponse> {
  const res = await fetch(`${BASE}/orders`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    cache: 'no-store',
  })
  if (!res.ok) throw new Error(`Failed to submit order: ${res.status}`)
  return res.json()
}

export interface PointsInfo {
  account_id: string
  total_points: number
  trade_points: number
  referral_points: number
  is_early_bird: boolean
  fair_estimate: number
}

export interface LeaderboardEntry {
  rank: number
  account_id: string
  total_points: number
  fair_estimate: number
}

export async function getPoints(accountId: string): Promise<PointsInfo> {
  const res = await fetch(`${BASE}/points/${accountId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch points: ${res.status}`)
  return res.json()
}

export async function getLeaderboard(): Promise<LeaderboardEntry[]> {
  const res = await fetch(`${BASE}/points/leaderboard`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch leaderboard: ${res.status}`)
  return res.json()
}

export async function setReferrer(accountId: string, referrerId: string): Promise<void> {
  const res = await fetch(`${BASE}/referral`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ account_id: accountId, referrer_id: referrerId }),
    cache: 'no-store',
  })
  if (!res.ok) throw new Error(`Failed to set referrer: ${res.status}`)
}

export async function requestFaucet(accountId: string, asset?: string): Promise<unknown> {
  const url = asset ? `${BASE}/faucet/${accountId}?asset=${encodeURIComponent(asset)}` : `${BASE}/faucet/${accountId}`
  const res = await fetch(url, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Faucet error: ${res.status}`)
  return res.json()
}

export interface ConditionalOrderRequest {
  account_id: string
  session_id: string
  market_id: string
  side: 'BUY' | 'SELL'
  order_type: 'MARKET' | 'LIMIT'
  price: number
  quantity: number
  trigger_price: number
  trigger_condition: 'GTE' | 'LTE'
  reduce_only: boolean
  expire_after_blocks: number
}

export interface ConditionalOrderResponse {
  order_id: string
  account_id: string
  market_id: string
  side: string
  trigger_price: number
  trigger_condition: string
  quantity: number
  status: string
  expire_block_height: number
}

export async function submitConditionalOrder(req: ConditionalOrderRequest): Promise<ConditionalOrderResponse> {
  const res = await fetch(`${BASE}/conditional-orders`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    cache: 'no-store',
  })
  if (!res.ok) throw new Error(`Failed to submit conditional order: ${res.status}`)
  return res.json()
}

export async function getConditionalOrders(accountId: string): Promise<ConditionalOrderResponse[]> {
  const res = await fetch(`${BASE}/conditional-orders/${accountId}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch conditional orders: ${res.status}`)
  return res.json()
}

export async function cancelConditionalOrder(orderId: string, accountId: string): Promise<void> {
  const res = await fetch(`${BASE}/conditional-orders/${orderId}?account_id=${encodeURIComponent(accountId)}`, {
    method: 'DELETE',
    cache: 'no-store',
  })
  if (!res.ok) throw new Error(`Failed to cancel conditional order: ${res.status}`)
}

// --- Auth endpoints ---

export interface NonceResponse {
  nonce: string
  issued_at: string
  message: string
}

export interface ConnectResponse {
  account_id: string
  session_id: string
  wallet_address: string
  session_public_key: string
}

export interface ConnectRequest {
  address: string
  signature: string
  nonce: string
  issued_at: string
  session_public_key: string
}

/** GET /auth/nonce?address=0x... — fetch a one-time SIWE challenge. */
export async function getNonce(address: string): Promise<NonceResponse> {
  const res = await fetch(`${BASE}/auth/nonce?address=${encodeURIComponent(address)}`, {
    cache: 'no-store',
  })
  if (!res.ok) throw new Error(`Failed to get nonce: ${res.status}`)
  return res.json()
}

/** POST /auth/connect — verify SIWE signature, get DEX credentials. */
export async function connectWallet(req: ConnectRequest): Promise<ConnectResponse> {
  const res = await fetch(`${BASE}/auth/connect`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    cache: 'no-store',
  })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`Auth failed (${res.status}): ${body}`)
  }
  return res.json()
}

/** GET /height — returns the current block height. */
export async function getHeight(): Promise<number> {
  const res = await fetch(`${BASE}/height`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to get height: ${res.status}`)
  const data = await res.json()
  return data.height as number
}

export interface OrderHistoryItem {
  order_id: string
  market_id: string
  side: string
  price: number
  quantity: number
  remaining_quantity: number
  filled_quantity: number
  status: string
  time_in_force: string
  created_block_height: number
  client_order_id?: string
}

export async function getOrderHistory(accountId: string, limit = 100): Promise<OrderHistoryItem[]> {
  const res = await fetch(`${BASE}/orders/${encodeURIComponent(accountId)}/history?limit=${limit}`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch order history: ${res.status}`)
  return res.json()
}

export async function getOpenOrders(accountId: string): Promise<OrderHistoryItem[]> {
  const res = await fetch(`${BASE}/orders/${encodeURIComponent(accountId)}/open`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Failed to fetch open orders: ${res.status}`)
  return res.json()
}
