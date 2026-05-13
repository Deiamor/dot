# perp-strategy-engine

A composable FSM execution framework and funding-aware market making model for perpetual DEXes, written in Go.

Two independent libraries in one monorepo:

| Package | What it is |
|---------|-----------|
| `fsm/` | Declarative state-machine execution layer for trading strategies |
| `mm/` | Production implementation of the funding-aware Avellaneda-Stoikov model ([arXiv:2605.06405](https://arxiv.org/abs/2605.06405)) |

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

## Running the MM bot

```bash
go run ./cmd/mmbot \
  --symbol BTC \
  --gamma 0.1 \
  --kappa 1.5 \
  --sigma 0.80 \
  --horizon 0.00274 \
  --alpha 1.0 \
  --order-size 0.01 \
  --interval 5s \
  --testnet
```

Set `HYPERLIQUID_KEY` and `HYPERLIQUID_ADDRESS` environment variables for order signing (currently requires EIP-712 implementation — see `fsm/exchange/hyperliquid/client.go`).

---

## Repository layout

```
fsm/
  core/           Engine, Strategy, State, Transition, Context
  exchange/       Exchange interface + types (Order, Position, MarketSnapshot …)
    hyperliquid/  HyperLiquid connector (read-only live; signing TODO)
  primitives/     Ready-to-use strategies (TWAP)
mm/
  model/          Avellaneda-Stoikov + funding rate math, VolatilityEstimator
  engine/         MakerStrategy FSM
cmd/
  mmbot/          Runnable market making bot
```

---

## Testing

```bash
go test ./...
go test -race ./...
```

---

## Roadmap

- [ ] HyperLiquid EIP-712 order signing
- [ ] dYdX v4 connector
- [ ] Backtester (replay historical snapshots through the FSM)
- [ ] WebSocket market data feed (replace polling)
- [ ] VWAP primitive
- [ ] Grid strategy primitive
