'use client'

import { useState, useEffect } from 'react'
import { useDexStore } from '@/lib/store'
import PositionsTable from './PositionsTable'
import type { Position, Balance } from '@/lib/api'
import clsx from 'clsx'

type Tab = 'positions' | 'orders' | 'history' | 'balances'

function fmtPrice(n: number) {
  return n.toLocaleString()
}

export default function BottomPanel({ marketId }: { marketId: string }) {
  const { accountId } = useDexStore()
  const [tab, setTab] = useState<Tab>('positions')
  const [positions, setPositions] = useState<Position[]>([])
  const [balances, setBalances] = useState<Balance[]>([])

  useEffect(() => {
    if (!accountId) return
    import('@/lib/api').then(({ getPositions, getBalances }) => {
      getPositions(accountId)
        .then(setPositions)
        .catch(() => {})
      getBalances(accountId)
        .then(setBalances)
        .catch(() => {})
    })
  }, [accountId])

  const tabs: { id: Tab; label: string }[] = [
    { id: 'positions', label: 'Positions' },
    { id: 'orders', label: 'Open Orders' },
    { id: 'history', label: 'Trade History' },
    { id: 'balances', label: 'Balances' },
  ]

  return (
    <div className="flex flex-col bg-[#161616] border-t border-[#2a2a2a]" style={{ minHeight: '200px' }}>
      {/* Tabs */}
      <div className="flex items-center border-b border-[#2a2a2a] px-4">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={clsx(
              'px-4 py-3 text-xs font-medium border-b-2 transition-colors -mb-px',
              tab === t.id
                ? 'border-[#00c076] text-[#e8e8e8]'
                : 'border-transparent text-[#808080] hover:text-[#e8e8e8]'
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto">
        {!accountId && (
          <div className="flex items-center justify-center h-24 text-[#808080] text-sm">
            Connect wallet to view account data
          </div>
        )}

        {accountId && tab === 'positions' && <PositionsTable positions={positions} />}

        {accountId && tab === 'orders' && (
          <div className="flex items-center justify-center h-24 text-[#808080] text-sm">
            No open orders
          </div>
        )}

        {accountId && tab === 'history' && (
          <div className="flex items-center justify-center h-24 text-[#808080] text-sm">
            No trade history
          </div>
        )}

        {accountId && tab === 'balances' && (
          <div className="overflow-x-auto">
            {balances.length === 0 ? (
              <div className="flex items-center justify-center h-24 text-[#808080] text-sm">No balances</div>
            ) : (
              <table className="w-full text-xs">
                <thead>
                  <tr className="border-b border-[#2a2a2a]">
                    <th className="px-3 py-2 text-left text-[#808080] font-medium">Asset</th>
                    <th className="px-3 py-2 text-right text-[#808080] font-medium">Available</th>
                    <th className="px-3 py-2 text-right text-[#808080] font-medium">Reserved</th>
                    <th className="px-3 py-2 text-right text-[#808080] font-medium">Total</th>
                  </tr>
                </thead>
                <tbody>
                  {balances.map((b) => (
                    <tr key={b.asset_id} className="border-b border-[#2a2a2a] hover:bg-white/5">
                      <td className="px-3 py-2 text-[#e8e8e8] font-mono">{b.asset_id}</td>
                      <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">{fmtPrice(b.available)}</td>
                      <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">{fmtPrice(b.reserved)}</td>
                      <td className="px-3 py-2 text-right text-[#e8e8e8] font-mono">
                        {fmtPrice(b.available + b.reserved)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
