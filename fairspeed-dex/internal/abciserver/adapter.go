// Package abciserver bridges our DEXApplication to the real CometBFT runtime.
//
// CometBFTAdapter wraps abci.DEXApplication and implements the real
// cometbft/abci/types.Application interface so CometBFT can call it directly
// over a socket or gRPC connection.
package abciserver

import (
	"context"

	cmttypes "github.com/cometbft/cometbft/abci/types"

	localabci "github.com/byunghee1994/fairspeed-dex/internal/abci"
)

// CometBFTAdapter wraps DEXApplication and implements cmttypes.Application.
// Unused methods (vote extensions, state sync) delegate to BaseApplication.
type CometBFTAdapter struct {
	cmttypes.BaseApplication
	app *localabci.DEXApplication
}

var _ cmttypes.Application = (*CometBFTAdapter)(nil)

// NewCometBFTAdapter creates an adapter around the given DEXApplication.
func NewCometBFTAdapter(app *localabci.DEXApplication) *CometBFTAdapter {
	return &CometBFTAdapter{app: app}
}

func (a *CometBFTAdapter) Info(_ context.Context, _ *cmttypes.InfoRequest) (*cmttypes.InfoResponse, error) {
	r := a.app.Info(localabci.RequestInfo{})
	return &cmttypes.InfoResponse{
		AppVersion:       r.AppVersion,
		LastBlockHeight:  r.LastBlockHeight,
		LastBlockAppHash: r.LastBlockAppHash,
	}, nil
}

func (a *CometBFTAdapter) InitChain(_ context.Context, req *cmttypes.InitChainRequest) (*cmttypes.InitChainResponse, error) {
	r := a.app.InitChain(localabci.RequestInitChain{
		ChainId:       req.ChainId,
		InitialHeight: req.InitialHeight,
	})
	return &cmttypes.InitChainResponse{AppHash: r.AppHash}, nil
}

func (a *CometBFTAdapter) CheckTx(_ context.Context, req *cmttypes.CheckTxRequest) (*cmttypes.CheckTxResponse, error) {
	r := a.app.CheckTx(localabci.RequestCheckTx{
		Tx: req.Tx,
		// v1 uses CheckTxType constants: CHECK_TX_TYPE_RECHECK == 1
		Type: localabci.CheckTxType(req.Type),
	})
	return &cmttypes.CheckTxResponse{Code: r.Code, Log: r.Log}, nil
}

func (a *CometBFTAdapter) PrepareProposal(_ context.Context, req *cmttypes.PrepareProposalRequest) (*cmttypes.PrepareProposalResponse, error) {
	r := a.app.PrepareProposal(localabci.RequestPrepareProposal{
		Txs:        req.Txs,
		Height:     req.Height,
		MaxTxBytes: req.MaxTxBytes,
	})
	return &cmttypes.PrepareProposalResponse{Txs: r.Txs}, nil
}

func (a *CometBFTAdapter) ProcessProposal(_ context.Context, req *cmttypes.ProcessProposalRequest) (*cmttypes.ProcessProposalResponse, error) {
	r := a.app.ProcessProposal(localabci.RequestProcessProposal{
		Txs:    req.Txs,
		Height: req.Height,
	})
	status := cmttypes.PROCESS_PROPOSAL_STATUS_ACCEPT
	if r.Status == localabci.ProcessProposalReject {
		status = cmttypes.PROCESS_PROPOSAL_STATUS_REJECT
	}
	return &cmttypes.ProcessProposalResponse{Status: status}, nil
}

func (a *CometBFTAdapter) FinalizeBlock(_ context.Context, req *cmttypes.FinalizeBlockRequest) (*cmttypes.FinalizeBlockResponse, error) {
	r := a.app.FinalizeBlock(localabci.RequestFinalizeBlock{
		Txs:    req.Txs,
		Height: req.Height,
		Hash:   req.Hash,
	})
	results := make([]*cmttypes.ExecTxResult, len(r.TxResults))
	for i, tr := range r.TxResults {
		results[i] = &cmttypes.ExecTxResult{Code: tr.Code, Log: tr.Log}
	}
	return &cmttypes.FinalizeBlockResponse{
		TxResults: results,
		AppHash:   r.AppHash,
	}, nil
}

func (a *CometBFTAdapter) Commit(_ context.Context, _ *cmttypes.CommitRequest) (*cmttypes.CommitResponse, error) {
	r := a.app.Commit(localabci.RequestCommit{})
	return &cmttypes.CommitResponse{RetainHeight: r.RetainHeight}, nil
}

func (a *CometBFTAdapter) Query(_ context.Context, req *cmttypes.QueryRequest) (*cmttypes.QueryResponse, error) {
	r := a.app.Query(localabci.RequestQuery{
		Path:   req.Path,
		Data:   req.Data,
		Height: req.Height,
	})
	return &cmttypes.QueryResponse{
		Code:  r.Code,
		Log:   r.Log,
		Value: r.Value,
	}, nil
}
