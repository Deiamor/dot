package scenario_test

// Stage 21: Perpetual market registration tests.
//
// Verifies:
//  1. PERP market registers with correct PerpConfig fields
//  2. SPOT markets retain Type=SPOT; PERP markets have Type=PERP
//  3. EventPerpMarketRegistered emitted with correct payload
//  4. PERP market defaults to ACTIVE status after registration
//  5. Circuit breaker (Halt/Resume) works the same on PERP markets
//  6. PerpConfig survives snapshot save + load round-trip

import (
	"testing"

	"github.com/byunghee1994/fairspeed-dex/internal/clob"
	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
	"github.com/byunghee1994/fairspeed-dex/internal/state"
)

// btcPerpPayload is a reusable PERP market config for BTC-USDC perps.
func btcPerpPayload() fairbatch.RegisterPerpMarketPayload {
	return fairbatch.RegisterPerpMarketPayload{
		MarketId:              "BTC-USDC-PERP",
		BaseAsset:             "BTC",
		QuoteAsset:            "USDC",
		InitialMarginBps:      1000, // 10%
		MaintenanceMarginBps:  500,  // 5%
		MaxLeverage:           10,
		FundingIntervalBlocks: 100,
		MaxFundingRateBps:     100, // 1% per epoch
	}
}

// -------------------------------------------------------------------------
// Scenario 1: PERP market registered, config retrieved correctly
// -------------------------------------------------------------------------
func TestPerpMarket_Register(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, err := n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(btcPerpPayload()).Build())
	if err != nil {
		t.Fatalf("register perp market: %v", err)
	}

	cfg, ok := n.GetPerpConfig("BTC-USDC-PERP")
	if !ok {
		t.Fatal("expected PerpConfig to be retrievable")
	}
	if cfg.InitialMarginBps != 1000 {
		t.Errorf("InitialMarginBps: want 1000, got %d", cfg.InitialMarginBps)
	}
	if cfg.MaintenanceMarginBps != 500 {
		t.Errorf("MaintenanceMarginBps: want 500, got %d", cfg.MaintenanceMarginBps)
	}
	if cfg.MaxLeverage != 10 {
		t.Errorf("MaxLeverage: want 10, got %d", cfg.MaxLeverage)
	}
	if cfg.FundingIntervalBlocks != 100 {
		t.Errorf("FundingIntervalBlocks: want 100, got %d", cfg.FundingIntervalBlocks)
	}
}

// -------------------------------------------------------------------------
// Scenario 2: SPOT vs PERP type distinction
// -------------------------------------------------------------------------
func TestPerpMarket_TypeDistinction(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	// Spot market: BTC-USDC already used in trading scenario — type defaults to SPOT.
	// PERP market: register explicitly.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(btcPerpPayload()).Build())

	if !n.IsPerp("BTC-USDC-PERP") {
		t.Error("expected BTC-USDC-PERP to be PERP type")
	}
	if n.IsPerp("BTC-USDC") {
		t.Error("BTC-USDC should be SPOT, not PERP")
	}

	// Spot market: GetPerpConfig should return false.
	_, ok := n.GetPerpConfig("BTC-USDC")
	if ok {
		t.Error("GetPerpConfig should return false for SPOT market")
	}
}

// -------------------------------------------------------------------------
// Scenario 3: EventPerpMarketRegistered emitted with correct fields
// -------------------------------------------------------------------------
func TestPerpMarket_EventEmitted(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	var ev state.PerpMarketRegisteredPayload
	n.Subscribe(state.EventPerpMarketRegistered, func(e state.Event) {
		ev = e.Payload.(state.PerpMarketRegisteredPayload)
	})

	p := btcPerpPayload()
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).AddRegisterPerpMarket(p).Build())

	if ev.MarketId != "BTC-USDC-PERP" {
		t.Errorf("marketId: want BTC-USDC-PERP, got %s", ev.MarketId)
	}
	if ev.InitialMarginBps != 1000 {
		t.Errorf("InitialMarginBps: want 1000, got %d", ev.InitialMarginBps)
	}
	if ev.MaintenanceMarginBps != 500 {
		t.Errorf("MaintenanceMarginBps: want 500, got %d", ev.MaintenanceMarginBps)
	}
	if ev.MaxLeverage != 10 {
		t.Errorf("MaxLeverage: want 10, got %d", ev.MaxLeverage)
	}
	if ev.BaseAsset != "BTC" || ev.QuoteAsset != "USDC" {
		t.Errorf("assets: want BTC/USDC, got %s/%s", ev.BaseAsset, ev.QuoteAsset)
	}
}

// -------------------------------------------------------------------------
// Scenario 4: PERP market is ACTIVE by default after registration
// -------------------------------------------------------------------------
func TestPerpMarket_ActiveByDefault(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(btcPerpPayload()).Build())

	status := n.GetMarketStatus("BTC-USDC-PERP")
	if status != clob.MarketStatusActive {
		t.Errorf("expected ACTIVE, got %s", status)
	}
}

// -------------------------------------------------------------------------
// Scenario 5: Circuit breaker works the same on PERP markets
// -------------------------------------------------------------------------
func TestPerpMarket_HaltResumePerp(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(btcPerpPayload()).Build())

	// Halt the PERP market.
	var haltedMarket string
	n.Subscribe(state.EventMarketHalted, func(e state.Event) {
		haltedMarket = e.Payload.(state.MarketHaltedPayload).MarketId
	})

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(5).
		AddHaltMarket("BTC-USDC-PERP", "volatility spike").Build())

	if haltedMarket != "BTC-USDC-PERP" {
		t.Errorf("expected BTC-USDC-PERP halted, got %s", haltedMarket)
	}
	if n.GetMarketStatus("BTC-USDC-PERP") != clob.MarketStatusHalted {
		t.Error("market should be HALTED")
	}

	// Resume it.
	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(6).
		AddResumeMarket("BTC-USDC-PERP").Build())

	if n.GetMarketStatus("BTC-USDC-PERP") != clob.MarketStatusActive {
		t.Error("market should be ACTIVE after resume")
	}
}

// -------------------------------------------------------------------------
// Scenario 6: PerpConfig survives snapshot save + load
// -------------------------------------------------------------------------
func TestPerpMarket_Snapshot(t *testing.T) {
	n, _, _, _, _ := bootstrapNode(t)

	_, _ = n.SubmitBatch(fairbatch.NewBatchBuilder(4).
		AddRegisterPerpMarket(btcPerpPayload()).Build())

	// Save snapshot.
	path := t.TempDir() + "/state.json"
	if err := n.SaveSnapshot(path); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// Create fresh node and load snapshot.
	n2, _, _, _, _ := bootstrapNode(t)
	if err := n2.LoadSnapshot(path); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	cfg, ok := n2.GetPerpConfig("BTC-USDC-PERP")
	if !ok {
		t.Fatal("PerpConfig not found after snapshot restore")
	}
	if cfg.InitialMarginBps != 1000 || cfg.MaxLeverage != 10 {
		t.Errorf("PerpConfig mismatch after restore: %+v", cfg)
	}
	if !n2.IsPerp("BTC-USDC-PERP") {
		t.Error("market should still be PERP after snapshot restore")
	}
}
