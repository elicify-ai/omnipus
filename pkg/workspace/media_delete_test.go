package workspace

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// media_delete_test.go — re-review FIX 2 regression tests.
//
// A prior wave made WorkspaceDeleteHook always return a non-500-worthy
// success (204) for a cascade whose media library was cascade-deleted
// successfully. The re-review found that library.CascadeDelete can return
// a non-nil error TOGETHER WITH a fully-populated `deleted`/`bytesFreed`
// when every manifest entry was correctly removed and only a final
// on-disk unlink of an already-quarantined file failed afterward — and
// rest_workspaces.go's handleWorkspaceDelete was treating THAT case
// identically to a genuine hard cascade failure, 500ing a delete that had
// actually fully succeeded (the straggler file lives under wsDir/media/,
// which the handler's own unconditional os.RemoveAll(wsDir) cleans up
// moments later regardless).
//
// wrapCascadeError is the pure discriminator this file introduces to tell
// the two cases apart. It is tested at two levels here:
//   - Unit: synthetic (deleted, cascadeErr) inputs, no filesystem.
//   - Integration: a REAL *library.Library, using library.go's own
//     exported WithFileRemover test seam to force a genuine final-unlink
//     failure, proving CascadeDelete really does return the
//     (non-empty deleted, non-nil err) shape wrapCascadeError assumes.
//     This seam is not reachable through WorkspaceDeleteHook's own
//     un-optioned library.New(home, workspaceID) call, which is why
//     WorkspaceDeleteHook itself cannot be exercised end-to-end into this
//     branch from a test — see wrapCascadeError's doc comment in
//     media_delete.go.

func TestWrapCascadeError_StragglerWrapsWhenDeletedNonEmpty(t *testing.T) {
	deleted := []gen.MediaLibraryEntry{{Filename: "a.txt"}}
	cascadeErr := errors.New("final unlink failed")

	wrapped := wrapCascadeError(deleted, cascadeErr)

	require.Error(t, wrapped)
	assert.True(t, errors.Is(wrapped, ErrCascadeStraggler),
		"a non-nil error with a non-empty deleted slice must be reported as a straggler")
	assert.True(t, errors.Is(wrapped, cascadeErr),
		"the original cascadeErr must still be inspectable via errors.Is on the wrapped error")
	assert.Contains(t, wrapped.Error(), cascadeErr.Error(),
		"the original error text must survive in the wrapped error's message")
}

func TestWrapCascadeError_HardFailureUnwrapped(t *testing.T) {
	cascadeErr := errors.New("quarantine rename failed")

	// Empty deleted slice — every hard-failure exit of CascadeDelete
	// (quarantine-rename failure in phase 1, or a persist failure in phase
	// 2 that rolls the manifest back) returns this shape.
	wrapped := wrapCascadeError(nil, cascadeErr)

	assert.Same(t, cascadeErr, wrapped,
		"a hard failure (nothing committed) must be returned unwrapped, unchanged")
	assert.False(t, errors.Is(wrapped, ErrCascadeStraggler),
		"a hard failure must never be misclassified as a benign straggler")
}

func TestWrapCascadeError_NilCascadeErrNeverWrapped(t *testing.T) {
	assert.NoError(t, wrapCascadeError(nil, nil))
	assert.NoError(t, wrapCascadeError([]gen.MediaLibraryEntry{{Filename: "x"}}, nil),
		"the ordinary successful case (deleted populated, no error) must never be wrapped")
}

// TestCascadeDelete_FinalUnlinkFailure_ReturnsNonEmptyDeletedWithError is
// the integration half: proves library.go's real CascadeDelete returns the
// exact (non-empty deleted, non-nil err) shape wrapCascadeError's
// discriminator assumes, under a genuine final-unlink failure forced via
// library.WithFileRemover.
func TestCascadeDelete_FinalUnlinkFailure_ReturnsNonEmptyDeletedWithError(t *testing.T) {
	home := t.TempDir()
	workspaceID := "ws-straggler"

	failUnlink := errors.New("final unlink boom")
	lib, err := library.New(home, workspaceID, library.WithFileRemover(func(path string) error {
		if strings.Contains(path, ".cascade-") {
			return failUnlink
		}
		return os.Remove(path)
	}))
	require.NoError(t, err)

	_, _, uploadErr := lib.Upload("note.txt", gen.UserUpload, strings.NewReader("bytes"))
	require.NoError(t, uploadErr)

	deleted, bytesFreed, cascadeErr := lib.CascadeDelete()

	require.Error(t, cascadeErr, "a final-unlink failure must be reported, not swallowed")
	require.NotEmpty(t, deleted, "the manifest commit must have gone through despite the unlink failure")
	assert.Equal(t, "note.txt", deleted[0].Filename)
	assert.Positive(t, bytesFreed)

	// wrapCascadeError, given exactly this real return shape, must produce
	// the straggler classification.
	wrapped := wrapCascadeError(deleted, cascadeErr)
	assert.True(t, errors.Is(wrapped, ErrCascadeStraggler))

	// The manifest commit is real: a freshly re-opened library (simulating
	// the REST handler's later os.RemoveAll + the client's own follow-up
	// state check) sees zero entries.
	lib2, err := library.New(home, workspaceID)
	require.NoError(t, err)
	assert.Empty(t, lib2.List())
}
