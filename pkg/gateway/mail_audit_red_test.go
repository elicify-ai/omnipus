package gateway

// T32 - TestMailAuditEvents (spec section 7 row 32, MC-19, D33).
// Characterization oracle (per dispatch): pin the audit events the mail panel
// currently emits, with the field sets CURRENTLY implemented - do not invent
// fields. The two send-path events (mail.panel.send, mail.panel.draft_sent)
// cannot execute today: the production SMTP client requires trusted TLS
// (unconditional STARTTLS on non-465; no loopback exception mirroring
// imapDial), so a local SMTP double cannot be spoken to. That gap is part of
// the RED evidence, reported as such.

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
		if uid, ok := details["uid"].(float64); !ok || uid < 1 {
			t.Fatalf("uid = %v, want a nonzero uid reference (characterization: the event records the updated copy's uid)", details["uid"])
		}
		require.Equal(t, float64(uv), details["uidvalidity"], "same Drafts folder, same UIDVALIDITY")
		require.Equal(t, float64(0), details["attachment_count"], "characterization: draft had no attachments")
		for k := range details {
			switch k {
			case "workspace_id", "agent_id", "message_id", "uid", "uidvalidity", "attachment_count":
			default:
				t.Fatalf("characterization: unexpected draft_updated field %q - field set drift", k)
			}
		}
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
		require.Equal(t, float64(1), details["uid"])
		require.Equal(t, float64(uv), details["uidvalidity"])
		require.Equal(t, true, details["expunged"])
		for k := range details {
			switch k {
			case "workspace_id", "agent_id", "message_id", "uid", "uidvalidity", "expunged":
			default:
				t.Fatalf("characterization: unexpected draft_discarded field %q - field set drift", k)
			}
		}
	})

	t.Run("draft send emits mail.panel.draft_sent", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		path := draftRefPath(uv, 1) + "/send"
		rec := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true,
			`{"uid":1,"uidvalidity":`+utoa(uv)+`,"to":["a@b.test"],"subject":"s","body_markdown":"hello"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("BLOCKED behind the loopback-SMTP gap (D36 fake-server strategy): draft send = %d, want 200; body=%s - the production SMTP client demands trusted TLS (unconditional STARTTLS), so no local double can complete a send, and the draft_sent audit path cannot execute. Required by MC-19/D33.", rec.Code, rec.Body.String())
		}
		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.draft_sent"))
		ev := findAuditEvent(t, auditDir, "mail.panel.draft_sent")
		details := ev["details"].(map[string]any)
		for k := range details {
			switch k {
			case "workspace_id", "agent_id", "message_id", "recipients_count", "attachment_count", "sent_saved", "draft_cleanup_warning", "expunged":
			default:
				t.Fatalf("characterization: unexpected draft_sent field %q - field set drift", k)
			}
		}
	})

	t.Run("manual send emits mail.panel.send", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, _ := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)

		rec := mailDo(env.mux, http.MethodPost, "/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages", nextMailIP(), true,
			`{"to":["a@b.test"],"subject":"s","body_markdown":"hello"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("BLOCKED behind the loopback-SMTP gap (D36 fake-server strategy): manual send = %d, want 200; body=%s - the production SMTP client demands trusted TLS (unconditional STARTTLS on non-465), so no local double can complete a send, and the mail.panel.send audit path cannot execute. Required by MC-19/D33.", rec.Code, rec.Body.String())
		}
		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.send"))
		ev := findAuditEvent(t, auditDir, "mail.panel.send")
		details := ev["details"].(map[string]any)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		for k := range details {
			switch k {
			case "workspace_id", "agent_id", "message_id", "recipients_count", "attachment_count", "sent_saved":
			default:
				t.Fatalf("characterization: unexpected send field %q - field set drift", k)
			}
		}
	})
}
