/**
 * EIP-1193 wallet provider detection for MetaMask, OKX, Bitget, and Binance Web3 wallets.
 * No external dependencies — uses only the browser's window object.
 */

export type WalletId = 'metamask' | 'okx' | 'bitget' | 'binance' | 'walletconnect'

export interface WalletInfo {
  id: WalletId
  name: string
  icon: string // SVG path or emoji fallback
  available: boolean
}

/** Minimal EIP-1193 provider interface */
export interface EIP1193Provider {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>
  isMetaMask?: boolean
  isOKExWallet?: boolean
  isBitKeep?: boolean
  isBinance?: boolean
}

declare global {
  interface Window {
    ethereum?: EIP1193Provider & {
      providers?: EIP1193Provider[]
    }
    okxwallet?: EIP1193Provider
    bitkeep?: { ethereum?: EIP1193Provider }
    BinanceChain?: EIP1193Provider
  }
}

/** Resolve the EIP-1193 provider for a given wallet ID. Returns null if not injected. */
export function getProvider(id: WalletId): EIP1193Provider | null {
  if (typeof window === 'undefined') return null

  switch (id) {
    case 'metamask': {
      // window.ethereum.providers array exists when multiple wallets inject
      const providers = window.ethereum?.providers
      if (providers) {
        const mm = providers.find((p) => p.isMetaMask && !p.isBitKeep)
        if (mm) return mm
      }
      if (window.ethereum?.isMetaMask) return window.ethereum
      return null
    }

    case 'okx': {
      // OKX injects into window.okxwallet (preferred) and also window.ethereum
      if (window.okxwallet) return window.okxwallet
      const providers = window.ethereum?.providers
      if (providers) {
        const okx = providers.find((p) => p.isOKExWallet)
        if (okx) return okx
      }
      if (window.ethereum?.isOKExWallet) return window.ethereum
      return null
    }

    case 'bitget': {
      if (window.bitkeep?.ethereum) return window.bitkeep.ethereum
      const providers = window.ethereum?.providers
      if (providers) {
        const bg = providers.find((p) => p.isBitKeep)
        if (bg) return bg
      }
      if (window.ethereum?.isBitKeep) return window.ethereum
      return null
    }

    case 'binance': {
      // Binance Web3 Wallet injects window.BinanceChain
      if (window.BinanceChain) return window.BinanceChain
      if (window.ethereum?.isBinance) return window.ethereum
      return null
    }

    case 'walletconnect':
      // WalletConnect uses a relay protocol; handled separately in auth.ts
      return null

    default:
      return null
  }
}

/** Returns the full list of wallets with their availability status. */
export function listWallets(): WalletInfo[] {
  return [
    {
      id: 'metamask',
      name: 'MetaMask',
      icon: '🦊',
      available: getProvider('metamask') !== null,
    },
    {
      id: 'okx',
      name: 'OKX Wallet',
      icon: '⭕',
      available: getProvider('okx') !== null,
    },
    {
      id: 'bitget',
      name: 'Bitget Wallet',
      icon: '🔵',
      available: getProvider('bitget') !== null,
    },
    {
      id: 'binance',
      name: 'Binance Web3',
      icon: '💛',
      available: getProvider('binance') !== null,
    },
    {
      id: 'walletconnect',
      name: 'WalletConnect',
      icon: '🔗',
      available: true, // always shown; deep-links or QR on click
    },
  ]
}

/** Request eth_requestAccounts from the given provider and return the first address. */
export async function requestAccount(provider: EIP1193Provider): Promise<string> {
  const accounts = (await provider.request({
    method: 'eth_requestAccounts',
  })) as string[]
  if (!accounts || accounts.length === 0) {
    throw new Error('No accounts returned by wallet')
  }
  return accounts[0]
}

/** Sign a message with personal_sign. Passes the message as hex to avoid encoding issues. */
export async function personalSign(
  provider: EIP1193Provider,
  message: string,
  address: string,
): Promise<string> {
  const msgHex = '0x' + stringToHex(message)
  const sig = (await provider.request({
    method: 'personal_sign',
    params: [msgHex, address],
  })) as string
  return sig
}

function stringToHex(str: string): string {
  return Array.from(new TextEncoder().encode(str), (b) =>
    b.toString(16).padStart(2, '0'),
  ).join('')
}
