BINARY     := ./bin/localnode
CHAIN_ID   := fairspeed-testnet-1
HOME_DIR   := ./nodedata
BASE_DIR   := .testnet
DOCKER_TAG := fairspeed-dex:latest

.PHONY: all build test lint clean \
        node-init node-start \
        testnet-init testnet-start testnet-stop testnet-clean \
        docker-build docker-up docker-down docker-logs

all: build

## ── Local build ──────────────────────────────────────────────────────────────

build:
	@mkdir -p bin
	go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/localnode
	@echo "Built $(BINARY)"

test:
	go test -count=1 -race ./...

lint:
	@command -v golangci-lint &>/dev/null && golangci-lint run ./... || go vet ./...

clean:
	rm -rf bin $(BASE_DIR) $(HOME_DIR)

## ── Single-node convenience ──────────────────────────────────────────────────

node-init: build
	$(BINARY) init \
	  -home     $(HOME_DIR) \
	  -chain-id $(CHAIN_ID) \
	  -moniker  local-validator

node-start: build
	$(BINARY) start \
	  -home       $(HOME_DIR) \
	  -addr       :8080 \
	  -log-events true

## ── Local 4-validator testnet ────────────────────────────────────────────────

testnet-init: build
	BINARY=$(BINARY) CHAIN_ID=$(CHAIN_ID) BASE_DIR=$(BASE_DIR) \
	  ./scripts/testnet.sh init

testnet-start:
	BINARY=$(BINARY) BASE_DIR=$(BASE_DIR) \
	  ./scripts/testnet.sh start

testnet-stop:
	BASE_DIR=$(BASE_DIR) ./scripts/testnet.sh stop

testnet-clean:
	BASE_DIR=$(BASE_DIR) ./scripts/testnet.sh clean

## ── Docker (6 containers: 4 validators + replica + sequencer) ────────────────

docker-build:
	docker build -t $(DOCKER_TAG) -f deployments/Dockerfile .

# Init validator dirs, init replica dir (not in genesis), generate .env.
docker-testnet-init: build
	BINARY=$(BINARY) CHAIN_ID=$(CHAIN_ID) BASE_DIR=$(BASE_DIR) \
	  ./scripts/testnet.sh init
	@echo "==> Initialising read replica"
	$(BINARY) init \
	  -home     $(BASE_DIR)/replica \
	  -chain-id $(CHAIN_ID) \
	  -moniker  replica
	@echo "  Copying shared genesis to replica"
	cp $(BASE_DIR)/genesis.json $(BASE_DIR)/replica/config/genesis.json
	BINARY=$(BINARY) BASE_DIR=$(BASE_DIR) \
	  ./scripts/gen-docker-env.sh

# Build image and start all 6 containers. Requires: make docker-testnet-init
docker-up: docker-build
	docker compose -f deployments/docker-compose.yml up -d
	@echo "Testnet running."
	@echo "  REST API (replica) : http://localhost:8084"
	@echo "  Sequencer ingress  : http://localhost:9090"
	@echo "  P2P node0 (ext)    : localhost:26656"
	@echo "Logs: make docker-logs"

docker-down:
	docker compose -f deployments/docker-compose.yml down

docker-logs:
	docker compose -f deployments/docker-compose.yml logs -f
