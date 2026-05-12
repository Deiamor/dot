BINARY     := ./bin/localnode
CHAIN_ID   := fairspeed-testnet-1
HOME_DIR   := ./nodedata
BASE_DIR   := .testnet
DOCKER_TAG := fairspeed-dex:latest

.PHONY: all build test lint clean \
        node-init node-start \
        testnet-init testnet-start testnet-stop testnet-clean \
        docker-build docker-up docker-down

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

## ── Docker ───────────────────────────────────────────────────────────────────

docker-build:
	docker build -t $(DOCKER_TAG) -f deployments/Dockerfile .

# Requires: make testnet-init  (creates .testnet/ node directories on host)
docker-up: testnet-init docker-build
	docker compose -f deployments/docker-compose.yml up -d
	@echo "Testnet running. API ports: 8080-8083"

docker-down:
	docker compose -f deployments/docker-compose.yml down
