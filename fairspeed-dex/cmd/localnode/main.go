package main

import (
	"fmt"
	"log"

	"github.com/byunghee1994/fairspeed-dex/internal/account"
	"github.com/byunghee1994/fairspeed-dex/internal/asset"
	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/node"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

func main() {
	n := node.NewLocalNode()
	n.RegisterAsset(asset.BTC)
	n.RegisterAsset(asset.USDC)

	n.Subscribe(state.EventAll, func(e state.Event) {
		log.Printf("[EVENT] height=%d type=%s", e.BlockHeight, e.Type)
	})

	// Block 1: Create accounts
	result, err := n.SubmitBatch(fairbatch.NewBatchBuilder(1).
		AddCreateAccount("alice@example.com", "alice-root-key", "alice-withdraw-key").
		AddCreateAccount("bob@example.com", "bob-root-key", "bob-withdraw-key").
		Build())
	mustOk(err, "block 1")
	fmt.Printf("Block 1: %d txs\n", result.TxCount)

	var aliceId, bobId string
	for accId, acc := range n.AppState.Accounts {
		if acc.OwnerAddress == "alice@example.com" {
			aliceId = accId
		} else if acc.OwnerAddress == "bob@example.com" {
			bobId = accId
		}
	}

	// Block 2: Create sessions
	result, err = n.SubmitBatch(fairbatch.NewBatchBuilder(2).
		AddCreateSession(aliceId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC"},
			MaxOrderAmount: 1000,
		}).
		AddCreateSession(bobId, account.SessionOptions{
			AllowedMarkets: []string{"BTC-USDC"},
			MaxOrderAmount: 1000,
		}).
		Build())
	mustOk(err, "block 2")

	var aliceSessionId, bobSessionId string
	for sesId, sess := range n.AppState.Sessions {
		if sess.AccountId == aliceId {
			aliceSessionId = sesId
		} else if sess.AccountId == bobId {
			bobSessionId = sesId
		}
	}

	// Block 3: Deposits
	result, err = n.SubmitBatch(fairbatch.NewBatchBuilder(3).
		AddDeposit(aliceId, "USDC", 10_000).
		AddDeposit(bobId, "BTC", 10).
		Build())
	mustOk(err, "block 3")
	fmt.Printf("Alice USDC: %d, Bob BTC: %d\n",
		n.GetBalance(aliceId, "USDC").Available,
		n.GetBalance(bobId, "BTC").Available)

	// Block 4: Bob places SELL
	bobSell := clob.NewLimitOrder(bobId, bobSessionId, "BTC-USDC",
		clob.OrderSideSell, 10_000, 1, clob.TimeInForceGtc, 4)
	result, err = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddSubmitOrder(bobSell).Build())
	mustOk(err, "block 4")
	fmt.Printf("Block 4: Bob SELL placed. Bob BTC reserved=%d\n", n.GetBalance(bobId, "BTC").Reserved)

	// Block 5: Alice places BUY → trade executes
	aliceBuy := clob.NewLimitOrder(aliceId, aliceSessionId, "BTC-USDC",
		clob.OrderSideBuy, 10_000, 1, clob.TimeInForceGtc, 5)
	result, err = n.SubmitBatch(fairbatch.NewBatchBuilder(5).AddSubmitOrder(aliceBuy).Build())
	mustOk(err, "block 5")
	fmt.Printf("Block 5: trades=%d\n", result.TradeCount)

	for _, t := range result.Trades {
		fmt.Printf("  Trade: price=%d qty=%d makerFee=%d takerFee=%d\n",
			t.Price, t.Quantity, t.MakerFeeAmount, t.TakerFeeAmount)
	}

	fmt.Printf("Alice BTC: %+v\n", n.GetBalance(aliceId, "BTC"))
	fmt.Printf("Alice USDC: %+v\n", n.GetBalance(aliceId, "USDC"))
	fmt.Printf("Bob BTC:  %+v\n", n.GetBalance(bobId, "BTC"))
	fmt.Printf("Bob USDC: %+v\n", n.GetBalance(bobId, "USDC"))
	fmt.Printf("Treasury USDC: %+v\n", n.GetTreasury("USDC"))
}

func mustOk(err error, label string) {
	if err != nil {
		log.Fatalf("%s: %v", label, err)
	}
}
