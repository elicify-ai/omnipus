package agent

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 4 item 2 (architect F4): a DELETED workspace-library
// entry resolves through media.ErrNotFound's sibling — library.ErrNotFound,
// forwarded unchanged by resolveWorkspaceRef — and must produce the SAME
// typed unavailable/re-attach notice (once per ref per turn) plus the
// content marker. A library error that is NOT not-found (workspace
// mismatch) must never be reported as "unavailable".
func TestIssue1435R4_DeletedWorkspaceLibraryEntryGetsNotice(t *testing.T) {
	workspace := "issue1435-r4-ws"
	newLibrary := func(t *testing.T) *library.Library {
		t.Helper()
		lib, err := library.New(t.TempDir(), workspace)
		require.NoError(t, err)
		return lib
	}
	countNotices := func(h *wu22Harness) int {
		h.mu.Lock()
		defer h.mu.Unlock()
		var notices []ErrorPayload
		for _, event := range h.events {
			if payload, ok := event.Payload.(ErrorPayload); ok && payload.Code == "media_unavailable" {
				notices = append(notices, payload)
			}
		}
		return len(notices)
	}
	countPersisted := func(t *testing.T, h *wu22Harness) int {
		t.Helper()
		entries, err := h.store.ReadTranscript(h.sid)
		require.NoError(t, err)
		var persisted []session.TranscriptEntry
		for _, entry := range entries {
			if entry.ErrorCode == "media_unavailable" {
				persisted = append(persisted, entry)
			}
		}
		return len(persisted)
	}

	t.Run("deleted_entry_notifies_once_per_ref_per_turn", func(t *testing.T) {
		h := wu22NewHarness(t, &issue1435R2Provider{})
		lib := newLibrary(t)
		uploadRef, _, err := lib.UploadFixture("accepted.png", bytes.NewReader(issue1435PNG(t, 32, 32)))
		require.NoError(t, err)
		_, mediaID, ok := media.ParseWorkspaceRef(uploadRef)
		require.True(t, ok, "instrument: UploadFixture must return a workspace ref")
		ref := media.WorkspaceRefPrefix + workspace + "/" + mediaID
		store := media.NewFileMediaStore()
		t.Cleanup(store.Stop)
		store.SetWorkspaceLibraryProvider(func(ws string) (media.WorkspaceLibraryResolver, error) {
			require.Equal(t, workspace, ws, "instrument: the provider is asked for the ref's own workspace")
			return lib, nil
		})
		h.al.SetMediaStore(store)
		h.ts.opts.WorkspaceID = workspace // the caller owns the ref's workspace
		h.ts.media, h.ts.opts.Media = []string{ref}, []string{ref}
		h.ts.userMessage = "Inspect this attachment and continue with the text."

		// Delete the entry: the conversation's accepted attachment is gone.
		_, err = lib.Delete(mediaID)
		require.NoError(t, err)

		rr := h.responseStage()
		rr.rq.ri.turnMediaStore = store
		rr.rq.ri.wsDir = t.TempDir()
		rp := &agentLoopRunTurnPrepare{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: rr}}}
		// Two real resolution passes on ONE turn state (initial assembly
		// re-run, as the production sites re-resolve): the notice must stay
		// at exactly one per ref per turn.
		for range 2 {
			_, stopped := rp.assembleInitialContext()
			require.False(t, stopped, "instrument: initial assembly must complete")
		}
		require.Equal(t, 1, countNotices(h),
			"#1435 R4 item 2: a deleted workspace-library attachment must produce exactly one typed notice per ref per turn")
		require.Equal(t, 1, countPersisted(t, h),
			"#1435 R4 item 2: the deleted-library notice must persist exactly once for replay")
		var marked string
		for _, message := range rr.rq.ri.messages {
			if strings.Contains(message.Content, "accepted.png") && strings.Contains(message.Content, "[attachment unavailable:") {
				marked = message.Content
			}
		}
		require.Contains(t, marked, "[attachment unavailable:",
			"#1435 R4 item 2: the model-facing marker must accompany the notice, as for a missing upload")
	})

	t.Run("workspace_mismatch_is_not_reported_unavailable", func(t *testing.T) {
		h := wu22NewHarness(t, &issue1435R2Provider{})
		lib := newLibrary(t)
		uploadRef, _, err := lib.UploadFixture("accepted.png", bytes.NewReader(issue1435PNG(t, 32, 32)))
		require.NoError(t, err)
		_, mediaID, ok := media.ParseWorkspaceRef(uploadRef)
		require.True(t, ok, "instrument: UploadFixture must return a workspace ref")
		ref := media.WorkspaceRefPrefix + workspace + "/" + mediaID
		store := media.NewFileMediaStore()
		t.Cleanup(store.Stop)
		store.SetWorkspaceLibraryProvider(func(ws string) (media.WorkspaceLibraryResolver, error) {
			return lib, nil
		})
		h.al.SetMediaStore(store)
		// The entry still exists; the caller claims a DIFFERENT workspace —
		// library.ErrWorkspaceMismatch, never "not found".
		other := workspace + "-other"
		h.ts.opts.WorkspaceID = other
		h.ts.media, h.ts.opts.Media = []string{ref}, []string{ref}
		h.ts.userMessage = "Inspect this attachment."

		rr := h.responseStage()
		rr.rq.ri.turnMediaStore = store
		rr.rq.ri.wsDir = t.TempDir()
		rp := &agentLoopRunTurnPrepare{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: rr}}}
		_, stopped := rp.assembleInitialContext()
		require.False(t, stopped, "instrument: initial assembly must complete")
		require.Equal(t, 0, countNotices(h),
			"#1435 R4 item 2: a library mismatch error is a different cause and must NOT surface as the unavailable notice")
		require.Equal(t, 0, countPersisted(t, h))
	})
}

// Issue #1435 Round 4 item 3 (PRT F4): the normalize cache key must keep
// distinguishing budgets that differ ONLY in the patch ceiling. Same bytes,
// model slot, byte limit and edge limit; loose ceiling, tighter ceiling,
// tighter ceiling again. The tight result must be respected on the second
// call (no unsafe reuse of the loose artifact) and the third call must
// reuse the TIGHT artifact (identical bytes), never the loose one.
func TestIssue1435R4_PatchLimitOnlyBudgetMissesCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch-only.png")
	// 512×512 = 16×16 = 256 patches: the loose ceiling keeps it whole, the
	// tight ceiling forces a real downscale (512→384→288, long edge ≥ the
	// 256-px ladder floor), so the two budgets produce different artifacts.
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, 512, 512))))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	const (
		slot    = "issue1435-r4-patch-only"
		maxSize = 10 << 20
	)
	loose := catalog.ResizeLimits{LongEdgePx: 7680, MaxBytes: maxSize, MaxPatches: 30000}
	tight := catalog.ResizeLimits{LongEdgePx: 7680, MaxBytes: maxSize, MaxPatches: 100}

	first := encodeImageToDataURLCached(path, "image/png", info, maxSize, slot, loose)
	second := encodeImageToDataURLCached(path, "image/png", info, maxSize, slot, tight)
	third := encodeImageToDataURLCached(path, "image/png", info, maxSize, slot, tight)

	firstWidth, firstHeight, _ := issue1435DecodeOutboundImage(t, first)
	require.Equal(t, 512, firstWidth, "loose ceiling keeps the full source (256 patches fits)")
	require.Equal(t, 512, firstHeight)

	secondWidth, secondHeight, _ := issue1435DecodeOutboundImage(t, second)
	require.LessOrEqual(t, issue1435PatchCount(secondWidth, secondHeight), 100,
		"#1435 R4 item 3: the tighter patch ceiling must be respected, not served from the loose cache entry")
	require.LessOrEqual(t, max(secondWidth, secondHeight), 320,
		"#1435 R4 item 3: tight output must be a real downscale of the same source")
	require.Greater(t, max(secondWidth, secondHeight), 256,
		"#1435 R4 item 3: the tight downscale must stay above the resize ladder floor")

	thirdWidth, thirdHeight, _ := issue1435DecodeOutboundImage(t, third)
	require.Equal(t, secondWidth, thirdWidth, "tight reuse must stay tight")
	require.Equal(t, secondHeight, thirdHeight)
	require.Equal(t, second, third,
		"#1435 R4 item 3: the third call must reuse the TIGHT artifact byte-identically, never the loose one")
}
