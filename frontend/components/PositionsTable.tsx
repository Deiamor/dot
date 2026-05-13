'use client'

import type { Position } from '@/lib/api'
import clsx from 'clsx'

function fmtPrice(n: number) {
  return n === 0 ? '—' : n.toLocaleString()
}

function fmtQty(n: number) {
  return n.toFixed(4)
}

function pnlStr(n: number) {
  const sign = n >= 0 ? '+' : ''
  return `${sign}${n.toLocaleString()}`
}

export default function PositionsTable({ positions }: { positions: Position[] }) {
  const open = positions.filter((p) => p.net_quantity !== 0)

  if (open.length === 0) {
    return (
      <div className="flex items-center justify-center h-24 text-[#808080] text-sm">
        No open positions
      </div>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs min-w-[720px]">
        <thead>
          <tr className="border-b border-[#2a2a2a]">
            <th className="px-3 py-2 text-left text-[#808080] font-medium">Market</th>
            <th className="px-3 py-2 text-left text-[#808080] font-medium">Side</th>
            <th className="px-3 py-2 text-right text-[#808080] font-medium">Size</th>
            <th className="px-3 py-2 text-right text-[#808080] font-medium">Entry</th>
            <th className="px-3 py-2 text-right text-[#808080] font-medium">Mark</th>
            <th className="px-3 py-2 text-right text-[#808080] font-medium">Liq. Price</th>
            <th className="px-3 py-2 text-right text-[#808080] font-medium">Unrealized PnL</th>
            <th className="px-3 py-2 text-right text-[#808080] font-medium">Margin</th>
          </tr>
        </thead>
        <tbody>
          {open.map((pos) => {
            const side = pos.side ?? (pos.net_quantity >= 0 ? 'LONG' : 'SHORT')
            const pnlPositive = pos.unrealized_pnl >= 0
            const pnlPct = pos.allocated_margin > 0
              ? ((pos.unrealized_pnl / pos.allocated_margin) * 100).toFixed(2)
              : '0.00'
            return (
              <tr key={pos.market_id} className="border-b border-[#2a2a2a] hover:bg-white/5">
                <td className="px-3 py-2 text-[#e8e8e8] font-mono">{pos.market_id}</td>
                <td className={clsx('px-3 py-2 font-semibold', side === 'LONG' ? 'text-[#00c076]' : 'text-[#f0444b]')}>
                  {side}
                </td>
                <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">{fmtQty(Math.abs(pos.net_quantity))}</td>
                <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">{fmtPrice(pos.avg_entry_price)}</td>
                <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">{fmtPrice(pos.mark_price)}</td>
                <td className="px-3 py-2 text-right text-[#f0444b] font-mono">{fmtPrice(pos.liquidation_price)}</td>
                <td className={clsx('px-3 py-2 text-right font-mono font-semibold', pnlPositive ? 'text-[#00c076]' : 'text-[#f0444b]')}>
                  {pnlStr(pos.unrealized_pnl)}
                  <span className="text-[10px] ml-1 opacity-70">({pnlPct}%)</span>
                </td>
                <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">{fmtPrice(pos.allocated_margin)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
