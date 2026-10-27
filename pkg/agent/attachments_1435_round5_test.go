package agent

import (
	"bytes"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 5 item 4 — independent CHECK survivor M3.
//
// The committed round-4 control
// (attachments_1435_round4_test.go::workspace_mismatch_is_not_reported_unavailable)
// asks for a ref while the CALLER claims a different workspace. That is
// rejected by the media store's own membership guard
// (media.ValidateCallerWorkspace -> ErrCrossWorkspaceRef) BEFORE the
// workspace library is ever consulted, so the library's own
// ErrWorkspaceMismatch branch is never reached. A mutant that reports the
// library's mismatch sentinel as "the attachment is unavailable" therefore
// survives the whole committed suite.
//
// This case reaches the GENUINE library sentinel: the caller legitimately
// owns the ref's workspace (so the store's guard authorizes the lookup) and
// the library that answers does not own the ref (so the library's own
// FR-028a guard refuses it). A workspace mismatch is a security refusal, not
// a lost attachment: it must never be reported to the person as
// "that attachment is no longer available — re-attach it".

func issue1435R5CountNotices(h *wu22Harness) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	notices := 0
	for _, event := range h.events {
		if payload, ok := event.Payload.(ErrorPayload); ok && payload.Code == "media_unavailable" {
			notices++
		}
	}
	return notices
}

func issue1435R5CountPersisted(t *testing.T, h *wu22Harness) int {
	t.Helper()
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	persisted := 0
	for _, entry := range entries {
		if entry.ErrorCode == "media_unavailable" {
			persisted++
		}
	}
	return persisted
}

func TestIssue1435R5_LibraryWorkspaceMismatchIsNotReportedUnavailable(t *testing.T) {
	const workspace = "issue1435-r5-ws"
	const otherWorkspace = "issue1435-r5-ws-other"

	// The entry lives in the caller's own workspace library.
	lib, err := library.New(t.TempDir(), workspace)
	require.NoError(t, err)
	uploadRef, _, err := lib.UploadFixture("accepted.png", bytes.NewReader(issue1435PNG(t, 32, 32)))
	require.NoError(t, err)
	_, mediaID, ok := media.ParseWorkspaceRef(uploadRef)
	require.True(t, ok, "instrument: UploadFixture must return a workspace ref")
	ref := media.WorkspaceRefPrefix + workspace + "/" + mediaID

	// The library that answers the lookup does not own the ref: the
	// library's own FR-028a guard refuses with ErrWorkspaceMismatch.
	otherLib, err := library.New(t.TempDir(), otherWorkspace)
	require.NoError(t, err)
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	store.SetWorkspaceLibraryProvider(func(ws string) (media.WorkspaceLibraryResolver, error) {
		return otherLib, nil
	})

	// Instrument: prove the setup reaches the GENUINE library sentinel and
	// not the store's cross-workspace guard, so a green run cannot be
	// explained by a different branch being exercised.
	_, _, resolveErr := store.ResolveWithMetaOpts(ref, media.WithCallerWorkspace(workspace))
	require.ErrorIs(t, resolveErr, library.ErrWorkspaceMismatch,
		"instrument: the setup must reach the library's own workspace-mismatch refusal")
	require.NotErrorIs(t, resolveErr, media.ErrCrossWorkspaceRef,
		"instrument: the store's cross-workspace guard must NOT be what refuses this lookup")

	// The real resolution path: the agent turn, with the typed notice wired.
	h := wu22NewHarness(t, &issue1435R2Provider{})
	h.al.SetMediaStore(store)
	h.ts.opts.WorkspaceID = workspace // the caller legitimately owns the ref's workspace
	h.ts.media, h.ts.opts.Media = []string{ref}, []string{ref}
	h.ts.userMessage = "Inspect this attachment and continue with the text."

	rr := h.responseStage()
	rr.rq.ri.turnMediaStore = store
	rr.rq.ri.wsDir = t.TempDir()
	rp := &agentLoopRunTurnPrepare{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: rr}}}
	_, stopped := rp.assembleInitialContext()
	require.False(t, stopped, "instrument: initial assembly must complete")

	require.Equal(t, 0, issue1435R5CountNotices(h),
		"#1435 R5 item 4: a library workspace mismatch is a security refusal, not a lost attachment — it must NOT be reported as the unavailable notice")
	require.Equal(t, 0, issue1435R5CountPersisted(t, h),
		"#1435 R5 item 4: no unavailable notice may be persisted for a workspace mismatch")
}
