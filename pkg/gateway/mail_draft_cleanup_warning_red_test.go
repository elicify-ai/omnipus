package gateway

// Round-4 RED — item 2: the draft UPDATE must surface draft_cleanup_warning
// like the send path already does. The contract field just landed
// (contracts/components/schemas/MailMessage.yaml::draft_cleanup_warning):
// "Set when the OLD draft copy could not be removed after a successful
// update-APPEND of the new copy (MAJ-009/MC-28 — the panel warns of a
// possible duplicate draft). ... Null when cleanup succeeded, and always null
// on the read paths (GET message, list messages) — only the update endpoint
// can set it."
//
// Today pkg/gateway/rest_mail_draft.go::handleMailDraftUpdate deletes the old
// copy via client.DeleteDraft and, on failure, only LOGS (slog.Warn) — the
// response never carries the warning. The send path's precedent mechanism
// (rest_mail_draft.go::handleMailDraftSendInner: old-copy delete attempted
// after the successful send/APPEND; on failure the handler logs AND surfaces
// the warning on its response) is the oracle's mechanism.
//
// Fault injection: the fixture IMAP server wraps the memserver session and
// refuses exactly the "\Deleted flag-add" STORE that pkg/email/view.go::
// DeleteDraftStatus issues — so the update's APPEND succeeds and only the
// old-copy delete fails, the precise condition the contract names.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// errDraftDeleteFault is the fixture's refusal of the \Deleted STORE.
var errDraftDeleteFault = errors.New("fixture: \\Deleted flag store refused")

// draftDeleteFaultSession delegates everything to the memserver session
// except the \Deleted flag-add STORE, which it refuses — the single IMAP step
// the update path's old-copy delete depends on.
type draftDeleteFaultSession struct {
	imapserver.Session
}

func (s *draftDeleteFaultSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if flags != nil && flags.Op == imap.StoreFlagsAdd {
		for _, f := range flags.Flags {
			if f == imap.FlagDeleted {
				return errDraftDeleteFault
			}
		}
	}
	return s.Session.Store(w, numSet, flags, options)
}

// startFaultIMAP returns a memserver-backed fixture whose sessions refuse the
// \Deleted STORE, plus a logged-in client for seeding.
func startFaultIMAP(t *testing.T) (int, *imapclient.Client) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("mailbox@test.local", "s3cret")
	for _, name := range []string{"INBOX", "Sent", "Drafts"} {
		require.NoError(t, user.Create(name, nil))
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &draftDeleteFaultSession{Session: mem.NewSession()}, nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })
	go func() { _ = srv.Serve(ln) }()

	cl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	require.NoError(t, cl.Login("mailbox@test.local", "s3cret").Wait())
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port, cl
}

// draftUpdateRequest is the panel's update body for the copy the ref names.
func draftUpdateRequest(uv, uid uint32) string {
	return fmt.Sprintf(
		`{"uid":%d,"uidvalidity":%d,"to":["a@b.test"],"subject":"edited","body_markdown":"edited body","keep_attachment_parts":[]}`,
		uid, uv)
}

func TestMailDraftUpdate_SurfacesDraftCleanupWarning(t *testing.T) {
	t.Run("failed old-copy delete surfaces a non-empty warning", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, cl := startFaultIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true, draftUpdateRequest(uv, 1))
		// The update itself must SUCCEED: the APPEND of the new copy ran
		// before the delete; a delete failure is a warning, never an error
		// (same shape as the send path — "sent, but the draft copy stayed").
		require.Less(t, rec.Code, 300, "update must succeed despite the old-copy delete failure; body: "+rec.Body.String())
		var msg gen.MailMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg), "update response must decode as MailMessage")
		if msg.DraftCleanupWarning == nil {
			t.Fatalf("draft_cleanup_warning is null although the old-copy delete failed - the update response must surface the warning like the send path does (MAJ-009/MC-28); body: %s", rec.Body.String())
		}
		require.NotEmpty(t, *msg.DraftCleanupWarning,
			"the surfaced draft_cleanup_warning must be a non-empty string (the panel shows it)")
	})

	t.Run("successful old-copy delete leaves the warning null", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, cl)

		rec := mailDo(env.mux, http.MethodPut, draftRefPath(uv, 1), nextMailIP(), true, draftUpdateRequest(uv, 1))
		require.Less(t, rec.Code, 300, "update must succeed on a healthy folder; body: "+rec.Body.String())
		var msg gen.MailMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &msg))
		require.Nil(t, msg.DraftCleanupWarning,
			"draft_cleanup_warning must stay null when cleanup succeeded (contract: only the failed-cleanup case sets it); body: %s", rec.Body.String())
	})
}
