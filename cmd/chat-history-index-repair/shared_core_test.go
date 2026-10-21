package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func repairCanonicalFacts(t *testing.T, root string) map[string]repairFileFact {
	t.Helper()
	out := repairTree(t, root)
	for path, f := range out {
		parts := strings.Split(path, "/")
		derived := false
		for _, p := range parts {
			if p == session.ChatHistoryIndexDirectory {
				derived = true
			}
		}
		if derived {
			delete(out, path)
			continue
		}
		if f.Mode.IsDir() {
			f.Mtime = 0
			f.Size = 0
			out[path] = f
		} // D13 excludes directory mtime, not canonical file mtime.
	}
	return out
}

// T22/T25: shared constructor, actual publish/verify and actual bounded reader.
// The injected small core fixture weights are not a replacement gateway meter.
func repairSharedCoreRoundTrip(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	dirs, err := os.ReadDir(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	var sid string
	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			require.Empty(t, sid, "fixture has exactly one canonical session")
			sid = d.Name()
		}
	}
	require.NotEmpty(t, sid)
	dir := filepath.Join(root, "sessions", sid)
	before := repairCanonicalFacts(t, root)
	require.NoError(t, session.VerifyChatHistoryIndex(ctx, dir), "positive control: real fresh writer established a healthy baseline")
	require.NoError(t, os.RemoveAll(filepath.Join(dir, session.ChatHistoryIndexDirectory)))
	build, err := session.PrepareChatHistoryIndex(ctx, dir, session.ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "actual shared Go constructor, no copied reducer")
	require.NotNil(t, build)
	summary := build.Summary()
	require.Equal(t, sid, summary.SessionID)
	require.Equal(t, "create", summary.Action)
	require.EqualValues(t, 40, summary.SourceGroupCount)
	require.Equal(t, 1, summary.PartitionCount)
	require.Equal(t, before, repairCanonicalFacts(t, root), "prepare/summary are source read-only")
	require.NoError(t, build.Publish(ctx))
	require.NoError(t, build.Close())
	require.NoError(t, session.VerifyChatHistoryIndex(ctx, dir))
	require.Equal(t, before, repairCanonicalFacts(t, root), "publish writes only the agreed derived subtree")
	indexBefore := repairTree(t, filepath.Join(dir, session.ChatHistoryIndexDirectory))
	repeat, err := session.PrepareChatHistoryIndex(ctx, dir, session.ChatHistoryIndexBuildOptions{})
	require.NoError(t, err)
	require.Equal(t, "unchanged", repeat.Summary().Action)
	require.NoError(t, repeat.Publish(ctx))
	require.NoError(t, repeat.Close())
	require.Equal(t, indexBefore, repairTree(t, filepath.Join(dir, session.ChatHistoryIndexDirectory)), "repeat keeps matching index byte/mode/mtime unchanged")
	s, err := session.NewUnifiedStoreWithHome(filepath.Join(root, "sessions"), root)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	weigh := func(_ context.Context, p session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
		n := int64(1024 + 128*len(p.Entries))
		return session.ChatHistoryWeight{RESTBytes: n, WSBytes: n, ProjectedBytes: n}, nil
	}
	page, err := s.ReadChatHistoryPage(ctx, sid, session.ChatHistoryPageOptions{Limit: 100, Weigh: weigh})
	require.NoError(t, err)
	require.Len(t, page.Entries, 40)
	for i, e := range page.Entries {
		require.Equal(t, fmt.Sprintf("entry-%03d", i+1), e.Entry.ID)
		require.Equal(t, "canonical fixture", e.Entry.Content)
		require.Len(t, string(e.Position), 47)
	}
	require.False(t, page.HasEarlier)
	require.False(t, page.HasLater)
}

func repairRefusalMatrix(t *testing.T, binary string) {
	t.Helper()
	for _, fault := range []string{"unknown_field", "unsupported_version", "traversal", "kind", "wrong_root"} {
		t.Run(fault, func(t *testing.T) {
			source := repairCanonicalFixture(t)
			backup := t.TempDir()
			backup, err := filepath.EvalSymlinks(backup)
			require.NoError(t, err)
			manifest := repairManifest(t, binary, source, backup)
			b, err := os.ReadFile(manifest)
			require.NoError(t, err)
			var m map[string]any
			require.NoError(t, json.Unmarshal(b, &m))
			switch fault {
			case "unknown_field":
				m["unknown_field"] = "must refuse"
			case "unsupported_version":
				m["schema_version"] = 999
			case "wrong_root":
				m["source_root"] = t.TempDir()
			default:
				entries, ok := m["entries"].([]any)
				require.True(t, ok)
				require.NotEmpty(t, entries)
				entry := entries[0].(map[string]any)
				if fault == "traversal" {
					entry["path"] = "../outside"
				} else {
					entry["kind"] = "symlink"
				}
			}
			modified, err := json.Marshal(m)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifest, modified, 0o600))
			before, copyBefore := repairTree(t, source), repairTree(t, backup)
			repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
			require.Equal(t, before, repairTree(t, source))
			require.Equal(t, copyBefore, repairTree(t, backup))
		})
	}
	t.Run("stale_post_cutover_backup", func(t *testing.T) {
		source := repairCanonicalFixture(t)
		legacy := filepath.Join(source, "sessions", ".context")
		require.NoError(t, os.MkdirAll(legacy, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "legacy.jsonl"), []byte("{\"role\":\"user\",\"content\":\"old saved bytes\",\"ts\":1}\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "legacy.meta.json"), []byte(`{"key":"legacy","skip":0,"count":1}`), 0o600))
		backup := t.TempDir()
		backup, err := filepath.EvalSymlinks(backup)
		require.NoError(t, err)
		manifest := repairManifest(t, binary, source, backup)
		require.NoError(t, session.CutoverSavedChatsAtBoot(filepath.Join(source, "sessions")), "existing product conversion only, never invoked by repair command")
		before, copyBefore := repairTree(t, source), repairTree(t, backup)
		out := repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
		require.Contains(t, out, "source_changed")
		require.Equal(t, before, repairTree(t, source))
		require.Equal(t, copyBefore, repairTree(t, backup))
	})
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt_archive_mixed_%t", mixed), func(t *testing.T) {
			source := repairCanonicalFixture(t)
			dirs, err := os.ReadDir(filepath.Join(source, "sessions"))
			require.NoError(t, err)
			for _, d := range dirs {
				if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
					require.NoError(t, os.WriteFile(filepath.Join(source, "sessions", d.Name(), "transcript.jsonl"), []byte("{broken canonical framing}\n"), 0o600))
				}
			}
			if mixed {
				legacy := filepath.Join(source, "sessions", ".context")
				require.NoError(t, os.MkdirAll(legacy, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(legacy, "legacy.jsonl"), []byte("{}\n"), 0o600))
			}
			before := repairTree(t, source)
			exit := 1
			if mixed {
				exit = 2
			}
			out := repairRun(t, binary, exit, "dry-run", "--data-root", source)
			require.Contains(t, out, "corrupt_archive")
			if mixed {
				require.Contains(t, out, "unconverted")
			}
			require.Equal(t, before, repairTree(t, source), "all-session preflight must not publish any generation")
		})
	}
}
