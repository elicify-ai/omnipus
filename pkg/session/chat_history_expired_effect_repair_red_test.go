package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gate0RepairCommandBinary(t *testing.T) string {
	t.Helper()
	name := "gate0-real-history-repair"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-tags", "goolm,stdjson", "-o", binary, "github.com/elicify-ai/omnipus/cmd/chat-history-index-repair")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "actual standalone repair build, not a fake dry-run: %s", output)
	t.Log("F3 standalone repair build exit0")
	return binary
}

func gate0RepairDryRun(t *testing.T, binary, home string, want int) string {
	t.Helper()
	cmd := exec.Command(binary, "dry-run", "--data-root", home)
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "real dispatched command, never missing executable accepted as refusal")
		code = exit.ExitCode()
	}
	t.Logf("F3 actual CLI dry-run exit=%d output=%s", code, output)
	assert.Equal(t, want, code, "F3/D13: proven retention expiry is NOT invalid_reference; unexplained targets still refuse")
	return string(output)
}

// Gate0 F3 / D13 one constructor + D14 proven-expiry distinction. Both the
// actual command and actual shared engine must agree with healthy indexed
// paging after retention, including an expired effect targeted by a retract.
func TestChatHistoryRepair_ProvenExpiredEffectsAreNotInvalidReferences(t *testing.T) {
	binary := gate0RepairCommandBinary(t)
	for _, kind := range []string{"settle", "project", "retract"} {
		t.Run(kind+"_target_proven_expired", func(t *testing.T) {
			s, sid, home, retained := gate0ExpiredEffectFixture(t, kind)
			canonical := partitionBytes(t, s, sid)
			gate0RepairDryRun(t, binary, home, 0)
			build, err := PrepareChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid), ChatHistoryIndexBuildOptions{})
			if assert.NoError(t, err, "F3: shared offline reconstruction must ignore ONLY references proven expired by validated retention facts") {
				require.NotNil(t, build)
				require.NoError(t, build.Publish(context.Background()))
				require.NoError(t, build.Close())
				require.NoError(t, VerifyChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid)))
				reopened, openErr := NewUnifiedStoreWithHome(s.BaseDir(), home)
				require.NoError(t, openErr)
				t.Cleanup(func() { require.NoError(t, reopened.Close()) })
				page := historyRead(t, reopened, sid, historyOptions(100))
				require.Equal(t, []string{"retained-one"}, historyIDs(page))
				require.Equal(t, historyExpectedPosition(sid, retained), page.Entries[0].Position, "reconstruction never renumbers retained original positions")
			}
			require.Equal(t, canonical, partitionBytes(t, s, sid), "offline dry-run/build never rewrite canonical/effect bytes")
		})
	}
	t.Run("unexplained_missing_target_is_still_invalid_reference", func(t *testing.T) {
		home := t.TempDir()
		s, err := NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		meta, err := s.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		addresses := historyRows(t, s, meta.ID, "real-owner", 1)
		require.Len(t, historyRead(t, s, meta.ID, historyOptions(100)).Entries, 1)
		missing := addresses[0]
		missing.EntryID = "never-existed-owner" // same retained partition, no expiry proof can authorize this reference
		record, err := newEffectRecord(ToolCallEffect{Settle: &ToolCallSettle{Target: missing, IfStatus: "pending",
			ToolCall: ToolCall{ID: "missing-call", Tool: "read_file", Status: "success"}}})
		require.NoError(t, err)
		archive, err := s.archiveStore(meta.ID)
		require.NoError(t, err)
		_, err = archive.Append(record) // deliberate canonical-reference corruption at the filesystem edge, not a success oracle
		require.NoError(t, err)
		require.NoError(t, s.Close())
		before := partitionBytes(t, s, meta.ID)
		_, err = PrepareChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), meta.ID), ChatHistoryIndexBuildOptions{})
		require.Error(t, err)
		var classified interface{ HistoryIndexBuildClass() string }
		require.ErrorAs(t, err, &classified)
		require.Equal(t, "invalid_reference", classified.HistoryIndexBuildClass())
		out := gate0RepairDryRun(t, binary, home, 1)
		require.Contains(t, out, "class=invalid_reference")
		require.Equal(t, before, partitionBytes(t, s, meta.ID))
	})
}
