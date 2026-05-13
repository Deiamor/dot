/**
 * 6-Layer Security Auth Flow
 *
 * Layer 1 — Wallet ownership proof:   SIWE signature (personal_sign)
 * Layer 2 — Account binding:          Ethereum address → DEX accountId (server-side)
 * Layer 3 — Session key:              Ed25519 non-extractable keypair (Web Crypto)
 * Layer 4 — Per-order signing:        Ed25519 sign(txHash) per order
 * Layer 5 — Replay protection:        AccountSequence (server increments before check)
 * Layer 6 — Withdrawal re-auth:       Session keys can never withdraw (server policy)
 */

import { type EIP1193Provider, requestAccount, personalSign } from './wallet/providers'
import { generateSessionKeyPair, type SessionKeyPair } from './wallet/session'
import { getNonce, connectWallet, type ConnectResponse } from './api'

export interface AuthResult extends ConnectResponse {
  sessionKeyPair: SessionKeyPair
}

/**
 * Full auth flow for an injected EIP-1193 wallet:
 * 1. Request accounts
 * 2. Generate Ed25519 session keypair (non-extractable)
 * 3. Fetch SIWE nonce from backend
 * 4. Sign SIWE message with wallet
 * 5. POST /auth/connect → get accountId + sessionId
 */
export async function authenticateWithProvider(
  provider: EIP1193Provider,
): Promise<AuthResult> {
  // Step 1: Get wallet address
  const address = await requestAccount(provider)

  // Step 2: Generate non-extractable Ed25519 session keypair
  const sessionKeyPair = await generateSessionKeyPair()

  // Step 3: Fetch SIWE nonce
  const { nonce, issued_at: issuedAt, message } = await getNonce(address)

  // Step 4: Sign the SIWE message with the wallet
  const signature = await personalSign(provider, message, address)

  // Step 5: Exchange signature for DEX credentials
  const credentials = await connectWallet({
    address,
    signature,
    nonce,
    issued_at: issuedAt,
    session_public_key: sessionKeyPair.publicKeyHex,
  })

  return { ...credentials, sessionKeyPair }
}

/**
 * Auth flow for WalletConnect (relay-based).
 * Requires @walletconnect/sign-client which must be installed separately.
 * Falls back to a helpful error if not configured.
 */
export async function authenticateWithWalletConnect(
  projectId: string,
): Promise<AuthResult> {
  if (!projectId) {
    throw new Error(
      'WalletConnect project ID not configured. Set NEXT_PUBLIC_WC_PROJECT_ID.',
    )
  }

  // Dynamic import so WalletConnect SDK is optional at build time.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let SignClient: any = null
  try {
    // @ts-expect-error — optional peer dependency; not installed by default
    const mod = await import('@walletconnect/sign-client')
    SignClient = await mod.SignClient.init({ projectId })
  } catch {
    throw new Error(
      'WalletConnect SDK not installed. Run: npm install @walletconnect/sign-client @walletconnect/modal',
    )
  }

  const { uri, approval } = await SignClient.connect({
    requiredNamespaces: {
      eip155: {
        methods: ['eth_requestAccounts', 'personal_sign'],
        chains: ['eip155:1'],
        events: ['accountsChanged'],
      },
    },
  })

  if (uri) {
    // Surface the URI — the UI layer should show a QR code
    window.dispatchEvent(new CustomEvent('wc:uri', { detail: uri }))
  }

  const session = await approval()
  const address = session.namespaces.eip155.accounts[0].split(':')[2]

  const sessionKeyPair = await generateSessionKeyPair()
  const { nonce, issued_at: issuedAt, message } = await getNonce(address)

  const signature = (await SignClient.request({
    topic: session.topic,
    chainId: 'eip155:1',
    request: {
      method: 'personal_sign',
      params: [
        '0x' + Array.from(new TextEncoder().encode(message), (b) =>
          b.toString(16).padStart(2, '0'),
        ).join(''),
        address,
      ],
    },
  })) as string

  const credentials = await connectWallet({
    address,
    signature,
    nonce,
    issued_at: issuedAt,
    session_public_key: sessionKeyPair.publicKeyHex,
  })

  return { ...credentials, sessionKeyPair }
}
