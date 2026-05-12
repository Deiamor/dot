# Multi-stage build for fairspeed-dex localnode binary.
#
# Stage 1 — build
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Copy vendored dependencies and source — no network access needed.
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /bin/localnode ./cmd/localnode

# Stage 2 — minimal runtime image
# The binary is statically compiled (CGO_ENABLED=0) so no apk packages
# are needed; skipping apk avoids network access in air-gapped build envs.
FROM alpine:3.20

COPY --from=builder /bin/localnode /usr/local/bin/localnode

# Default home directory (can be overridden via -v mount).
ENV HOME_DIR=/data
VOLUME ["/data"]

# Ports: REST API, CometBFT P2P, CometBFT RPC
EXPOSE 8080 26656 26657

ENTRYPOINT ["localnode"]
CMD ["start", "-home", "/data", "-addr", ":8080", "-log-events", "true"]
