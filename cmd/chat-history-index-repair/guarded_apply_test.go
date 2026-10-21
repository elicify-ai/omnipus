package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type repairGuardedFixture struct {
	source, backup, manifest, binding, receipt, sid string
}

func (f repairGuardedFixture) applyArgs() []string {
	return []string{"apply", "--data-root", f.source, "--backup-root", f.backup, "--backup-manifest", f.manifest, "--instance-binding", f.binding, "--stopped-receipt", f.receipt, "--confirm-stopped"}
}

func repairMissingIndexFixture(t *testing.T) (string, string) {
	t.Helper()
	root := repairCanonicalFixture(t) // Real canonical writer; Close joined its flusher.
	dirs, err := os.ReadDir(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	var sid string
	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			require.Empty(t, sid, "one real canonical session")
			sid = d.Name()
		}
	}
	require.NotEmpty(t, sid)
	before := repairCanonicalFacts(t, root)
	require.NoError(t, os.RemoveAll(filepath.Join(root, "sessions", sid, session.ChatHistoryIndexDirectory)), "only agreed disposable derived subtree, before backup")
	require.Equal(t, before, repairCanonicalFacts(t, root))
	return root, sid
}

func repairFullCopyFacts(t *testing.T, root string) map[string]repairFileFact {
	t.Helper()
	facts := repairTree(t, root)
	for path, fact := range facts {
		if fact.Mode.IsDir() {
			fact.Size, fact.Mtime = 0, 0 // D13 inventory excludes directory mtime/size, never files.
			facts[path] = fact
		}
	}
	return facts
}

func repairCountVerifiedBackup(t *testing.T, tool, source string) (string, string) {
	t.Helper()
	backup, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	manifest := repairManifest(t, tool, source, backup)
	want, copied := repairFullCopyFacts(t, source), repairFullCopyFacts(t, backup)
	require.Equal(t, want, copied, "independent full inventory: names/kinds/counts and every file's bytes/mode/mtime, not just producer success")
	files, directories := 0, 0
	for path, fact := range want {
		if path == "." {
			continue
		}
		if fact.Mode.IsDir() {
			directories++
		} else {
			files++
		}
	}
	require.Equal(t, files+directories+1, len(copied), "full source/copy entry count, including root")
	t.Logf("independent complete backup counts: files=%d directories=%d (root excluded), entries=%d", files, directories, files+directories)
	return backup, manifest
}

func repairStoppedCLIFixture(t *testing.T, tool, helper, action string, exit int, dryRunFirst bool) repairGuardedFixture {
	t.Helper()
	root, sid := repairMissingIndexFixture(t)
	if dryRunFirst {
		before := repairTree(t, root)
		repairRun(t, tool, 0, "dry-run", "--data-root", root)
		require.Equal(t, before, repairTree(t, root), "initial read-only preflight requires no backup/stop")
	}
	child := repairStartNative(t, helper, root)
	binding, identity := repairCapture(t, tool, helper, root, child)
	repairStopNative(t, child, action, exit)
	// Always take the authoritative copy after real Wait. Clean PID removal is
	// not a canonical-equality exclusion, and a running backup can become stale.
	backup, manifest := repairCountVerifiedBackup(t, tool, root)
	state := "absent"
	if action == "crash" {
		state = "same_stale"
	}
	receipt := repairProduceReceipt(t, tool, root, binding, identity, state)
	return repairGuardedFixture{source: root, backup: backup, manifest: manifest, binding: binding, receipt: receipt, sid: sid}
}

func repairReadAppliedPage(t *testing.T, f repairGuardedFixture) []string {
	t.Helper()
	require.NoError(t, session.VerifyChatHistoryIndex(context.Background(), filepath.Join(f.source, "sessions", f.sid)), "real published shared-core verifier")
	before := repairCanonicalFacts(t, f.source)
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(f.source, "sessions"), f.source)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, store.Close())
		require.Equal(t, before, repairCanonicalFacts(t, f.source), "actual reader/flusher lifetime cannot rewrite canonical bytes/mode/mtime")
	}()
	weigh := func(_ context.Context, page session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
		weight := int64(1024 + 128*len(page.Entries)) // Controlled small core fixture meter, not a gateway-wire claim.
		return session.ChatHistoryWeight{RESTBytes: weight, WSBytes: weight, ProjectedBytes: weight}, nil
	}
	page, err := store.ReadChatHistoryPage(context.Background(), f.sid, session.ChatHistoryPageOptions{Limit: 100, Weigh: weigh})
	require.NoError(t, err, "actual bounded Go reader, not a command success assertion")
	require.Equal(t, f.sid, page.SessionID)
	require.Len(t, page.Entries, 40, "D13 closed fixture's actual forty canonical writes")
	positions := make([]string, 0, 40)
	for i, entry := range page.Entries {
		require.Equal(t, fmt.Sprintf("entry-%03d", i+1), entry.Entry.ID)
		require.Equal(t, "canonical fixture", entry.Entry.Content)
		require.Equal(t, "user", entry.Entry.Role)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, string(entry.Position))
		positions = append(positions, string(entry.Position))
	}
	require.False(t, page.HasEarlier)
	require.False(t, page.HasLater)
	require.Empty(t, page.NextBefore)
	require.Empty(t, page.NextAfter)
	return positions
}

// Count semantics are specified; presentation is not. Accept ordinary key=value
// or JSON fields rather than inventing a new required CLI serialization format.
func repairAssertPublicationCounts(t *testing.T, output string, published, unchanged, refused int) {
	t.Helper()
	for name, want := range map[string]int{"published": published, "unchanged": unchanged, "refused": refused} {
		pattern := `(?:^|[\s,{])"?` + name + `"?\s*[:=]\s*([0-9]+)(?:$|[\s,}])`
		matches := regexp.MustCompile(pattern).FindAllStringSubmatch(output, -1)
		require.Len(t, matches, 1, "one explicit actual aggregate %s count, never inferred from exit0", name)
		got, err := strconv.Atoi(matches[0][1])
		require.NoError(t, err)
		require.Equal(t, want, got, "exact actual %s count", name)
	}
}

// T22/T25/T30, B30/B36/B44/B45: closed quiescent fixture only. F1=A accepts a
// residual live race; this repeat is not an automatic live retry or a lease.
func TestHistoryRepairCommand_GuardedCLIApplyReadsAndRepeats(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, tc := range []struct {
		name, action string
		exit         int
	}{{"graceful", "stop", 0}, {"crash", "crash", 86}} {
		t.Run(tc.name, func(t *testing.T) {
			f := repairStoppedCLIFixture(t, tool, helper, tc.action, tc.exit, true)
			canonical, backup := repairCanonicalFacts(t, f.source), repairTree(t, f.backup)
			out := repairRun(t, tool, 0, f.applyArgs()...)
			repairAssertPublicationCounts(t, out, 1, 0, 0)
			require.Equal(t, canonical, repairCanonicalFacts(t, f.source), "publish only derived index, never canonical/PID state")
			require.Equal(t, backup, repairTree(t, f.backup), "backup is immutable")
			positions := repairReadAppliedPage(t, f)
			index := filepath.Join(f.source, "sessions", f.sid, session.ChatHistoryIndexDirectory)
			indexBefore, wholeBefore := repairTree(t, index), repairTree(t, f.source)
			require.Greater(t, len(indexBefore), 1, "real index files exist, not an empty mkdir success")
			repairRun(t, tool, 0, "verify", "--data-root", f.source, "--backup-root", f.backup, "--backup-manifest", f.manifest)
			require.Equal(t, wholeBefore, repairTree(t, f.source), "CLI verify is read-only")
			out = repairRun(t, tool, 0, f.applyArgs()...)
			repairAssertPublicationCounts(t, out, 0, 1, 0)
			require.Equal(t, wholeBefore, repairTree(t, f.source), "matching repeat has no source byte/mode/mtime writes")
			require.Equal(t, indexBefore, repairTree(t, index), "matching derived generation stays byte/mode/mtime identical")
			require.Equal(t, backup, repairTree(t, f.backup))
			require.Equal(t, positions, repairReadAppliedPage(t, f), "repeat cannot renumber occurrence tokens")
		})
	}
}
