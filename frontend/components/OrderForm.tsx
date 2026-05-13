'use client'

import { useState } from 'react'
import { useDexStore } from '@/lib/store'
import { submitOrder, submitConditionalOrder } from '@/lib/api'
import clsx from 'clsx'

type OrderMode = 'limit' | 'conditional'

export default function OrderForm({ marketId }: { marketId: string }) {
  const { accountId, sessionId } = useDexStore()
  const isPerp = marketId.endsWith('-PERP')

  const [mode, setMode] = useState<OrderMode>('limit')
  const [side, setSide] = useState<'BUY' | 'SELL'>('BUY')
  const [price, setPrice] = useState('')
  const [quantity, setQuantity] = useState('')
  const [leverage, setLeverage] = useState(1)

  // Conditional order state
  const [triggerPrice, setTriggerPrice] = useState('')
  const [triggerCondition, setTriggerCondition] = useState<'GTE' | 'LTE'>('LTE')
  const [reduceOnly, setReduceOnly] = useState(false)
  const [expireAfterBlocks, setExpireAfterBlocks] = useState('3600')

  const [submitting, setSubmitting] = useState(false)
  const [message, setMessage] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  const baseAsset = marketId.split('-')[0] ?? marketId

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!accountId || !sessionId) {
      setMessage({ type: 'err', text: 'Connect wallet first' })
      return
    }
    setSubmitting(true)
    setMessage(null)
    try {
      if (mode === 'limit') {
        const res = await submitOrder({
          account_id: accountId,
          session_id: sessionId,
          market_id: marketId,
          side,
          price: parseInt(price, 10),
          quantity: parseFloat(quantity),
          time_in_force: 'GTC',
        })
        setMessage({ type: 'ok', text: `Order ${res.order_id} — ${res.status}` })
        setPrice('')
        setQuantity('')
      } else {
        const res = await submitConditionalOrder({
          account_id: accountId,
          session_id: sessionId,
          market_id: marketId,
          side,
          order_type: 'MARKET',
          price: 0,
          quantity: parseFloat(quantity),
          trigger_price: parseInt(triggerPrice, 10),
          trigger_condition: triggerCondition,
          reduce_only: reduceOnly,
          expire_after_blocks: parseInt(expireAfterBlocks, 10) || 3600,
        })
        setMessage({ type: 'ok', text: `Conditional order ${res.order_id} placed` })
        setQuantity('')
        setTriggerPrice('')
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setMessage({ type: 'err', text: msg })
    } finally {
      setSubmitting(false)
    }
  }

  const conditionLabel =
    triggerCondition === 'GTE'
      ? side === 'SELL' ? 'Take Profit (≥)' : 'Stop Buy (≥)'
      : side === 'SELL' ? 'Stop Loss (≤)' : 'Stop Buy (≤)'

  return (
    <div className="flex flex-col h-full bg-[#161616] p-4">
      {/* Order mode tabs */}
      <div className="flex rounded-lg overflow-hidden border border-[#2a2a2a] mb-3">
        <button
          className={clsx(
            'flex-1 py-1.5 text-xs font-semibold transition-colors',
            mode === 'limit' ? 'bg-[#2a2a2a] text-[#e8e8e8]' : 'text-[#808080] hover:text-[#e8e8e8]'
          )}
          onClick={() => setMode('limit')}
        >
          Limit
        </button>
        <button
          className={clsx(
            'flex-1 py-1.5 text-xs font-semibold transition-colors',
            mode === 'conditional' ? 'bg-[#2a2a2a] text-[#e8e8e8]' : 'text-[#808080] hover:text-[#e8e8e8]'
          )}
          onClick={() => setMode('conditional')}
        >
          SL / TP
        </button>
      </div>

      {/* BUY / SELL tabs */}
      <div className="flex rounded-lg overflow-hidden border border-[#2a2a2a] mb-4">
        <button
          className={clsx(
            'flex-1 py-2 text-sm font-semibold transition-colors',
            side === 'BUY' ? 'bg-[#00c076] text-black' : 'bg-transparent text-[#808080] hover:text-[#e8e8e8]'
          )}
          onClick={() => setSide('BUY')}
        >
          Buy
        </button>
        <button
          className={clsx(
            'flex-1 py-2 text-sm font-semibold transition-colors',
            side === 'SELL' ? 'bg-[#f0444b] text-white' : 'bg-transparent text-[#808080] hover:text-[#e8e8e8]'
          )}
          onClick={() => setSide('SELL')}
        >
          Sell
        </button>
      </div>

      <form onSubmit={handleSubmit} className="flex flex-col gap-3 flex-1">
        {mode === 'limit' ? (
          <>
            <div>
              <label className="block text-xs text-[#808080] mb-1">Price (USD)</label>
              <input
                type="number"
                step="1"
                value={price}
                onChange={(e) => setPrice(e.target.value)}
                placeholder="0"
                required
                className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
              />
            </div>

            <div>
              <label className="block text-xs text-[#808080] mb-1">Quantity ({baseAsset})</label>
              <input
                type="number"
                step="0.001"
                min="0"
                value={quantity}
                onChange={(e) => setQuantity(e.target.value)}
                placeholder="0.000"
                required
                className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
              />
            </div>

            {isPerp && (
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label className="text-xs text-[#808080]">Leverage</label>
                  <span className="text-xs font-semibold text-[#e8e8e8]">{leverage}x</span>
                </div>
                <input
                  type="range"
                  min={1}
                  max={20}
                  step={1}
                  value={leverage}
                  onChange={(e) => setLeverage(parseInt(e.target.value, 10))}
                  className="w-full accent-[#00c076]"
                />
                <div className="flex justify-between text-[10px] text-[#808080] mt-0.5">
                  <span>1x</span>
                  <span>5x</span>
                  <span>10x</span>
                  <span>20x</span>
                </div>
              </div>
            )}
          </>
        ) : (
          /* Conditional order fields */
          <>
            <div>
              <label className="block text-xs text-[#808080] mb-1">Condition</label>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setTriggerCondition('LTE')}
                  className={clsx(
                    'flex-1 py-1.5 text-xs rounded border transition-colors',
                    triggerCondition === 'LTE'
                      ? 'bg-[#f0444b]/20 border-[#f0444b]/50 text-[#f0444b]'
                      : 'border-[#2a2a2a] text-[#808080] hover:text-[#e8e8e8]'
                  )}
                >
                  ≤ Stop Loss
                </button>
                <button
                  type="button"
                  onClick={() => setTriggerCondition('GTE')}
                  className={clsx(
                    'flex-1 py-1.5 text-xs rounded border transition-colors',
                    triggerCondition === 'GTE'
                      ? 'bg-[#00c076]/20 border-[#00c076]/50 text-[#00c076]'
                      : 'border-[#2a2a2a] text-[#808080] hover:text-[#e8e8e8]'
                  )}
                >
                  ≥ Take Profit
                </button>
              </div>
              <p className="text-[10px] text-[#808080] mt-1">{conditionLabel}</p>
            </div>

            <div>
              <label className="block text-xs text-[#808080] mb-1">Trigger Price (USD)</label>
              <input
                type="number"
                step="1"
                value={triggerPrice}
                onChange={(e) => setTriggerPrice(e.target.value)}
                placeholder="0"
                required
                className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
              />
            </div>

            <div>
              <label className="block text-xs text-[#808080] mb-1">Quantity ({baseAsset})</label>
              <input
                type="number"
                step="0.001"
                min="0"
                value={quantity}
                onChange={(e) => setQuantity(e.target.value)}
                placeholder="0.000"
                required
                className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
              />
            </div>

            <div>
              <label className="block text-xs text-[#808080] mb-1">Expires After (blocks)</label>
              <input
                type="number"
                step="100"
                min="100"
                value={expireAfterBlocks}
                onChange={(e) => setExpireAfterBlocks(e.target.value)}
                className="w-full bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg px-3 py-2 text-sm text-[#e8e8e8] placeholder-[#808080] focus:outline-none focus:border-[#00c076]"
              />
              <p className="text-[10px] text-[#808080] mt-0.5">≈{Math.round(parseInt(expireAfterBlocks || '3600', 10) / 3600)}h at 1s/block</p>
            </div>

            {isPerp && (
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={reduceOnly}
                  onChange={(e) => setReduceOnly(e.target.checked)}
                  className="accent-[#00c076]"
                />
                <span className="text-xs text-[#808080]">Reduce Only</span>
              </label>
            )}
          </>
        )}

        {!accountId && (
          <div className="p-2.5 bg-[#1e1e1e] border border-[#2a2a2a] rounded-lg text-xs text-[#808080]">
            Demo mode — connect wallet to trade
          </div>
        )}

        {message && (
          <div
            className={clsx(
              'p-2.5 rounded-lg text-xs',
              message.type === 'ok'
                ? 'bg-[#00c076]/10 border border-[#00c076]/30 text-[#00c076]'
                : 'bg-[#f0444b]/10 border border-[#f0444b]/30 text-[#f0444b]'
            )}
          >
            {message.text}
          </div>
        )}

        <button
          type="submit"
          disabled={submitting}
          className={clsx(
            'mt-auto w-full py-3 rounded-lg text-sm font-semibold transition-colors disabled:opacity-50 disabled:cursor-not-allowed',
            side === 'BUY'
              ? 'bg-[#00c076] hover:bg-[#00a865] text-black'
              : 'bg-[#f0444b] hover:bg-[#d93840] text-white'
          )}
        >
          {submitting
            ? 'Submitting...'
            : mode === 'conditional'
            ? `Set ${conditionLabel}`
            : `${side} ${marketId}`}
        </button>
      </form>
    </div>
  )
}
