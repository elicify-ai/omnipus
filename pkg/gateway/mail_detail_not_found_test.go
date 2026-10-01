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

// The detail GET's 404 covers both ref forms when the addressed Drafts folder
// has no matching non-deleted message (contracts/openapi.yaml::getMailMessage).
func TestMailMessageDetail_MissingRefsAre404(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft, imap.FlagDeleted})
	currentRaw := strings.Replace(draftRaw, "<draft@example.test>", "<current@example.test>", 1)
	appendRaw(t, cl, "Drafts", []byte(currentRaw), []imap.Flag{imap.FlagDraft})
	uv := draftUIDValidity(t, cl)

	for _, tc := range []struct {
		name, path string
	}{
		{"deleted MID", mailMessagesPath("drafts") + "/mid%3A%3Cdraft%40example.test%3E"},
		{"absent MID", mailMessagesPath("drafts") + "/mid%3A%3Cabsent%40example.test%3E"},
		{"absent UID", draftRefPath(uv, 3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := mailDo(env.mux, http.MethodGet, tc.path, nextMailIP(), true, "")
			require.Equal(t, http.StatusNotFound, rec.Code, "missing draft must be 404, not an upstream 502: %s", rec.Body.String())
			require.Equal(t, "application/json", rec.Header().Get("Content-Type"), "404 must come from the mail handler")
			require.Equal(t, gen.ErrorResponse{Error: "message not found"}, decodeMailErr(t, rec), "404 must not disclose a server error class")
		})
	}

	t.Run("current MID is still readable", func(t *testing.T) {
		rec := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts")+"/mid%3A%3Ccurrent%40example.test%3E", nextMailIP(), true, "")
		require.Equal(t, http.StatusOK, rec.Code, "current draft must still be addressable: %s", rec.Body.String())
		var msg gen.MailMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg))
		require.Equal(t, int64(2), msg.Uid, "the second APPEND is the only non-deleted draft")
		require.Equal(t, "draft", msg.Subject, "the fixture's Subject header must survive the read")
	})
}

// A real IMAP authentication failure is not a missing message: MC-8 keeps it
// a sanitized 502 with its machine-readable upstream error class.
func TestMailMessageDetail_UpstreamAuthFailureIs502(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, _ := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	require.NoError(t, env.api.credStore.Set(mailboxCredKey(mailRedAgent, mailRedWS), "wrong-password"))

	rec := mailDo(env.mux, http.MethodGet, mailMessagesPath("drafts")+"/mid%3A%3Cabsent%40example.test%3E", nextMailIP(), true, "")
	require.Equal(t, http.StatusBadGateway, rec.Code, "failed IMAP login must stay an upstream 502: %s", rec.Body.String())
	er := decodeMailErr(t, rec)
	require.Equal(t, "mail server error: auth_failed", er.Error)
	require.NotNil(t, er.Code)
	require.Equal(t, "auth_failed", *er.Code)
	assertNoUpstreamLeak(t, rec.Body.String())
}
