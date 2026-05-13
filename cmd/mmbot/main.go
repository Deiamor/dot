// Command mmbot runs the funding-aware market making strategy against Binance
// USDT-M Perpetual Futures.
//
// Usage:
//
//	mmbot [flags]
//
// Required environment variables for live trading:
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

	"github.com/deiamor/perp-strategy-engine/fsm/core"
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/binance"
	engine "github.com/deiamor/perp-strategy-engine/mm/engine"
	"github.com/deiamor/perp-strategy-engine/mm/model"
)

func main() {
	symbol      := flag.String("symbol", "BTCUSDT", "Binance futures symbol (e.g. BTCUSDT, ETHUSDT)")
	gamma        := flag.Float64("gamma", 0.1, "risk aversion coefficient")
	kappa        := flag.Float64("kappa", 1.5, "order arrival intensity (fills/sec)")
	sigma        := flag.Float64("sigma", 0.80, "initial realised volatility (annualised)")
	horizon      := flag.Float64("horizon", 1.0/365, "strategy horizon in years")
	alpha        := flag.Float64("alpha", 1.0, "funding sensitivity [0,1]")
	fundingEpoch := flag.Float64("funding-epoch", 1.0/365/3, "funding epoch in years (8h = 1/365/3)")
	maxInv       := flag.Float64("max-inventory", 0.1, "max inventory in base units")
	minSpread    := flag.Float64("min-spread", 0.0001, "min half-spread as fraction of mid")
	orderSize    := flag.Float64("order-size", 0.001, "base order size in base units")
	invThresh    := flag.Float64("inventory-threshold", 0.5, "skewing threshold (fraction of max-inventory)")
	pauseFunding := flag.Float64("pause-funding", 2.0, "pause when funding cost / spread revenue > this")
	pauseVol     := flag.Float64("pause-vol", 2.0, "pause when realised vol / model vol > this")
	volWindow    := flag.Int("vol-window", 100, "rolling window size for vol estimator (ticks)")
	interval     := flag.Duration("interval", 5*time.Second, "tick interval")
	testnet      := flag.Bool("testnet", false, "use Binance Futures testnet")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := []binance.Option{}
	if *testnet {
		opts = append(opts, binance.WithTestnet())
		log.Info("using Binance Futures testnet")
	}

	apiKey := os.Getenv("BINANCE_API_KEY")
	secretKey := os.Getenv("BINANCE_SECRET_KEY")
	if apiKey != "" && secretKey != "" {
		opts = append(opts, binance.WithKey(apiKey, secretKey))
		log.Info("order signing enabled")
	} else {
		log.Warn("BINANCE_API_KEY / BINANCE_SECRET_KEY not set — read-only mode (orders will fail)")
	}

	ex := binance.New(opts...)

	kill := make(chan struct{})

	cfg := engine.Config{
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

	strategy, err := engine.New(cfg, kill)
	if err != nil {
		log.Error("failed to create strategy", "err", err)
		os.Exit(1)
	}

	eng, err := core.NewEngine(strategy, ex, *symbol, *interval, nil, log)
	if err != nil {
		log.Error("failed to create engine", "err", err)
		os.Exit(1)
	}

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

	log.Info("mmbot started", "symbol", *symbol, "interval", *interval)
	if err := eng.Run(ctx); err != nil {
		log.Info("engine stopped", "reason", err)
	}
}
