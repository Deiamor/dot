#!/usr/bin/env bash
# scripts/gen-docker-env.sh — generate deployments/.env with Docker peer addresses.
#
# Run this after `make testnet-init`. Reads node IDs from .testnet/ and writes:
#   NODE{N}_PEERS          — full-mesh peers for each validator
#   REPLICA_PEERS          — connects replica to all 4 validators
#   SEQUENCER_VALIDATORS   — CometBFT RPC endpoints for the sequencer
#
# RPC port convention: P2P + 10 (e.g. P2P 26656 → RPC 26666).
set -euo pipefail

BINARY="${BINARY:-./bin/localnode}"
BASE_DIR="${BASE_DIR:-.testnet}"
NUM_VALIDATORS=4
P2P_PORT=26656   # uniform internal port (each container listens on same port)
RPC_OFFSET=10    # RPC = P2P + 10
OUT="deployments/.env"

[[ -x "$BINARY" ]] || { echo "ERROR: binary not found at $BINARY"; exit 1; }

echo "==> Reading node IDs"
ids=()
for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
  id=$("$BINARY" show-node-id -home "$BASE_DIR/node$i")
  ids+=("$id")
  echo "  node$i → $id"
done

echo "==> Writing $OUT"
mkdir -p "$(dirname "$OUT")"
{
  # Full-mesh peers for each validator (exclude self).
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    peers=""
    for j in $(seq 0 $((NUM_VALIDATORS - 1))); do
      [[ "$j" == "$i" ]] && continue
      peer="${ids[$j]}@node${j}:${P2P_PORT}"
      peers="${peers:+$peers,}$peer"
    done
    echo "NODE${i}_PEERS=${peers}"
  done

  # Replica peers: connect to all 4 validators.
  replica_peers=""
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    peer="${ids[$i]}@node${i}:${P2P_PORT}"
    replica_peers="${replica_peers:+$replica_peers,}$peer"
  done
  echo "REPLICA_PEERS=${replica_peers}"

  # Sequencer validator RPC endpoints (internal Docker hostnames).
  seq_validators=""
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    rpc="http://node${i}:$((P2P_PORT + RPC_OFFSET))"
    seq_validators="${seq_validators:+$seq_validators,}$rpc"
  done
  echo "SEQUENCER_VALIDATORS=${seq_validators}"
} > "$OUT"

echo "==> Done. Contents:"
cat "$OUT"
