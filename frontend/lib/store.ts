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
  setAccount: (accountId: string, sessionId: string) => void
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
      setAccount: (accountId, sessionId) => set({ accountId, sessionId }),
      setWallet: (address) => set({ walletAddress: address }),
      setMarket: (marketId) => set({ selectedMarket: marketId }),
      setOrderBook: (ob) => set({ orderBook: ob }),
      addTrade: (trade) =>
        set((state) => ({
          recentTrades: [trade, ...state.recentTrades].slice(0, 30),
        })),
      clearAccount: () => set({ accountId: null, sessionId: null, walletAddress: null }),
    }),
    {
      name: 'fairspeed-dex',
      partialize: (state) => ({
        accountId: state.accountId,
        sessionId: state.sessionId,
        walletAddress: state.walletAddress,
        selectedMarket: state.selectedMarket,
      }),
    }
  )
)
