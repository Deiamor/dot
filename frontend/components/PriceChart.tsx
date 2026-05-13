'use client'

import { useEffect, useRef, useCallback } from 'react'
import { createChart, LineSeries } from 'lightweight-charts'

interface PricePoint {
  time: number   // Unix seconds
  price: number
}

interface Props {
  marketId: string
}

const BASE = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080'

export default function PriceChart({ marketId }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const seriesRef = useRef<ReturnType<typeof createChart> extends infer C
    ? C extends { addSeries: (...args: any[]) => infer S } ? S : never
    : never>(null)
  const chartRef = useRef<ReturnType<typeof createChart> | null>(null)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const lastPriceRef = useRef<number>(0)
  const nextTimeRef = useRef<number>(Math.floor(Date.now() / 1000))

  const fetchAndAppend = useCallback(async () => {
    if (!seriesRef.current) return
    try {
      const res = await fetch(`${BASE}/markprice/${encodeURIComponent(marketId)}`, { cache: 'no-store' })
      if (!res.ok) return
      const data = await res.json()
      const price: number = data.mark_price ?? 0
      if (price === 0) return

      const now = Math.floor(Date.now() / 1000)
      // Ensure strictly increasing time
      if (now <= nextTimeRef.current) {
        nextTimeRef.current++
      } else {
        nextTimeRef.current = now
      }

      if (price !== lastPriceRef.current) {
        seriesRef.current.update({ time: nextTimeRef.current as any, value: price })
        lastPriceRef.current = price
      }
    } catch { /* ignore network errors */ }
  }, [marketId])

  useEffect(() => {
    if (!containerRef.current) return

    const chart = createChart(containerRef.current, {
      layout: {
        background: { color: '#0d0d0d' },
        textColor: '#808080',
      },
      grid: {
        vertLines: { color: '#1a1a1a' },
        horzLines: { color: '#1a1a1a' },
      },
      crosshair: {
        vertLine: { color: '#444' },
        horzLine: { color: '#444' },
      },
      rightPriceScale: {
        borderColor: '#2a2a2a',
      },
      timeScale: {
        borderColor: '#2a2a2a',
        timeVisible: true,
        secondsVisible: false,
      },
      width: containerRef.current.clientWidth,
      height: containerRef.current.clientHeight,
    })
    chartRef.current = chart

    const series = chart.addSeries(LineSeries, {
      color: '#00c076',
      lineWidth: 2,
      priceLineVisible: true,
      lastValueVisible: true,
    })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    seriesRef.current = series as any

    // Load historical price data
    fetch(`${BASE}/markprice/${encodeURIComponent(marketId)}/history`, { cache: 'no-store' })
      .then((r) => r.json())
      .then((hist: PricePoint[]) => {
        if (!Array.isArray(hist) || hist.length === 0) return
        const data = hist.map((p) => ({ time: p.time as any, value: p.price }))
        series.setData(data)
        nextTimeRef.current = hist[hist.length - 1].time + 1
        lastPriceRef.current = hist[hist.length - 1].price
        chart.timeScale().fitContent()
      })
      .catch(() => {})

    // Poll current mark price every second
    timerRef.current = setInterval(fetchAndAppend, 1000)

    const observer = new ResizeObserver(() => {
      if (containerRef.current) {
        chart.resize(containerRef.current.clientWidth, containerRef.current.clientHeight)
      }
    })
    observer.observe(containerRef.current)

    return () => {
      observer.disconnect()
      if (timerRef.current) clearInterval(timerRef.current)
      // Null out seriesRef first so any in-flight async fetch won't call update()
      // on a destroyed series.
      seriesRef.current = null
      chart.remove()
      chartRef.current = null
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [marketId])

  return <div ref={containerRef} className="w-full h-full" />
}
