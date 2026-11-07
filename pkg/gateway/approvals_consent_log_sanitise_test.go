package gateway

import (
	"math"
	"testing"
)

// CodeQL #3035 (go/log-injection) on approvals_consent.go::consentArgsHash:
// when the card arguments cannot be hashed, the error log carries the
// request-influenced approval id and tool name. A control in either must not
// reach the log record. Reuses the #1618 raw slog capture; do not run in
// parallel while the default logger is replaced.
func TestCodeQL3035_ConsentHashLogStripsControlCharacters(t *testing.T) {
	logs := captureRedactionSlog(t)
	const message = "approval audit: could not hash the approved arguments; args_hash is empty"
	entry := &approvalEntry{
		ApprovalID: "apr-\n\x00-3035",
		ToolName:   "tool-\r\x7f-3035",
		Args:       map[string]any{"n": math.NaN()},
	}

	hash := consentArgsHash(entry)

	if hash != "" {
		t.Fatalf("a hash failure must still return an empty digest, got %q", hash)
	}
	record := codeQL1618SingleRecord(t, logs, message)
	codeQL1618AssertSafeRecord(t, record, "approval_id", "tool")
}
