package gateway

// T32 - TestMailAuditEvents (spec section 7 row 32, MC-19, D33).
// Round-2 fix-forward (D46): the round-1 characterization pins on the
// count-only field shape (recipients_count/attachment_count/uid/uidvalidity/
// expunged and the field-set-drift guards) are SUPERSEDED by MC-19's full
// field set - a full-fields oracle that contradicted nothing but pinned the
// very shape the founder ruled against. They are removed; the full-field
// oracle now lives in mail_audit_full_fields_red_test.go. This file keeps
// the spec-derived identity assertions: exactly one event per action, with
// pair and Message-ID. The two send subtests now run against startSMTPSink
// (the loopback plaintext exception, 89e16221f, makes a local sink speakable
// - the round-1 BLOCKED fatals are stale and gone).

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

const auditDraftRaw = "From: mailbox@test.local\r\nTo: a@b.test\r\nSubject: audit draft\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <audit-draft@example.test>\r\n" +
	"X-Omnipus-Draft: yes\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n\r\nsign here\r\n"

// mailAuditLogger wires a real audit logger into the env and returns the dir
// its JSONL lands in.
func mailAuditLogger(t *testing.T, env *mailRedEnv) string {
	t.Helper()
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	env.api.auditor = wireAuditor(logger)
	return auditDir
}

// draftUIDValidity reads the Drafts folder UIDVALIDITY from the server.
func draftUIDValidity(t *testing.T, cl *imapclient.Client) uint32 {
	t.Helper()
	data, err := cl.Select("Drafts", &imap.SelectOptions{ReadOnly: true}).Wait()
	require.NoError(t, err)
	return data.UIDValidity
}

func draftRefPath(uv uint32, uid uint32) string {
	return fmt.Sprintf("/api/v1/workspaces/%s/mail/%s/folders/drafts/messages/uid:%d:%d",
		mailRedWS, mailRedAgent, uv, uid)
}

func utoa(u uint32) string { return strconv.FormatUint(uint64(u), 10) }

func wireAuditor(l *audit.Logger) *audit.Logger { return l }

// auditDebugLines dumps raw audit JSONL for failure diagnostics.
func auditDebugLines(t *testing.T, auditDir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	var out []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err == nil {
			out = append(out, string(raw))
		}
	}
	return strings.Join(out, "\n---\n")
}

func TestMailAuditEvents(t *testing.T) {
	t.Run("draft update emits mail.panel.draft_updated", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		path := draftRefPath(uv, 1)
		rec := mailDo(env.mux, http.MethodPut, path, nextMailIP(), true,
			`{"uid":1,"uidvalidity":`+utoa(uv)+`,"to":["a@b.test"],"subject":"edited","body_markdown":"new body"}`)
		if rec.Code >= 300 {
			t.Fatalf("draft update = %d, want 2xx. body=%s", rec.Code, rec.Body.String())
		}
		if n := countAuditEvents(t, auditDir, "mail.panel.draft_updated"); n != 1 {
			t.Fatalf("exactly one draft_updated event for one PUT, got %d; lines:\n%s", n, auditDebugLines(t, auditDir))
		}
		ev := findAuditEvent(t, auditDir, "mail.panel.draft_updated")
		details := ev["details"].(map[string]any)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		require.Equal(t, "<audit-draft@example.test>", details["message_id"], "bracketed Message-ID of the edited draft")
	})

	t.Run("draft discard emits mail.panel.draft_discarded", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		rec := mailDo(env.mux, http.MethodDelete, draftRefPath(uv, 1), nextMailIP(), true, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("draft discard = %d, want 204. body=%s", rec.Code, rec.Body.String())
		}
		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.draft_discarded"),
			"exactly one audit event for one draft discard")
		ev := findAuditEvent(t, auditDir, "mail.panel.draft_discarded")
		details := ev["details"].(map[string]any)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		require.Equal(t, "<audit-draft@example.test>", details["message_id"])
		// Superseded pins removed (round-2 fix-forward): uid/uidvalidity and
		// expunged are fields MC-19 does not specify; the full-field oracle
		// lives in mail_audit_full_fields_red_test.go.
	})

	t.Run("draft send emits mail.panel.draft_sent", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		appendRaw(t, cl, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		path := draftRefPath(uv, 1) + "/send"
		rec := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true,
			`{"uid":1,"uidvalidity":`+utoa(uv)+`,"to":["a@b.test"],"subject":"s","body_markdown":"hello"}`)
		require.Equal(t, http.StatusOK, rec.Code, "draft send must succeed against the loopback sink; body: "+rec.Body.String())
		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.draft_sent"))
		ev := findAuditEvent(t, auditDir, "mail.panel.draft_sent")
		details := ev["details"].(map[string]any)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		require.Equal(t, "<audit-draft@example.test>", details["message_id"])
	})

	t.Run("manual send emits mail.panel.send", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, _ := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

		rec := mailDo(env.mux, http.MethodPost, "/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages", nextMailIP(), true,
			`{"to":["a@b.test"],"subject":"s","body_markdown":"hello"}`)
		require.Equal(t, http.StatusOK, rec.Code, "manual send must succeed against the loopback sink; body: "+rec.Body.String())
		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.send"))
		ev := findAuditEvent(t, auditDir, "mail.panel.send")
		details := ev["details"].(map[string]any)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		// Superseded count-only pins removed (round-2 fix-forward): the
		// full-field oracle lives in mail_audit_full_fields_red_test.go.
	})
}
