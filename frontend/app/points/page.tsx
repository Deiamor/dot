'use client'

import { useEffect, useState } from 'react'
import type { LeaderboardEntry, PointsInfo } from '@/lib/api'
import { useDexStore } from '@/lib/store'

function fmtFAIR(n: number) {
  return (n / 1_000_000).toLocaleString(undefined, { maximumFractionDigits: 2 }) + ' FAIR'
}

function fmtPoints(n: number) {
  return n.toLocaleString()
}

function PersonalCard({ accountId }: { accountId: string }) {
  const [info, setInfo] = useState<PointsInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [referrerId, setReferrerId] = useState('')
  const [referralStatus, setReferralStatus] = useState<string | null>(null)
  const [faucetStatus, setFaucetStatus] = useState<string | null>(null)

  useEffect(() => {
    import('@/lib/api').then(({ getPoints }) => {
      getPoints(accountId)
        .then((d) => { setInfo(d); setLoading(false) })
        .catch(() => setLoading(false))
    })
  }, [accountId])

  const handleReferral = async () => {
    if (!referrerId.trim()) return
    const { setReferrer } = await import('@/lib/api')
    try {
      await setReferrer(accountId, referrerId.trim())
      setReferralStatus('Referrer set successfully!')
    } catch {
      setReferralStatus('Failed to set referrer.')
    }
  }

  const handleFaucet = async () => {
    const { requestFaucet } = await import('@/lib/api')
    try {
      await requestFaucet(accountId)
      setFaucetStatus('Testnet tokens sent! Refresh balances.')
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Faucet error'
      setFaucetStatus(msg)
    }
  }

  if (loading) return <div className="text-[#808080] text-sm">Loading your points...</div>
  if (!info) return null

  return (
    <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-[#e8e8e8]">Your Points</h2>
        {info.is_early_bird && (
          <span className="text-xs bg-yellow-500/20 text-yellow-400 px-2 py-0.5 rounded font-semibold">
            Early Bird 2x
          </span>
        )}
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <p className="text-xs text-[#808080]">Total Points</p>
          <p className="text-2xl font-bold text-[#e8e8e8] font-mono">{fmtPoints(info.total_points)}</p>
        </div>
        <div>
          <p className="text-xs text-[#808080]">Est. FAIR Airdrop</p>
          <p className="text-2xl font-bold text-[#00c076] font-mono">{fmtFAIR(info.fair_estimate)}</p>
        </div>
        <div>
          <p className="text-xs text-[#808080]">Trade Points</p>
          <p className="text-lg font-semibold text-[#e8e8e8]">{fmtPoints(info.trade_points)}</p>
        </div>
        <div>
          <p className="text-xs text-[#808080]">Referral Points</p>
          <p className="text-lg font-semibold text-[#e8e8e8]">{fmtPoints(info.referral_points)}</p>
        </div>
      </div>

      <div className="border-t border-[#2a2a2a] pt-4 space-y-2">
        <p className="text-xs text-[#808080] font-medium">Set Referrer</p>
        <div className="flex gap-2">
          <input
            value={referrerId}
            onChange={(e) => setReferrerId(e.target.value)}
            placeholder="Referrer account ID"
            className="flex-1 bg-[#0d0d0d] border border-[#2a2a2a] rounded px-3 py-1.5 text-sm text-[#e8e8e8] placeholder-[#505050] outline-none focus:border-[#4a4a4a]"
          />
          <button
            onClick={handleReferral}
            className="px-3 py-1.5 bg-blue-600/80 hover:bg-blue-600 text-white text-sm rounded transition-colors"
          >
            Set
          </button>
        </div>
        {referralStatus && <p className="text-xs text-[#808080]">{referralStatus}</p>}
      </div>

      <div className="border-t border-[#2a2a2a] pt-4">
        <p className="text-xs text-[#808080] font-medium mb-2">Testnet Faucet</p>
        <button
          onClick={handleFaucet}
          className="w-full py-2 bg-[#1e1e1e] hover:bg-[#2a2a2a] border border-[#2a2a2a] text-sm text-[#e8e8e8] rounded transition-colors"
        >
          Request Testnet Tokens
        </button>
        {faucetStatus && <p className="text-xs text-[#808080] mt-2">{faucetStatus}</p>}
      </div>
    </div>
  )
}

export default function PointsPage() {
  const accountId = useDexStore((s) => s.accountId)
  const [leaderboard, setLeaderboard] = useState<LeaderboardEntry[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    import('@/lib/api').then(({ getLeaderboard }) => {
      getLeaderboard()
        .then((data) => { setLeaderboard(data); setLoading(false) })
        .catch(() => setLoading(false))
    })
  }, [])

  return (
    <div className="max-w-5xl mx-auto w-full px-6 py-8 space-y-8">
      <div>
        <h1 className="text-2xl font-bold text-[#e8e8e8]">Points & Airdrop</h1>
        <p className="text-sm text-[#808080] mt-1">
          Trade to earn points. Top traders receive FAIR token allocations at TGE.
        </p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-1 space-y-4">
          {accountId ? (
            <PersonalCard accountId={accountId} />
          ) : (
            <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 text-center text-sm text-[#808080]">
              Connect a wallet to view your points
            </div>
          )}

          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 space-y-3">
            <h3 className="text-sm font-semibold text-[#e8e8e8]">How to Earn</h3>
            <ul className="space-y-2 text-xs text-[#808080]">
              <li className="flex items-start gap-2">
                <span className="text-[#00c076] mt-0.5">▸</span>
                <span><strong className="text-[#e8e8e8]">1 point</strong> per $1 notional traded (SPOT)</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-[#00c076] mt-0.5">▸</span>
                <span><strong className="text-[#e8e8e8]">2x boost</strong> on all PERP trades</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-[#00c076] mt-0.5">▸</span>
                <span><strong className="text-[#e8e8e8]">1.5x boost</strong> for maker orders</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-[#00c076] mt-0.5">▸</span>
                <span><strong className="text-[#e8e8e8]">10%</strong> of referee points (referral bonus)</span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-yellow-400 mt-0.5">★</span>
                <span><strong className="text-yellow-400">2x early-bird</strong> for first 10,000 wallets</span>
              </li>
            </ul>
          </div>
        </div>

        <div className="lg:col-span-2">
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl overflow-hidden">
            <div className="px-4 py-3 border-b border-[#2a2a2a]">
              <h2 className="text-sm font-semibold text-[#e8e8e8]">Leaderboard — Top 100</h2>
            </div>
            <table className="w-full">
              <thead>
                <tr className="border-b border-[#2a2a2a]">
                  <th className="px-4 py-2.5 text-left text-xs text-[#808080] font-medium w-12">Rank</th>
                  <th className="px-4 py-2.5 text-left text-xs text-[#808080] font-medium">Account</th>
                  <th className="px-4 py-2.5 text-right text-xs text-[#808080] font-medium">Points</th>
                  <th className="px-4 py-2.5 text-right text-xs text-[#808080] font-medium">Est. FAIR</th>
                </tr>
              </thead>
              <tbody>
                {loading && (
                  <tr>
                    <td colSpan={4} className="px-4 py-8 text-center text-[#808080] text-sm">
                      Loading leaderboard...
                    </td>
                  </tr>
                )}
                {!loading && leaderboard.length === 0 && (
                  <tr>
                    <td colSpan={4} className="px-4 py-8 text-center text-[#808080] text-sm">
                      No activity yet. Start trading to earn points!
                    </td>
                  </tr>
                )}
                {leaderboard.map((entry) => {
                  const isMe = entry.account_id === accountId
                  return (
                    <tr
                      key={entry.account_id}
                      className={`border-b border-[#2a2a2a] ${isMe ? 'bg-blue-500/10' : 'hover:bg-white/5'} transition-colors`}
                    >
                      <td className="px-4 py-2.5">
                        <span className={`text-sm font-bold ${entry.rank <= 3 ? 'text-yellow-400' : 'text-[#505050]'}`}>
                          #{entry.rank}
                        </span>
                      </td>
                      <td className="px-4 py-2.5">
                        <span className="font-mono text-xs text-[#e8e8e8]">
                          {entry.account_id.slice(0, 8)}...{entry.account_id.slice(-6)}
                        </span>
                        {isMe && (
                          <span className="ml-2 text-xs bg-blue-500/20 text-blue-400 px-1.5 py-0.5 rounded">
                            You
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-sm text-[#e8e8e8]">
                        {fmtPoints(entry.total_points)}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-sm text-[#00c076]">
                        {fmtFAIR(entry.fair_estimate)}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  )
}
