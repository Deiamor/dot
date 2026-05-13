package compliance

import "fmt"

// SanctionEntry records why and from which list an account was sanctioned.
type SanctionEntry struct {
	AccountId   string
	Reason      string // e.g. "OFAC SDN list — proliferation"
	ListName    string // e.g. "OFAC", "UN", "EU"
	AddedHeight int64  // block height when the sanction was applied
}

// SanctionsStore is the persistence interface used by the screener and block processor.
// AppState implements this.
type SanctionsStore interface {
	IsSanctioned(accountId string) bool
	AddSanction(entry SanctionEntry)
	RemoveSanction(accountId string)
	AllSanctions() []SanctionEntry
}

// RiskSanctionsStore is the narrower interface used by risk.RiskChecker.
// This avoids importing compliance from risk (would create a cycle).
// AppState also implements this.
type RiskSanctionsStore interface {
	IsSanctioned(accountId string) bool
}

// SanctionsScreener applies on-chain sanctions list checks.
// It is integrated into RiskChecker to block any order from a sanctioned account.
type SanctionsScreener struct {
	store SanctionsStore
}

func NewSanctionsScreener(store SanctionsStore) *SanctionsScreener {
	return &SanctionsScreener{store: store}
}

// CheckAccount returns an error if accountId is on the sanctions list.
func (s *SanctionsScreener) CheckAccount(accountId string) error {
	if s.store.IsSanctioned(accountId) {
		return fmt.Errorf("account %s is sanctioned and cannot trade", accountId)
	}
	return nil
}

// AddSanction adds an account to the on-chain sanctions list.
func (s *SanctionsScreener) AddSanction(accountId, reason, listName string, blockHeight int64) {
	s.store.AddSanction(SanctionEntry{
		AccountId:   accountId,
		Reason:      reason,
		ListName:    listName,
		AddedHeight: blockHeight,
	})
}

// RemoveSanction removes an account from the on-chain sanctions list.
func (s *SanctionsScreener) RemoveSanction(accountId string) {
	s.store.RemoveSanction(accountId)
}

// AllSanctions returns all current sanctions entries.
func (s *SanctionsScreener) AllSanctions() []SanctionEntry {
	return s.store.AllSanctions()
}
