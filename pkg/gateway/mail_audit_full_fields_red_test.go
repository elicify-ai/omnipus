// RED round 2 — MC-19 FULL audit fields (founder ruling relayed as D46:
// implement the spec's full field set, not the count-only shape the code
// currently emits). Oracle: spec email-mail-view-spec.md MC-19 (line 801) +
// FR-025 (line 1601): each mail panel audit event carries pair, folder,
// Message-ID, recipient ADDRESSES (incl. Bcc — MAJ-005), origin
// (human|agent-draft|owner-draft — MIN-006), argument hash and outcome —
// and, for attachment-bearing messages, per-attachment filenames and sizes
// (D33). Written from the spec before reading any handler.
//
// Derivations the spec makes and this file pins:
//   - origin describes the authorship of the draft the action acted upon:
//     "human" for the compose send (no draft involved); "agent-draft" when
//     the acted-on draft carries X-Omnipus-Draft; "owner-draft" when it does
//     not (the foreign-draft case MIN-006 names).
//   - attachments are recorded like the wire's own MailMessage attachment
//     shape: filename + size_bytes of the decoded part.
//   - arg_hash: MC-19 names "argument hash" without fixing the pre-image;
//     pinned as present, 64-char lowercase hex (the pkg/audit.ArgsHash
//     format — FR-080), deterministic for identical request args and
//     distinct for distinct args. The exact pre-image is a reported spec gap.
package gateway

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// argHashPattern is the FR-080 ArgsHash format: 64 lowercase hex chars.
var argHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// requireArgHash asserts the argument hash is present in the event details in
// the ArgsHash format.
func requireArgHash(t *testing.T, details map[string]any) string {
	t.Helper()
	raw, ok := details["arg_hash"]
	require.True(t, ok, "MC-19: event must carry the argument hash; details: %v", details)
	s, ok := raw.(string)
	require.True(t, ok, "arg_hash must be a string, got %T", raw)
	require.Regexp(t, argHashPattern, s, "arg_hash must be 64 lowercase hex (ArgsHash format)")
	return s
}

// requireRecipients asserts the event carries the full recipient ADDRESS list
// (MC-19: addresses, not a count), compared as exact set equality.
func requireRecipients(t *testing.T, details map[string]any, want ...string) {
	t.Helper()
	raw, ok := details["recipients"]
	require.True(t, ok, "MC-19: event must carry recipient addresses (incl. Bcc); details: %v", details)
	list, ok := raw.([]any)
	require.True(t, ok, "recipients must be a list, got %T", raw)
	got := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := e.(string)
		require.True(t, ok, "recipient entries must be strings, got %T", e)
		got = append(got, s)
	}
	require.ElementsMatch(t, want, got, "recipient addresses must match exactly (incl. Bcc)")
}

// requireAttachments asserts per-attachment filename+size records (D33).
func requireAttachments(t *testing.T, details map[string]any, want map[string]int) {
	t.Helper()
	raw, ok := details["attachments"]
	require.True(t, ok, "MC-19/D33: attachment-bearing send must record filenames and sizes; details: %v", details)
	list, ok := raw.([]any)
	require.True(t, ok, "attachments must be a list, got %T", raw)
	require.Len(t, list, len(want))
	got := map[string]int{}
	for _, e := range list {
		m, ok := e.(map[string]any)
		require.True(t, ok, "attachment entries must be objects, got %T", e)
		name, ok := m["filename"].(string)
		require.True(t, ok, "attachment filename must be a string")
		size, ok := m["size_bytes"].(float64)
		require.True(t, ok, "attachment size_bytes must be numeric")
		got[name] = int(size)
	}
	require.Equal(t, want, got, "attachment filenames and sizes must match exactly")
}

// composeDraftRaw builds a full RFC822 draft through the production
// composer and returns its transmitted bytes for APPENDing to Drafts.
// draft=true marks it an Omnipus (agent) draft (X-Omnipus-Draft);
// draft=false leaves the header off — the owner-authored (foreign) shape.
func composeDraftRaw(t *testing.T, draft bool, attName string, attData []byte) []byte {
	t.Helper()
	in := email.ComposeInput{
		From:     "mailbox@test.local",
		To:       []string{"owner-addr@example.test"},
		Subject:  "panel draft",
		Markdown: "draft body",
		Draft:    draft,
	}
	if attName != "" {
		in.Attachments = []email.Attachment{
			{Name: attName, ContentType: "application/pdf", Data: attData},
		}
	}
	out, err := email.Compose(in)
	require.NoError(t, err)
	return out.Transmitted
}

func TestMailAuditFullFields(t *testing.T) {
	t.Run("manual send carries recipients origin arg_hash attachments folder", func(t *testing.T) {
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, _ := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

		body := `{"to":["to1@example.test","to2@example.test"],` +
			`"cc":["cc1@example.test"],"bcc":["bcc-hidden@example.test"],` +
			`"subject":"MC-19 full fields","body_markdown":"full fields body",` +
			`"attachments":[{"filename":"q3-report.pdf","content_type":"application/pdf",` +
			`"data_base64":"` + b64Of(t, 1234) + `"}]}`
		rec := mailDo(env.mux, http.MethodPost,
			"/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages",
			nextMailIP(), true, body)
		require.Equal(t, http.StatusOK, rec.Code, "send must succeed (loopback SMTP); body: "+rec.Body.String())

		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.send"))
		details := requireAuditDetails(t, findAuditEvent(t, auditDir, "mail.panel.send"))

		requireRecipients(t, details,
			"to1@example.test", "to2@example.test", "cc1@example.test", "bcc-hidden@example.test")
		require.Equal(t, "human", details["origin"], "MC-19: a compose send originates from the human")
		requireArgHash(t, details)
		requireAttachments(t, details, map[string]int{"q3-report.pdf": 1234})
		require.Equal(t, "Sent", details["folder"], "FR-025: the event names the folder the send produced (Sent, D33)")
	})
}

// b64Of returns n deterministic bytes, base64-encoded, for attachment bodies.
func b64Of(t *testing.T, n int) string {
	t.Helper()
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// TestMailAuditFullFields_DraftPaths — the three draft-derived events with
// their full MC-19 field sets. Origin follows the acted-on draft's
// authorship: agent-draft (X-Omnipus-Draft present) vs owner-draft (header
// absent — MIN-006's foreign-draft case).
func TestMailAuditFullFields_DraftPaths(t *testing.T) {
	const attName = "q3-report.pdf"
	const attSize = 2048
	attData := make([]byte, attSize)
	for i := range attData {
		attData[i] = byte(i % 251)
	}

	setup := func(t *testing.T, draft bool) (*mailRedEnv, string, uint32) {
		t.Helper()
		env := newMailRedEnv(t)
		auditDir := mailAuditLogger(t, env)
		imapPort, cl := startPlainIMAP(t)
		sink := startSMTPSink(t)
		pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
		raw := composeDraftRaw(t, draft, attName, attData)
		appendRaw(t, cl, "Drafts", raw, []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)
		return env, auditDir, uv
	}

	t.Run("panel send of an agent draft records origin agent-draft with carried attachments", func(t *testing.T) {
		env, auditDir, uv := setup(t, true)
		rec := mailDo(env.mux, http.MethodPost, draftRefPath(uv, 1)+"/send", nextMailIP(), true,
			`{"uid":1,"uidvalidity":`+utoa(uv)+`,"to":["human-a@example.test"],`+
				`"cc":["human-c@example.test"],"bcc":["human-b@example.test"],`+
				`"subject":"s","body_markdown":"send it"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body: "+rec.Body.String())

		details := requireAuditDetails(t, findAuditEvent(t, auditDir, "mail.panel.draft_sent"))
		require.Equal(t, 1, countAuditEvents(t, auditDir, "mail.panel.draft_sent"))
		requireRecipients(t, details, "human-a@example.test", "human-c@example.test", "human-b@example.test")
		require.Equal(t, "agent-draft", details["origin"], "MIN-006/MC-19: the acted-on draft is an Omnipus draft")
		requireArgHash(t, details)
		requireAttachments(t, details, map[string]int{attName: attSize})
		require.Equal(t, "Drafts", details["folder"], "the send acts on a draft in Drafts")
	})

	t.Run("panel send of a foreign draft records origin owner-draft", func(t *testing.T) {
		env, auditDir, uv := setup(t, false)
		rec := mailDo(env.mux, http.MethodPost, draftRefPath(uv, 1)+"/send", nextMailIP(), true,
			`{"uid":1,"uidvalidity":`+utoa(uv)+`,"to":["owner@example.test"],"subject":"s","body_markdown":"send"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body: "+rec.Body.String())

		details := requireAuditDetails(t, findAuditEvent(t, auditDir, "mail.panel.draft_sent"))
		requireRecipients(t, details, "owner@example.test")
		require.Equal(t, "owner-draft", details["origin"], "MIN-006: a panel send of a FOREIGN draft")
		requireArgHash(t, details)
	})

	t.Run("panel edit records full fields with origin agent-draft", func(t *testing.T) {
		env, auditDir, uv := setup(t, true)
		rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true,
			`{"uid":1,"uidvalidity":`+utoa(uv)+`,"to":["edit-a@example.test"],`+
				`"bcc":["edit-b@example.test"],"subject":"edited","body_markdown":"new body"}`)
		require.Less(t, rec.Code, 300, "body: "+rec.Body.String())

		details := requireAuditDetails(t, findAuditEvent(t, auditDir, "mail.panel.draft_updated"))
		requireRecipients(t, details, "edit-a@example.test", "edit-b@example.test")
		require.Equal(t, "agent-draft", details["origin"])
		requireArgHash(t, details)
		require.Equal(t, "Drafts", details["folder"])
	})

	t.Run("panel discard records recipients origin and argument hash", func(t *testing.T) {
		env, auditDir, uv := setup(t, true)
		rec := mailDo(env.mux, http.MethodDelete, draftRefPath(uv, 1), nextMailIP(), true, "")
		require.Equal(t, http.StatusNoContent, rec.Code, "body: "+rec.Body.String())

		details := requireAuditDetails(t, findAuditEvent(t, auditDir, "mail.panel.draft_discarded"))
		// No request args exist for a discard: the recipients are the draft's
		// own stored addresses (the fixture draft's To header).
		requireRecipients(t, details, "owner-addr@example.test")
		require.Equal(t, "agent-draft", details["origin"])
		requireArgHash(t, details)
	})
}
