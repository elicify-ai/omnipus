package gateway

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// controlBearingMarshalError is an argument value whose JSON encoding fails
// with an error text carrying raw controls. audit.ArgsHash falls back to
// json.Marshal for any type outside its own switch
// (pkg/audit/argshash.go::canonicalEncode), and encoding/json wraps a
// MarshalJSON failure into the returned error text, so the control reaches
// consentArgsHash's err.Error() through the real production path.
type controlBearingMarshalError struct{}

func (controlBearingMarshalError) MarshalJSON() ([]byte, error) {
	return nil, errors.New("cannot encode\nforged=1\r\x00\x7f-3035err")
}

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

// CodeQL #3035: the alert's sink is the logged "error" value (err.Error()).
// The NaN fixture above yields control-free error text, so it cannot guard
// that wrap. Here the hash failure itself carries raw controls: the logged
// error field must still be one control-free line that keeps the diagnostic.
func TestCodeQL3035_ConsentHashLogErrorFieldStripsControlCharacters(t *testing.T) {
	logs := captureRedactionSlog(t)
	const message = "approval audit: could not hash the approved arguments; args_hash is empty"
	entry := &approvalEntry{
		ApprovalID: "apr-3035-err",
		ToolName:   "tool-3035-err",
		Args:       map[string]any{"n": controlBearingMarshalError{}},
	}

	hash := consentArgsHash(entry)

	if hash != "" {
		t.Fatalf("a hash failure must still return an empty digest, got %q", hash)
	}
	record := codeQL1618SingleRecord(t, logs, message)
	codeQL1618AssertSafeRecord(t, record, "approval_id", "tool", "error")
	// Fixture self-check: the failure text really is control-bearing, so a
	// control-free logged value can only come from sanitising it.
	errText := record.attrs["error"]
	if !strings.Contains(errText, "cannot encode") || !strings.Contains(errText, "-3035err") {
		t.Fatalf("logged error must keep the diagnostic text, got %q", errText)
	}
	if strings.ContainsAny(errText, "\n\r\x00\x7f") {
		t.Fatalf("logged error field contains a raw control: %q", errText)
	}
}
