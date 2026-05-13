'use client'

import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { OrderBook, Trade } from './api'

interface DexStore {
  accountId: string | null
  sessionId: string | null
  walletAddress: string | null
  selectedMarket: string
  orderBook: OrderBook | null
  recentTrades: Trade[]
  // sessionKey is NOT persisted — it lives only in memory for this page session.
  // The CryptoKey is non-extractable; losing it on refresh requires re-auth.
  sessionKey: CryptoKey | null
  setAccount: (accountId: string, sessionId: string, walletAddress?: string) => void
  setSessionKey: (key: CryptoKey) => void
  setWallet: (address: string) => void
  setMarket: (marketId: string) => void
  setOrderBook: (ob: OrderBook) => void
  addTrade: (trade: Trade) => void
  clearAccount: () => void
}

export const useDexStore = create<DexStore>()(
  persist(
    (set) => ({
      accountId: null,
      sessionId: null,
      walletAddress: null,
      selectedMarket: 'BTC-USDC-PERP',
      orderBook: null,
      recentTrades: [],
      sessionKey: null,
      setAccount: (accountId, sessionId, walletAddress) =>
        set({ accountId, sessionId, ...(walletAddress ? { walletAddress } : {}) }),
      setSessionKey: (key) => set({ sessionKey: key }),
      setWallet: (address) => set({ walletAddress: address }),
      setMarket: (marketId) => set({ selectedMarket: marketId }),
      setOrderBook: (ob) => set({ orderBook: ob }),
      addTrade: (trade) =>
        set((state) => ({
          recentTrades: [trade, ...state.recentTrades].slice(0, 30),
        })),
      clearAccount: () =>
        set({ accountId: null, sessionId: null, walletAddress: null, sessionKey: null }),
    }),
    {
      name: 'fairspeed-dex',
      // sessionKey intentionally excluded — non-extractable CryptoKey can't be serialized
      partialize: (state) => ({
        accountId: state.accountId,
        sessionId: state.sessionId,
        walletAddress: state.walletAddress,
        selectedMarket: state.selectedMarket,
      }),
    },
  ),
)
