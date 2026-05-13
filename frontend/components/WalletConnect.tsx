'use client'

import { useState } from 'react'
import { Wallet, X } from 'lucide-react'
import { useDexStore } from '@/lib/store'

export default function WalletConnect() {
  const { accountId, setAccount, clearAccount } = useDexStore()
  const [open, setOpen] = useState(false)
  const [inputAccountId, setInputAccountId] = useState('')
  const [inputSessionId, setInputSessionId] = useState('')

  function handleConnect() {
    if (inputAccountId.trim() && inputSessionId.trim()) {
      setAccount(inputAccountId.trim(), inputSessionId.trim())
      setOpen(false)
      setInputAccountId('')
      setInputSessionId('')
    }
  }

  function truncate(s: string) {
    if (s.length <= 12) return s
    return s.slice(0, 6) + '...' + s.slice(-4)
  }

  return (
    <>
      {accountId ? (
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-2 px-3 py-1.5 bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg text-sm text-[#e8e8e8]">
            <div className="w-2 h-2 rounded-full bg-[#00c076]" />
            <span className="font-mono">{truncate(accountId)}</span>
          </div>
          <button
            onClick={clearAccount}
            className="p-1.5 text-[#808080] hover:text-[#e8e8e8] transition-colors"
            title="Disconnect"
          >
            <X size={14} />
          </button>
        </div>
      ) : (
        <button
          onClick={() => setOpen(true)}
          className="flex items-center gap-2 px-4 py-1.5 bg-[#00c076] hover:bg-[#00a865] text-black font-semibold text-sm rounded-lg transition-colors"
        >
          <Wallet size={14} />
          Connect Wallet
        </button>
      )}

      {open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70">
          <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 w-full max-w-md shadow-2xl">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[#e8e8e8] font-semibold text-lg">Connect Wallet</h2>
              <button onClick={() => setOpen(false)} className="text-[#808080] hover:text-[#e8e8e8]">
                <X size={18} />
              </button>
            </div>

            <div className="mb-4 p-3 bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg text-xs text-[#808080]">
              Testnet / Demo mode — enter any account ID and session ID to get started.
            </div>

            <div className="space-y-3">
              <div>
                <label className="block text-xs text-[#808080] mb-1">Account ID</label>
                <input
                  type="text"
                  value={inputAccountId}
                  onChange={(e) => setInputAccountId(e.target.value)}
                  placeholder="e.g. 0xabc123..."
                  className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
                />
              </div>
              <div>
                <label className="block text-xs text-[#808080] mb-1">Session ID</label>
                <input
                  type="text"
                  value={inputSessionId}
                  onChange={(e) => setInputSessionId(e.target.value)}
                  placeholder="e.g. session_abc123"
                  className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
                />
              </div>
            </div>

            <button
              onClick={handleConnect}
              disabled={!inputAccountId.trim() || !inputSessionId.trim()}
              className="mt-4 w-full py-2.5 bg-[#00c076] hover:bg-[#00a865] disabled:opacity-40 disabled:cursor-not-allowed text-black font-semibold text-sm rounded-lg transition-colors"
            >
              Connect
            </button>
          </div>
        </div>
      )}
    </>
  )
}
