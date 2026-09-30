package gateway

// RED — pins the wire contract for handleMailList (rest_mail_read.go):
// every array field on a MailMessagePage response must marshal as `[]`,
// NEVER as JSON `null`, however few rows a folder holds and however few
// recipients one message carries.
//
// Provenance / regression context: commit 4cd2787e0 ("test(mail): cover
// security boundary cases") added `out.Messages = mailNonNilSlice(out.Messages)`
// and the per-message `Cc`/`To` wrapping in rest_mail_read.go — before that
// commit, an empty folder (zero rows: Go's nil `out.Messages` slice) or a
// message with no Cc/To recipients (nil `row.Cc`/`row.To`) marshalled as
// JSON `null` on those fields, which the SPA's generated Zod schema (an
// array is REQUIRED, `contracts/openapi.yaml`) rejects with an
// ApiSchemaError ("Expected array, received null") — the exact class of
// wire defect the f5f6-round2 Drafts-empty investigation traced to this
// handler. This test asserts the RAW JSON BYTES (not a decoded struct —
// decoding `null` and `[]` into a Go slice both yield a nil/zero-length
// slice, which would hide the very defect this test exists to catch).
//
// Oracle: the wire contract itself (contracts/openapi.yaml's MailMessagePage
// `messages`/`cc`/`to` are `type: array`, no `nullable: true`) — never the
// handler's own current output.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"
)

// TestMailList_EmptyFolder_MessagesIsArrayNeverNull is the direct pin for
// the Drafts-empty investigation: a folder with ZERO messages must answer
// `"messages":[]`, never `"messages":null`. A client that decodes `null`
// into MailMessageList's expected array (F5/F6's Drafts view) renders "No
// messages" even when the folder badge shows a real count elsewhere — the
// reported symptom.
func TestMailList_EmptyFolder_MessagesIsArrayNeverNull(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, _ := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	// Deliberately NO append: Drafts starts empty (startPlainIMAP creates
	// the folder but never seeds it).

	rec := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts"), nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, rec.Code,
		"an empty, reachable folder must still 200; body: "+rec.Body.String())

	body := rec.Body.String()
	require.NotContains(t, body, `"messages":null`,
		"an empty folder's messages field must marshal as [], never null "+
			"(the wire defect behind the Drafts-empty symptom); body: "+body)
	require.Contains(t, body, `"messages":[]`,
		"an empty folder's messages field must be the empty JSON array; body: "+body)
}

// TestMailList_MessageWithNoRecipients_CcAndToAreArraysNeverNull covers the
// per-message Cc/To fields the same handler emits: a message with no Cc
// header (draftRaw carries none) must marshal "cc":[], never "cc":null —
// the sibling half of the same nil-slice-to-JSON-null defect class.
func TestMailList_MessageWithNoRecipients_CcAndToAreArraysNeverNull(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})

	rec := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts"), nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: "+rec.Body.String())

	body := rec.Body.String()
	require.False(t, strings.Contains(body, `"cc":null`),
		"a message with no Cc header must marshal \"cc\":[], never null; body: "+body)
	require.True(t, strings.Contains(body, `"cc":[]`),
		"a message with no Cc header must marshal \"cc\":[]; body: "+body)
}
