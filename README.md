# perp-strategy-engine

A composable FSM execution framework and funding-aware market making model for perpetual DEXes, written in Go.

Two independent libraries in one monorepo:

| Package | What it is |
|---------|-----------|
| `fsm/` | Declarative state-machine execution layer for trading strategies |
| `mm/` | Production implementation of the funding-aware Avellaneda-Stoikov model ([arXiv:2605.06405](https://arxiv.org/abs/2605.06405)) |

**Supported venues**: Binance USDT-M Perpetual Futures (full — market data + signed execution), HyperLiquid (market data only)

**Execution modes**: Live trading, Paper trading (virtual account), Backtesting (offline kline replay)

**Dashboard**: real-time web UI — equity curve, P&L, fills, FSM state, kline downloader, backtest runner

---

## Why this exists

Most open-source trading frameworks are either:
- **Signal-only** (TradingView Pine Script, backtesting libraries) — no execution layer
- **CEX wrappers** (ccxt) — no strategy abstraction, no FSM lifecycle
- **Monolithic bots** — hardcoded strategies, not composable

This repo provides the **execution substrate**: a typed FSM that any strategy can plug into, plus a mathematically grounded MM model that accounts for perpetual funding rates — a real P&L driver that vanilla A-S ignores.

No existing open-source project implements the funding-aware A-S model from the 2025 arXiv paper in a production-ready form.

---

## FSM execution layer (`fsm/`)

### Core concepts

```
Strategy → Engine → tick loop
              │
              ├── buildContext (market snapshot + position + open orders)
              ├── State.OnTick
              └── evaluate Transitions → OnExit → Action → OnEnter
```

A **Strategy** declares:
- **States** with optional `OnEnter`, `OnTick`, `OnExit` hooks
- **Transitions** with a `Guard` predicate and optional `Action`
- Wildcard source (`"*"`) matches any state — useful for kill switches

The **Engine** drives the tick loop, refreshes market context before each tick, and manages state lifecycle automatically.

### Defining a strategy

```go
type MyStrategy struct{}

func (s *MyStrategy) Name() string    { return "my-strategy" }
func (s *MyStrategy) Initial() string { return "IDLE" }

func (s *MyStrategy) States() []core.State {
    return []core.State{
        {Name: "IDLE"},
        {Name: "ACTIVE", OnTick: s.onTick},
        {Name: "DONE"},
    }
}

func (s *MyStrategy) Transitions() []core.Transition {
    return []core.Transition{
        // Kill switch: any state → DONE
        {From: "*", To: "DONE", Guard: s.killFired},
        // IDLE → ACTIVE when market is live
        {From: "IDLE", To: "ACTIVE", Guard: func(ctx *core.Context) bool {
            return ctx.Market.Mid > 0
        }},
    }
}
```

### Running the engine

```go
ex := hyperliquid.New(hyperliquid.WithKey(privKey, address))

eng, err := core.NewEngine(
    &MyStrategy{},
    ex,
    "BTC",
    5*time.Second,
    nil,
    slog.Default(),
)
if err != nil {
    log.Fatal(err)
}

if err := eng.Run(context.Background()); err != nil {
    log.Println("engine stopped:", err)
}
```

### Context

Every hook and guard receives `*core.Context`:

```go
type Context struct {
    Symbol     string
    Market     exchange.MarketSnapshot  // mid, bid, ask, funding rate
    Position   exchange.Position        // signed Qty: +long / -short
    OpenOrders []exchange.Order
    Tick       int64
    Elapsed    time.Duration
    Exchange   exchange.Exchange        // place/cancel orders
    Log        *slog.Logger
    GoCtx      context.Context         // pass to Exchange methods
}
```

### Built-in primitive: TWAP

```go
tw, _ := primitives.NewTWAP(primitives.TWAPConfig{
    Symbol:        "ETH",
    Side:          exchange.Buy,
    TotalQty:      10.0,
    Slices:        20,
    Duration:      10 * time.Minute,
    LimitPriceBps: 5, // 0 = market orders
})

eng, _ := core.NewEngine(tw, ex, "ETH", 30*time.Second, nil, log)
eng.Run(ctx)
```

### Exchange interface

Implement `exchange.Exchange` for any venue. HyperLiquid read-only is included:

```go
type Exchange interface {
    MarketSnapshot(ctx, symbol) (MarketSnapshot, error)
    OrderBook(ctx, symbol, depth) (OrderBook, error)
    Position(ctx, symbol) (Position, error)
    Balances(ctx) ([]Balance, error)
    OpenOrders(ctx, symbol) ([]Order, error)
    PlaceOrder(ctx, req) (Order, error)
    CancelOrder(ctx, symbol, orderID) error
    CancelAllOrders(ctx, symbol) error
}
```

---

## Funding-aware market making model (`mm/`)

### Theory

Classical Avellaneda-Stoikov (2008) optimal market making model extended with perpetual funding payments as per [arXiv:2605.06405](https://arxiv.org/abs/2605.06405).

**Reservation price** (inventory + funding adjusted):

```
r = s - q·γ·σ²·(T−t) − α·f·q·Δt
```

**Optimal half-spread**:

```
δ = γ·σ²·(T−t)/2 + (1/γ)·ln(1 + γ/κ)
```

**Optimal quotes**: `bid = r − δ`, `ask = r + δ`

where:
- `s` = mid price, `q` = signed inventory, `γ` = risk aversion
- `σ` = realised volatility (annualised), `T−t` = remaining horizon
- `α` = funding sensitivity [0, 1], `f` = current funding rate, `Δt` = epoch duration
- `κ` = order arrival intensity

### Usage

```go
m, err := model.New(model.Params{
    Gamma:        0.1,           // risk aversion
    Kappa:        1.5,           // fills/sec
    Sigma:        0.80,          // 80% annualised vol
    Horizon:      1.0 / 365,     // 1-day horizon
    Alpha:        1.0,           // full funding adjustment
    FundingEpoch: 1.0 / 365 / 3, // 8-hour epoch
    MaxInventory: 10.0,
    MinSpread:    0.0001,        // 1 bps floor
})

bid, ask := m.Quotes(mid, inventory, fundingRate, elapsed)
```

### Funding-aware MM strategy

The `mm/engine.MakerStrategy` is a complete FSM strategy implementing this model:

```
IDLE → QUOTING → SKEWING → PAUSED
                     ↑________↓
any → FLAT (kill switch)
```

State transitions:
- **QUOTING → SKEWING** when `|inventory| > threshold × MaxInventory`
- **QUOTING → PAUSED** when `funding_cost / spread_revenue > threshold` or volatility spike
- **SKEWING → QUOTING** when inventory returns to range
- **any → FLAT** when kill channel is closed

### Volatility estimator

Rolling window log-return variance, annualised:

```go
est := model.NewVolatilityEstimator(100, 5*time.Second)
est.Observe(midPrice)
sigma := est.Sigma() // annualised realised vol
m.UpdateSigma(sigma)
```

---

## Quickstart (5 minutes)

**Prerequisites:** Go 1.24+ · internet access (for paper/live mode) · no API key needed for paper mode

```bash
# 1. Clone and build
git clone https://github.com/deiamor/perp-strategy-engine
cd perp-strategy-engine
go build -o mmbot ./cmd/mmbot

# 2. Run paper trading with dashboard
./mmbot --mode paper --symbol BTCUSDT --dashboard
# → open http://localhost:8080

# 3. Download kline data from the dashboard (Data tab → Download)
#    or download via curl:
#    http://localhost:8080  → "Data" tab → symbol=BTCUSDT, interval=5m, date range → Download

# 4. Run backtest on downloaded data
./mmbot --mode backtest \
  --dataset ./data/BTCUSDT_5m_20240101_20240201.json \
  --dashboard
# → open http://localhost:8080 → Backtest tab → select dataset → Run Backtest

# 5. (Optional) Grid search for best parameters
#    Dashboard → Optimize tab → configure grid → Run
```

> **Live trading** requires `BINANCE_API_KEY` + `BINANCE_SECRET_KEY` (see below).
> The dashboard has a Settings panel to enter keys without environment variables.

---

## Running the MM bot

### Paper trading (no keys needed — good for testing)

```bash
go run ./cmd/mmbot --mode paper --symbol BTCUSDT --dashboard
# Dashboard: http://localhost:8080
```

### Backtesting

```bash
# Step 1: download data from the dashboard (http://localhost:8080 → Data tab)
# or use the CLI after starting with --dashboard

# Step 2: run backtest
go run ./cmd/mmbot --mode backtest --dataset ./data/BTCUSDT_5m_20240101_20240201.json
```

### Live trading

```bash
export BINANCE_API_KEY=your_api_key
export BINANCE_SECRET_KEY=your_secret_key

go run ./cmd/mmbot \
  --mode live \
  --symbol BTCUSDT \
  --gamma 0.1 \
  --kappa 1.5 \
  --sigma 0.80 \
  --order-size 0.001 \
  --interval 5s \
  --dashboard
```

### All flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `paper` | `live` \| `paper` \| `backtest` |
| `--symbol` | `BTCUSDT` | Binance futures symbol |
| `--dataset` | — | Kline JSON file path (backtest only) |
| `--initial-balance` | `10000` | Starting USDT (paper/backtest) |
| `--dashboard` | `true` | Enable web dashboard |
| `--dashboard-addr` | `:8080` | Dashboard listen address |
| `--data-dir` | `./data` | Local kline storage directory |
| `--testnet` | `false` | Use Binance testnet |
| `--interval` | `5s` | Tick interval (live/paper) |
| `--gamma` | `0.1` | Risk aversion |
| `--kappa` | `1.5` | Order arrival intensity |
| `--sigma` | `0.80` | Initial volatility (annualised) |
| `--alpha` | `1.0` | Funding sensitivity |
| `--order-size` | `0.001` | Base order size |
| `--max-inventory` | `0.1` | Max inventory (base units) |

---

## Repository layout

```
fsm/
  core/             Engine, Strategy, State, Transition, Context
  exchange/         Exchange interface + types + ErrTerminal + TradingEvent
    binance/        Binance USDT-M Futures (full: market data + signed execution)
    paper/          Paper trading exchange (virtual account, live market data)
    backtest/       Backtest exchange (kline replay) + JSON loader
    hyperliquid/    HyperLiquid connector (market data only)
  primitives/       Ready-to-use strategies (TWAP)
mm/
  model/            Avellaneda-Stoikov + funding rate math, VolatilityEstimator
  engine/           MakerStrategy FSM
dashboard/
  server.go         HTTP server + REST API + SSE stream
  collector.go      TradingEvent fan-in, SSE broadcast hub
  downloader.go     Binance kline downloader (chunked, with progress)
  backtest_runner.go Synchronous backtest runner (called from HTTP handler)
  static/           Chart.js frontend (equity curve, fills, FSM state, data download UI)
cmd/
  mmbot/            Runnable bot (--mode live|paper|backtest, --dashboard)
data/               Downloaded kline JSON files (gitignored)
```

---

## Testing

```bash
go test ./...
go test -race ./...
```

---

## Dashboard

Open `http://localhost:8080` after starting with `--dashboard`.

| Tab | Features |
|-----|---------|
| **Live / Paper** | Real-time equity curve, mid price chart, position chart, fill history table, FSM state badge |
| **Backtest** | Dataset selector, model parameter inputs, run button, results (PnL, return %, win rate, equity curve) |
| **Data** | Symbol / interval / date range selector, download button with progress bar, available datasets list |

## Roadmap

- [ ] HyperLiquid EIP-712 order signing
- [ ] dYdX v4 connector
- [ ] WebSocket market data feed (replace REST polling)
- [ ] Sharpe ratio + max drawdown in backtest results
- [ ] VWAP primitive
- [ ] Grid strategy primitive
