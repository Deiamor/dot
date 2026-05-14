// Command mmbot runs the funding-aware market making strategy.
//
// Modes:
//
//	live    — real orders on Binance (requires BINANCE_API_KEY + BINANCE_SECRET_KEY)
//	paper   — live market data, virtual account, no real orders
//	backtest — replay downloaded kline data offline
//
// Usage:
//
//	mmbot --mode paper --dashboard --symbol BTCUSDT
//	mmbot --mode backtest --dataset ./data/BTCUSDT_5m_20240101_20240201.json
//
// Environment variables (live/paper modes):
//
//	BINANCE_API_KEY    Binance API key
//	BINANCE_SECRET_KEY Binance secret key
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/deiamor/perp-strategy-engine/dashboard"
	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/backtest"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/binance"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/paper"
	"github.com/deiamor/perp-strategy-engine/fsm/risk"
	mmengine "github.com/deiamor/perp-strategy-engine/mm/engine"
	"github.com/deiamor/perp-strategy-engine/mm/model"
	"github.com/deiamor/perp-strategy-engine/notify"
)

func main() {
	mode         := flag.String("mode", "paper", "trading mode: live | paper | backtest")
	symbol       := flag.String("symbol", "BTCUSDT", "Binance futures symbol")
	dataset      := flag.String("dataset", "", "path to kline JSON file (backtest mode)")
	dashEnabled  := flag.Bool("dashboard", true, "start the web dashboard")
	dashAddr     := flag.String("dashboard-addr", ":8080", "dashboard listen address")
	dataDir      := flag.String("data-dir", "./data", "directory for downloaded kline files")
	testnet      := flag.Bool("testnet", false, "use Binance Futures testnet")
	initBalance  := flag.Float64("initial-balance", 10_000, "initial USDT balance (paper/backtest)")
	slippageBps  := flag.Float64("slippage-bps", 1.0, "fill slippage in bps (paper mode)")

	telegramToken  := flag.String("telegram-token", "", "Telegram bot token (optional)")
	telegramChatID := flag.String("telegram-chat-id", "", "Telegram chat ID (optional)")
	webhookURL     := flag.String("webhook-url", "", "Webhook URL for alerts (optional)")
	notifyFills    := flag.Bool("notify-fills", true, "Alert on every fill")

	maxPosition  := flag.Float64("max-position", 0.0, "risk: max absolute position in base units (0=off)")
	maxDailyLoss := flag.Float64("max-daily-loss", 0.0, "risk: max daily loss in USDT (0=off)")
	maxDrawdown  := flag.Float64("max-drawdown-pct", 0.0, "risk: max drawdown % from equity peak (0=off)")
	minEquity    := flag.Float64("min-equity", 0.0, "risk: minimum equity in USDT, triggers kill (0=off)")

	gamma        := flag.Float64("gamma", 0.1, "risk aversion coefficient")
	kappa        := flag.Float64("kappa", 1.5, "order arrival intensity (fills/sec)")
	sigma        := flag.Float64("sigma", 0.80, "initial realised volatility (annualised)")
	horizon      := flag.Float64("horizon", 1.0/365, "strategy horizon in years")
	alpha        := flag.Float64("alpha", 1.0, "funding sensitivity [0,1]")
	fundingEpoch := flag.Float64("funding-epoch", 1.0/365/3, "funding epoch in years")
	maxInv       := flag.Float64("max-inventory", 0.1, "max inventory in base units")
	minSpread    := flag.Float64("min-spread", 0.0001, "min half-spread as fraction of mid")
	orderSize    := flag.Float64("order-size", 0.001, "base order size in base units")
	invThresh    := flag.Float64("inventory-threshold", 0.5, "skewing threshold")
	pauseFunding := flag.Float64("pause-funding", 2.0, "funding pause multiplier")
	pauseVol     := flag.Float64("pause-vol", 2.0, "volatility pause multiplier")
	volWindow    := flag.Int("vol-window", 100, "vol estimator rolling window (ticks)")
	interval     := flag.Duration("interval", 5*time.Second, "tick interval")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── Build notifier ─────────────────────────────────────────────────────────
	var notif notify.Notifier = notify.Noop{}
	if *telegramToken != "" {
		notif = notify.NewTelegram(*telegramToken, *telegramChatID)
	}
	if *webhookURL != "" {
		notif = notify.NewMulti(notif, notify.NewWebhook(*webhookURL))
	}

	// ── Build exchange ─────────────────────────────────────────────────────────
	var (
		ex        exchange.Exchange
		emitter   exchange.EventEmitter
		collector = dashboard.NewCollector()
	)

	switch *mode {
	case "live":
		opts := buildBinanceOpts(*testnet)
		ex = binance.New(opts...)
		log.Info("mode: LIVE (real orders)", "symbol", *symbol)

	case "paper":
		opts := buildBinanceOpts(*testnet)
		realEx := binance.New(opts...)
		pex := paper.New(paper.Config{
			InitialBalance: *initBalance,
			SlippageBps:    *slippageBps,
			TakerFeeBps:    4,
			MakerFeeBps:    2,
		}, realEx)
		go collector.Ingest(pex.Events())
		emitter = pex
		ex = pex
		log.Info("mode: PAPER", "symbol", *symbol, "balance", *initBalance)

	case "backtest":
		if *dataset == "" {
			log.Error("--dataset is required for backtest mode")
			os.Exit(1)
		}
		snaps, err := backtest.LoadSnapshots(*dataset, *symbol)
		if err != nil {
			log.Error("load dataset", "err", err)
			os.Exit(1)
		}
		bex := backtest.New(backtest.Config{
			InitialBalance: *initBalance,
			TakerFeeBps:    4,
			MakerFeeBps:    2,
		}, snaps)
		go collector.Ingest(bex.Events())
		emitter = bex
		ex = bex
		log.Info("mode: BACKTEST", "dataset", *dataset, "snapshots", len(snaps))

	default:
		log.Error("unknown mode", "mode", *mode)
		os.Exit(1)
	}

	// ── Wrap exchange with risk manager ───────────────────────────────────────
	kill := make(chan struct{})
	if *maxPosition > 0 || *maxDailyLoss > 0 || *maxDrawdown > 0 || *minEquity > 0 {
		ex = risk.New(risk.Config{
			MaxPositionQty:   *maxPosition,
			MaxDailyLossUSDT: *maxDailyLoss,
			MaxDrawdownPct:   *maxDrawdown,
			MinEquityUSDT:    *minEquity,
		}, ex, kill)
		log.Info("risk manager enabled",
			"maxPosition", *maxPosition,
			"maxDailyLoss", *maxDailyLoss,
			"maxDrawdownPct", *maxDrawdown,
			"minEquity", *minEquity,
		)
	}

	// ── Build strategy + engine ────────────────────────────────────────────────
	cfg := mmengine.Config{
		Model: model.Params{
			Gamma:        *gamma,
			Kappa:        *kappa,
			Sigma:        *sigma,
			Horizon:      *horizon,
			Alpha:        *alpha,
			FundingEpoch: *fundingEpoch,
			MaxInventory: *maxInv,
			MinSpread:    *minSpread,
		},
		OrderSize:             *orderSize,
		InventoryThreshold:    *invThresh,
		PauseFundingThreshold: *pauseFunding,
		PauseVolatilityMult:   *pauseVol,
		VolWindow:             *volWindow,
		TickInterval:          *interval,
	}

	strategy, err := mmengine.New(cfg, kill)
	if err != nil {
		log.Error("create strategy", "err", err)
		os.Exit(1)
	}

	tickInterval := *interval
	if *mode == "backtest" {
		tickInterval = time.Nanosecond // run as fast as possible
	}

	eng, err := core.NewEngine(strategy, ex, *symbol, tickInterval, nil, log)
	if err != nil {
		log.Error("create engine", "err", err)
		os.Exit(1)
	}

	// ── Context + signals ──────────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Warn("signal received, activating kill switch", "signal", sig)
		close(kill)
		time.Sleep(*interval + time.Second)
		cancel()
	}()

	// ── Start watcher (paper/live modes only) ──────────────────────────────────
	if *mode != "backtest" && emitter != nil {
		w := notify.NewWatcher(notif, *symbol)
		if !*notifyFills {
			// Set a large threshold to suppress fill alerts.
			w.FillAlertMinPnL = 1e18
		}
		go w.Watch(ctx, emitter.Events())
	}

	// ── Dashboard ──────────────────────────────────────────────────────────────
	if *dashEnabled {
		srv := dashboard.NewServer(collector, *dataDir, log)
		go func() {
			if err := srv.Run(ctx, *dashAddr); err != nil {
				log.Error("dashboard error", "err", err)
			}
		}()
		log.Info("dashboard started", "url", "http://localhost"+*dashAddr)
	}

	// ── Run ────────────────────────────────────────────────────────────────────
	log.Info("mmbot started", "symbol", *symbol, "mode", *mode)
	if err := eng.Run(ctx); err != nil {
		log.Info("engine stopped", "reason", err)
	}

	if *mode == "paper" {
		if pex, ok := ex.(*paper.Exchange); ok {
			log.Info("paper trading summary", "stats", pex.Stats())
		}
	}
	if *mode == "backtest" {
		if bex, ok := ex.(*backtest.Exchange); ok {
			s := bex.Stats()
			log.Info("backtest complete",
				"netPnl", s.NetPnL,
				"returnPct", s.ReturnPct(),
				"fills", s.NumFills,
				"winRate", s.WinRate,
			)
		}
	}
}

func buildBinanceOpts(testnet bool) []binance.Option {
	var opts []binance.Option
	if testnet {
		opts = append(opts, binance.WithTestnet())
	}
	if key := os.Getenv("BINANCE_API_KEY"); key != "" {
		secret := os.Getenv("BINANCE_SECRET_KEY")
		opts = append(opts, binance.WithKey(key, secret))
	}
	return opts
}
