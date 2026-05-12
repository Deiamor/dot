package evidence

import (
	"crypto/rand"
	"fmt"
)

type EvidenceType string

const (
	EvidenceDoubleSign   EvidenceType = "DOUBLE_SIGN"
	EvidenceInvalidOrder EvidenceType = "INVALID_ORDER"
	EvidenceFrontRun     EvidenceType = "FRONT_RUN"
)

type Evidence struct {
	EvidenceId  string
	Type        EvidenceType
	BlockHeight int64
	Description string
	Payload     any
}

func NewEvidence(t EvidenceType, blockHeight int64, desc string, payload any) Evidence {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return Evidence{
		EvidenceId:  fmt.Sprintf("evi-%x", b),
		Type:        t,
		BlockHeight: blockHeight,
		Description: desc,
		Payload:     payload,
	}
}
