'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { usePathname } from 'next/navigation'
import type { Market, MarkPriceResponse } from '@/lib/api'
import clsx from 'clsx'

const QUICK_MARKETS = ['BTC-USDC-PERP', 'ETH-USDC-PERP', 'BTC-USDC', 'ETH-USDC']

export default function MarketHeader({ marketId }: { marketId: string }) {
  const [markPrice, setMarkPrice] = useState<MarkPriceResponse | null>(null)
  const [markets, setMarkets] = useState<Market[]>([])

  useEffect(() => {
    import('@/lib/api').then(({ getMarkPrice, getMarkets }) => {
      getMarkPrice(marketId)
        .then(setMarkPrice)
        .catch(() => {})
      getMarkets()
        .then(setMarkets)
        .catch(() => {})
    })
  }, [marketId])

  const isPerp = marketId.endsWith('-PERP')
  const currentMarket = markets.find((m) => m.market_id === marketId)

  return (
    <div className="flex items-center gap-4 px-4 py-2 overflow-x-auto">
      {/* Quick market tabs */}
      <div className="flex items-center gap-1 flex-shrink-0">
        {QUICK_MARKETS.map((m) => (
          <Link
            key={m}
            href={`/trade/${m}`}
            className={clsx(
              'px-3 py-1.5 text-xs rounded-md whitespace-nowrap transition-colors',
              m === marketId
                ? 'bg-[#1e1e1e] text-[#e8e8e8] font-semibold'
                : 'text-[#808080] hover:text-[#e8e8e8] hover:bg-[#1e1e1e]'
            )}
          >
            {m}
          </Link>
        ))}
      </div>

      <div className="w-px h-6 bg-[#2a2a2a] flex-shrink-0" />

      {/* Mark price */}
      <div className="flex items-center gap-6 flex-shrink-0">
        <div>
          <div className="text-[10px] text-[#808080] mb-0.5">Mark Price</div>
          <div className="text-sm font-semibold font-mono text-[#e8e8e8]">
            {markPrice ? markPrice.mark_price.toLocaleString() : '—'}
          </div>
        </div>

        {isPerp && markPrice && (
          <div>
            <div className="text-[10px] text-[#808080] mb-0.5">Funding Rate</div>
            <div
              className={clsx(
                'text-sm font-mono',
                markPrice.funding_rate_bps >= 0 ? 'text-[#00c076]' : 'text-[#f0444b]'
              )}
            >
              {markPrice.funding_rate_bps >= 0 ? '+' : ''}
              {markPrice.funding_rate_bps} bps
            </div>
          </div>
        )}

        <div>
          <div className="text-[10px] text-[#808080] mb-0.5">Type</div>
          <div className="text-sm text-[#e8e8e8]">
            <span
              className={clsx(
                'px-1.5 py-0.5 rounded text-[10px] font-semibold',
                isPerp ? 'bg-purple-500/20 text-purple-400' : 'bg-blue-500/20 text-blue-400'
              )}
            >
              {isPerp ? 'PERP' : 'SPOT'}
            </span>
          </div>
        </div>

        {currentMarket && (
          <div>
            <div className="text-[10px] text-[#808080] mb-0.5">Status</div>
            <div
              className={clsx(
                'text-xs',
                currentMarket.status === 'ACTIVE' ? 'text-[#00c076]' : 'text-[#808080]'
              )}
            >
              {currentMarket.status}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
