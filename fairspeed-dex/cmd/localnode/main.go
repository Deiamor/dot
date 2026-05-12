// Command localnode manages a single-node or multi-node fairspeed-dex instance.
//
// Usage:
//
//	localnode <subcommand> [flags]
//
// Subcommands:
//
//	init          Initialise a new node home directory (keys + genesis + config.toml)
//	start         Start the node (embedded CometBFT + REST API)
//	show-node-id  Print the P2P node ID for a home directory
//	demo          Run a built-in Alice/Bob demonstration and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/byunghee1994/fairspeed-dex/internal/abci"
	"github.com/byunghee1994/fairspeed-dex/internal/abciserver"
	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/api"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/noderunner"
	"github.com/byunghee1994/fairspeed-dex/internal/sequencer"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
	"github.com/byunghee1994/fairspeed-dex/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: localnode <init|start|show-node-id|demo> [flags]\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:])
	case "start":
		runStart(os.Args[2:])
	case "show-node-id":
		runShowNodeID(os.Args[2:])
	case "sequencer":
		runSequencer(os.Args[2:])
	case "demo":
		runDemoCmd(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand %q. Use init, start, show-node-id, sequencer, or demo.\n", os.Args[1])
		os.Exit(1)
	}
}

// runInit initialises the node home directory (keys, genesis, config.toml).
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	homeDir := fs.String("home", "./nodedata", "node home directory")
	chainId := fs.String("chain-id", "fairspeed-1", "chain identifier")
	moniker := fs.String("moniker", "fairspeed-node", "node moniker")
	_ = fs.Parse(args)

	if err := noderunner.InitNode(*homeDir, *chainId, *moniker); err != nil {
		log.Fatalf("init: %v", err)
	}
}

// runStart starts the embedded CometBFT node with REST API.
// Both CometBFT and the REST API share the same *node.LocalNode instance.
// Pass -read-only to run as a non-voting read replica instead of a validator.
func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	homeDir := fs.String("home", "./nodedata", "node home directory")
	addr := fs.String("addr", ":8080", "HTTP API listen address")
	logEvents := fs.Bool("log-events", false, "log all chain events to stdout")
	peers := fs.String("peers", "", "comma-separated persistent_peers override (e.g. for Docker)")
	p2pPort := fs.Int("p2p-port", 0, "P2P listen port override (0 = use config.toml)")
	readOnly := fs.Bool("read-only", false, "run as non-voting read replica (serves API only)")
	_ = fs.Parse(args)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := noderunner.RunNode(ctx, noderunner.RunConfig{
		HomeDir:   *homeDir,
		LogEvents: *logEvents,
		Peers:     *peers,
		P2PPort:   *p2pPort,
		ReadOnly:  *readOnly,
	})
	if err != nil {
		log.Fatalf("start node: %v", err)
	}
	log.Printf("CometBFT node started, home=%s", *homeDir)

	// Use the same DEX node instance for the REST API.
	srv := api.NewServer(result.Node, *addr)
	go func() {
		log.Printf("API server listening on %s", *addr)
		if err := srv.Start(); err != nil {
			log.Printf("API server stopped: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received %s — shutting down…", sig)

	cancel()
	result.Service.Stop() //nolint:errcheck

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := srv.Stop(stopCtx); err != nil {
		log.Printf("API graceful shutdown: %v", err)
	}
}

// runShowNodeID prints the P2P node ID derived from node_key.json.
func runShowNodeID(args []string) {
	fs := flag.NewFlagSet("show-node-id", flag.ExitOnError)
	homeDir := fs.String("home", "./nodedata", "node home directory")
	_ = fs.Parse(args)

	id, err := noderunner.NodeID(*homeDir)
	if err != nil {
		log.Fatalf("show-node-id: %v", err)
	}
	fmt.Println(id)
}

// runSequencer starts the transaction sequencer service.
//
// The sequencer is a lightweight HTTP gateway that accepts WireTx JSON on
// POST /tx, batches incoming transactions, and broadcasts them to one or more
// validator CometBFT RPC endpoints via broadcast_tx_async.
// It does NOT participate in consensus.
//
// Example:
//
//	localnode sequencer \
//	  -addr :9090 \
//	  -validators http://node0:26666,http://node1:26676,http://node2:26686,http://node3:26696 \
//	  -interval 100ms
func runSequencer(args []string) {
	fs := flag.NewFlagSet("sequencer", flag.ExitOnError)
	addr := fs.String("addr", ":9090", "HTTP listen address for transaction ingress")
	validators := fs.String("validators", "", "comma-separated CometBFT RPC endpoints")
	interval := fs.Duration("interval", 100*time.Millisecond, "batch flush interval")
	maxBatch := fs.Int("max-batch", 500, "max transactions per flush")
	queueDepth := fs.Int("queue", 10000, "internal queue depth (429 when full)")
	_ = fs.Parse(args)

	var rpcs []string
	for _, r := range strings.Split(*validators, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			rpcs = append(rpcs, r)
		}
	}
	if len(rpcs) == 0 {
		log.Fatal("sequencer: -validators is required (e.g. http://localhost:26666)")
	}

	cfg := sequencer.Config{
		ListenAddr:    *addr,
		ValidatorRPCs: rpcs,
		BatchInterval: *interval,
		MaxBatchSize:  *maxBatch,
		QueueDepth:    *queueDepth,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-quit
		log.Printf("Received %s — sequencer shutting down…", sig)
		cancel()
	}()

	seq := sequencer.New(cfg)
	if err := seq.Start(ctx); err != nil {
		log.Fatalf("sequencer: %v", err)
	}
}

// runDemoCmd runs the Alice/Bob demo (standalone in-memory mode).
func runDemoCmd(args []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP API listen address")
	abciAddr := fs.String("abci-addr", "tcp://0.0.0.0:26658", "CometBFT ABCI listen address (empty = disabled)")
	abciTransport := fs.String("abci-transport", "socket", "ABCI transport: socket or grpc")
	chainId := fs.String("chain-id", "fairspeed-1", "chain identifier")
	dataDir := fs.String("data-dir", "./data", "directory for WAL and snapshot files")
	logEvents := fs.Bool("log-events", false, "log all chain events to stdout")
	bootstrap := fs.Bool("bootstrap", false, "run Alice/Bob demo blocks then exit")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	prefix := *dataDir + "/appstate"
	ps, err := store.Open(prefix, 100)
	if err != nil {
		log.Fatalf("open persistent store: %v", err)
	}
	defer ps.Close()
	log.Printf("Persistent store opened at %s (height=%d)", prefix, ps.Height())

	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	if *logEvents {
		n.Subscribe(state.EventAll, func(e state.Event) {
			log.Printf("[event] h=%d type=%s", e.BlockHeight, e.Type)
		})
	}

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

	if *abciAddr != "" {
		abciSrv, err := abciserver.StartServer(app, *abciAddr, abciserver.Transport(*abciTransport))
		if err != nil {
			log.Fatalf("start ABCI server: %v", err)
		}
		defer abciSrv.Stop() //nolint:errcheck
		log.Printf("ABCI server listening on %s (%s)", *abciAddr, *abciTransport)
	}

	srv := api.NewServer(n, *addr)
	go func() {
		log.Printf("API server listening on %s", *addr)
		if err := srv.Start(); err != nil {
			log.Printf("API server stopped: %v", err)
		}
	}()

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

func runDemo(n *node.LocalNode) {
	log.Println("Running Alice/Bob demo…")

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

	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 100_000).
		AddDeposit(bobId, "BTC", 100).
		Build())
	must(err, "block 3")

	sell := clob.NewLimitOrder(bobId, bobSess, "BTC-USDC", clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	_, err = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(sell).Build())
	must(err, "block 4")

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
