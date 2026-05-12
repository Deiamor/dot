// Package genesis constructs the CometBFT GenesisDoc for a fairspeed-dex chain.
//
// The AppState embedded in genesis.json tells each node which assets are
// registered at height 0, before any transactions are processed.
package genesis

import (
	"encoding/json"
	"fmt"
	"time"

	cmtconfig "github.com/cometbft/cometbft/config"
	cmtcrypto "github.com/cometbft/cometbft/crypto"
	cmttypes "github.com/cometbft/cometbft/types"
)

// AppState is the DEX-specific genesis state embedded in genesis.json.
type AppState struct {
	// RegisteredAssets lists asset IDs available at chain start.
	RegisteredAssets []AssetGenesis `json:"registered_assets"`
}

// AssetGenesis describes an asset registered at genesis.
type AssetGenesis struct {
	AssetId  string `json:"asset_id"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
}

// ValidatorEntry carries the public key and initial voting power for one validator.
type ValidatorEntry struct {
	PubKey      cmtcrypto.PubKey
	Power       int64
	Name        string
}

// DefaultAppState returns the standard DEX genesis state (BTC + USDC registered).
func DefaultAppState() AppState {
	return AppState{
		RegisteredAssets: []AssetGenesis{
			{AssetId: "BTC", Symbol: "BTC", Decimals: 8},
			{AssetId: "USDC", Symbol: "USDC", Decimals: 6},
		},
	}
}

// BuildGenesisDoc creates a CometBFT GenesisDoc for the given chain and validators.
// Each validator entry contributes a GenesisValidator with the specified voting power.
func BuildGenesisDoc(chainId string, validators []ValidatorEntry, appState AppState) (*cmttypes.GenesisDoc, error) {
	if len(validators) == 0 {
		return nil, fmt.Errorf("at least one validator required")
	}

	gv := make([]cmttypes.GenesisValidator, len(validators))
	for i, v := range validators {
		power := v.Power
		if power <= 0 {
			power = 100
		}
		gv[i] = cmttypes.GenesisValidator{
			Address: v.PubKey.Address(),
			PubKey:  v.PubKey,
			Power:   power,
			Name:    v.Name,
		}
	}

	appStateBytes, err := json.Marshal(appState)
	if err != nil {
		return nil, fmt.Errorf("marshal app state: %w", err)
	}

	consParams := cmttypes.DefaultConsensusParams()

	doc := &cmttypes.GenesisDoc{
		GenesisTime:     time.Now().UTC(),
		ChainID:         chainId,
		InitialHeight:   1,
		ConsensusParams: consParams,
		Validators:      gv,
		AppState:        appStateBytes,
	}
	if err := doc.ValidateAndComplete(); err != nil {
		return nil, fmt.Errorf("validate genesis: %w", err)
	}
	return doc, nil
}

// WriteGenesisDoc saves a GenesisDoc to the path configured in cfg.
func WriteGenesisDoc(doc *cmttypes.GenesisDoc, cfg *cmtconfig.Config) error {
	return doc.SaveAs(cfg.GenesisFile())
}

// LoadAppState parses the DEX AppState from a GenesisDoc's AppState field.
func LoadAppState(doc *cmttypes.GenesisDoc) (AppState, error) {
	var as AppState
	if len(doc.AppState) == 0 {
		return DefaultAppState(), nil
	}
	if err := json.Unmarshal(doc.AppState, &as); err != nil {
		return AppState{}, fmt.Errorf("parse app state: %w", err)
	}
	return as, nil
}
