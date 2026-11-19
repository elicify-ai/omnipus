// pending_record_red_test.go — RED tests for epic #1094 slice S1a task T-11
// (the pending agent record, spec revision 7).
//
// Spec: docs/internal/specs/export-import-s1-spec.md rev 7 — FR-049 (the
// record is written as entities/agents/<id>.pending, invisible to every
// reader, and the import commits with one rename plus a directory sync),
// the Rollback contract mechanism steps 1–3 and C2, effect inventory E4/E7,
// "Not guaranteed" N2 (syncDir seam), D7 rows 1/3 (staging failures),
// row 22 (the rename seam), row 24 (orphan cleanup), test row 20b; and
// ADR-export-import-packages.md A9 (all-or-nothing). The methods under test
// are compile-only stubs (stub_for_red.go, "DELETE when GREEN lands"): every
// test here must fail on an assertion or a not-implemented error until GREEN
// lands, never on an undefined symbol.
//
// Fault injection uses the store's own stageFile/replaceFile seams (the same
// pattern state_test.go already uses) and the stub's package-level
// syncDirFn/publishRenameFn seams (nil in production). No sleeps, no network.
package agentstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pendingRecordPath(home, id string) string {
	return filepath.Join(RecordsDir(home), id+".pending")
}

func pendingAgentDir(home, id string) string {
	return filepath.Join(home, "agents", id)
}

func pendingTestAgent(id string) *config.AgentConfig {
	return &config.AgentConfig{ID: id, Name: "Pending " + id}
}

func pendingPathMissing(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

// TestCreatePendingState_WritesPendingRecord pins mechanism step 1: the whole
// agent record (entity + SOUL.md + AGENT.md) is written in ONE call, with the
// entity file named <id>.pending and the same staging discipline as
// CreateState. Import calls this before joining the workspace team, so a
// crash mid-import leaves no visible, half-built agent (A9 all-or-nothing,
// E4).
func TestCreatePendingState_WritesPendingRecord(t *testing.T) {
	home := t.TempDir()
	s := New(home)

	res, err := s.CreatePendingState("pendw1", pendingTestAgent("pendw1"), "the pending soul", "the pending agent md")
	require.NoError(t, err, "the pending record write must succeed")
	assert.Equal(t, PersistenceComplete, res.PersistenceStatus)

	// The entity record is at <id>.pending, byte-readable as the agent JSON.
	data, readErr := os.ReadFile(pendingRecordPath(home, "pendw1"))
	require.NoError(t, readErr, "the pending entity record must exist at <id>.pending")
	var stored config.AgentConfig
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, "pendw1", stored.ID)
	assert.Equal(t, "Pending pendw1", stored.Name)

	// SOUL.md and AGENT.md carry exactly the text the caller passed.
	soul, readErr := os.ReadFile(filepath.Join(pendingAgentDir(home, "pendw1"), "SOUL.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "the pending soul", string(soul))
	agentMD, readErr := os.ReadFile(filepath.Join(pendingAgentDir(home, "pendw1"), "AGENT.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "the pending agent md", string(agentMD))
}

// TestCreatePendingState_InvisibleToReaders pins FR-049's invisibility at the
// store boundary: a .pending record is returned by NO reader — Get, List and
// ReadState only ever see <id>.json (entity.Store lists and reads only
// *.json). Until the commit rename, the half-imported agent must not appear
// in any screen or tool (US-3 AS-15, test 20b).
func TestCreatePendingState_InvisibleToReaders(t *testing.T) {
	home := t.TempDir()
	s := New(home)

	_, err := s.CreatePendingState("pendv1", pendingTestAgent("pendv1"), "soul", "")
	require.NoError(t, err)

	_, err = s.Get("pendv1")
	require.ErrorIs(t, err, entity.ErrNotFound, "Get must not see a pending record")
	agents, _, listErr := s.List()
	require.NoError(t, listErr)
	for _, agent := range agents {
		assert.NotEqual(t, "pendv1", agent.ID, "List must not include a pending record")
	}
	_, err = s.ReadState("pendv1")
	require.ErrorIs(t, err, entity.ErrNotFound, "ReadState must not see a pending record")
}

// TestCreatePendingState_PartialWrites pins D7 rows 1/3 at the pending path:
// a staging failure persists NOTHING (PersistenceNone); a mid-sequence
// replace failure is reported as PersistencePartial with its error stage —
// and no reader can see any of it, so import's failure path can discard
// safely.
func TestCreatePendingState_PartialWrites(t *testing.T) {
	t.Run("a stage failure persists nothing", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		s.stageFile = func(path string, data []byte, mode os.FileMode) (string, error) {
			return "", errors.New("stage failed: disk full")
		}

		res, err := s.CreatePendingState("pendp1", pendingTestAgent("pendp1"), "soul", "")
		require.Error(t, err)
		require.ErrorContains(t, err, "stage failed", "the stage failure's own cause must surface to the caller")
		assert.Equal(t, "stage_entity", res.ErrorStage, "the stage that failed is named (same staging discipline as CreateState)")
		assert.Equal(t, PersistenceNone, res.PersistenceStatus)
		assert.True(t, pendingPathMissing(t, pendingRecordPath(home, "pendp1")), "no pending record is left behind")
		assert.True(t, pendingPathMissing(t, pendingAgentDir(home, "pendp1")), "no agent folder is left behind")
	})

	t.Run("a replace failure is PersistencePartial with its stage", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		calls := 0
		realReplace := s.replaceFile
		s.replaceFile = func(src, dst string) error {
			calls++
			if calls == 1 { // replace_entity: the first replace of the sequence
				return errors.New("replace failed: device busy")
			}
			return realReplace(src, dst)
		}

		res, err := s.CreatePendingState("pendp2", pendingTestAgent("pendp2"), "soul", "")
		require.Error(t, err)
		assert.Equal(t, PersistencePartial, res.PersistenceStatus)
		assert.Equal(t, "replace_entity", res.ErrorStage)

		// Whatever landed must be invisible and unpublished.
		assert.True(t, pendingPathMissing(t, filepath.Join(RecordsDir(home), "pendp2.json")),
			"a partial write must never produce a visible record")
		_, err = s.Get("pendp2")
		require.ErrorIs(t, err, entity.ErrNotFound)
	})
}

// TestPublishPending_IsTheCommitRename pins mechanism step 3 and FR-049's
// commit point: exactly one rename of <id>.pending to <id>.json (the moment
// the agent becomes visible), THEN one directory sync of the records folder
// before PublishPending answers (N2), and a refusal that never touches an
// already-published record.
func TestPublishPending_IsTheCommitRename(t *testing.T) {
	t.Run("the commit is one rename then one directory sync", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		_, err := s.CreatePendingState("pendc1", pendingTestAgent("pendc1"), "soul", "")
		require.NoError(t, err)

		var events []string
		publishRenameFn = func(from, to string) error {
			events = append(events, "rename "+from+" -> "+to)
			return nil
		}
		syncDirFn = func(dir string) error {
			events = append(events, "sync "+dir)
			return nil
		}
		t.Cleanup(func() { publishRenameFn = nil; syncDirFn = nil })

		require.NoError(t, s.PublishPending("pendc1"))

		require.Len(t, events, 2, "exactly one rename and one sync, in that order")
		assert.Equal(t, "rename "+pendingRecordPath(home, "pendc1")+" -> "+filepath.Join(RecordsDir(home), "pendc1.json"), events[0])
		assert.Equal(t, "sync "+RecordsDir(home), events[1], "the sync targets the records folder")

		// The rename is the commit point: the record is now a visible agent.
		got, err := s.Get("pendc1")
		require.NoError(t, err)
		assert.Equal(t, "Pending pendc1", got.Name)
		assert.True(t, pendingPathMissing(t, pendingRecordPath(home, "pendc1")), "the pending record is gone after the commit")
	})

	t.Run("a failed rename commits nothing and syncs nothing", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		_, err := s.CreatePendingState("pendc2", pendingTestAgent("pendc2"), "soul", "")
		require.NoError(t, err)

		renameErr := errors.New("rename failed: cross-device link")
		publishRenameFn = func(from, to string) error { return renameErr }
		syncCalls := 0
		syncDirFn = func(dir string) error { syncCalls++; return nil }
		t.Cleanup(func() { publishRenameFn = nil; syncDirFn = nil })

		require.ErrorIs(t, s.PublishPending("pendc2"), renameErr)
		assert.Equal(t, 0, syncCalls, "no directory sync runs after a failed rename")
		assert.False(t, pendingPathMissing(t, pendingRecordPath(home, "pendc2")), "the pending record survives a failed commit")
		assert.True(t, pendingPathMissing(t, filepath.Join(RecordsDir(home), "pendc2.json")), "nothing became visible")
		_, err = s.Get("pendc2")
		require.ErrorIs(t, err, entity.ErrNotFound)
	})

	t.Run("a published record is never clobbered", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		if _, err := s.CreateState("pendc3", &config.AgentConfig{ID: "pendc3", Name: "Published"}, "published soul"); err != nil {
			t.Fatal(err)
		}
		before, readErr := os.ReadFile(filepath.Join(RecordsDir(home), "pendc3.json"))
		require.NoError(t, readErr)

		require.ErrorContains(t, s.PublishPending("pendc3"), "already published", "publishing over a live agent is refused")
		after, readErr := os.ReadFile(filepath.Join(RecordsDir(home), "pendc3.json"))
		require.NoError(t, readErr)
		assert.Equal(t, string(before), string(after), "the published record must be byte-identical after the refusal")
	})

	t.Run("publishing with no pending record fails", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)

		require.ErrorContains(t, s.PublishPending("pendc4"), "no pending record", "there is nothing to commit")
		assert.True(t, pendingPathMissing(t, filepath.Join(RecordsDir(home), "pendc4.json")))
	})
}

// TestDiscardPending_RemovesRecordAndFolder pins C2: the discard removes the
// pending record and the agents/<id>/ folder with everything in it, never
// touches a published record, and is idempotent — so the rollback contract
// (and the orphan cleanup of D7 row 24) can run it unconditionally.
func TestDiscardPending_RemovesRecordAndFolder(t *testing.T) {
	t.Run("a pending record and its folder are removed", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		_, err := s.CreatePendingState("pendd1", pendingTestAgent("pendd1"), "soul", "")
		require.NoError(t, err)
		// An unrelated file in the agent folder must go with the folder (C2:
		// "delete agents/<id>/ with everything in it").
		unrelated := filepath.Join(pendingAgentDir(home, "pendd1"), "scratch.txt")
		require.NoError(t, os.WriteFile(unrelated, []byte("half-imported"), 0o600))

		require.NoError(t, s.DiscardPending("pendd1"))
		assert.True(t, pendingPathMissing(t, pendingRecordPath(home, "pendd1")))
		assert.True(t, pendingPathMissing(t, pendingAgentDir(home, "pendd1")), "the whole agent folder is removed")
		_, err = s.Get("pendd1")
		require.ErrorIs(t, err, entity.ErrNotFound)

		require.NoError(t, s.DiscardPending("pendd1"), "the discard is idempotent")
	})

	t.Run("a published record and its folder are never touched", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		if _, err := s.CreateState("pendd2", &config.AgentConfig{ID: "pendd2", Name: "Published"}, "published soul"); err != nil {
			t.Fatal(err)
		}

		require.ErrorContains(t, s.DiscardPending("pendd2"), "already published", "C2 must refuse to discard a live agent")
		_, err := s.Get("pendd2")
		require.NoError(t, err, "the published record survives the refused discard")
		soul, err := os.ReadFile(filepath.Join(pendingAgentDir(home, "pendd2"), "SOUL.md"))
		require.NoError(t, err)
		assert.Equal(t, "published soul", string(soul), "the published agent folder survives")
	})

	t.Run("an orphan pending record with no folder is cleaned up", func(t *testing.T) {
		home := t.TempDir()
		s := New(home)
		require.NoError(t, os.MkdirAll(RecordsDir(home), 0o700))
		require.NoError(t, os.WriteFile(pendingRecordPath(home, "pendd3"), []byte("{\"id\":\"pendd3\"}"), 0o600))

		require.NoError(t, s.DiscardPending("pendd3"), "an orphan pending record is cleaned up")
		assert.True(t, pendingPathMissing(t, pendingRecordPath(home, "pendd3")))
	})
}
