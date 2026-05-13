// Command mmbot runs the funding-aware market making strategy against HyperLiquid.
//
// Usage:
//
//	mmbot [flags]
//
// Required environment variables when running live:
//
//	HYPERLIQUID_KEY     hex-encoded secp256k1 private key (64 chars)
//	HYPERLIQUID_ADDRESS 0x wallet address
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
	"github.com/deiamor/perp-strategy-engine/fsm/exchange/hyperliquid"
	engine "github.com/deiamor/perp-strategy-engine/mm/engine"
	"github.com/deiamor/perp-strategy-engine/mm/model"
)

func main() {
	symbol      := flag.String("symbol", "BTC", "trading pair symbol")
	gamma        := flag.Float64("gamma", 0.1, "risk aversion coefficient")
	kappa        := flag.Float64("kappa", 1.5, "order arrival intensity (fills/sec)")
	sigma        := flag.Float64("sigma", 0.80, "initial realised volatility (annualised)")
	horizon      := flag.Float64("horizon", 1.0/365, "strategy horizon in years")
	alpha        := flag.Float64("alpha", 1.0, "funding sensitivity [0,1]")
	fundingEpoch := flag.Float64("funding-epoch", 1.0/365/3, "funding epoch duration in years")
	maxInv       := flag.Float64("max-inventory", 5.0, "max inventory in base units")
	minSpread    := flag.Float64("min-spread", 0.0001, "min half-spread as fraction of mid")
	orderSize    := flag.Float64("order-size", 0.01, "base order size in base units")
	invThresh    := flag.Float64("inventory-threshold", 0.5, "inventory skewing threshold (fraction of max)")
	pauseFunding := flag.Float64("pause-funding", 2.0, "pause when funding cost / spread revenue > this")
	pauseVol     := flag.Float64("pause-vol", 2.0, "pause when realised vol / model vol > this multiple")
	volWindow    := flag.Int("vol-window", 100, "rolling window size for vol estimator (ticks)")
	interval     := flag.Duration("interval", 5*time.Second, "tick interval")
	testnet      := flag.Bool("testnet", false, "use HyperLiquid testnet")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := []hyperliquid.Option{}
	if *testnet {
		opts = append(opts, hyperliquid.WithTestnet())
		log.Info("using HyperLiquid testnet")
	}
	if key := os.Getenv("HYPERLIQUID_KEY"); key != "" {
		addr := os.Getenv("HYPERLIQUID_ADDRESS")
		opts = append(opts, hyperliquid.WithKey(key, addr))
		log.Info("order signing enabled", "address", addr)
	} else {
		log.Warn("HYPERLIQUID_KEY not set — read-only mode (orders will fail)")
	}

	ex := hyperliquid.New(opts...)

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

	// Graceful shutdown on SIGINT/SIGTERM.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Warn("signal received, activating kill switch", "signal", sig)
		close(kill)
		// Give the engine one more tick to flatten, then cancel context.
		time.Sleep(*interval + time.Second)
		cancel()
	}()

	log.Info("mmbot started", "symbol", *symbol, "interval", *interval)
	if err := eng.Run(ctx); err != nil {
		log.Info("engine stopped", "reason", err)
	}
}
