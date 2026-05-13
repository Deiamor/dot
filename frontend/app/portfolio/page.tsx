'use client'

import { useEffect, useState } from 'react'
import { useDexStore } from '@/lib/store'
import PositionsTable from '@/components/PositionsTable'
import type { Balance, Position } from '@/lib/api'
import { X } from 'lucide-react'

function fmtPrice(n: number) {
  return n.toLocaleString()
}

export default function PortfolioPage() {
  const { accountId, setAccount } = useDexStore()
  const [balances, setBalances] = useState<Balance[]>([])
  const [positions, setPositions] = useState<Position[]>([])
  const [loading, setLoading] = useState(false)
  const [showDepositModal, setShowDepositModal] = useState(false)
  const [modalType, setModalType] = useState<'deposit' | 'withdraw'>('deposit')
  const [demoAccountId, setDemoAccountId] = useState('')
  const [demoSessionId, setDemoSessionId] = useState('')

  useEffect(() => {
    if (!accountId) return
    setLoading(true)
    import('@/lib/api').then(({ getBalances, getPositions }) => {
      Promise.all([getBalances(accountId), getPositions(accountId)])
        .then(([b, p]) => {
          setBalances(b)
          setPositions(p)
          setLoading(false)
        })
        .catch(() => setLoading(false))
    })
  }, [accountId])

  function handleDemoConnect() {
    if (demoAccountId.trim() && demoSessionId.trim()) {
      setAccount(demoAccountId.trim(), demoSessionId.trim())
      setShowDepositModal(false)
    }
  }

  return (
    <div className="max-w-5xl mx-auto w-full px-6 py-8">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-[#e8e8e8]">Portfolio</h1>
          {accountId && (
            <p className="text-sm text-[#808080] mt-1 font-mono">{accountId}</p>
          )}
        </div>
        <div className="flex gap-3">
          <button
            onClick={() => { setModalType('deposit'); setShowDepositModal(true) }}
            className="px-4 py-2 bg-[#00c076] hover:bg-[#00a865] text-black font-semibold text-sm rounded-lg transition-colors"
          >
            Deposit
          </button>
          <button
            onClick={() => { setModalType('withdraw'); setShowDepositModal(true) }}
            className="px-4 py-2 bg-[#1e1e1e] hover:bg-[#2a2a2a] border border-[#2a2a2a] text-[#e8e8e8] text-sm rounded-lg transition-colors"
          >
            Withdraw
          </button>
        </div>
      </div>

      {!accountId && (
        <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-8 text-center">
          <p className="text-[#808080] mb-4">Connect your wallet to view portfolio</p>
          <button
            onClick={() => { setModalType('deposit'); setShowDepositModal(true) }}
            className="px-4 py-2 bg-[#00c076] hover:bg-[#00a865] text-black font-semibold text-sm rounded-lg transition-colors"
          >
            Connect Wallet
          </button>
        </div>
      )}

      {accountId && (
        <>
          {/* Balances */}
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl overflow-hidden mb-6">
            <div className="px-4 py-3 border-b border-[#2a2a2a]">
              <h2 className="text-sm font-semibold text-[#e8e8e8]">Balances</h2>
            </div>
            {loading ? (
              <div className="px-4 py-8 text-center text-[#808080] text-sm">Loading...</div>
            ) : balances.length === 0 ? (
              <div className="px-4 py-8 text-center text-[#808080] text-sm">No balances</div>
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="border-b border-[#2a2a2a]">
                    <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Asset</th>
                    <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Available</th>
                    <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Reserved</th>
                    <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Total</th>
                  </tr>
                </thead>
                <tbody>
                  {balances.map((b) => (
                    <tr key={b.asset_id} className="border-b border-[#2a2a2a] hover:bg-white/5">
                      <td className="px-4 py-3 font-mono text-sm text-[#e8e8e8]">{b.asset_id}</td>
                      <td className="px-4 py-3 text-right font-mono text-sm text-[#e8e8e8]">{fmtPrice(b.available)}</td>
                      <td className="px-4 py-3 text-right font-mono text-sm text-[#e8e8e8]">{fmtPrice(b.reserved)}</td>
                      <td className="px-4 py-3 text-right font-mono text-sm font-semibold text-[#e8e8e8]">
                        {fmtPrice(b.available + b.reserved)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {/* Positions */}
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl overflow-hidden">
            <div className="px-4 py-3 border-b border-[#2a2a2a]">
              <h2 className="text-sm font-semibold text-[#e8e8e8]">Open Positions</h2>
            </div>
            {loading ? (
              <div className="px-4 py-8 text-center text-[#808080] text-sm">Loading...</div>
            ) : (
              <PositionsTable positions={positions} />
            )}
          </div>
        </>
      )}

      {/* Deposit/Withdraw Modal */}
      {showDepositModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70">
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 w-full max-w-md shadow-2xl">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[#e8e8e8] font-semibold text-lg capitalize">{modalType}</h2>
              <button onClick={() => setShowDepositModal(false)} className="text-[#808080] hover:text-[#e8e8e8]">
                <X size={18} />
              </button>
            </div>

            <div className="mb-4 p-3 bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg text-xs text-[#808080]">
              Testnet / Demo mode — enter account credentials to connect.
            </div>

            {!accountId && (
              <div className="space-y-3 mb-4">
                <div>
                  <label className="block text-xs text-[#808080] mb-1">Account ID</label>
                  <input
                    type="text"
                    value={demoAccountId}
                    onChange={(e) => setDemoAccountId(e.target.value)}
                    placeholder="e.g. 0xabc123..."
                    className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
                  />
                </div>
                <div>
                  <label className="block text-xs text-[#808080] mb-1">Session ID</label>
                  <input
                    type="text"
                    value={demoSessionId}
                    onChange={(e) => setDemoSessionId(e.target.value)}
                    placeholder="e.g. session_abc123"
                    className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
                  />
                </div>
              </div>
            )}

            {accountId && (
              <div className="mb-4 p-3 bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg text-xs text-[#e8e8e8]">
                Connected as: <span className="font-mono text-[#00c076]">{accountId}</span>
                <br />
                <span className="text-[#808080] mt-1 block">
                  {modalType === 'deposit'
                    ? 'On mainnet, deposit would initiate a blockchain transaction.'
                    : 'On mainnet, withdraw would submit a withdrawal request.'}
                </span>
              </div>
            )}

            <button
              onClick={accountId ? () => setShowDepositModal(false) : handleDemoConnect}
              disabled={!accountId && (!demoAccountId.trim() || !demoSessionId.trim())}
              className="w-full py-2.5 bg-[#00c076] hover:bg-[#00a865] disabled:opacity-40 disabled:cursor-not-allowed text-black font-semibold text-sm rounded-lg transition-colors"
            >
              {accountId ? 'Close' : 'Connect'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
