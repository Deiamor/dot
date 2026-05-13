'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import type { Market } from '@/lib/api'
import clsx from 'clsx'

function fmtPrice(n: number) {
  return n.toLocaleString()
}

export default function MarketsPage() {
  const router = useRouter()
  const [markets, setMarkets] = useState<Market[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    import('@/lib/api').then(({ getMarkets }) => {
      getMarkets()
        .then((data) => {
          setMarkets(data)
          setLoading(false)
        })
        .catch((err) => {
          setError(err.message)
          setLoading(false)
        })
    })
  }, [])

  return (
    <div className="max-w-5xl mx-auto w-full px-6 py-8">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-[#e8e8e8]">Markets</h1>
        <p className="text-sm text-[#808080] mt-1">All available trading markets</p>
      </div>

      <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="border-b border-[#2a2a2a]">
              <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Market</th>
              <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Type</th>
              <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Mark Price</th>
              <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">24h Volume</th>
              <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {loading && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-[#808080] text-sm">
                  Loading markets...
                </td>
              </tr>
            )}
            {error && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-[#f0444b] text-sm">
                  {error}
                </td>
              </tr>
            )}
            {!loading && !error && markets.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-[#808080] text-sm">
                  No markets available
                </td>
              </tr>
            )}
            {markets.map((market) => (
              <tr
                key={market.market_id}
                onClick={() => router.push(`/trade/${market.market_id}`)}
                className="border-b border-[#2a2a2a] hover:bg-white/5 cursor-pointer transition-colors"
              >
                <td className="px-4 py-3 font-mono text-sm text-[#e8e8e8] font-semibold">{market.market_id}</td>
                <td className="px-4 py-3">
                  <span
                    className={clsx(
                      'px-2 py-0.5 rounded text-xs font-semibold',
                      market.type === 'PERP'
                        ? 'bg-purple-500/20 text-purple-400'
                        : 'bg-blue-500/20 text-blue-400'
                    )}
                  >
                    {market.type}
                  </span>
                </td>
                <td className="px-4 py-3 text-right font-mono text-sm text-[#e8e8e8]">
                  {fmtPrice(market.mark_price)}
                </td>
                <td className="px-4 py-3 text-right text-sm text-[#808080]">—</td>
                <td className="px-4 py-3">
                  <span
                    className={clsx(
                      'text-xs',
                      market.status === 'ACTIVE' ? 'text-[#00c076]' : 'text-[#808080]'
                    )}
                  >
                    {market.status}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
