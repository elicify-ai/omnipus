// Package mailattachment is the single application-level save/read/download
// service behind the panel's Open/Save and the agent's attachment tools
// (ADR-20261001 F1/F2/F3; w4 spec §3.1 — one service, one cap, one naming
// rule, one audit event for every caller). It sits BELOW the Library leaf's
// consumers: it imports pkg/library and pkg/email, never pkg/tools and
// never gateway HTTP.
package mailattachment

import "sync"

// AuditStatus values — the truthful audit outcome carried on every save
// receipt (the generated MailAttachmentSaveResponse.audit_status enum).
const (
	AuditRecorded = "recorded"
	AuditDisabled = "disabled"
	AuditFailed   = "failed"
)

// WarningAuditWriteFailed is the one Phase-1 warning code: the file is saved
// and usable, but its audit event could not be recorded (US-2.AC-5 — saved
// stays true; no retry is invited, a retry would duplicate the file).
const WarningAuditWriteFailed = "audit_write_failed"

// SaveReceipt is the outcome of one committed save — the prior receipt a
// same-token retry returns. Exported fields mirror the response shape's
// payload (the gateway maps it onto the generated type).
type SaveReceipt struct {
	Token         string
	WorkspaceID   string
	Path          string // workspace-relative, suffixed final name included
	AbsolutePath  string
	SizeBytes     int64
	AuditStatus   string
	WarningCode   string
	FromReceipt   bool // true when returned by the reconciliation store, not a fresh save
	MailboxLabel  string
	SavedAtUnixMS int64
}

// SaveReceiptStore is the bounded save-operation-token reconciliation
// (correction M-02): an explicit same-token retry resolves to the PRIOR
// receipt — exactly one file — and only a genuinely uncommitted token
// performs a save. The bound (grill-2 F-9, DS-TOKEN): receipts live for the
// GATEWAY PROCESS LIFETIME with NO EVICTION — a receipt is never dropped to
// make room, so a late legitimate retry always finds its receipt while the
// process lives; the restart is the only bound (after a restart the retry is
// a new save attempt that lands numbered and says so). This is deliberately
// NOT a general exactly-once framework: no automatic replay ever fires, and
// a different token is simply a new save.
type SaveReceiptStore struct {
	mu       sync.Mutex
	receipts map[string]SaveReceipt
}

// NewSaveReceiptStore constructs the process-lifetime store.
func NewSaveReceiptStore() *SaveReceiptStore {
	return &SaveReceiptStore{receipts: make(map[string]SaveReceipt)}
}

// Lookup returns the receipt recorded for an explicit same-token retry, and
// whether one exists. An unknown token is the normal first-attempt path.
func (s *SaveReceiptStore) Lookup(token string) (SaveReceipt, bool) {
	if s == nil || token == "" {
		return SaveReceipt{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.receipts[token]
	return r, ok
}

// Record stores the receipt of a committed save. A token is recorded exactly
// once — a second Record with the same token is refused (it would mean a
// second commit under one token, which the reconciliation exists to make
// impossible; the caller surfaces the refusal rather than overwriting the
// first outcome).
func (s *SaveReceiptStore) Record(r SaveReceipt) error {
	if s == nil {
		return nil
	}
	if r.Token == "" {
		return ErrInvalidToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.receipts[r.Token]; exists {
		return ErrDuplicateTokenCommit
	}
	s.receipts[r.Token] = r
	return nil
}
