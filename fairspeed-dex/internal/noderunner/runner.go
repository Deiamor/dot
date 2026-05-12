// Package noderunner embeds CometBFT consensus into the fairspeed-dex binary.
//
// Instead of running CometBFT as a separate process and connecting via ABCI
// socket, RunNode starts consensus in-process using proxy.NewLocalClientCreator.
// This produces a single deployable binary with no external dependencies.
package noderunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cmtconfig "github.com/cometbft/cometbft/config"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtservice "github.com/cometbft/cometbft/libs/service"
	cmtnode "github.com/cometbft/cometbft/node"
	cmtp2p "github.com/cometbft/cometbft/p2p"
	cmtprivval "github.com/cometbft/cometbft/privval"
	cmtproxy "github.com/cometbft/cometbft/proxy"

	localabci "github.com/byunghee1994/fairspeed-dex/internal/abci"
	"github.com/byunghee1994/fairspeed-dex/internal/abciserver"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/genesis"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
)

// Result bundles the CometBFT service and the live DEX node so callers
// (e.g. the REST API) can share the exact same state instance.
type Result struct {
	Service cmtservice.Service
	Node    *node.LocalNode
}

// RunConfig holds all parameters for RunNode.
type RunConfig struct {
	HomeDir   string
	LogEvents bool
	// Peers overrides persistent_peers in config.toml (e.g. Docker container names).
	// Empty means use whatever is in config.toml.
	Peers string
	// P2PPort overrides the P2P listen port (0 = use config.toml value).
	// Useful when all Docker containers should listen on the same internal port.
	P2PPort int
}

// configFilePath returns the path to config.toml inside homeDir.
func configFilePath(homeDir string) string {
	return filepath.Join(homeDir, cmtconfig.DefaultConfigDir, cmtconfig.DefaultConfigFileName)
}

// InitNode initialises the home directory for a new node.
// It generates node and validator keys, and writes a single-validator genesis.json.
// For multi-validator chains, replace genesis.json after collecting all keys.
func InitNode(homeDir, chainId, moniker string) error {
	cfg := cmtconfig.DefaultConfig()
	cfg.SetRoot(homeDir)
	cfg.Moniker = moniker

	// Create required directories.
	for _, dir := range []string{cfg.DBDir(), filepath.Dir(cfg.GenesisFile())} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Generate node key (P2P identity).
	nodeKey, err := cmtp2p.LoadOrGenNodeKey(cfg.NodeKeyFile())
	if err != nil {
		return fmt.Errorf("gen node key: %w", err)
	}
	fmt.Printf("Node ID    : %s\n", nodeKey.ID())

	// Generate validator key.
	privKey := cmted25519.GenPrivKey()
	pv := cmtprivval.NewFilePV(privKey, cfg.PrivValidatorKeyFile(), cfg.PrivValidatorStateFile())
	pv.Save()
	pubKey, err := pv.GetPubKey()
	if err != nil {
		return fmt.Errorf("get pub key: %w", err)
	}
	fmt.Printf("Validator  : %s\n", pubKey.Address())

	// Write default genesis (single-validator, BTC+USDC registered).
	doc, err := genesis.BuildGenesisDoc(chainId, []genesis.ValidatorEntry{
		{PubKey: pubKey, Power: 100, Name: moniker},
	}, genesis.DefaultAppState())
	if err != nil {
		return fmt.Errorf("build genesis: %w", err)
	}
	if err := genesis.WriteGenesisDoc(doc, cfg); err != nil {
		return fmt.Errorf("write genesis: %w", err)
	}

	// Write config.toml.
	cmtconfig.WriteConfigFile(configFilePath(homeDir), cfg)
	fmt.Printf("Home dir   : %s\n", homeDir)
	fmt.Printf("Chain ID   : %s\n", chainId)
	return nil
}

// NodeID returns the P2P node ID for an initialised home directory.
// The ID is derived from node_key.json and is stable across restarts.
func NodeID(homeDir string) (string, error) {
	cfg := cmtconfig.DefaultConfig()
	cfg.SetRoot(homeDir)
	nodeKey, err := cmtp2p.LoadOrGenNodeKey(cfg.NodeKeyFile())
	if err != nil {
		return "", fmt.Errorf("load node key: %w", err)
	}
	return string(nodeKey.ID()), nil
}

// RunNode starts an embedded CometBFT node with the DEX application.
// It returns a Result containing both the CometBFT service and the live
// DEX node so callers can share the same state instance for the REST API.
func RunNode(ctx context.Context, rc RunConfig) (Result, error) {
	cmtCfg := cmtconfig.DefaultConfig()
	cmtCfg.SetRoot(rc.HomeDir)

	// Apply runtime overrides (useful for multi-node testnet and Docker).
	if rc.Peers != "" {
		cmtCfg.P2P.PersistentPeers = rc.Peers
		// Give extra time for P2P connections to establish before the first
		// consensus round. Without this, nodes that haven't yet received the
		// proposal from the proposer will prevote nil and the chain stalls.
		cmtCfg.Consensus.TimeoutPropose = 10 * time.Second
		cmtCfg.Consensus.TimeoutProposeDelta = 500 * time.Millisecond
		cmtCfg.Consensus.TimeoutPrevote = 5 * time.Second
		cmtCfg.Consensus.TimeoutPrecommit = 5 * time.Second
	}
	if rc.P2PPort > 0 {
		cmtCfg.P2P.ListenAddress = fmt.Sprintf("tcp://0.0.0.0:%d", rc.P2PPort)
		// Derive a unique RPC port so multiple nodes on the same host don't clash.
		// Convention: RPC = P2P + 10 (e.g. P2P 26656 → RPC 26666).
		cmtCfg.RPC.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", rc.P2PPort+10)
		// All nodes share 127.0.0.1 on a single host; without this CometBFT
		// rejects every inbound connection after the first as "duplicate IP".
		cmtCfg.P2P.AllowDuplicateIP = true
	}

	// Build the DEX node. Populate assets from genesis.json so the live
	// node matches exactly what was declared at chain start.
	dexNode := node.NewLocalNode()

	genesisProvider := cmtnode.DefaultGenesisDocProviderFunc(cmtCfg)
	if checksummed, err := genesisProvider(); err == nil {
		if appState, err := genesis.LoadAppState(checksummed.GenesisDoc); err == nil {
			for _, a := range appState.RegisteredAssets {
				dexNode.RegisterAsset(asset.Asset{
					AssetId:  a.AssetId,
					Symbol:   a.Symbol,
					Decimals: a.Decimals,
				})
			}
		}
	}
	// Always ensure BTC and USDC are registered (idempotent).
	dexNode.RegisterAsset(asset.BTC)
	dexNode.RegisterAsset(asset.USDC)

	dexApp := localabci.NewDEXApplication(dexNode)
	adapter := abciserver.NewCometBFTAdapter(dexApp)

	var logger cmtlog.Logger
	if rc.LogEvents {
		logger = cmtlog.NewTMLogger(cmtlog.NewSyncWriter(os.Stdout)).With("module", "fairspeed")
	} else {
		logger = cmtlog.NewNopLogger()
	}

	// Load keys generated by InitNode.
	nodeKey, err := cmtp2p.LoadOrGenNodeKey(cmtCfg.NodeKeyFile())
	if err != nil {
		return Result{}, fmt.Errorf("load node key: %w", err)
	}

	var pv *cmtprivval.FilePV
	if _, err := os.Stat(cmtCfg.PrivValidatorKeyFile()); err == nil {
		pv = cmtprivval.LoadFilePV(cmtCfg.PrivValidatorKeyFile(), cmtCfg.PrivValidatorStateFile())
	} else {
		privKey := cmted25519.GenPrivKey()
		pv = cmtprivval.NewFilePV(privKey, cmtCfg.PrivValidatorKeyFile(), cmtCfg.PrivValidatorStateFile())
		pv.Save()
	}

	// Create and start the embedded CometBFT node.
	cmtNode, err := cmtnode.NewNode(
		ctx,
		cmtCfg,
		pv,
		nodeKey,
		cmtproxy.NewLocalClientCreator(adapter),
		cmtnode.DefaultGenesisDocProviderFunc(cmtCfg),
		cmtconfig.DefaultDBProvider,
		cmtnode.DefaultMetricsProvider(cmtCfg.Instrumentation),
		logger,
	)
	if err != nil {
		return Result{}, fmt.Errorf("create cometbft node: %w", err)
	}
	if err := cmtNode.Start(); err != nil {
		return Result{}, fmt.Errorf("start cometbft node: %w", err)
	}
	return Result{Service: cmtNode, Node: dexNode}, nil
}
