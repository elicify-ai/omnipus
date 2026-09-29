// Omnipus — FR-VA-032/034 P7: pending folder moves must fail closed when
// nested roots change or a case-only path aliases the destination.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/require"
)

func pendingFolderRetryFixture(t *testing.T, folder, dest, id string) (*restAPI, string, string) {
	t.Helper()
	api, ws, vault, _ := twoKBWorkspace(t)
	fromBase := folder + "/Projects.base"
	fromView := folder + "/Open.view"
	toBase := dest + "/Projects.base"
	toView := dest + "/Open.view"
	plantTrackedView(t, api.homePath, vault, fromBase, fromView)
	stagePendingViewMove(t, api.homePath, vault, id, folder, dest, true,
		pendingViewMember(fromBase, toBase, fromView, toView), time.Now().UTC())
	return api, ws, vault
}

func TestRetryMove_NestedKBAddedToPendingFolderMoveSubtreeAfterPending_RefusesPreflight(t *testing.T) {
	api, ws, vault := pendingFolderRetryFixture(t, "Folder", "Moved", redRetryID)
	nested := filepath.Join(vault, "Folder", "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	makeKnowledgeBase(t, nested, "Nested KB")
	plantTrackedView(t, api.homePath, nested, "Nested.base", "Nested.view")
	before := loadRecordedViews(t, api.homePath, vault)
	fileBefore, err := os.ReadFile(filepath.Join(vault, "Folder", "Open.view"))
	require.NoError(t, err)
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vault), "no pending receipt can be consumed")
	after, err := os.ReadFile(filepath.Join(vault, "Folder", "Open.view"))
	require.NoError(t, err)
	require.Equal(t, fileBefore, after)
	require.NoDirExists(t, filepath.Join(vault, "Moved"))
	require.Equal(t, "Nested.view", loadRecordedViews(t, api.homePath, nested).Bases["Nested.base"]["open"])
}

func TestRetryMove_OtherConcurrentPendingJournalGainsNestedKB_NeitherMoveReplayed(t *testing.T) {
	api, ws, vault := pendingFolderRetryFixture(t, "Alpha", "AlphaMoved", redRetryID)
	plantTrackedView(t, api.homePath, vault, "Beta/Projects.base", "Beta/Open.view")
	otherID := "test-other-pending-folder"
	stagePendingViewMove(t, api.homePath, vault, otherID, "Beta", "BetaMoved", true,
		pendingViewMember("Beta/Projects.base", "BetaMoved/Projects.base", "Beta/Open.view", "BetaMoved/Open.view"), time.Now().UTC())

	// Two membership receipts alone would not exercise the actual recovery
	// hazard: Renamer.RecoverPending replays EVERY pending journal. Persist two
	// genuine folder-rename plans before adding the tracked nested KB to Beta.
	collection, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), vault)
	require.NoError(t, err)
	store := knowledge.NewJournalStore(knowledge.DefaultJournalDir(vault))
	renamer := &knowledge.Renamer{Root: collection, Store: store}
	for _, subject := range []struct{ from, to string }{{"Alpha", "AlphaMoved"}, {"Beta", "BetaMoved"}} {
		plan, planErr := renamer.Plan(knowledge.RenameRequest{From: subject.from, To: subject.to, Folder: true})
		require.NoError(t, planErr)
		require.NoError(t, store.Write(plan.Journal))
	}
	journalsBefore, err := store.List()
	require.NoError(t, err)
	require.Len(t, journalsBefore, 2, "the other pending journal must really be recoverable")

	nested := filepath.Join(vault, "Beta", "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	makeKnowledgeBase(t, nested, "Nested KB")
	plantTrackedView(t, api.homePath, nested, "Nested.base", "Nested.view")
	before := loadRecordedViews(t, api.homePath, vault)
	alphaBytes, err := os.ReadFile(filepath.Join(vault, "Alpha", "Open.view"))
	require.NoError(t, err)
	betaBytes, err := os.ReadFile(filepath.Join(vault, "Beta", "Open.view"))
	require.NoError(t, err)

	// Chief's P7(b) ruling: "named paths only" limits authority re-enrollment;
	// recovery still sees both journals, so a tracked nested KB under Beta
	// must refuse Alpha's Retry BEFORE replaying either journal.
	failure := requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
	visible := failure.Error
	if failure.Paths != nil {
		visible += strings.Join(*failure.Paths, " ")
	}
	require.Contains(t, visible, "Beta/NestedKB", "the refusal must identify the blocking nested KB")
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vault), "both pending receipts must remain intact")
	journalsAfter, err := store.List()
	require.NoError(t, err)
	require.Equal(t, journalsBefore, journalsAfter, "neither pending rename journal may be consumed")
	require.NoDirExists(t, filepath.Join(vault, "AlphaMoved"))
	require.NoDirExists(t, filepath.Join(vault, "BetaMoved"))
	alphaAfter, err := os.ReadFile(filepath.Join(vault, "Alpha", "Open.view"))
	require.NoError(t, err)
	betaAfter, err := os.ReadFile(filepath.Join(vault, "Beta", "Open.view"))
	require.NoError(t, err)
	require.Equal(t, alphaBytes, alphaAfter)
	require.Equal(t, betaBytes, betaAfter)
	require.Equal(t, "Nested.view", loadRecordedViews(t, api.homePath, nested).Bases["Nested.base"]["open"])
}

func TestRetryMove_CaseOnlyFolderRenameFailsClosedOnCaseInsensitiveFilesystem(t *testing.T) {
	api, ws, vault := pendingFolderRetryFixture(t, "Foo", "foo", redRetryID)
	source, destination := filepath.Join(vault, "Foo"), filepath.Join(vault, "foo")
	fromStat, err := os.Stat(source)
	require.NoError(t, err)
	toStat, err := os.Stat(destination)
	if err != nil || !os.SameFile(fromStat, toStat) {
		t.Fatal("BLOCKED: P7(c) requires a case-insensitive filesystem for Foo/foo aliasing; this test's workspace filesystem distinguishes the two names. A case-sensitive pass would not prove the specified collision")
	}
	before := loadRecordedViews(t, api.homePath, vault)
	viewBytes, err := os.ReadFile(filepath.Join(source, "Open.view"))
	require.NoError(t, err)
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vault))
	after, err := os.ReadFile(filepath.Join(source, "Open.view"))
	require.NoError(t, err)
	require.Equal(t, viewBytes, after)
}
