package gateway

// RED (combined feature-gate review, item 2, code-reviewer — IMPORTANT).
// Oracle: save_warning and draft_cleanup_warning are two independently
// meaningful response fields — the send handler itself sets each from a
// DIFFERENT failure (rest_mail_draft.go::handleMailDraftSendInner: SaveWarning
// from a failed Sent-folder AppendMessage; DraftCleanupWarning from a failed
// old-copy delete). The replay-record construction a few lines below
// (draftSendRecords[key] = draftSendRecord{..., saveWarning:
// cleanupWarningOf(resp), cleanupWarn: cleanupWarn, ...}) populates
// saveWarning from cleanupWarningOf(resp) — which reads
// resp.DraftCleanupWarning — never from resp.SaveWarning. On an idempotent
// replay of the same send, the reconstructed response therefore loses the
// real "Sent-folder save failed" warning text.
//
// Fixture: startIMAPNoSent (the existing MC-9/D18 fixture — INBOX + Drafts
// only, no Sent folder) makes the Sent-copy AppendMessage fail for real,
// producing a genuine non-empty SaveWarning; Drafts itself is healthy, so
// the old-copy delete succeeds and DraftCleanupWarning stays nil. A real
// SMTP sink lets the send itself succeed, so the replayed value traces to
// an ACTUAL prior send, not a synthetic one.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

const draftSendReplayRaw = "From: mailbox@test.local\r\nTo: a@b.test\r\nSubject: replay warning fixture\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <replay-warning-fixture@example.test>\r\n" +
	"X-Omnipus-Draft: yes\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n\r\nreplay warning body\r\n"

func TestMailDraftSend_ReplayPreservesOriginalSaveWarning(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort := startIMAPNoSent(t) // INBOX + Drafts only — the Sent-copy APPEND must fail
	cl, err := imapclient.DialInsecure(fmt.Sprintf("127.0.0.1:%d", imapPort), nil)
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, cl.Login("mailbox@test.local", "s3cret").Wait())

	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))
	appendRaw(t, cl, "Drafts", []byte(draftSendReplayRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)

	reqBody := fmt.Sprintf(`{"uid":1,"uidvalidity":%d,"to":["a@b.test"],"subject":"replay send","body_markdown":"replay body"}`, uv)
	path := draftRefPath(uv, 1) + "/send"

	first := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, reqBody)
	require.Equal(t, http.StatusOK, first.Code,
		"first send must succeed (SMTP delivers; only the Sent-copy save fails); body: "+first.Body.String())
	var firstResp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResp))
	require.False(t, firstResp.SentSaved, "instrument: the Sent-folder save must have failed (no Sent folder exists in this fixture)")
	require.NotNil(t, firstResp.SaveWarning, "instrument: a failed Sent-copy save must produce a non-empty save_warning")
	require.NotEmpty(t, *firstResp.SaveWarning)
	originalSaveWarning := *firstResp.SaveWarning
	require.Nil(t, firstResp.DraftCleanupWarning, "instrument: draft cleanup must have succeeded (no cleanup fault was injected)")

	// Replay: the identical request against the same ref — the idempotent
	// retry a client makes after e.g. a network blip on the original response.
	second := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, reqBody)
	require.Equal(t, http.StatusOK, second.Code, "the idempotent replay must also succeed; body: "+second.Body.String())
	var secondResp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondResp))

	require.NotNil(t, secondResp.SaveWarning,
		"the replayed response's save_warning is nil — the ORIGINAL save-warning text %q was lost on replay "+
			"(handleMailDraftSendInner stores saveWarning: cleanupWarningOf(resp), which reads "+
			"resp.DraftCleanupWarning, never resp.SaveWarning)", originalSaveWarning)
	if secondResp.SaveWarning != nil {
		require.Equal(t, originalSaveWarning, *secondResp.SaveWarning,
			"the replayed save_warning must be the ORIGINAL save-warning text — not empty, and not the "+
				"cleanup warning's text")
	}
}
