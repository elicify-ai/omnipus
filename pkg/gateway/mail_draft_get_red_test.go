package gateway

// Bug fix RED — GET on a Drafts-folder message detail (US-7, spec §2.3).
//
// rest_mail.go::handleWorkspaceMail's drafts case (len(tail)==5,
// tail[2]=="drafts") shadows the generic read case for EVERY method,
// including GET — it dispatches straight to handleMailDraftAction, which
// answers only PUT/DELETE and 405s everything else. The Mail panel's
// Drafts folder could therefore never open a draft to edit it: the human
// half of US-7 (agent drafts, human opens/edits/sends) was unreachable.
//
// This is a Go dispatch bug, not a contract gap: the generic path
// /workspaces/{id}/mail/{agentId}/folders/{folder}/messages/{ref}
// (contracts/openapi.yaml) already enumerates folder=drafts as a valid
// value for its GET, and defines no `get` verb on the literal
// .../folders/drafts/messages/{ref} path item at all — GET on that URL was
// always meant to answer through the generic read handler
// (rest_mail_read.go::handleMailFolderMessage), while PUT/DELETE/the send
// POST answer through the draft-action handlers. Only the switch's method
// gate needed fixing.
//
// Oracle provenance: is_draft/folder/subject/body values are asserted
// against the fixture's OWN raw RFC 5322 bytes (draftRaw, mail_fixture_red_
// test.go) and the \Draft flag passed to appendRaw — never read off the
// handler under test. The unsupported-method and PUT-still-works checks
// pin the OTHER two methods the same case dispatches, so a fix that
// over-widens GET into swallowing PUT/DELETE/PATCH fails here too.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestMailDraftDetail_GetReachesReadPath(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)
	path := draftRefPath(uv, 1)

	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, rec.Code,
		"US-7: GET on a Drafts message must reach the read path (200), not "+
			"the draft-mutation dispatch's 405; body: "+rec.Body.String())

	var msg gen.MailMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg),
		"GET drafts detail must decode as the generated MailMessage shape")

	require.True(t, msg.IsDraft,
		"the appended copy carries \\Draft (imap.FlagDraft); is_draft must be true")
	require.Equal(t, gen.MailMessageFolder("drafts"), msg.Folder,
		"the response's folder must name the folder it was read from")
	require.Equal(t, "draft", msg.Subject,
		"subject must be the fixture's own Subject header, decoded")
	require.Contains(t, msg.BodyText, "hello",
		"body_text must carry the fixture's own plain-text body")
}

func TestMailDraftDetail_PutStillUpdates(t *testing.T) {
	// Guard: the GET fix must not swallow the sibling PUT case the same
	// switch arm dispatches.
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)
	path := draftRefPath(uv, 1)

	body := `{"uid":1,"uidvalidity":` + utoa(uv) + `,"to":["a@example.test"],"subject":"updated","body_markdown":"body","keep_attachment_parts":[]}`
	rec := mailDo(env.mux, http.MethodPut, path, nextMailIP(), true, body)
	require.Equal(t, http.StatusOK, rec.Code,
		"PUT on the same drafts message path must still update; body: "+rec.Body.String())
}

func TestMailDraftDetail_UnsupportedMethodStill405(t *testing.T) {
	// Guard: a method neither the read path nor the draft actions answer
	// must still 405 (never silently fall through to the read handler).
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)
	path := draftRefPath(uv, 1)

	rec := mailDo(env.mux, http.MethodPatch, path, nextMailIP(), true, "")
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"PATCH on a drafts message path must still 405; body: "+rec.Body.String())
}
