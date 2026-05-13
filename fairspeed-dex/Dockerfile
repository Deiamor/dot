FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/fairspeed-dex ./cmd/localnode

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /app/fairspeed-dex .
COPY configs/ ./configs/
EXPOSE 8080
ENTRYPOINT ["/app/fairspeed-dex"]
