# Multi-stage build for fairspeed-dex localnode binary.
#
# Stage 1 — build
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build the binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /bin/localnode ./cmd/localnode

# Stage 2 — minimal runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/localnode /usr/local/bin/localnode

# Default home directory (can be overridden via -v mount).
ENV HOME_DIR=/data
VOLUME ["/data"]

# Ports: REST API, CometBFT P2P, CometBFT RPC
EXPOSE 8080 26656 26657

ENTRYPOINT ["localnode"]
CMD ["start", "-home", "/data", "-addr", ":8080", "-log-events", "true"]
