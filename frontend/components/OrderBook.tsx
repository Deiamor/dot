'use client'

import { useEffect, useState } from 'react'
import { useDexStore } from '@/lib/store'
import type { OrderBook as OrderBookType, PriceLevel } from '@/lib/api'

const BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'
const MAX_ROWS = 12

function fmtPrice(n: number) {
  return n.toLocaleString()
}

function fmtQty(n: number) {
  return n.toFixed(4)
}

function DepthRow({
  level,
  side,
  maxQty,
}: {
  level: PriceLevel
  side: 'bid' | 'ask'
  maxQty: number
}) {
  const pct = maxQty > 0 ? (level.quantity / maxQty) * 100 : 0
  const bgColor = side === 'bid' ? 'rgba(0,192,118,0.12)' : 'rgba(240,68,75,0.12)'
  const textColor = side === 'bid' ? 'text-[#00c076]' : 'text-[#f0444b]'

  return (
    <div
      className="relative flex items-center justify-between px-3 py-0.5 text-xs font-mono hover:bg-white/5 cursor-default select-none"
      style={{
        background: `linear-gradient(${side === 'bid' ? 'to left' : 'to right'}, ${bgColor} ${pct}%, transparent ${pct}%)`,
      }}
    >
      <span className={textColor}>{fmtPrice(level.price)}</span>
      <span className="text-[#e8e8e8]">{fmtQty(level.quantity)}</span>
      <span className="text-[#808080]">{level.orders}</span>
    </div>
  )
}

export default function OrderBook({ marketId }: { marketId: string }) {
  const { setOrderBook } = useDexStore()
  const [ob, setOb] = useState<OrderBookType | null>(null)

  useEffect(() => {
    // Subscribe to SSE for real-time updates
    const url = `${BASE}/stream/orderbook?market=${encodeURIComponent(marketId)}`
    const es = new EventSource(url)

    es.addEventListener('orderbook', (e: MessageEvent) => {
      try {
        const data: OrderBookType = JSON.parse(e.data)
        setOb(data)
        setOrderBook(data)
      } catch {}
    })

    es.onerror = () => {
      // SSE disconnected, try to fall back to REST
      import('@/lib/api').then(({ getOrderBook }) => {
        getOrderBook(marketId)
          .then((data) => {
            setOb(data)
            setOrderBook(data)
          })
          .catch(() => {})
      })
    }

    return () => {
      es.close()
    }
  }, [marketId, setOrderBook])

  const asks = ob ? [...ob.asks].slice(0, MAX_ROWS).reverse() : []
  const bids = ob ? ob.bids.slice(0, MAX_ROWS) : []

  const maxAskQty = asks.reduce((m, l) => Math.max(m, l.quantity), 0)
  const maxBidQty = bids.reduce((m, l) => Math.max(m, l.quantity), 0)

  const bestBid = bids[0]?.price ?? null
  const bestAsk = asks[asks.length - 1]?.price ?? null
  const spread = bestBid !== null && bestAsk !== null ? bestAsk - bestBid : null

  return (
    <div className="flex flex-col h-full bg-[#161616] border-r border-[#2a2a2a]">
      <div className="px-3 py-2 border-b border-[#2a2a2a]">
        <span className="text-xs font-semibold text-[#808080] uppercase tracking-wider">Order Book</span>
      </div>

      {/* Column headers */}
      <div className="flex items-center justify-between px-3 py-1 border-b border-[#2a2a2a]">
        <span className="text-[10px] text-[#808080]">Price</span>
        <span className="text-[10px] text-[#808080]">Qty</span>
        <span className="text-[10px] text-[#808080]">Orders</span>
      </div>

      {/* Asks (sells) — reversed so lowest ask is closest to spread */}
      <div className="flex-1 flex flex-col justify-end overflow-hidden">
        {asks.length === 0 && (
          <div className="flex-1 flex items-center justify-center text-[#808080] text-xs">
            {ob === null ? 'Loading...' : 'No asks'}
          </div>
        )}
        {asks.map((level) => (
          <DepthRow key={level.price} level={level} side="ask" maxQty={maxAskQty} />
        ))}
      </div>

      {/* Spread */}
      <div className="px-3 py-1.5 border-y border-[#2a2a2a] flex items-center justify-between bg-[#0d0d0d]">
        <span className="text-xs text-[#808080]">Spread</span>
        {spread !== null ? (
          <span className="text-xs text-[#e8e8e8] font-mono">{fmtPrice(spread)}</span>
        ) : (
          <span className="text-xs text-[#808080]">—</span>
        )}
      </div>

      {/* Bids (buys) */}
      <div className="flex-1 overflow-hidden">
        {bids.length === 0 && (
          <div className="flex items-center justify-center h-full text-[#808080] text-xs">
            {ob === null ? 'Loading...' : 'No bids'}
          </div>
        )}
        {bids.map((level) => (
          <DepthRow key={level.price} level={level} side="bid" maxQty={maxBidQty} />
        ))}
      </div>
    </div>
  )
}
