package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

const historyCrashEnv = "OMNIPUS_HISTORY_TEST_CRASH" // Test-executable input only, never production.

func historyCrashChild(t *testing.T) {
	point := os.Getenv(historyCrashEnv)
	root := os.Getenv("OMNIPUS_HISTORY_TEST_ROOT")
	sid := os.Getenv("OMNIPUS_HISTORY_TEST_SID")
	n, err := strconv.Atoi(os.Getenv("OMNIPUS_HISTORY_TEST_PENDING"))
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(root), "crash child requires an explicit owned temporary root")
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	tmpRoot, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)
	rel, err := filepath.Rel(tmpRoot, realRoot)
	require.NoError(t, err)
	require.True(t, rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), "crash fixture cannot target developer/live data")
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.Point == point {
			fmt.Printf("CRASH_SESSION=%s\n", e.SessionID)
			os.Exit(86)
		}
		if point == "canonical_synced" && e.Operation == "reconcile" && e.Point == "before_checkpoint_publish" {
			return errors.New("test blocks prior derived checkpoint to retain full suffix")
		}
		return nil
	}
	if os.Getenv("OMNIPUS_HISTORY_TEST_OPERATION") == "repair" {
		build, err := PrepareChatHistoryIndex(context.Background(), filepath.Join(root, sid), ChatHistoryIndexBuildOptions{})
		require.NoError(t, err, "real offline constructor before publication crash")
		require.NotNil(t, build)
		require.NoError(t, build.Publish(context.Background()))
		require.NoError(t, build.Close())
		t.Fatalf("offline publication never reached %s", point)
	}
	s, err := NewUnifiedStore(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	if strings.Contains(point, "identity") || point == "empty_checkpoint_ready" {
		_, err = s.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		t.Fatalf("actual creation never reached publication point %s", point)
	}
	if point == "partition_unlinked" {
		_, err = s.RetentionSweep(90)
		require.NoError(t, err)
		t.Fatal("actual sweep did not reach successful unlink publication point")
	}
	if point == "canonical_synced" {
		historyUnpublishedArchive(t, s, sid, n-1)
		_, err = s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "last-canonical", Role: "user", Content: "accepted last", AgentID: "mia"})
		require.NoError(t, err)
	} else {
		if !strings.Contains(point, "torn_tail") {
			historyUnpublishedArchive(t, s, sid, n)
		}
		_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
		if n == 65 {
			historyRequireUnavailable(t, err, "catchup_limit")
			return
		}
		require.NoError(t, err)
	}
	t.Fatalf("real engine never invoked publication point %s", point)
}

func historyRunCrash(t *testing.T, root, sid, point string, n, wantExit int, operation ...string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^TestChatHistoryRecovery_PublicationCrashBoundAndIdempotence$", "-test.v")
	op := ""
	if len(operation) > 0 {
		op = operation[0]
	}
	cmd.Env = append(os.Environ(), historyCrashEnv+"="+point, "OMNIPUS_HISTORY_TEST_ROOT="+root, "OMNIPUS_HISTORY_TEST_SID="+sid, "OMNIPUS_HISTORY_TEST_PENDING="+strconv.Itoa(n), "OMNIPUS_HISTORY_TEST_OPERATION="+op)
	out, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee)
		exit = ee.ExitCode()
	}
	t.Logf("real crash child point=%s pending=%d exit=%d output=%s", point, n, exit, out)
	require.Equal(t, wantExit, exit, "child must hit the actual hook, not merely unwind an injected error")
}

// T29/D-H1: crash/reopen, not a same-process cache or a fake fsync. Child
// probes belong on remote CI; a pending0 narrow RED case never spawns a child.
func TestChatHistoryRecovery_PublicationCrashBoundAndIdempotence(t *testing.T) {
	if os.Getenv(historyCrashEnv) != "" {
		historyCrashChild(t)
		return
	}
	points := []string{"canonical_synced", "mutation_written", "mutation_synced", "lookups_staged", "generation_verified", "before_checkpoint_publish", "checkpoint_published", "root_installed"}
	for _, point := range points {
		for _, n := range []int{0, 1, 63, 64, 65} {
			t.Run(fmt.Sprintf("%s_pending%d", point, n), func(t *testing.T) {
				s, sid := newEffectsSession(t)
				historyRows(t, s, sid, "base", 1)
				before := historyState(t, s, sid)
				if n == 0 {
					files := historyTree(t, historyIndexDir(s, sid))
					result, err := s.reconcileChatHistoryIndex(context.Background(), sid)
					require.NoError(t, err, "operational zero-delta reconciler")
					require.Zero(t, result.ObservedItems)
					require.False(t, result.Published)
					require.Equal(t, before, historyState(t, s, sid))
					require.Equal(t, files, historyTree(t, historyIndexDir(s, sid)))
					return
				}
				checkpoint := filepath.Join(historyIndexDir(s, sid), "checkpoint.json")
				old, err := os.ReadFile(checkpoint)
				require.NoError(t, err)
				require.NoError(t, s.Close())
				wantExit := 86
				if n == 65 && point != "canonical_synced" {
					wantExit = 0
				}
				historyRunCrash(t, s.BaseDir(), sid, point, n, wantExit)
				afterCrash, err := os.ReadFile(checkpoint)
				require.NoError(t, err)
				if point == "checkpoint_published" || point == "root_installed" {
					if n <= 64 {
						require.NotEqual(t, string(old), string(afterCrash))
					} else {
						require.Equal(t, string(old), string(afterCrash))
					}
				} else {
					require.Equal(t, string(old), string(afterCrash), "no root over unpublished candidate")
				}
				reopened, err := NewUnifiedStore(s.BaseDir())
				require.NoError(t, err)
				defer func() { require.NoError(t, reopened.Close()) }()
				canonical := partitionBytes(t, reopened, sid)
				if n == 65 {
					for i := 0; i < 2; i++ {
						result, err := reopened.reconcileChatHistoryIndex(context.Background(), sid)
						historyRequireUnavailable(t, err, "catchup_limit")
						require.False(t, result.Published)
						require.Zero(t, result.AppliedItems)
						require.Equal(t, before, historyState(t, reopened, sid))
					}
				} else {
					p := historyRead(t, reopened, sid, historyOptions(100))
					require.Len(t, p.Entries, n+1)
					ids := map[string]bool{}
					for _, entry := range p.Entries {
						require.False(t, ids[entry.Entry.ID], "redo duplicated canonical occurrence")
						ids[entry.Entry.ID] = true
					}
					stable := historyState(t, reopened, sid)
					r, err := reopened.reconcileChatHistoryIndex(context.Background(), sid)
					require.NoError(t, err)
					require.Zero(t, r.AppliedItems)
					require.False(t, r.Published)
					require.Equal(t, stable, historyState(t, reopened, sid))
				}
				require.Equal(t, canonical, partitionBytes(t, reopened, sid), "recovery never trims/rewrites canonical bytes")
			})
		}
	}
	for _, point := range []string{"empty_checkpoint_ready", "before_identity_publish", "identity_published"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			historyRunCrash(t, root, "", point, 1, 86)
			s, err := NewUnifiedStore(root)
			require.NoError(t, err)
			defer func() { require.NoError(t, s.Close()) }()
			metas, err := s.ListSessions()
			require.NoError(t, err)
			if point != "identity_published" {
				require.Empty(t, metas, "identity must not be readable before healthy empty index")
			} else {
				require.Len(t, metas, 1)
				historyState(t, s, metas[0].ID)
			}
		})
	}
}

func TestChatHistoryRecovery_PrivateCoverageAndMixedDispositionBound(t *testing.T) {
	for _, n := range []int{63, 64, 65} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "input", n)
			before := historyState(t, s, sid)
			old := chatHistoryPublicationHook
			chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
				if e.SessionID == sid && (e.Point == "canonical_synced" || e.Point == "before_checkpoint_publish") {
					return errors.New("derived-only injected failure")
				}
				return nil
			}
			t.Cleanup(func() { chatHistoryPublicationHook = old })
			for i := 0; i < n; i++ {
				if i%2 == 0 {
					_, _, err := s.AppendModelMessage(context.Background(), sid, ModelAppend{Message: providers.Message{Role: "user", Content: fmt.Sprintf("private-only-%d", i)}, ViewMembership: ViewMembershipModel, Source: EntrySource{Kind: "user"}, AgentID: "mia"})
					require.NoError(t, err, "canonical model save remains accepted after derived failure")
				} else {
					require.NoError(t, s.RecordInputDiscarded(sid, fmt.Sprintf("input-%06d", i+1), ""))
				}
			}
			chatHistoryPublicationHook = old
			r, err := s.reconcileChatHistoryIndex(context.Background(), sid)
			if n == 65 {
				historyRequireUnavailable(t, err, "catchup_limit")
				require.False(t, r.Published)
				require.Equal(t, before, historyState(t, s, sid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, n, r.ObservedItems)
			require.Equal(t, n, r.AppliedItems)
			require.Equal(t, (n+1)/2, r.ArchiveRecords)
			require.Equal(t, n/2, r.DispositionRecords)
			p := historyRead(t, s, sid, historyOptions(100))
			require.Len(t, p.Entries, n, "private model records advance coverage, never public rows")
			for i, e := range p.Entries {
				require.NotContains(t, e.Entry.Content, "private-only-")
				if i%2 == 1 {
					require.NotNil(t, e.Entry.InputDisposition)
					require.Equal(t, "discarded", e.Entry.InputDisposition.State)
				}
			}
		})
	}
}

func TestChatHistoryRecovery_CheckpointFailuresAndSavedRowDeduplication(t *testing.T) {
	for _, point := range []string{"mutation_synced", "before_checkpoint_publish", "checkpoint_published"} {
		t.Run(point, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "base", 1)
			historyState(t, s, sid)
			old := chatHistoryPublicationHook
			fired := 0
			chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
				if e.SessionID == sid && e.Point == point {
					fired++
					return errors.New("derived publication fault")
				}
				return nil
			}
			_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "saved-once", Role: "user", Content: "durably saved", AgentID: "mia"})
			require.NoError(t, err, "durable canonical save is not unsaved merely because derived work failed")
			chatHistoryPublicationHook = old
			t.Cleanup(func() { chatHistoryPublicationHook = old })
			require.Positive(t, fired, "real writer invoked agreed hook")
			for i := 0; i < 2; i++ {
				_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
				require.NoError(t, err)
			}
			p := historyRead(t, s, sid, historyOptions(40))
			require.Equal(t, []string{"base-000001", "saved-once"}, historyIDs(p))
		})
	}
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint_write_after_%t", after), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "base", 1)
			historyState(t, s, sid)
			old := chatHistoryWriteCheckpoint
			calls := 0
			chatHistoryWriteCheckpoint = func(path string, data []byte, mode os.FileMode) error {
				calls++
				if after {
					if err := old(path, data, mode); err != nil {
						return err
					}
				}
				return errors.New("injected checked-durability failure")
			}
			_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "accepted", Role: "user", Content: "accepted", AgentID: "mia"})
			require.NoError(t, err)
			chatHistoryWriteCheckpoint = old
			t.Cleanup(func() { chatHistoryWriteCheckpoint = old })
			require.Positive(t, calls)
			_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
			require.NoError(t, err)
			require.Equal(t, []string{"base-000001", "accepted"}, historyIDs(historyRead(t, s, sid, historyOptions(40))))
		})
	}
}

func TestChatHistoryRecovery_CorruptSavedPrefixNeverTrimsCanonical(t *testing.T) {
	for _, kind := range []string{"changed_anchor", "truncated_anchor", "torn_canonical", "missing_current", "repointed_current", "incompatible_checkpoint", "missing_baseline"} {
		t.Run(kind, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "base", 1)
			historyState(t, s, sid)
			current := filepath.Join(s.BaseDir(), sid, "transcript.jsonl")
			b, err := os.ReadFile(current)
			require.NoError(t, err)
			switch kind {
			case "changed_anchor":
				require.NoError(t, os.WriteFile(current, []byte(strings.Replace(string(b), "row 1", "row X", 1)), 0o600))
			case "truncated_anchor":
				require.NoError(t, os.Truncate(current, int64(len(b)/2)))
			case "torn_canonical":
				f, err := os.OpenFile(current, os.O_APPEND|os.O_WRONLY, 0)
				require.NoError(t, err)
				_, err = f.WriteString(`{"id":"torn`)
				require.NoError(t, err)
				require.NoError(t, f.Close())
			case "missing_current":
				require.NoError(t, os.Remove(current))
			case "repointed_current":
				require.NoError(t, os.WriteFile(current, []byte(strings.Replace(string(b), "base-000001", "other-id-01", 1)), 0o600))
			case "incompatible_checkpoint":
				require.NoError(t, os.WriteFile(filepath.Join(historyIndexDir(s, sid), "FORMAT"), []byte("omnipus-chat-history-index/999\n"), 0o600))
			case "missing_baseline":
				require.NoError(t, os.RemoveAll(historyIndexDir(s, sid)))
			}
			before := partitionBytes(t, s, sid)
			_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
			require.ErrorIs(t, err, ErrChatHistoryUnavailable)
			require.Equal(t, before, partitionBytes(t, s, sid), "refusal never truncates damaged canonical bytes")
		})
	}
}

func TestChatHistoryRecovery_ConcurrentOwnerAndCapturedEnd(t *testing.T) {
	s, sid := newEffectsSession(t)
	historyRows(t, s, sid, "base", 1)
	historyState(t, s, sid)
	historyUnpublishedArchive(t, s, sid, 1)
	var wg sync.WaitGroup
	results := make(chan chatHistoryReconcileResult, 8)
	errs := make(chan error, 8)
	var published, writes atomic.Int64
	oldHook, oldWrite := chatHistoryPublicationHook, chatHistoryWriteCheckpoint
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == sid && e.Point == "checkpoint_published" {
			published.Add(1)
		}
		return nil
	}
	chatHistoryWriteCheckpoint = func(path string, b []byte, mode os.FileMode) error { writes.Add(1); return oldWrite(path, b, mode) }
	t.Cleanup(func() { chatHistoryPublicationHook = oldHook; chatHistoryWriteCheckpoint = oldWrite })
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.reconcileChatHistoryIndex(context.Background(), sid)
			results <- r
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err, "coalesced recoverers, not concurrent competing publication")
	}
	for r := range results {
		require.LessOrEqual(t, r.AppliedItems, 1)
	}
	require.EqualValues(t, 1, published.Load(), "one owner emits publication once, not a count of follower return flags")
	require.EqualValues(t, 1, writes.Load(), "one actual checked checkpoint replacement")
	require.Equal(t, []string{"base-000001", "suffix-000"}, historyIDs(historyRead(t, s, sid, historyOptions(40))))
}
