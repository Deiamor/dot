'use client'

import { useState } from 'react'
import { Wallet, X } from 'lucide-react'
import { useDexStore } from '@/lib/store'
import WalletModal from './WalletModal'

export default function WalletConnect() {
  const { accountId, walletAddress, clearAccount } = useDexStore()
  const [showModal, setShowModal] = useState(false)

  function truncate(s: string) {
    if (s.length <= 12) return s
    return s.slice(0, 6) + '...' + s.slice(-4)
  }

  const displayLabel = walletAddress ? truncate(walletAddress) : accountId ? truncate(accountId) : null

  return (
    <>
      {displayLabel ? (
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-2 px-3 py-1.5 bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg text-sm text-[#e8e8e8]">
            <div className="w-2 h-2 rounded-full bg-[#00c076]" />
            <span className="font-mono">{displayLabel}</span>
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
          onClick={() => setShowModal(true)}
          className="flex items-center gap-2 px-4 py-1.5 bg-[#00c076] hover:bg-[#00a865] text-black font-semibold text-sm rounded-lg transition-colors"
        >
          <Wallet size={14} />
          Connect Wallet
        </button>
      )}

      {showModal && <WalletModal onClose={() => setShowModal(false)} />}
    </>
  )
}
