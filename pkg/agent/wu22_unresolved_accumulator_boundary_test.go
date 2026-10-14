package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wu22UnresolvedPrefix = "An unfinished answer, "
const wu22UnresolvedSuffix = "with its last produced fragment."
const wu22UnresolvedPartial = wu22UnresolvedPrefix + wu22UnresolvedSuffix
const wu22UnresolvedTerminalSentinel = "Do not replace the pending partial with this terminal candidate."

// not-wire-format: an internal test receipt from the finalizer's publication
// edge. Every value is observed from the real stage/state/store or actual I/O.
type wu22UnresolvedPublication struct {
	content          string
	reason           string
	continuation     string
	unresolved       bool
	foregroundActive bool
	foregroundTurnID string
	cancelSelectable bool
	failedWrite      bool
}

// The agent stage and store are REAL. Only the external bus.Streamer endpoint
// is this filesystem-backed receipt. It forwards the supplied final content to
// real AppendTranscriptStrict and publishes only after that actual I/O attempt.
// It never invents an error or performs the preservation work being tested.
type wu22UnresolvedStoreStreamer struct {
	t            *testing.T
	h            *wu22Harness
	reason       string
	full         string
	calls        int
	appendErr    error
	publications []wu22UnresolvedPublication
}

func (*wu22UnresolvedStoreStreamer) Update(context.Context, string) error    { return nil }
func (*wu22UnresolvedStoreStreamer) Cancel(context.Context)                  {}
func (s *wu22UnresolvedStoreStreamer) SetContinuationContent(content string) { s.full = content }
func (s *wu22UnresolvedStoreStreamer) SetTruncation(reason string)           { s.reason = reason }
func (s *wu22UnresolvedStoreStreamer) Finalize(_ context.Context, content string) error {
	s.calls++
	turnID, _, _, active := s.h.al.ActiveForegroundTurnInfo(s.h.sid)
	cancel := s.h.al.activeTurnForCancel(s.h.sid, CancelScope{SessionID: s.h.sid, TurnOnly: true})
	publication := wu22UnresolvedPublication{
		content: content, reason: s.reason, continuation: s.full,
		unresolved: s.h.ts.continuationUnresolved(), foregroundActive: active,
		foregroundTurnID: turnID, cancelSelectable: cancel != nil,
	}
	s.appendErr = s.h.store.AppendTranscriptStrict(s.h.sid, session.TranscriptEntry{
		ID: "wu22-unresolved-final", Role: "assistant", AgentID: s.h.agent.ID,
		TurnID: s.h.ts.turnID, Content: content,
		Truncated: s.reason != "", TruncationReason: s.reason,
	})
	publication.failedWrite = s.appendErr != nil
	s.publications = append(s.publications, publication)
	return s.appendErr
}

func wu22UnresolvedStageFixture(t *testing.T) (*wu22Harness, *wu22UnresolvedStoreStreamer, *agentLoopRunTurnFinalize) {
	t.Helper()
	h := wu22NewHarness(t, wu22HealthyProvider())
	h.ts.appendToAccumulator(wu22UnresolvedPrefix)
	h.ts.appendToAccumulator(wu22UnresolvedSuffix)
	h.ts.markContinuationDispatched()
	require.True(t, h.ts.continuationUnresolved(), "SETUP: meaningful pending continuation, not the resolved baseline")
	require.Equal(t, wu22UnresolvedPartial, h.ts.continuationAccumulated())
	require.Equal(t, "", h.ts.getTruncationReason(), "SETUP: annotation must come from inline preservation, not the fixture")
	stream := &wu22UnresolvedStoreStreamer{
		t: t, h: h, publications: make([]wu22UnresolvedPublication, 0, 1),
	}
	h.ts.setLastStreamer(stream)
	h.al.registerActiveTurn(h.ts)
	t.Cleanup(func() { h.al.clearActiveTurn(h.ts) })
	require.NotNil(t, h.al.activeTurnForCancel(h.sid, CancelScope{SessionID: h.sid, TurnOnly: true}), "SETUP: real turn is registered before retirement")
	rr := h.responseStage()
	rz := &agentLoopRunTurnFinalize{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{
		ctx: context.Background(), rr: rr, finalContent: wu22UnresolvedTerminalSentinel,
	}}}
	return h, stream, rz
}

func wu22AssertUnresolvedPreservedAtPublication(t *testing.T, stream *wu22UnresolvedStoreStreamer, failed bool) {
	t.Helper()
	require.Equal(t, 1, stream.calls, "inline plus deferred finalization must not publish twice")
	require.Len(t, stream.publications, 1)
	publication := stream.publications[0]
	assert.Equal(t, wu22UnresolvedPartial, publication.content, "pending accumulator must reach the finalizer argument before done, not the unrelated terminal candidate")
	assert.Equal(t, wu22UnresolvedPartial, publication.continuation, "full continuation transfer is exact, with no lost/duplicated segment")
	assert.Equal(t, "max_output_tokens", publication.reason, "inline preservation must supply truncation annotation before final publication")
	assert.False(t, publication.unresolved, "inline preservation must resolve the pending flag before done; the later defer is too late")
	assert.False(t, publication.foregroundActive, "partial publication must follow retirement")
	assert.Equal(t, "", publication.foregroundTurnID, "no retired turn identity remains visible at done")
	assert.False(t, publication.cancelSelectable, "exact activeTurnForCancel selector must reject the retired turn at done")
	assert.Equal(t, failed, publication.failedWrite)
}

// ADR-087 D6.1/D6.8 and Chief's Q1 A oracle. The seeded state reaches the
// actual inline stage; no fixture calls preserveTruncatedAccumulator before it.
func TestWU22UnresolvedPartialHealthyFinalizePreservesBeforeDone(t *testing.T) {
	h, stream, rz := wu22UnresolvedStageFixture(t)
	_, err := rz.finalizeTurn()
	require.NoError(t, err)
	wu22AssertUnresolvedPreservedAtPublication(t, stream, false)
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one persisted partial, not duplicate terminal/accumulator copies")
	assert.Equal(t, wu22UnresolvedPartial, entries[0].Content, "persisted transcript must contain the exact pending partial")
	assert.True(t, entries[0].Truncated)
	assert.Equal(t, "max_output_tokens", entries[0].TruncationReason)
	assert.Equal(t, stream.publications[0].content, entries[0].Content, "published final content and durable partial agree")

	// Exercise unchanged unwind repetitions after the inline stage: none may
	// republish or append a second copy once preservation settled the chain.
	h.al.clearActiveTurn(h.ts)
	h.al.preserveTruncatedAccumulator(h.ts)
	require.NoError(t, h.ts.finalizeStreamer(context.Background()))
	again, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	assert.Equal(t, entries, again, "deferred repetitions cannot duplicate the partial")
	assert.Equal(t, 1, stream.calls)
}

func wu22BlockUnresolvedAppend(t *testing.T, h *wu22Harness) func() {
	t.Helper()
	path := filepath.Join(h.store.BaseDir(), h.sid, "transcript.jsonl")
	backup := filepath.Join(t.TempDir(), "saved-partial-archive.jsonl")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	return restore
}

// Q1 A: a failed NEW append need not commit. The prior durable partial must
// survive unchanged, while the late finalizer still receives the exact full
// accumulator+annotation before it returns its real filesystem error.
func TestWU22UnresolvedPartialLateFailurePreservesPriorAndFinalizerInput(t *testing.T) {
	h, stream, rz := wu22UnresolvedStageFixture(t)
	prior := session.TranscriptEntry{
		ID: "wu22-prior-partial", Role: "assistant", AgentID: h.agent.ID,
		TurnID: h.ts.turnID, Content: wu22UnresolvedPartial,
		Truncated: true, TruncationReason: "max_output_tokens",
	}
	require.NoError(t, h.store.AppendTranscriptStrict(h.sid, prior))
	before, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	require.Len(t, before, 1)
	// Arm only at the external finalizer entry, AFTER the model append, not
	// before finalizeTurn's first archive write.
	cut := &wu22UnresolvedCutStreamer{wu22UnresolvedStoreStreamer: stream}
	h.ts.setLastStreamer(cut)
	result, err := rz.finalizeTurn()
	var cause *os.PathError
	require.ErrorAs(t, err, &cause, "late append's actual filesystem cause is returned, never swallowed")
	assert.Equal(t, "open", cause.Op)
	assert.Equal(t, filepath.Join(h.store.BaseDir(), h.sid, "transcript.jsonl"), cause.Path)
	assert.Equal(t, TurnEndStatusError, result.status)
	assert.True(t, result.turnFailed)
	wu22AssertUnresolvedPreservedAtPublication(t, stream, true)
	cut.restore()
	after, readErr := h.store.ReadTranscript(h.sid)
	require.NoError(t, readErr)
	assert.Equal(t, before, after, "failed append cannot delete, rewrite or duplicate the previously committed partial")
	assert.Equal(t, wu22UnresolvedPartial, after[0].Content)
	assert.True(t, after[0].Truncated)
	assert.Equal(t, "max_output_tokens", after[0].TruncationReason)
	assert.Equal(t, after[0].Content, stream.publications[0].content)
	h.al.preserveTruncatedAccumulator(h.ts)
	require.NoError(t, h.ts.finalizeStreamer(context.Background()))
	repeated, readErr := h.store.ReadTranscript(h.sid)
	require.NoError(t, readErr)
	assert.Equal(t, before, repeated, "unchanged deferred guard must not append a second partial after failure")
	assert.Equal(t, 1, stream.calls)
}

type wu22UnresolvedCutStreamer struct {
	*wu22UnresolvedStoreStreamer
	restore func()
}

func (s *wu22UnresolvedCutStreamer) Finalize(ctx context.Context, content string) error {
	s.restore = wu22BlockUnresolvedAppend(s.t, s.h)
	return s.wu22UnresolvedStoreStreamer.Finalize(ctx, content)
}

var _ bus.Streamer = (*wu22UnresolvedStoreStreamer)(nil)
var _ bus.Streamer = (*wu22UnresolvedCutStreamer)(nil)
