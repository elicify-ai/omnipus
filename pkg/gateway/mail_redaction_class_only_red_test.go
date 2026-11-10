package gateway

// RED — w5-integration claim 5: raw provider error text never reaches a log
// line or persisted state. The wave fixed mailErr502 plus the four
// mutation-tail warnings; the audit found NO positive class-only oracle
// anywhere (its M11 healed the old forging subtest instead of being caught).
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-7.4: "Given a gateway mail failure, When this package
//     logs/maps it, Then only the safe class and approved fields enter the
//     log/response, never the raw error value."
//   - w5 spec MC-15: gateway diagnostics contain zero content/credential/
//     raw-error markers and VISIBLE safe classes.
//   - w5 spec B-30: "the SAME scan finds the marker in the separate control
//     fixture and in the provider input, proving it could detect a leak" —
//     a scan without a positive control proves nothing.
//
// Mutation this pack must kill (check-integration-report.md §2, M11):
// mailErr502 logs the raw provider error value again next to the class.
//
// Named gap (honest scope): the four mutation-tail warning sites
// (rest_mail_send.go sent-copy APPEND; rest_mail_draft.go old-draft delete,
// draft sent-copy APPEND, draft cleanup after send) fire only when the main
// operation SUCCEEDS and its tail FAILS — that needs the scripted IMAP fault
// harness w6 §6.2 plans (imapserver_faults_test.go). This pack pins the
// shared contract at the one site the audit mutated (M11) and at the live
// handler seam; the tail sites need that harness and are recorded as a gap.

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/stretchr/testify/require"
)

const redactionMarker = "IMAP502RAW-7Q2MARKER leakahead subject-was-Secret-Plans"

func containsMarker(hay string) bool {
	return strings.Contains(hay, "7Q2MARKER")
}

func TestMailErr502_LogsClassOnlyNeverRawError(t *testing.T) {
	capture := &physicalLogCapture{messagePrefix: "rest: mail upstream failure"}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	defer slog.SetDefault(previous)

	rawErr := fmt.Errorf("%s: * BYE [SERVERBUG] remote raw text", redactionMarker)

	// Positive control (B-30): the SAME scan finds the marker in the raw
	// error we are about to feed the seam — proving this instrument could
	// detect a leak if the seam emitted one.
	if !containsMarker(rawErr.Error()) {
		t.Fatal("instrument error: the marker scan cannot see the marker in its own fixture")
	}

	rec := httptest.NewRecorder()
	mailErr502(rec, rawErr)

	capture.mu.Lock()
	logged := capture.buf.String()
	capture.mu.Unlock()

	if n := strings.Count(logged, "\n"); n != 1 {
		t.Fatalf("US-7.4: exactly one log record must carry the failure, got %d (%q)", n, logged)
	}
	if !strings.Contains(logged, "class=") {
		t.Fatalf("MC-15: the log record must carry the VISIBLE safe class, got %q", logged)
	}
	if containsMarker(logged) {
		t.Fatalf("US-7.4/MC-15: the raw provider error text reached the log line: %q", logged)
	}

	// The wire envelope is class-only too (US-7.4: "the log/response").
	if body := rec.Body.String(); containsMarker(body) {
		t.Fatalf("US-7.4: the raw provider error text reached the response body: %q", body)
	}
	class := email.ClassifyMailError(rawErr)
	if !mailUpstreamClasses[class] {
		t.Fatalf("MC-15: the mapped class %q must be a member of the closed safe class set", class)
	}
}

func TestMailErr502_HandlerSeamClassOnly(t *testing.T) {
	// The same contract driven through a REAL handler failure: the folders
	// route dials a port that accepts and immediately closes connections, so
	// the upstream failure carries provider-flavoured text on the wire path.
	// The captured log line must show class-only, never the raw dial error.
	env := newMailRedEnv(t)
	deadPort, _ := listenCount(t)
	smtpPort, _ := listenCount(t)
	pointMailboxAt(t, env, deadPort, smtpPort)

	capture := &physicalLogCapture{messagePrefix: "rest: mail upstream failure"}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rec := mailDo(env.mux, http.MethodGet, mailFoldersPath(), nextMailIP(), true, "")
	require.Equal(t, http.StatusBadGateway, rec.Code, "body=%s", rec.Body.String())

	capture.mu.Lock()
	logged := capture.buf.String()
	capture.mu.Unlock()
	if !strings.Contains(logged, "class=") {
		t.Fatalf("MC-15: the handler failure must log the visible safe class, got %q", logged)
	}
	for _, leak := range []string{"127.0.0.1", "dial tcp", "connection reset", "EOF"} {
		if strings.Contains(strings.ToLower(logged), strings.ToLower(leak)) {
			t.Fatalf("US-7.4/MC-15: raw upstream failure detail %q reached the log line: %q", leak, logged)
		}
	}
}

// TestRedactionErrorsAreErrorsNotStrings pins that the redaction seam's
// failures are surfaced as real error values carrying the closed class —
// a truncated raw string masquerading as redaction (US-7.2: "Class labels
// remain visible, not truncated raw text masquerading as redaction") is the
// failure shape this guards against.
func TestRedactionErrorsAreErrorsNotStrings(t *testing.T) {
	raw := errors.New(redactionMarker + ": raw")
	class := email.ClassifyMailError(raw)
	if class == "" {
		t.Fatal("MC-15: every provider failure must map to a closed safe class, got an empty class")
	}
	if containsMarker(class) {
		t.Fatal("US-7.2: the mapped class carries raw provider text — truncation is not classification")
	}
	if !mailUpstreamClasses[class] {
		t.Fatalf("MC-15: class %q is outside the closed safe class set", class)
	}
}
