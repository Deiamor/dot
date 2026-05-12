// Package abci defines the Application BlockChain Interface that bridges
// the DEX application layer with a consensus engine (CometBFT).
//
// These types mirror the CometBFT ABCI types exactly so that swapping in the
// real dependency is a mechanical import-path substitution:
//
//   import abci "github.com/cometbft/cometbft/abci/types"
//
// Until that switch, the DEX is fully testable without the CometBFT runtime.
package abci

// CheckTxType distinguishes first-time checks from re-checks after a block.
type CheckTxType int32

const (
	CheckTxNew     CheckTxType = 0
	CheckTxRecheck CheckTxType = 1
)

// ProcessProposalStatus signals whether a validator accepts or rejects a block.
type ProcessProposalStatus int32

const (
	ProcessProposalAccept ProcessProposalStatus = 0
	ProcessProposalReject ProcessProposalStatus = 1
)

// ABCI response codes. 0 = OK, non-zero = error.
const (
	CodeOK    uint32 = 0
	CodeError uint32 = 1
)

// --- Info ----------------------------------------------------------------

type RequestInfo struct{}

type ResponseInfo struct {
	AppVersion       uint64
	LastBlockHeight  int64
	LastBlockAppHash []byte
}

// --- CheckTx -------------------------------------------------------------

type RequestCheckTx struct {
	Tx   []byte
	Type CheckTxType
}

type ResponseCheckTx struct {
	Code uint32
	Log  string
}

// --- InitChain -----------------------------------------------------------

type RequestInitChain struct {
	ChainId       string
	InitialHeight int64
	AppStateBytes []byte // genesis JSON (not used in Phase 6; reserved)
}

type ResponseInitChain struct {
	AppHash []byte
}

// --- PrepareProposal -----------------------------------------------------

type RequestPrepareProposal struct {
	Txs        [][]byte
	Height     int64
	MaxTxBytes int64
}

type ResponsePrepareProposal struct {
	Txs [][]byte
}

// --- ProcessProposal -----------------------------------------------------

type RequestProcessProposal struct {
	Txs    [][]byte
	Height int64
}

type ResponseProcessProposal struct {
	Status ProcessProposalStatus
}

// --- FinalizeBlock -------------------------------------------------------

type ExecTxResult struct {
	Code uint32
	Log  string
}

type RequestFinalizeBlock struct {
	Txs    [][]byte
	Height int64
	Hash   []byte
}

type ResponseFinalizeBlock struct {
	TxResults []*ExecTxResult
	AppHash   []byte
}

// --- Commit --------------------------------------------------------------

type RequestCommit struct{}

type ResponseCommit struct {
	RetainHeight int64
}

// --- Query ---------------------------------------------------------------

type RequestQuery struct {
	Path   string // e.g. "/orderbook/BTC-USDC", "/trades/BTC-USDC", "/balance/{accountId}/{assetId}"
	Data   []byte
	Height int64
}

type ResponseQuery struct {
	Code  uint32
	Log   string
	Value []byte // JSON-encoded response
}

// --- Application interface -----------------------------------------------

// Application is the interface that DEXApplication implements.
// It mirrors abci.Application from CometBFT v0.38 / v1.x.
type Application interface {
	Info(RequestInfo) ResponseInfo
	CheckTx(RequestCheckTx) ResponseCheckTx
	InitChain(RequestInitChain) ResponseInitChain
	PrepareProposal(RequestPrepareProposal) ResponsePrepareProposal
	ProcessProposal(RequestProcessProposal) ResponseProcessProposal
	FinalizeBlock(RequestFinalizeBlock) ResponseFinalizeBlock
	Commit(RequestCommit) ResponseCommit
	Query(RequestQuery) ResponseQuery
}
