'use client'

import { useEffect, useState } from 'react'
import type { Trade } from '@/lib/api'

const BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'

function fmtPrice(n: number) {
  return n.toLocaleString()
}

function fmtQty(n: number) {
  return n.toFixed(4)
}

export default function RecentTrades({ marketId }: { marketId: string }) {
  const [trades, setTrades] = useState<Trade[]>([])

  useEffect(() => {
    // Initial fetch
    import('@/lib/api').then(({ getTrades }) => {
      getTrades(marketId)
        .then((data) => setTrades(data.slice(0, 30)))
        .catch(() => {})
    })

    // Subscribe to SSE stream
    const url = `${BASE}/stream/trades`
    const es = new EventSource(url)

    es.addEventListener('trade', (e: MessageEvent) => {
      try {
        const trade: Trade = JSON.parse(e.data)
        if (trade.market_id === marketId) {
          setTrades((prev) => [trade, ...prev].slice(0, 30))
        }
      } catch {}
    })

    es.onerror = () => {}

    return () => {
      es.close()
    }
  }, [marketId])

  return (
    <div className="flex flex-col h-full bg-[#161616]">
      <div className="px-3 py-2 border-b border-[#2a2a2a]">
        <span className="text-xs font-semibold text-[#808080] uppercase tracking-wider">Recent Trades</span>
      </div>

      <div className="flex items-center justify-between px-3 py-1 border-b border-[#2a2a2a]">
        <span className="text-[10px] text-[#808080]">Price</span>
        <span className="text-[10px] text-[#808080]">Qty</span>
        <span className="text-[10px] text-[#808080]">Block</span>
      </div>

      <div className="flex-1 overflow-y-auto">
        {trades.length === 0 && (
          <div className="flex items-center justify-center h-16 text-[#808080] text-xs">No trades</div>
        )}
        {trades.map((trade, i) => {
          const prevPrice = trades[i + 1]?.price ?? trade.price
          const color =
            trade.price > prevPrice
              ? 'text-[#00c076]'
              : trade.price < prevPrice
              ? 'text-[#f0444b]'
              : 'text-[#e8e8e8]'
          return (
            <div
              key={trade.trade_id}
              className="flex items-center justify-between px-3 py-0.5 text-xs font-mono hover:bg-white/5"
            >
              <span className={color}>{fmtPrice(trade.price)}</span>
              <span className="text-[#e8e8e8]">{fmtQty(trade.quantity)}</span>
              <span className="text-[#808080]">{trade.block_height}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
