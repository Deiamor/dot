#!/usr/bin/env bash
# scripts/testnet.sh — initialise and start a 4-validator local testnet.
#
# Usage:
#   ./scripts/testnet.sh init    # create validator home dirs + patch genesis
#   ./scripts/testnet.sh start   # start all 4 nodes in the background
#   ./scripts/testnet.sh stop    # kill all node processes
#   ./scripts/testnet.sh clean   # remove generated directories
set -euo pipefail

BINARY="${BINARY:-./bin/localnode}"
CHAIN_ID="${CHAIN_ID:-fairspeed-testnet-1}"
BASE_DIR="${BASE_DIR:-.testnet}"
NUM_VALIDATORS=4

# Port base offsets per validator (p2p=26656, rpc=26657, api=8080, abci=26658)
p2p_base=26656
rpc_base=26657
api_base=8080
abci_base=26658

die() { echo "ERROR: $*" >&2; exit 1; }

require_binary() {
  [[ -x "$BINARY" ]] || die "Binary not found at $BINARY. Run: make build"
}

cmd_init() {
  require_binary
  echo "==> Initialising $NUM_VALIDATORS validators (chain=$CHAIN_ID)"

  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    home="$BASE_DIR/node$i"
    moniker="validator-$i"
    "$BINARY" init \
      -home    "$home" \
      -chain-id "$CHAIN_ID" \
      -moniker  "$moniker"
    echo "  node$i initialised at $home"
  done

  # Collect all validator public keys from each node's genesis.json,
  # then build a shared genesis.json with all validators and copy it
  # to every node.
  echo "==> Merging validator sets into shared genesis"

  # Use node0's genesis as the base; jq merges the validator arrays.
  # Requires jq to be installed.
  if command -v jq &>/dev/null; then
    base_genesis="$BASE_DIR/node0/config/genesis.json"
    shared_genesis="$BASE_DIR/genesis.json"

    cp "$base_genesis" "$shared_genesis"
    for i in $(seq 1 $((NUM_VALIDATORS - 1))); do
      src="$BASE_DIR/node$i/config/genesis.json"
      # Merge validators array from node_i into shared genesis.
      jq --slurpfile src "$src" \
        '.validators += $src[0].validators' \
        "$shared_genesis" > "$shared_genesis.tmp" && mv "$shared_genesis.tmp" "$shared_genesis"
    done

    # Copy shared genesis to all nodes.
    for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
      cp "$shared_genesis" "$BASE_DIR/node$i/config/genesis.json"
      echo "  node$i genesis updated"
    done
  else
    echo "  WARNING: jq not found — each node keeps its own single-validator genesis."
    echo "           Install jq and re-run 'init' for a proper multi-validator setup."
  fi

  # Build persistent_peers string using the built-in show-node-id subcommand.
  echo "==> Collecting node IDs"
  peers=""
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    home="$BASE_DIR/node$i"
    port=$((p2p_base + i))
    node_id=$("$BINARY" show-node-id -home "$home" 2>/dev/null || true)
    if [[ -n "$node_id" ]]; then
      peer="${node_id}@127.0.0.1:${port}"
      peers="${peers:+$peers,}$peer"
      echo "  node$i id=$node_id"
    else
      echo "  WARNING: could not determine node ID for node$i"
    fi
  done

  # Patch config.toml for each node.
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    home="$BASE_DIR/node$i"
    cfg="$home/config/config.toml"
    p2p_port=$((p2p_base + i))
    rpc_port=$((rpc_base + i * 10))   # spread: 26657, 26667, 26677, 26687
    abci_port=$((abci_base + i * 10))

    # Use sed to patch key fields (portable, no jq needed for TOML).
    sed -i.bak \
      -e "s|^laddr = \"tcp://0.0.0.0:26656\"|laddr = \"tcp://0.0.0.0:${p2p_port}\"|" \
      -e "s|^laddr = \"tcp://127.0.0.1:26657\"|laddr = \"tcp://127.0.0.1:${rpc_port}\"|" \
      -e "s|^proxy_app = .*|proxy_app = \"tcp://127.0.0.1:${abci_port}\"|" \
      -e "s|^persistent_peers = .*|persistent_peers = \"${peers}\"|" \
      "$cfg"
    rm -f "$cfg.bak"
    echo "  node$i config patched (p2p=$p2p_port rpc=$rpc_port abci=$abci_port)"
  done

  echo "==> Init complete. Run: ./scripts/testnet.sh start"
}

cmd_start() {
  require_binary
  echo "==> Starting $NUM_VALIDATORS nodes"
  mkdir -p "$BASE_DIR/logs"

  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    home="$BASE_DIR/node$i"
    api_port=$((api_base + i))
    log="$BASE_DIR/logs/node$i.log"

    "$BINARY" start \
      -home       "$home" \
      -addr       ":${api_port}" \
      -log-events true \
      > "$log" 2>&1 &
    pid=$!
    echo "$pid" > "$BASE_DIR/node$i.pid"
    echo "  node$i started (pid=$pid api=:${api_port} log=$log)"
  done

  echo "==> All nodes started. Logs in $BASE_DIR/logs/"
  echo "    Stop with: ./scripts/testnet.sh stop"
}

cmd_stop() {
  echo "==> Stopping nodes"
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    pidfile="$BASE_DIR/node$i.pid"
    if [[ -f "$pidfile" ]]; then
      pid=$(cat "$pidfile")
      if kill -0 "$pid" 2>/dev/null; then
        kill "$pid"
        echo "  node$i (pid=$pid) stopped"
      else
        echo "  node$i (pid=$pid) already stopped"
      fi
      rm -f "$pidfile"
    fi
  done
}

cmd_clean() {
  cmd_stop 2>/dev/null || true
  echo "==> Removing $BASE_DIR"
  rm -rf "$BASE_DIR"
}

case "${1:-}" in
  init)  cmd_init  ;;
  start) cmd_start ;;
  stop)  cmd_stop  ;;
  clean) cmd_clean ;;
  *)
    echo "Usage: $0 {init|start|stop|clean}"
    echo "  BINARY=$BINARY   CHAIN_ID=$CHAIN_ID   BASE_DIR=$BASE_DIR"
    exit 1
    ;;
esac
