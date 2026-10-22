package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type repairLateBatch struct { // not-wire-format: test-owned copied-data batch.
	repairGuardedFixture
	ids       []string
	positions map[string][]string
}

func repairLateSessionDir(batch repairLateBatch, id string) string {
	return filepath.Join(batch.source, "sessions", id)
}

func repairLateBatchFixture(t *testing.T, tool, helper string) repairLateBatch {
	t.Helper()
	root := repairCanonicalFixture(t)
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(root, "sessions"), root)
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	for i := 1; i <= 40; i++ {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
			ID: fmt.Sprintf("entry-%03d", i), Role: "user", Content: "canonical fixture", AgentID: "mia",
			Timestamp: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		}))
	}
	require.NoError(t, store.Close(), "join canonical fixture writers before native capture/backup")
	directories, err := os.ReadDir(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	var ids []string
	for _, d := range directories {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			ids = append(ids, d.Name())
		}
	}
	sort.Strings(ids)
	require.Len(t, ids, 2, "actual two-session command batch, never a single-session proxy")
	canonical := repairCanonicalFacts(t, root)
	positions := make(map[string][]string, len(ids))
	for _, id := range ids {
		dir := filepath.Join(root, "sessions", id)
		require.NoError(t, session.VerifyChatHistoryIndex(context.Background(), dir), "instrument: real canonical writers produced readable complete indexes")
		positions[id] = repairReadAppliedPage(t, repairGuardedFixture{source: root, sid: id})
		require.NoError(t, os.RemoveAll(filepath.Join(dir, session.ChatHistoryIndexDirectory)), "only designated disposable derived indexes removed")
	}
	require.Equal(t, canonical, repairCanonicalFacts(t, root))
	before := repairTree(t, root)
	out := repairRun(t, tool, 0, "dry-run", "--data-root", root)
	require.Contains(t, out, "sessions=2")
	require.Contains(t, out, "create=2")
	require.Equal(t, before, repairTree(t, root), "initial read-only preflight")
	child := repairStartNative(t, helper, root)
	binding, identity := repairCapture(t, tool, helper, root, child)
	repairStopNative(t, child, "stop", 0)
	backup, manifest := repairCountVerifiedBackup(t, tool, root)
	receipt := repairProduceReceipt(t, tool, root, binding, identity, "absent")
	return repairLateBatch{repairGuardedFixture: repairGuardedFixture{source: root, backup: backup, manifest: manifest,
		binding: binding, receipt: receipt, sid: ids[0]}, ids: ids, positions: positions}
}

func repairLatePublicationState(t *testing.T, batch repairLateBatch, wantFirstValid bool) {
	t.Helper()
	first := session.VerifyChatHistoryIndex(context.Background(), repairLateSessionDir(batch, batch.ids[0]))
	if wantFirstValid {
		require.NoError(t, first, "first generation actually published and reads through the real shared verifier")
	} else {
		require.Error(t, first, "controlled damaged canonical data fails actual post-check; no false safety claim")
	}
	require.Error(t, session.VerifyChatHistoryIndex(context.Background(), repairLateSessionDir(batch, batch.ids[1])),
		"second session has no completed publication after the late failure")
}
