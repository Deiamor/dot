import type { Metadata } from 'next'
import { Geist, Geist_Mono } from 'next/font/google'
import Link from 'next/link'
import { Zap } from 'lucide-react'
import WalletConnect from '@/components/WalletConnect'
import './globals.css'

const geistSans = Geist({
  variable: '--font-geist-sans',
  subsets: ['latin'],
})

const geistMono = Geist_Mono({
  variable: '--font-geist-mono',
  subsets: ['latin'],
})

export const metadata: Metadata = {
  title: 'FairSpeed DEX',
  description: 'Fast on-chain DEX with SPOT and PERP markets',
}

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode
}>) {
  return (
    <html lang="en" className={`${geistSans.variable} ${geistMono.variable} h-full`}>
      <body className="min-h-full flex flex-col bg-[#0d0d0d] text-[#e8e8e8] antialiased">
        <nav className="flex items-center justify-between px-6 h-14 border-b border-[#2a2a2a] bg-[#0d0d0d] sticky top-0 z-40">
          <div className="flex items-center gap-8">
            <Link href="/" className="flex items-center gap-1.5 font-bold text-lg text-[#e8e8e8] hover:text-white transition-colors">
              <Zap size={18} className="text-[#00c076]" fill="#00c076" />
              FairSpeed
            </Link>
            <div className="flex items-center gap-1">
              <Link
                href="/trade/BTC-USDC-PERP"
                className="px-3 py-1.5 text-sm text-[#808080] hover:text-[#e8e8e8] rounded-md hover:bg-[#161616] transition-colors"
              >
                Trade
              </Link>
              <Link
                href="/markets"
                className="px-3 py-1.5 text-sm text-[#808080] hover:text-[#e8e8e8] rounded-md hover:bg-[#161616] transition-colors"
              >
                Markets
              </Link>
              <Link
                href="/portfolio"
                className="px-3 py-1.5 text-sm text-[#808080] hover:text-[#e8e8e8] rounded-md hover:bg-[#161616] transition-colors"
              >
                Portfolio
              </Link>
              <Link
                href="/points"
                className="px-3 py-1.5 text-sm text-[#808080] hover:text-[#e8e8e8] rounded-md hover:bg-[#161616] transition-colors"
              >
                Points
              </Link>
            </div>
          </div>
          <WalletConnect />
        </nav>
        <main className="flex-1 flex flex-col">{children}</main>
      </body>
    </html>
  )
}
