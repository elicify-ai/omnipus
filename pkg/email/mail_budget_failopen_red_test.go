package email

// RED (round 8, silent-failure-hunter F4 — MEDIUM). Oracle: FR-018/FR-036
// (failures never silent in the logs) and the finding's accepted-residual
// shape: pkg/email/mail_budget.go::backoffRefusal fails OPEN on an unreadable
// or corrupt state file — the accepted stance — but the fail-open is
// UNLOGGED, so a corrupt email-watch/<pair>.json silently removes all MC-33
// politeness gating while every surface renders normal. The fix mirrors the
// summary endpoint's state_unreadable row: one slog.Warn on the
// non-ErrNoWatcherState branch, fail-open behavior unchanged.
//
// The no-state-file case (ErrNoWatcherState) is the normal never-ran case and
// must STAY silent — asserted here as the negative control so the fix cannot
// pass by logging unconditionally.

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureLogs swaps the default slog logger for a text handler capturing to
// buf (the house pattern; restored on cleanup). Returns the previous logger.
func captureLogs(t *testing.T, buf *strings.Builder) {
	t.Helper()
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(bufWriter{buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })
}

type bufWriter struct{ b *strings.Builder }

func (w bufWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

const unreadablePairAgent = "agent-ur"
const unreadablePairWS = "ws-ur"

func unreadableStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pair := keyFor(unreadablePairAgent, unreadablePairWS)
	p := filepath.Join(dir, "email-watch", pair+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte("{corrupt-json-not-a-state"), 0o600))
	return dir
}

func TestMailBudgetBackoffRefusal_UnreadableStateLogsFailOpen(t *testing.T) {
	var buf strings.Builder
	captureLogs(t, &buf)

	b := NewMailBudget(unreadableStateDir(t))
	refusal := b.backoffRefusal(MailBudgetRequest{
		Account:     "imap.test.local|mailbox@test.local",
		AgentID:     unreadablePairAgent,
		WorkspaceID: unreadablePairWS,
		Operation:   "read_inbox",
	})

	// Fail-open is the accepted stance — the gate's job is politeness, not
	// correctness. The finding is that the degrade is invisible.
	require.Nil(t, refusal, "fail-open on unreadable state stays the behavior; the finding is the missing log")

	require.Contains(t, buf.String(), "level=WARN",
		"FR-018/FR-036: the unreadable-state fail-open must log a WARN")
	require.Contains(t, buf.String(), "unreadable",
		"FR-018/FR-036: the WARN must name the unreadable state (the summary endpoint's state_unreadable family)")
}

func TestMailBudgetBackoffRefusal_MissingStateFileStaysSilent(t *testing.T) {
	var buf strings.Builder
	captureLogs(t, &buf)

	b := NewMailBudget(t.TempDir()) // no email-watch/ directory at all
	refusal := b.backoffRefusal(MailBudgetRequest{
		Account:     "imap.test.local|mailbox@test.local",
		AgentID:     unreadablePairAgent,
		WorkspaceID: unreadablePairWS,
		Operation:   "read_inbox",
	})

	require.Nil(t, refusal, "no state yet (ErrNoWatcherState) is not in backoff — the normal never-ran case")
	require.NotContains(t, buf.String(), "level=WARN",
		"a never-checked mailbox is the normal case; the fix must NOT log for it (mirrors the finding's non-ErrNoWatcherState scope)")
}
