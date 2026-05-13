'use client'

import { useState, useEffect } from 'react'
import { X, Loader2, CheckCircle, AlertCircle } from 'lucide-react'
import { listWallets, getProvider, type WalletId } from '@/lib/wallet/providers'
import { authenticateWithProvider, authenticateWithWalletConnect } from '@/lib/auth'
import { useDexStore } from '@/lib/store'

interface WalletModalProps {
  onClose: () => void
}

type Step = 'pick' | 'connecting' | 'signing' | 'done' | 'error'

export default function WalletModal({ onClose }: WalletModalProps) {
  const { setAccount, setSessionKey } = useDexStore()
  const [wallets, setWallets] = useState(listWallets())
  const [step, setStep] = useState<Step>('pick')
  const [selectedWallet, setSelectedWallet] = useState<WalletId | null>(null)
  const [errorMsg, setErrorMsg] = useState('')
  const [wcUri, setWcUri] = useState<string | null>(null)

  // Refresh wallet availability on mount (window might not exist at SSR)
  useEffect(() => {
    setWallets(listWallets())

    // Listen for WalletConnect URI event
    const handleUri = (e: CustomEvent<string>) => setWcUri(e.detail)
    window.addEventListener('wc:uri', handleUri as EventListener)
    return () => window.removeEventListener('wc:uri', handleUri as EventListener)
  }, [])

  async function handleWalletSelect(id: WalletId) {
    setSelectedWallet(id)
    setErrorMsg('')
    setStep('connecting')

    try {
      let result

      if (id === 'walletconnect') {
        const projectId = process.env.NEXT_PUBLIC_WC_PROJECT_ID ?? ''
        setStep('signing')
        result = await authenticateWithWalletConnect(projectId)
      } else {
        const provider = getProvider(id)
        if (!provider) {
          throw new Error(`${id} wallet not detected. Please install the extension.`)
        }
        setStep('signing')
        result = await authenticateWithProvider(provider)
      }

      // Store credentials and session key
      setAccount(result.account_id, result.session_id, result.wallet_address)
      setSessionKey(result.sessionKeyPair.privateKey)

      setStep('done')
      setTimeout(onClose, 800)
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err))
      setStep('error')
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm">
      <div className="bg-[#161616] border border-[#2a2a2a] rounded-xl p-6 w-full max-w-md shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-[#e8e8e8] font-semibold text-lg">
            {step === 'pick' && 'Connect Wallet'}
            {step === 'connecting' && 'Connecting…'}
            {step === 'signing' && 'Sign to Authenticate'}
            {step === 'done' && 'Connected!'}
            {step === 'error' && 'Connection Failed'}
          </h2>
          <button
            onClick={onClose}
            className="text-[#808080] hover:text-[#e8e8e8] transition-colors"
          >
            <X size={18} />
          </button>
        </div>

        {/* Wallet list */}
        {step === 'pick' && (
          <>
            <p className="text-xs text-[#808080] mb-4">
              Sign once with your wallet to get a secure session key. No private key ever
              leaves your device.
            </p>
            <div className="space-y-2">
              {wallets.map((w) => (
                <button
                  key={w.id}
                  onClick={() => handleWalletSelect(w.id)}
                  className="w-full flex items-center gap-3 px-4 py-3 bg-[#1e1e1e] hover:bg-[#252525] border border-[#2a2a2a] hover:border-[#3a3a3a] rounded-lg transition-all group"
                >
                  <span className="text-xl w-8 text-center">{w.icon}</span>
                  <span className="flex-1 text-left text-sm text-[#e8e8e8] font-medium">
                    {w.name}
                  </span>
                  {w.available ? (
                    <span className="text-xs text-[#00c076] font-mono">Detected</span>
                  ) : (
                    <span className="text-xs text-[#808080]">Not installed</span>
                  )}
                </button>
              ))}
            </div>

            {/* Security note */}
            <div className="mt-4 p-3 bg-[#1a1a2e] border border-[#2a2a4a] rounded-lg">
              <p className="text-xs text-[#6080c0]">
                🔒 Session keys are generated in-browser and never sent to any server.
                Your wallet signature proves ownership — your private key stays safe.
              </p>
            </div>
          </>
        )}

        {/* Connecting / Signing status */}
        {(step === 'connecting' || step === 'signing') && (
          <div className="flex flex-col items-center gap-4 py-6">
            <Loader2 size={40} className="text-[#00c076] animate-spin" />
            {step === 'connecting' && (
              <p className="text-sm text-[#808080] text-center">
                Opening {selectedWallet} wallet…
              </p>
            )}
            {step === 'signing' && (
              <div className="text-center space-y-2">
                <p className="text-sm text-[#e8e8e8]">Check your wallet</p>
                <p className="text-xs text-[#808080]">
                  Sign the login message to authenticate.
                  <br />
                  This does not cost any gas.
                </p>
              </div>
            )}
            {/* WalletConnect QR URI hint */}
            {wcUri && (
              <div className="w-full p-2 bg-[#1e1e1e] rounded-lg text-center">
                <p className="text-xs text-[#808080] break-all font-mono">{wcUri}</p>
              </div>
            )}
          </div>
        )}

        {/* Success */}
        {step === 'done' && (
          <div className="flex flex-col items-center gap-3 py-6">
            <CheckCircle size={40} className="text-[#00c076]" />
            <p className="text-sm text-[#e8e8e8]">Wallet connected securely!</p>
          </div>
        )}

        {/* Error */}
        {step === 'error' && (
          <div className="space-y-4">
            <div className="flex items-start gap-3 p-3 bg-[#2a1010] border border-[#4a1010] rounded-lg">
              <AlertCircle size={18} className="text-[#ff4444] shrink-0 mt-0.5" />
              <p className="text-xs text-[#ff8888]">{errorMsg}</p>
            </div>
            <button
              onClick={() => setStep('pick')}
              className="w-full py-2 text-sm text-[#808080] hover:text-[#e8e8e8] border border-[#2a2a2a] hover:border-[#3a3a3a] rounded-lg transition-colors"
            >
              Try Again
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
