// Command localnode starts a single-node fairspeed-dex instance.
//
// Usage:
//
//	localnode [flags]
//
// Flags:
//
//	-addr       HTTP API listen address (default ":8080")
//	-chain-id   Chain identifier for ABCI InitChain (default "fairspeed-1")
//	-data-dir   Directory for WAL + snapshot files (default "./data")
//	-log-events Whether to log every chain event to stdout (default false)
//	-bootstrap  Run a built-in Alice/Bob demo then exit (default false)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/abci"
	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/api"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP API listen address")
	chainId := flag.String("chain-id", "fairspeed-1", "chain identifier")
	dataDir := flag.String("data-dir", "./data", "directory for WAL and snapshot files")
	logEvents := flag.Bool("log-events", false, "log all chain events to stdout")
	bootstrap := flag.Bool("bootstrap", false, "run Alice/Bob demo blocks then exit")
	flag.Parse()

	// Ensure data directory exists.
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Open persistent store (WAL + snapshot).
	prefix := *dataDir + "/appstate"
	ps, err := store.Open(prefix, 100)
	if err != nil {
		log.Fatalf("open persistent store: %v", err)
	}
	defer ps.Close()
	log.Printf("Persistent store opened at %s (height=%d)", prefix, ps.Height())

	// Build the local node.
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	if *logEvents {
		n.Subscribe(state.EventAll, func(e state.Event) {
			log.Printf("[event] h=%d type=%s", e.BlockHeight, e.Type)
		})
	}

	// Wire ABCI application.
	app := abci.NewDEXApplication(n)
	initResp := app.InitChain(abci.RequestInitChain{
		ChainId:       *chainId,
		InitialHeight: 1,
	})
	log.Printf("Chain %q initialised  app_hash=%x", *chainId, initResp.AppHash)

	if *bootstrap {
		runDemo(n)
		return
	}

	// Start the REST + SSE API server.
	srv := api.NewServer(n, *addr)
	go func() {
		log.Printf("API server listening on %s", *addr)
		if err := srv.Start(); err != nil {
			log.Printf("API server stopped: %v", err)
		}
	}()

	// Block until SIGINT / SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received %s — shutting down…", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		log.Printf("Graceful shutdown error: %v", err)
	}
	log.Printf("Node stopped at height %d", n.CurrentHeight())
}

// runDemo executes a hardcoded Alice/Bob scenario and prints the results.
// Invoked when -bootstrap flag is set.
func runDemo(n *node.LocalNode) {
	log.Println("Running Alice/Bob demo…")

	// Block 1: accounts.
	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice@example.com", "alice-root", "alice-withdraw").
		AddCreateAccount("bob@example.com", "bob-root", "bob-withdraw").
		Build())
	must(err, "block 1")

	var aliceId, bobId string
	for _, acc := range n.AppState.Accounts {
		switch acc.OwnerAddress {
		case "alice@example.com":
			aliceId = acc.AccountId
		case "bob@example.com":
			bobId = acc.AccountId
		}
	}

	// Block 2: sessions.
	opts := account.SessionOptions{AllowedMarkets: []string{"BTC-USDC"}, MaxOrderAmount: 1000}
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddCreateSession(aliceId, opts).
		AddCreateSession(bobId, opts).
		Build())
	must(err, "block 2")

	var aliceSess, bobSess string
	for _, sess := range n.AppState.Sessions {
		switch sess.AccountId {
		case aliceId:
			aliceSess = sess.SessionId
		case bobId:
			bobSess = sess.SessionId
		}
	}

	// Block 3: deposits.
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 100_000).
		AddDeposit(bobId, "BTC", 100).
		Build())
	must(err, "block 3")

	// Block 4: Bob SELL.
	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	must(err, "block 4")

	// Block 5: Alice BUY → trade.
	buy := clob.NewLimitOrder(aliceId, aliceSess, "BTC-USDC", clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	result, err := n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(buy).Build())
	must(err, "block 5")

	fmt.Println("=== Demo Result ===")
	fmt.Printf("Trades in block 5: %d\n", result.TradeCount)
	for _, t := range result.Trades {
		fmt.Printf("  price=%-6d  qty=%-3d  makerFee=%-3d  takerFee=%d\n",
			t.Price, t.Quantity, t.MakerFeeAmount, t.TakerFeeAmount)
	}
	fmt.Printf("Alice BTC  : %+v\n", n.GetBalance(aliceId, "BTC"))
	fmt.Printf("Alice USDC : %+v\n", n.GetBalance(aliceId, "USDC"))
	fmt.Printf("Bob   BTC  : %+v\n", n.GetBalance(bobId, "BTC"))
	fmt.Printf("Bob   USDC : %+v\n", n.GetBalance(bobId, "USDC"))
	fmt.Printf("Treasury   : %+v\n", n.GetTreasury("USDC"))
}

func must(err error, label string) {
	if err != nil {
		log.Fatalf("%s: %v", label, err)
	}
}
