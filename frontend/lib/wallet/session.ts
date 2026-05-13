/**
 * Ed25519 session keypair management using the Web Crypto API.
 *
 * The private key is NON-EXTRACTABLE — it never leaves the browser's
 * crypto subsystem. Only the public key (as hex) is sent to the backend.
 *
 * The keypair lives only in memory for the session duration and is NOT
 * persisted to localStorage (a compromised localStorage cannot leak it).
 */

export interface SessionKeyPair {
  publicKeyHex: string
  privateKey: CryptoKey // non-extractable
}

/** Generate a fresh Ed25519 keypair. Returns publicKeyHex + non-extractable privateKey. */
export async function generateSessionKeyPair(): Promise<SessionKeyPair> {
  const keyPair = await crypto.subtle.generateKey(
    { name: 'Ed25519' } as EcKeyGenParams,
    false, // non-extractable — private key stays inside the crypto engine
    ['sign', 'verify'],
  )

  const pubKeyRaw = await crypto.subtle.exportKey('raw', keyPair.publicKey)
  const publicKeyHex = bufToHex(pubKeyRaw)

  return {
    publicKeyHex,
    privateKey: keyPair.privateKey,
  }
}

/**
 * Sign a message (txHash string) with the non-extractable private key.
 * Returns the 64-byte Ed25519 signature as a hex string.
 */
export async function signWithSessionKey(
  privateKey: CryptoKey,
  txHash: string,
): Promise<string> {
  const msgBytes = new TextEncoder().encode(txHash)
  const sigBuf = await crypto.subtle.sign('Ed25519', privateKey, msgBytes)
  return bufToHex(sigBuf)
}

/**
 * Compute the transaction hash for a LIMIT order submission.
 * This mirrors the Go implementation in fairbatch/batch.go:
 *   SHA256("3:accountId:sessionId::accountId:sessionId:marketId:side:LIMIT:price:qty:tif:clientOrderId:sequence:blockHeight")
 *
 * The signature field is excluded (circular dependency prevention).
 */
export async function computeOrderTxHash(params: {
  accountId: string
  sessionId: string
  marketId: string
  side: 'BUY' | 'SELL'
  price: number
  quantity: number
  timeInForce: string
  clientOrderId: string
  accountSequence: number
  blockHeight: number
}): Promise<string> {
  const {
    accountId,
    sessionId,
    marketId,
    side,
    price,
    quantity,
    timeInForce,
    clientOrderId,
    accountSequence,
    blockHeight,
  } = params

  // TxSubmitOrder = 3; empty string at position 3 = signature placeholder (excluded)
  const data = [
    '3',
    accountId,
    sessionId,
    '',
    accountId,
    sessionId,
    marketId,
    side,
    'LIMIT',
    String(price),
    String(quantity),
    timeInForce,
    clientOrderId,
    String(accountSequence),
    String(blockHeight),
  ].join(':')

  const msgBytes = new TextEncoder().encode(data)
  const hashBuf = await crypto.subtle.digest('SHA-256', msgBytes)
  return bufToHex(hashBuf)
}

function bufToHex(buf: ArrayBuffer): string {
  return Array.from(new Uint8Array(buf), (b) => b.toString(16).padStart(2, '0')).join('')
}
