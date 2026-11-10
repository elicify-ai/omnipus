package email

// RED (combined feature-gate review, item 3, code-reviewer — IMPORTANT).
// Oracle: pkg/email/transport.go::fetchMessages explicitly guards
// `m == nil || m.Envelope == nil` before dereferencing (its own comment
// labels this Round-8 F5, FR-018/FR-036: a buffer without its envelope
// cannot render a row, but the drop must never be invisible — one
// slog.Warn per dropped row). Its sibling pkg/email/view.go::fetchMailRows
// (used by ReadFolderPage, the Mail panel's inbox/sent list path) only
// guards `buf == nil` and then unconditionally dereferences
// buf.Envelope.MessageID / buf.Envelope.Subject / buf.Envelope.Date —
// which panics on a nil Envelope instead of dropping the row the way its
// sibling does.
//
// Fixture: reuses this package's own envDropSession
// (fetch_envelope_drop_red_test.go) — a FETCH response answered with
// UID+FLAGS only, every buffer's Envelope nil — driven through
// ReadFolderPage instead of ReadInbox, since ReadFolderPage is what calls
// fetchMailRows directly.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchMailRows_EnvelopeLessRowDoesNotPanic(t *testing.T) {
	var logBuf strings.Builder
	captureLogs(t, &logBuf)

	cl := startEnvDropIMAP(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("fetchMailRows (via ReadFolderPage) panicked on an envelope-less FETCH buffer: %v — "+
				"its sibling fetchMessages guards m == nil || m.Envelope == nil (Round-8 F5, FR-018/FR-036) "+
				"before dereferencing; fetchMailRows only guards buf == nil", r)
		}
	}()

	rows, _, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 25, 0)

	require.NoError(t, err, "the read itself must not fail — a crash or an invisible drop is the finding, not an error return")
	require.Empty(t, rows, "envelope-less buffers cannot render rows; the drop stays (matching fetchMessages's own behavior)")
	require.Contains(t, logBuf.String(), "level=WARN",
		"FR-018/FR-036: dropping an envelope-less fetched row must log a WARN, matching fetchMessages's own fix")
	require.Contains(t, strings.ToLower(logBuf.String()), "envelope",
		"FR-018/FR-036: the WARN must name the envelope-less condition")
}
