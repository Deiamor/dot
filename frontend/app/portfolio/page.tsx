'use client'

import { useEffect, useState } from 'react'
import { useDexStore } from '@/lib/store'
import PositionsTable from '@/components/PositionsTable'
import type { Balance, Position, OrderHistoryItem } from '@/lib/api'
import { X } from 'lucide-react'

function fmtPrice(n: number) {
  return n.toLocaleString()
}

function SideChip({ side }: { side: string }) {
  const isBuy = side.toUpperCase() === 'BUY'
  return (
    <span className={`font-semibold ${isBuy ? 'text-[#00c076]' : 'text-[#f6465d]'}`}>
      {side.toUpperCase()}
    </span>
  )
}

function StatusChip({ status }: { status: string }) {
  const colors: Record<string, string> = {
    FILLED: 'text-[#00c076]',
    PARTIAL: 'text-[#f0b90b]',
    OPEN: 'text-[#e8e8e8]',
    CANCELLED: 'text-[#808080]',
    REJECTED: 'text-[#f6465d]',
    EXPIRED: 'text-[#808080]',
  }
  return <span className={colors[status] ?? 'text-[#e8e8e8]'}>{status}</span>
}

function OrdersTable({ orders, emptyMsg }: { orders: OrderHistoryItem[]; emptyMsg: string }) {
  if (orders.length === 0) {
    return <div className="px-4 py-8 text-center text-[#808080] text-sm">{emptyMsg}</div>
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[700px]">
        <thead>
          <tr className="border-b border-[#2a2a2a]">
            <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Market</th>
            <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Side</th>
            <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Price</th>
            <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Qty</th>
            <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Filled</th>
            <th className="px-4 py-3 text-left text-xs text-[#808080] font-medium">Status</th>
            <th className="px-4 py-3 text-right text-xs text-[#808080] font-medium">Block</th>
          </tr>
        </thead>
        <tbody>
          {orders.map((o) => (
            <tr key={o.order_id} className="border-b border-[#2a2a2a] hover:bg-white/5">
              <td className="px-4 py-3 font-mono text-sm text-[#e8e8e8]">{o.market_id}</td>
              <td className="px-4 py-3 text-sm"><SideChip side={o.side} /></td>
              <td className="px-4 py-3 text-right font-mono text-sm text-[#e8e8e8]">{fmtPrice(o.price)}</td>
              <td className="px-4 py-3 text-right font-mono text-sm text-[#e8e8e8]">{o.quantity}</td>
              <td className="px-4 py-3 text-right font-mono text-sm text-[#e8e8e8]">
                {o.filled_quantity}
                <span className="text-[#808080]">/{o.quantity}</span>
              </td>
              <td className="px-4 py-3 text-sm"><StatusChip status={o.status} /></td>
              <td className="px-4 py-3 text-right font-mono text-xs text-[#808080]">{o.created_block_height}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

type Tab = 'positions' | 'open-orders' | 'history'

export default function PortfolioPage() {
  const { accountId, setAccount } = useDexStore()
  const [balances, setBalances] = useState<Balance[]>([])
  const [positions, setPositions] = useState<Position[]>([])
  const [openOrders, setOpenOrders] = useState<OrderHistoryItem[]>([])
  const [orderHistory, setOrderHistory] = useState<OrderHistoryItem[]>([])
  const [loading, setLoading] = useState(false)
  const [tab, setTab] = useState<Tab>('positions')
  const [showModal, setShowModal] = useState(false)
  const [modalType, setModalType] = useState<'deposit' | 'withdraw'>('deposit')
  const [demoAccountId, setDemoAccountId] = useState('')
  const [demoSessionId, setDemoSessionId] = useState('')

  useEffect(() => {
    if (!accountId) return
    setLoading(true)
    import('@/lib/api').then(({ getBalances, getPositions, getOpenOrders, getOrderHistory }) => {
      Promise.all([getBalances(accountId), getPositions(accountId), getOpenOrders(accountId), getOrderHistory(accountId)])
        .then(([b, p, oo, oh]) => {
          setBalances(b)
          setPositions(p)
          setOpenOrders(oo)
          setOrderHistory(oh)
          setLoading(false)
        })
        .catch(() => setLoading(false))
    })
  }, [accountId])

  function handleDemoConnect() {
    if (demoAccountId.trim() && demoSessionId.trim()) {
      setAccount(demoAccountId.trim(), demoSessionId.trim())
      setShowModal(false)
    }
  }

  const tabs: { id: Tab; label: string; count?: number }[] = [
    { id: 'positions', label: 'Positions', count: positions.filter(p => p.net_quantity !== 0).length },
    { id: 'open-orders', label: 'Open Orders', count: openOrders.length },
    { id: 'history', label: 'Order History', count: orderHistory.length },
  ]

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
            onClick={() => { setModalType('deposit'); setShowModal(true) }}
            className="px-4 py-2 bg-[#00c076] hover:bg-[#00a865] text-black font-semibold text-sm rounded-lg transition-colors"
          >
            Deposit
          </button>
          <button
            onClick={() => { setModalType('withdraw'); setShowModal(true) }}
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
            onClick={() => { setModalType('deposit'); setShowModal(true) }}
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

          {/* Tabbed section: Positions / Open Orders / History */}
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl overflow-hidden">
            <div className="flex border-b border-[#2a2a2a]">
              {tabs.map((t) => (
                <button
                  key={t.id}
                  onClick={() => setTab(t.id)}
                  className={`px-4 py-3 text-sm font-medium transition-colors flex items-center gap-1.5 ${
                    tab === t.id
                      ? 'text-[#e8e8e8] border-b-2 border-[#00c076] -mb-px'
                      : 'text-[#808080] hover:text-[#e8e8e8]'
                  }`}
                >
                  {t.label}
                  {t.count !== undefined && t.count > 0 && (
                    <span className="text-xs bg-[#2a2a2a] text-[#808080] px-1.5 py-0.5 rounded-full">
                      {t.count}
                    </span>
                  )}
                </button>
              ))}
            </div>

            {loading ? (
              <div className="px-4 py-8 text-center text-[#808080] text-sm">Loading...</div>
            ) : (
              <>
                {tab === 'positions' && <PositionsTable positions={positions} />}
                {tab === 'open-orders' && (
                  <OrdersTable orders={openOrders} emptyMsg="No open orders" />
                )}
                {tab === 'history' && (
                  <OrdersTable orders={orderHistory} emptyMsg="No order history" />
                )}
              </>
            )}
          </div>
        </>
      )}

      {/* Deposit/Withdraw Modal */}
      {showModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70">
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 w-full max-w-md shadow-2xl">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[#e8e8e8] font-semibold text-lg capitalize">{modalType}</h2>
              <button onClick={() => setShowModal(false)} className="text-[#808080] hover:text-[#e8e8e8]">
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
              onClick={accountId ? () => setShowModal(false) : handleDemoConnect}
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
