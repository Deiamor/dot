#!/usr/bin/env bash
# scripts/gen-docker-env.sh — generate deployments/.env with Docker peer addresses.
#
# Run this after `make testnet-init`. Reads node IDs from .testnet/ and writes
# per-node PEERS_nodeX variables that docker-compose passes to each container.
set -euo pipefail

BINARY="${BINARY:-./bin/localnode}"
BASE_DIR="${BASE_DIR:-.testnet}"
NUM_VALIDATORS=4
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
  for i in $(seq 0 $((NUM_VALIDATORS - 1))); do
    peers=""
    for j in $(seq 0 $((NUM_VALIDATORS - 1))); do
      [[ "$j" == "$i" ]] && continue
      peer="${ids[$j]}@node${j}:26656"
      peers="${peers:+$peers,}$peer"
    done
    echo "NODE${i}_PEERS=${peers}"
  done
} > "$OUT"

echo "==> Done. Contents:"
cat "$OUT"
