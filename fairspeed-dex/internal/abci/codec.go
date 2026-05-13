package abci

import (
	"encoding/json"
	"fmt"

	"github.com/byunghee1994/fairspeed-dex/internal/fairbatch"
)

// WireTx is the JSON-serialisable envelope for a Transaction on the wire.
// fairbatch.Transaction.Payload is `any`, which JSON cannot round-trip without
// type information, so we store TxType alongside a raw JSON payload.
// TxHash is included so decoders can verify it without access to block height.
type WireTx struct {
	TxType    int             `json:"t"`
	Payload   json.RawMessage `json:"p"`
	Signature string          `json:"s,omitempty"`
	AccountId string          `json:"a,omitempty"`
	SessionId string          `json:"ss,omitempty"`
	TxHash    string          `json:"h,omitempty"`
}

// EncodeTx serialises a fairbatch.Transaction to a byte slice.
func EncodeTx(tx fairbatch.Transaction) ([]byte, error) {
	rawPayload, err := json.Marshal(tx.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	wire := WireTx{
		TxType:    int(tx.TxType),
		Payload:   rawPayload,
		Signature: tx.Signature,
		AccountId: tx.AccountId,
		SessionId: tx.SessionId,
		TxHash:    tx.TxHash,
	}
	return json.Marshal(wire)
}

// DecodeTx deserialises a byte slice back into a fairbatch.Transaction.
func DecodeTx(raw []byte) (fairbatch.Transaction, error) {
	var wire WireTx
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fairbatch.Transaction{}, fmt.Errorf("decode wire tx: %w", err)
	}

	tx := fairbatch.Transaction{
		TxType:    fairbatch.TransactionType(wire.TxType),
		Signature: wire.Signature,
		AccountId: wire.AccountId,
		SessionId: wire.SessionId,
		TxHash:    wire.TxHash,
	}

	var err error
	switch tx.TxType {
	case fairbatch.TxCreateAccount:
		var p fairbatch.CreateAccountPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxCreateSession:
		var p fairbatch.CreateSessionPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxDeposit:
		var p fairbatch.DepositPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxSubmitOrder:
		var p fairbatch.SubmitOrderPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxCancelOrder:
		var p fairbatch.CancelOrderPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxWithdraw:
		var p fairbatch.WithdrawPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxKYCApprove:
		var p fairbatch.KYCApprovePayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxBondValidator:
		var p fairbatch.BondValidatorPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	case fairbatch.TxUnbondValidator:
		var p fairbatch.UnbondValidatorPayload
		err = json.Unmarshal(wire.Payload, &p)
		tx.Payload = p
	default:
		return tx, fmt.Errorf("unknown tx type: %d", wire.TxType)
	}
	if err != nil {
		return tx, fmt.Errorf("decode payload (type %d): %w", tx.TxType, err)
	}
	return tx, nil
}
