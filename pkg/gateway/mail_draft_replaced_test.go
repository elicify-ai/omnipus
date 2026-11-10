package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// US-7: a saved edit replaces the old UID even when the server defers expunge.
// The fixture deliberately does not advertise UIDPLUS, like the real-browser
// fakemail server: the old UID remains on IMAP with \Deleted set.
func TestMailDraftUpdate_OnlyCurrentCopyListsAndSends(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)

	oldPath := draftRefPath(uv, 1)
	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "draft update: %s", updated.Body.String())
	var newDraft gen.MailMessage
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &newDraft))
	require.Greater(t, newDraft.Uid, int64(1), "APPEND must return a new draft UID")
	require.Equal(t, int64(uv), newDraft.Uidvalidity)

	list := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts")+"?limit=1", nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, list.Code, "draft list: %s", list.Body.String())
	var page gen.MailMessagePage
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
	require.Len(t, page.Messages, 1, "only the edited draft may appear in the page")
	require.Equal(t, newDraft.Uid, page.Messages[0].Uid)
	require.False(t, page.Truncated, "deleted old UID must not count as another page")

	oldDetail := mailDo(env.mux, http.MethodGet, oldPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusNotFound, oldDetail.Code, "deleted old draft must not be visible by UID: %s", oldDetail.Body.String())
	newDetail := mailDo(env.mux, http.MethodGet, draftRefPath(uv, uint32(newDraft.Uid)), nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, newDetail.Code, "current draft must be viewable: %s", newDetail.Body.String())
	var current gen.MailMessage
	require.NoError(t, json.Unmarshal(newDetail.Body.Bytes(), &current))
	require.Equal(t, newDraft.Uid, current.Uid)
	require.Contains(t, current.BodyText, "edited body")

	oldSend := gen.MailDraftSendRequest{
		Uid: 1, Uidvalidity: int64(uv), To: []string{"a@example.test"},
		Subject: "draft", BodyMarkdown: "hello",
	}
	oldBody, err := json.Marshal(oldSend)
	require.NoError(t, err)
	oldResult := mailDo(env.mux, http.MethodPost, oldPath+"/send", nextMailIP(), true, string(oldBody))
	require.Equal(t, http.StatusConflict, oldResult.Code, "superseded draft must not send: %s", oldResult.Body.String())
	oldError := decodeMailErr(t, oldResult)
	require.NotNil(t, oldError.Code)
	require.Equal(t, "stale_draft", *oldError.Code)
	count, _ := sink.acceptedBodies()
	require.Zero(t, count, "stale send must not reach SMTP")

	newSend := gen.MailDraftSendRequest{
		Uid: newDraft.Uid, Uidvalidity: newDraft.Uidvalidity,
		To: []string{"a@b.test"}, Subject: "edited", BodyMarkdown: "edited body",
	}
	newBody, err := json.Marshal(newSend)
	require.NoError(t, err)
	sent := mailDo(env.mux, http.MethodPost, draftRefPath(uv, uint32(newDraft.Uid))+"/send", nextMailIP(), true, string(newBody))
	require.Equal(t, http.StatusOK, sent.Code, "current draft send: %s", sent.Body.String())
	count, bodies := sink.acceptedBodies()
	require.Equal(t, 1, count, "only the edited copy may reach SMTP")
	require.Len(t, bodies, 1)
	require.Contains(t, bodies[0], "edited body")
	require.NotContains(t, bodies[0], "hello", "the original body must not be transmitted")

	listAfterSend := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts"), nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, listAfterSend.Code, "draft list after send: %s", listAfterSend.Body.String())
	var afterPage gen.MailMessagePage
	require.NoError(t, json.Unmarshal(listAfterSend.Body.Bytes(), &afterPage))
	require.Empty(t, afterPage.Messages, "sent and superseded drafts must both disappear")
}

// Even when deleting a replaced draft fails, its unflagged predecessor must
// not reappear on a later page. A separate draft remains visible and defines
// the actual next-page cursor (US-7: the old copy is not a selectable draft).
func TestMailDraftList_HidesUnflaggedSupersededCopyAcrossPages(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startFaultIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	otherRaw := strings.Replace(draftRaw, "draft@example.test", "unrelated@example.test", 1)
	appendRaw(t, imapClient, "Drafts", []byte(otherRaw), []imap.Flag{imap.FlagDraft})
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)

	updated := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 2), nextMailIP(), true, draftUpdateRequest(uv, 2))
	require.Equal(t, http.StatusOK, updated.Code, "append replacement: %s", updated.Body.String())
	var current gen.MailMessage
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &current))
	require.NotNil(t, current.DraftCleanupWarning, "old UID 2 remains unflagged")
	require.Equal(t, int64(3), current.Uid)

	first := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts")+"?limit=1", nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, first.Code, "first draft page: %s", first.Body.String())
	var firstPage gen.MailMessagePage
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Messages, 1)
	require.Equal(t, current.Uid, firstPage.Messages[0].Uid)
	require.True(t, firstPage.Truncated, "the unrelated draft still needs a second page")
	require.NotNil(t, firstPage.NextBeforeUid)
	require.Equal(t, current.Uid, *firstPage.NextBeforeUid)

	second := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts")+"?limit=1&before_uid=3", nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, second.Code, "second draft page: %s", second.Body.String())
	var secondPage gen.MailMessagePage
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondPage))
	require.Len(t, secondPage.Messages, 1, "the unflagged UID 2 must not occupy the page")
	require.Equal(t, int64(1), secondPage.Messages[0].Uid)
	require.False(t, secondPage.Truncated, "the superseded UID must not count as a third page")
	require.Nil(t, secondPage.NextBeforeUid)
}

// If the old-copy STORE fails after APPEND, it remains unflagged. The warning
// does not make it safe to send a superseded revision of the same Message-ID.
func TestMailDraftSend_RejectsSupersededUnflaggedCopy(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startFaultIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)

	oldPath := draftRefPath(uv, 1)
	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "APPEND succeeds despite old-copy STORE failure: %s", updated.Body.String())
	var newDraft gen.MailMessage
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &newDraft))
	require.NotNil(t, newDraft.DraftCleanupWarning, "old copy was not marked deleted")
	require.Greater(t, newDraft.Uid, int64(1))

	oldSend := gen.MailDraftSendRequest{Uid: 1, Uidvalidity: int64(uv), To: []string{"a@example.test"}, Subject: "draft", BodyMarkdown: "hello"}
	body, err := json.Marshal(oldSend)
	require.NoError(t, err)
	result := mailDo(env.mux, http.MethodPost, oldPath+"/send", nextMailIP(), true, string(body))
	require.Equal(t, http.StatusConflict, result.Code, "older unflagged copy cannot be sent: %s", result.Body.String())
	resp := decodeMailErr(t, result)
	require.NotNil(t, resp.Code)
	require.Equal(t, "stale_draft", *resp.Code)
	count, bodies := sink.acceptedBodies()
	require.Zero(t, count, "no SMTP DATA for superseded UID; bodies=%s", strings.Join(bodies, ", "))
}

func TestMailDraftSend_RejectsDeletedOldCopy(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)
	oldPath := draftRefPath(uv, 1)
	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "draft update: %s", updated.Body.String())

	oldSend := gen.MailDraftSendRequest{Uid: 1, Uidvalidity: int64(uv), To: []string{"a@example.test"}, Subject: "draft", BodyMarkdown: "hello"}
	body, err := json.Marshal(oldSend)
	require.NoError(t, err)
	result := mailDo(env.mux, http.MethodPost, oldPath+"/send", nextMailIP(), true, string(body))
	require.Equal(t, http.StatusConflict, result.Code, "deleted copy must not be sent: %s", result.Body.String())
	resp := decodeMailErr(t, result)
	require.NotNil(t, resp.Code)
	require.Equal(t, "stale_draft", *resp.Code)
	count, _ := sink.acceptedBodies()
	require.Zero(t, count, "deleted copy must not reach SMTP")
}

func TestMailDraftRead_RejectsDeletedOldCopy(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)
	oldPath := draftRefPath(uv, 1)
	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "draft update: %s", updated.Body.String())

	oldDetail := mailDo(env.mux, http.MethodGet, oldPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusNotFound, oldDetail.Code, "deleted copy must not be viewable: %s", oldDetail.Body.String())
}

func TestMailDraftRead_RejectsSupersededUnflaggedCopy(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startFaultIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)
	oldPath := draftRefPath(uv, 1)
	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "draft update: %s", updated.Body.String())
	var newDraft gen.MailMessage
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &newDraft))
	require.NotNil(t, newDraft.DraftCleanupWarning, "the old UID stayed unflagged")

	oldDetail := mailDo(env.mux, http.MethodGet, oldPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusNotFound, oldDetail.Code, "unflagged predecessor must not be viewable: %s", oldDetail.Body.String())
	newDetail := mailDo(env.mux, http.MethodGet, draftRefPath(uv, uint32(newDraft.Uid)), nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, newDetail.Code, "newest copy must remain viewable: %s", newDetail.Body.String())
}

// RE-DERIVED for the metadata-only mint (2026-10-03). The pre-redesign
// oracle pinned the EAGER mint: a replaced draft's old ref was refused 404
// AT MINT — knowable only by dialing the mailbox at mint time. That
// behaviour was superseded by w5 spec US-6.3 acceptance item 3 ("it dials
// nothing: mint authorizes the pair and issues the ref-bound token; the
// first serve performs the fetch and surfaces 404 (missing) ... a mint that
// would have failed under today's eager fetch now fails at serve with the
// same safe classes") and Scenario B-26 ("the first serve ... surfaces the
// same safe 404 class the eager mint used to produce — the failure moved,
// it did not disappear"), both standing on §13 Q5's decided default: no
// mail dial at metadata mint. MC-13 orders exactly this re-derivation
// ("existing preview control assertions re-derived for metadata-only
// grants, never weakened"); the same treatment already re-derived
// TestMailPreviewMint_NinthIs429AndMintNeverDials.
// The protection is kept at the same strength, at the settled place: the
// stale ref is still never silently resolved — its first serve is refused
// 404 and leaks neither the old nor any other message's content — and the
// current draft still previews its edited body. The mint's zero-dial
// property itself is pinned by TestMailPreviewMint_NinthIs429AndMintNeverDials
// ("a mint dials nothing, successful or refused", US-6.3/MC-12) with an
// accept-counting listener; this test pins the dial-free mint's observable
// consequence for THIS case (a 200 + real token for a ref the mint has not
// checked) rather than duplicating that counter behind a forwarding proxy.
func TestMailDraftRead_StalePreviewRefIsRefusedAtServe(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, imapClient, "Drafts", []byte(mailPreviewHTMLRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)
	oldPath := draftRefPath(uv, 1)
	previewReq := gen.MailHtmlPreviewTokenRequest{
		WorkspaceId: mailRedWS, AgentId: mailRedAgent,
		Folder:     gen.MailHtmlPreviewTokenRequestFolderDrafts,
		MessageRef: strings.TrimPrefix(oldPath, mailMessagesPath("drafts")+"/"),
	}
	oldBody, err := json.Marshal(previewReq)
	require.NoError(t, err)
	before := mailDo(env.mux, http.MethodPost, mailPreviewMintPath, nextMailIP(), true, string(oldBody))
	require.Equal(t, http.StatusOK, before.Code, "the original draft has previewable HTML: %s", before.Body.String())

	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "draft update: %s", updated.Body.String())
	var current gen.MailMessage
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &current))

	// The dial-free metadata mint cannot know the old UID was replaced: it
	// still authorizes the ref and issues a token (US-6.3 item 3, §13 Q5).
	// A refusal here would require the forbidden at-mint mailbox dial —
	// that superseded assertion is gone, not moved.
	staleMint := mailDo(env.mux, http.MethodPost, mailPreviewMintPath, nextMailIP(), true, string(oldBody))
	require.Equal(t, http.StatusOK, staleMint.Code, "the metadata mint must not dial to refuse the stale ref (US-6.3/MC-12): %s", staleMint.Body.String())
	var staleToken gen.MailHtmlPreviewTokenResponse
	require.NoError(t, json.Unmarshal(staleMint.Body.Bytes(), &staleToken))
	require.NotEmpty(t, staleToken.Token, "the stale mint must issue a real token — an empty one would turn the serve 404 below into a routing artifact instead of the refusal")

	// B-26: the failure moved, it did not disappear. The first serve of the
	// stale grant fetches, finds the ref gone, and surfaces the same safe
	// 404 class the eager mint used to produce — leaking neither the old
	// draft's body nor any other message's content through the refusal.
	staleServe := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+staleToken.Token, nextMailIP(), false, "")
	require.Equal(t, http.StatusNotFound, staleServe.Code, "the stale ref must be refused at the serve, never silently resolved: %s", staleServe.Body.String())
	require.NotContains(t, staleServe.Body.String(), "Hello there", "the old draft's body must not be served")
	require.NotContains(t, staleServe.Body.String(), "edited body", "no other message's content may ride the stale refusal")

	previewReq.MessageRef = strings.TrimPrefix(draftRefPath(uv, uint32(current.Uid)), mailMessagesPath("drafts")+"/")
	newBody, err := json.Marshal(previewReq)
	require.NoError(t, err)
	newMint := mailDo(env.mux, http.MethodPost, mailPreviewMintPath, nextMailIP(), true, string(newBody))
	require.Equal(t, http.StatusOK, newMint.Code, "new preview ref must work: %s", newMint.Body.String())
	var minted gen.MailHtmlPreviewTokenResponse
	require.NoError(t, json.Unmarshal(newMint.Body.Bytes(), &minted))
	served := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+minted.Token, nextMailIP(), false, "")
	require.Equal(t, http.StatusOK, served.Code, "current HTML preview: %s", served.Body.String())
	require.Contains(t, served.Body.String(), "edited body")
	require.NotContains(t, served.Body.String(), "Hello there")
}

// An attachment route must not expose an old draft's bytes after Save, even
// when the physical UID remains present on a non-UIDPLUS server.
func TestMailDraftRead_RefusesStaleAttachmentDownload(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, imapClient, "Drafts", []byte(htmlAttachRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)
	oldPath := draftRefPath(uv, 1)
	attachmentPath := oldPath + "/attachments/1"
	before := mailDo(env.mux, http.MethodGet, attachmentPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, before.Code, "original attachment is downloadable: %s", before.Body.String())
	require.Contains(t, before.Body.String(), "alert(1)", "fixture must carry bytes that would leak on a stale download")

	updated := mailDo(env.mux, http.MethodPut, oldPath, nextMailIP(), true, draftUpdateRequest(uv, 1))
	require.Equal(t, http.StatusOK, updated.Code, "draft update: %s", updated.Body.String())
	stale := mailDo(env.mux, http.MethodGet, attachmentPath, nextMailIP(), true, "")
	require.Equal(t, http.StatusNotFound, stale.Code, "old attachment must not be downloadable: %s", stale.Body.String())
	require.NotContains(t, stale.Body.String(), "alert(1)")
}

// A successful send is replayable, including on a server without UIDPLUS
// that keeps the already-sent source UID flagged as deleted.
func TestMailDraftSend_ReplaysCompletedRevisionWithoutResending(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, imapClient := startPlainIMAP(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	appendRaw(t, imapClient, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, imapClient)
	path := draftRefPath(uv, 1) + "/send"
	req := gen.MailDraftSendRequest{Uid: 1, Uidvalidity: int64(uv), To: []string{"a@example.test"}, Subject: "draft", BodyMarkdown: "hello"}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	first := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, string(body))
	require.Equal(t, http.StatusOK, first.Code, "first send: %s", first.Body.String())
	second := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, string(body))
	require.Equal(t, http.StatusOK, second.Code, "replay: %s", second.Body.String())
	require.JSONEq(t, first.Body.String(), second.Body.String(), "replay must preserve the first outcome")
	count, _ := sink.acceptedBodies()
	require.Equal(t, 1, count, "replay must not transmit twice")
}
