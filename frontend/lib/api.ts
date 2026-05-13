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
  unrealized_pnl: number
  allocated_margin: number
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
  const url = asset ? `${BASE}/faucet/${accountId}?asset=${asset}` : `${BASE}/faucet/${accountId}`
  const res = await fetch(url, { cache: 'no-store' })
  if (!res.ok) throw new Error(`Faucet error: ${res.status}`)
  return res.json()
}
