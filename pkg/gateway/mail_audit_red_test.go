package gateway

// T32 - TestMailAuditEvents (spec section 7 row 32, MC-19, D33).
// Round-2 fix-forward (D46) moved the full-field oracle to
// mail_audit_full_fields_red_test.go; round 3 (CHECK fixes) RESTORES the
// field pins the fix-forward dropped alongside it - uid/uidvalidity/
// attachment_count/expunged per event, exactly where the current event
// blocks in rest_mail_draft.go still emit them - and REPLACES the old
// exact-field-set drift guards (which rejected the new required D46 fields
// by construction) with closed-set guards written against the post-D46
// baseline: D46 fields PLUS the pre-existing fields each event still
// carries, so a FUTURE field addition/removal is still caught. This file
// keeps the spec-derived identity assertions: exactly one event per action,
// with pair and Message-ID. The two send subtests run against startSMTPSink
// (the loopback plaintext exception, 89e16221f, makes a local sink speakable).

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
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

// requireAuditFieldSet is the round-3 closed-set drift guard (CHECK finding
// F4): the event's details must carry EXACTLY the known field set - a future
// field addition or removal fails here, naming the drift. The wanted sets are
// the post-D46 characterization baseline: the MC-19/D46 full fields PLUS the
// pre-existing fields each event still carries, taken from the current
// event-construction blocks (rest_mail_draft.go, rest_mail_send.go). A drift
// guard's oracle IS "the field SET doesn't silently change", so deriving the
// set from current-correct code is the intended and only way to write one
// (characterization baseline); the VALUES inside those fields are spec-derived
// (MC-19/FR-025) and pinned in mail_audit_full_fields_red_test.go and in the
// per-subtest pins below.
func requireAuditFieldSet(t *testing.T, details map[string]any, event string, want ...string) {
	t.Helper()
	got := make([]string, 0, len(details))
	for k := range details {
		got = append(got, k)
	}
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	var unexpected, missing []string
	for _, k := range got {
		if !slices.Contains(wantSorted, k) {
			unexpected = append(unexpected, k)
		}
	}
	for _, k := range wantSorted {
		if !slices.Contains(got, k) {
			missing = append(missing, k)
		}
	}
	require.Empty(t, unexpected, "%s: unexpected detail fields - field-set drift (closed-set guard)", event)
	require.Empty(t, missing, "%s: missing detail fields - field-set drift (closed-set guard)", event)
}

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

func requireAuditDetails(t *testing.T, event map[string]any) map[string]any {
	t.Helper()
	raw, ok := event["details"]
	require.True(t, ok, "audit event must carry details: %v", event)
	details, ok := raw.(map[string]any)
	require.True(t, ok, "audit details must be an object, got %T", raw)
	return details
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
		details := requireAuditDetails(t, ev)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		require.Equal(t, "<audit-draft@example.test>", details["message_id"], "bracketed Message-ID of the edited draft")
		// Restored round-1 pins (round-2 CHECK: the D46 fix-forward dropped
		// real regression coverage along with the shape it legitimately had
		// to drop). uid: the NEW copy's server-assigned uid — the value is
		// server-assigned, so the pin is presence+sanity (characterization).
		if uid, ok := details["uid"].(float64); !ok || uid < 1 {
			t.Fatalf("uid = %v, want a nonzero uid reference (characterization: the event records the updated copy's uid)", details["uid"])
		}
		require.Equal(t, float64(uv), details["uidvalidity"], "same Drafts folder, same UIDVALIDITY")
		require.Equal(t, float64(0), details["attachment_count"], "the update request carried no attachments")
		// Closed-set drift guard at the post-D46 baseline (F4).
		requireAuditFieldSet(t, details, "mail.panel.draft_updated",
			"workspace_id", "agent_id", "message_id",
			"uid", "uidvalidity", "attachment_count",
			"recipients", "origin", "arg_hash", "folder", "attachments")
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
		details := requireAuditDetails(t, ev)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		require.Equal(t, "<audit-draft@example.test>", details["message_id"])
		// Restored round-1 pins (round-2 CHECK): the discard acts on the copy
		// the ref addressed, in the same folder. expunged is pinned as the
		// TRUTH-TELLING invariant, not a literal: the event must report
		// exactly whether the server actually expunged the copy (true iff
		// the server advertises UIDPLUS and UIDExpunge ran — the contract in
		// pkg/email/view.go::DeleteDraftStatus; round-1's unvalidated
		// `expunged == true` literal is false on this non-UIDPLUS fixture).
		require.Equal(t, float64(1), details["uid"], "the uid of the copy the discard addressed")
		require.Equal(t, float64(uv), details["uidvalidity"], "same Drafts folder, same UIDVALIDITY")
		require.Equal(t, cl.Caps().Has(imap.CapUIDPlus), details["expunged"],
			"expunged must tell the truth: true iff the server actually expunged the copy (UIDPLUS)")
		// Closed-set drift guard at the post-D46 baseline (F4).
		requireAuditFieldSet(t, details, "mail.panel.draft_discarded",
			"workspace_id", "agent_id", "message_id",
			"uid", "uidvalidity", "expunged",
			"recipients", "origin", "arg_hash", "folder")
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
		details := requireAuditDetails(t, ev)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		require.Equal(t, "<audit-draft@example.test>", details["message_id"])
		// Restored pins for the fields the event carries that round 1 never
		// pinned individually (round-2 CHECK): the fixture draft carries no
		// attachments, and expunged pinned as the truth-telling invariant
		// (same oracle as the discard pin above).
		require.Equal(t, float64(0), details["attachment_count"], "the fixture draft carries no attachments")
		require.Equal(t, cl.Caps().Has(imap.CapUIDPlus), details["expunged"],
			"expunged must tell the truth: true iff the server actually expunged the draft copy (UIDPLUS)")
		// Closed-set drift guard at the post-D46 baseline (F4).
		requireAuditFieldSet(t, details, "mail.panel.draft_sent",
			"workspace_id", "agent_id", "message_id",
			"recipients_count", "attachment_count", "sent_saved", "draft_cleanup_warning", "expunged",
			"recipients", "origin", "arg_hash", "folder", "attachments")
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
		details := requireAuditDetails(t, ev)
		require.Equal(t, mailRedWS, details["workspace_id"])
		require.Equal(t, mailRedAgent, details["agent_id"])
		// Closed-set drift guard at the post-D46 baseline (F4).
		requireAuditFieldSet(t, details, "mail.panel.send",
			"workspace_id", "agent_id", "message_id",
			"recipients_count", "attachment_count", "sent_saved",
			"recipients", "origin", "arg_hash", "folder", "attachments")
	})
}
