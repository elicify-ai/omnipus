// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// truncation_nonstreaming_single_write_test.go — ADR-087 D4a/D4b non-streaming
// write-choke-point regression: the non-streaming path's two truncation exits
// (loop.go's preserveTruncatedAccumulator and runTurn's own tail) must stamp
// Truncated/TruncationReason on the assistant transcript entry in the SAME
// write that persists its content, via turnState.appendAssistantTranscriptTruncated
// (pkg/agent/turn.go) — not via a follow-up session.MarkLastEntryTruncated
// call that re-reads, re-parses and rewrites the entire transcript.jsonl to
// stamp two fields on the entry constructed one call earlier.
//
// The streamed (webchat) path already fixed this — see
// pkg/gateway/websocket_truncation_finalize_test.go, commit 47c086ca — this
// file is that fix's non-streaming counterpart.
//
// Oracle: session.MarkLastEntryTruncated persists via fileutil.WriteFileAtomic
// (temp file + rename), which always swaps in a NEW inode; AppendTranscriptStrict
// persists via fileutil.AppendJSONL, opened O_APPEND, which never changes the
// file's inode. So a transcript.jsonl whose inode is unchanged before vs.
// after a truncated turn proves that turn made zero MarkLastEntryTruncated
// calls — a strictly stronger, non-content-based proof than comparing bytes,
// since a rewrite that happens to re-marshal untouched entries byte-identically
// would otherwise be invisible to a content-only comparison.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson \
//        -run 'TestTruncation' -p 1 ./pkg/agent/

package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// fileInode returns path's platform inode number. ok is false on a platform
// where os.FileInfo.Sys() is not *syscall.Stat_t (e.g. Windows) — callers
// must skip the inode assertion rather than fail in that case, since the
// property this proves (append vs. rewrite) has no meaning there.
func fileInode(t *testing.T, path string) (ino uint64, ok bool) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "stat transcript file")
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}

// ---------------------------------------------------------------------------
// Call site 2 — runTurn's own non-streaming tail (loop.go, gated on
// !hasActiveStreamer). D4a: zero content. D4b: real content, via the D6.4
// iteration-cap scenario (truncated content on the LAST permitted round,
// which reaches the tail directly — no continuation is ever dispatched).
// ---------------------------------------------------------------------------

// TestTruncationD4a_NonStreamingEntryStampedInSingleWrite pins the
// non-streaming D4a write choke point (runTurn's tail): a turn truncated at
// the output-token limit with literally no content produced must persist
// exactly ONE new assistant entry — content "", Truncated=true, reason
// max_output_tokens, TurnID stamped — in the SAME write, leaving an earlier,
// unrelated turn's assistant entry byte-identical and the transcript file's
// inode unchanged (proving no MarkLastEntryTruncated rewrite occurred).
func TestTruncationD4a_NonStreamingEntryStampedInSingleWrite(t *testing.T) {
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "", finishReason: "truncated"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)

	// Seed an EARLIER, unrelated (different TurnID) assistant entry — a
	// previously completed turn's own final answer — to prove this turn's
	// write never touches it.
	earlier := session.TranscriptEntry{
		ID:      "entry-earlier-turn-d4a",
		Role:    "assistant",
		AgentID: "truncation-test-agent",
		TurnID:  "turn-earlier-unrelated-d4a",
		Content: "An earlier, completed answer.",
	}
	require.NoError(t, store.AppendTranscriptStrict(sessionID, earlier))

	before, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, before, 1, "only the seeded earlier entry must exist before this turn")
	beforeEarlierEntry := before[0]

	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	beforeRaw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	beforeInode, haveInode := fileInode(t, transcriptPath)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d4a-single-write-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "write something",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Empty(t, answer, "D4a's answer must stay empty")
	assert.Equal(t, 1, provider.CallCount(), "D4a must not retry the identical request")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 2,
		"the seeded earlier entry plus exactly one NEW annotated zero-content entry from this turn")

	assert.Equal(t, beforeEarlierEntry, assistantEntries[0],
		"the earlier, unrelated turn's assistant entry must be byte-identical after this turn's write — "+
			"a MarkLastEntryTruncated rewrite would re-marshal it even if the values happen not to change")

	newEntry := assistantEntries[1]
	assert.NotEmpty(t, newEntry.TurnID, "the new entry must carry this turn's own TurnID")
	assert.NotEqual(t, beforeEarlierEntry.TurnID, newEntry.TurnID)
	assert.Empty(t, newEntry.Content, "D4a: the entry must be zero-content")
	assert.True(t, newEntry.Truncated, "the entry must be flagged Truncated")
	assert.Equal(t, "max_output_tokens", newEntry.TruncationReason)

	afterRaw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(afterRaw, beforeRaw),
		"the earlier entry's raw bytes must remain an untouched prefix of the file")

	if haveInode {
		afterInode, ok := fileInode(t, transcriptPath)
		require.True(t, ok)
		assert.Equal(t, beforeInode, afterInode,
			"the transcript file's inode must be unchanged: this turn's write must be a pure "+
				"O_APPEND (AppendTranscriptStrict), never a temp-file+rename rewrite of the whole "+
				"file (session.MarkLastEntryTruncated) — that rewrite is exactly the "+
				"append-then-MarkLastEntryTruncated pattern this fix removes")
	} else {
		t.Log("platform does not expose *syscall.Stat_t via os.FileInfo.Sys() — inode assertion skipped")
	}
}

// TestTruncationD4b_NonStreamingEntryStampedInSingleWrite pins the
// non-streaming D4b write choke point (runTurn's tail, D6.4's iteration-cap
// exit): truncated real content on the LAST permitted iteration must reach
// the tail directly (no continuation is ever dispatched — MaxIterations=1)
// and persist content + Truncated/TruncationReason in the SAME write.
func TestTruncationD4b_NonStreamingEntryStampedInSingleWrite(t *testing.T) {
	const content = "This is as far as I can get before the limit."

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: content, finishReason: "truncated"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 1, 0)

	earlier := session.TranscriptEntry{
		ID:      "entry-earlier-turn-d4b",
		Role:    "assistant",
		AgentID: "truncation-test-agent",
		TurnID:  "turn-earlier-unrelated-d4b",
		Content: "An earlier, completed answer.",
	}
	require.NoError(t, store.AppendTranscriptStrict(sessionID, earlier))

	before, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, before, 1)
	beforeEarlierEntry := before[0]

	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	beforeRaw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	beforeInode, haveInode := fileInode(t, transcriptPath)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d4b-single-write-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "go as far as you can",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Equal(t, content, answer,
		"the last permitted iteration's truncated answer must stand")
	assert.Equal(t, 1, provider.CallCount(),
		"MaxIterations=1 leaves no room for a continuation round")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 2)

	assert.Equal(t, beforeEarlierEntry, assistantEntries[0],
		"the earlier, unrelated turn's assistant entry must be byte-identical after this turn's write")

	newEntry := assistantEntries[1]
	assert.NotEmpty(t, newEntry.TurnID)
	assert.NotEqual(t, beforeEarlierEntry.TurnID, newEntry.TurnID)
	assert.Equal(t, content, newEntry.Content)
	assert.True(t, newEntry.Truncated)
	assert.Equal(t, "max_output_tokens", newEntry.TruncationReason)

	afterRaw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(afterRaw, beforeRaw),
		"the earlier entry's raw bytes must remain an untouched prefix of the file")

	if haveInode {
		afterInode, ok := fileInode(t, transcriptPath)
		require.True(t, ok)
		assert.Equal(t, beforeInode, afterInode,
			"the transcript file's inode must be unchanged — a single append, never a "+
				"MarkLastEntryTruncated rewrite")
	} else {
		t.Log("platform does not expose *syscall.Stat_t via os.FileInfo.Sys() — inode assertion skipped")
	}
}

// ---------------------------------------------------------------------------
// Call site 1 — preserveTruncatedAccumulator (loop.go): fires only when a D6
// continuation WAS dispatched but never resolved (a rate-limit denial or hard
// cancel arriving before the continuation call completes). Mirrors
// TestTruncationD6_RateLimitDenialPreservesPartial's scenario with the same
// single-write oracle added.
// ---------------------------------------------------------------------------

// TestTruncationD6_RateLimitDenialSingleWrite pins the preserveTruncatedAccumulator
// write choke point: a rate-limit denial on the would-be continuation call
// must persist round 1's accumulated partial with Truncated/TruncationReason
// stamped in the SAME write as its content, never via a follow-up
// MarkLastEntryTruncated rewrite.
func TestTruncationD6_RateLimitDenialSingleWrite(t *testing.T) {
	const partial = "Here is the beginning of a long answer that gets cut off"

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: partial, finishReason: "truncated"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 1)

	earlier := session.TranscriptEntry{
		ID:      "entry-earlier-turn-d6-ratelimit",
		Role:    "assistant",
		AgentID: "truncation-test-agent",
		TurnID:  "turn-earlier-unrelated-d6-ratelimit",
		Content: "An earlier, completed answer.",
	}
	require.NoError(t, store.AppendTranscriptStrict(sessionID, earlier))

	before, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, before, 1)
	beforeEarlierEntry := before[0]

	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	beforeRaw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	beforeInode, haveInode := fileInode(t, transcriptPath)

	_, err = al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "rate-denial-single-write-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "write a long answer",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "the denied continuation must surface as an error")
	assert.Equal(t, 1, provider.CallCount())

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 2)

	assert.Equal(t, beforeEarlierEntry, assistantEntries[0],
		"the earlier, unrelated turn's assistant entry must be byte-identical after this turn's write")

	newEntry := assistantEntries[1]
	assert.NotEmpty(t, newEntry.TurnID)
	assert.NotEqual(t, beforeEarlierEntry.TurnID, newEntry.TurnID)
	assert.Equal(t, partial, newEntry.Content)
	assert.True(t, newEntry.Truncated)
	assert.Equal(t, "max_output_tokens", newEntry.TruncationReason)

	afterRaw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(afterRaw, beforeRaw),
		"the earlier entry's raw bytes must remain an untouched prefix of the file")

	if haveInode {
		afterInode, ok := fileInode(t, transcriptPath)
		require.True(t, ok)
		assert.Equal(t, beforeInode, afterInode,
			"the transcript file's inode must be unchanged — preserveTruncatedAccumulator's "+
				"single append, never a MarkLastEntryTruncated rewrite")
	} else {
		t.Log("platform does not expose *syscall.Stat_t via os.FileInfo.Sys() — inode assertion skipped")
	}
}
