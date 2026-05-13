import { Suspense } from 'react'
import Link from 'next/link'
import OrderBook from '@/components/OrderBook'
import OrderForm from '@/components/OrderForm'
import RecentTrades from '@/components/RecentTrades'
import BottomPanel from '@/components/BottomPanel'
import MarketHeader from '@/components/MarketHeader'

export default async function TradePage({
  params,
}: {
  params: Promise<{ market: string }>
}) {
  const { market } = await params
  const marketId = decodeURIComponent(market)

  return (
    <div className="flex flex-col flex-1 h-full overflow-hidden">
      {/* Market Header */}
      <div className="border-b border-[#2a2a2a] bg-[#161616]">
        <MarketHeader marketId={marketId} />
      </div>

      {/* Main 3-column layout */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left: OrderBook */}
        <div className="w-56 flex-shrink-0 border-r border-[#2a2a2a] flex flex-col overflow-hidden">
          <OrderBook marketId={marketId} />
        </div>

        {/* Center: Chart + Recent Trades */}
        <div className="flex-1 flex flex-col overflow-hidden border-r border-[#2a2a2a]">
          {/* Chart area (placeholder) */}
          <div className="flex-1 bg-[#0d0d0d] flex items-center justify-center border-b border-[#2a2a2a]">
            <div className="text-center text-[#808080]">
              <div className="text-4xl mb-2">📈</div>
              <div className="text-sm">Chart coming soon</div>
              <div className="text-xs mt-1">{marketId}</div>
            </div>
          </div>
          {/* Recent Trades */}
          <div className="h-64 overflow-hidden">
            <RecentTrades marketId={marketId} />
          </div>
        </div>

        {/* Right: Order Form */}
        <div className="w-72 flex-shrink-0 overflow-hidden">
          <OrderForm marketId={marketId} />
        </div>
      </div>

      {/* Bottom Panel */}
      <div className="h-56 flex-shrink-0">
        <BottomPanel marketId={marketId} />
      </div>
    </div>
  )
}
