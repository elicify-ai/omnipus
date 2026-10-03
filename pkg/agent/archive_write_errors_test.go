//go:build goolm && stdjson

package agent

// RED plan, #1081 archive write errors (fire-and-forget archive appends).
//
// The seam: pkg/session/unified_write.go::UnifiedStore.AddMessage/AddFullMessage
// and pkg/session/jsonl_backend.go::JSONLBackend.AddMessage/AddFullMessage log
// the durability error and return nothing, while pkg/memory/jsonl.go::
// JSONLStore.addMsgLocked is the real durability point (fresh O_APPEND open +
// fsync per line) and UnifiedStore.Save is a documented unconditional no-op
// (pkg/session/CLAUDE.md, "Durability"). Every pkg/agent call site riding that
// seam can therefore lose a message on a disk error while the turn proceeds
// as if it had succeeded. This file covers the EVERY-TURN site first:
// pkg/agent/loop_run_turn.go::finalizeTurn persisting the final assistant
// reply via AddMessage + Save — whose own save-error branch (turn status
// TurnEndStatusError, EventKindError with stage "session_save", classified
// transcript error, non-nil return) is dead code today because AddMessage
// cannot fail it and Save cannot fail at all.
//
// Oracle (derived from existing authority, never from observed behaviour):
// a filesystem refusal at the final append must SURFACE — non-nil error from
// finalizeTurn, the iteration's turn status set to TurnEndStatusError, and a
// session_save EventKindError a user-facing channel can render. Sources: the
// dead branch's own contract text in finalizeTurn, and the checked twin
// pkg/agent/window_runtime.go::turnState.appendWindowMessage, which latches
// and returns the same storage error so the turn exits visibly. The fix
// cannot make the write succeed under a refusing filesystem, so the required
// outcome is "visible storage failure / no false success" — NOT the presence
// of the assistant line in the archive.
//
// Real units: memory.JSONLStore rooted at a temp dir (real append + fsync),
// session.JSONLBackend, a real turnState from newTurnState, the exact
// agentLoopRunTurn* chain AgentLoop.runTurn builds (pkg/agent/loop.go::
// AgentLoop.runTurn), the real AgentLoop.emitEvent path over a real
// EventBus. No provider is needed — finalizeTurn performs no LLM call. The
// transcript store is deliberately not wired (appendErrorTranscript /
// appendAssistantTranscript suppress with a WARN on a nil store,
// pkg/agent/turn_transcript.go) so the user-facing transcript.jsonl surface
// and the model archive stay distinguished: this test asserts on the model
// archive only.
//
// Failure injection is a genuine external filesystem condition, never a
// production hook, flag, or input branch: after the earlier setup (the user
// input) is persisted and verified on disk, the archive file is swapped for
// a DIRECTORY at the same path, so the next append's open(O_CREATE|O_WRONLY|
// O_APPEND) fails EISDIR — deterministic and uid-independent, unlike chmod
// under a root runner. A capability probe opens the swapped path for append
// before the scenario and refuses with a loud BLOCKED fatal (never a skip)
// if the environment can still write. The exact pre-failure bytes are
// restored afterwards so the archive assertions see the true post-failure
// state: earlier data intact, assistant line absent.
//
// Cases: (1) final assistant reply against a refused archive must not
// report success — RED on pre-fix code (today the error is swallowed and
// finalizeTurn completes "successfully"); (2) positive control — the
// identical harness with a healthy archive persists the reply and completes
// cleanly, proving the harness and instrument are not what fails case 1.
//
// Not in this file (follow-up commits, same seam): SubTurn-result poll
// (loop_run_turn_tools.go), continuation flush (loop_truncation.go),
// graceful-shutdown marker (loop.go::writeTurnCancelledRestartForActiveTurns),
// duplicate-tool denial (loop_run_turn.go). CHECK (fresh instance) owns
// GREEN and the mutation probes (e.g. returning the append error to the
// caller, dropping the session_save event emit, forcing turnStatus
// completed on the error path); no mutation runs in this RED lane.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// archiveErrHarness wires the real durability stack the finalize path uses:
// real JSONL store + real backend + real event bus. Nothing here mocks any
// unit under test; the filesystem condition is applied by the tests after
// setup, outside this constructor.
type archiveErrHarness struct {
	al    *AgentLoop
	agent *AgentInstance
	store *memory.JSONLStore
	dir   string // the store's data dir ({home}/context)
	key   string // session key of the archive under test
}

func newArchiveErrHarness(t *testing.T, key string) *archiveErrHarness {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "context")
	store, err := memory.NewJSONLStore(dir)
	require.NoError(t, err, "real JSONL fixture must open")
	t.Cleanup(func() { require.NoError(t, store.Close(), "close real store") })
	cfg := &config.Config{Context: config.DefaultContextSettings()}
	agent := &AgentInstance{
		ID: "arch-err", Sessions: session.NewJSONLBackend(store), MaxIterations: 30,
	}
	al := &AgentLoop{cfg: cfg, eventBus: NewEventBus()}
	return &archiveErrHarness{al: al, agent: agent, store: store, dir: dir, key: key}
}

// persistUserInput writes the earlier setup message through the CHECKED
// admission seam (memory.JSONLStore.AddFullMessage — the same append path
// and lock the production appends ride, per that function's own comment) and
// verifies it is on disk before the failure is applied, so the scenario can
// never mistake a missing setup write for the targeted failure.
func (h *archiveErrHarness) persistUserInput(t *testing.T, content string) {
	t.Helper()
	require.NoError(t, h.store.AddFullMessage(context.Background(), h.key,
		providers.Message{Role: "user", Content: content}),
		"setup append must not hide storage errors")
	archive, err := h.store.ReadArchive(context.Background(), h.key)
	require.NoError(t, err, "read archive after setup append")
	require.Len(t, archive, 1, "setup must leave exactly the user line on disk")
	require.Equal(t, "user", archive[0].Role)
	require.Equal(t, content, archive[0].Content)
}

// archiveFilePath derives the on-disk archive from the store's own layout
// ({dir}/{key}.jsonl — pkg/memory/jsonl.go::JSONLStore.jsonlPath) by
// requiring exactly one .jsonl file to exist, instead of re-implementing
// key sanitisation in the test.
func (h *archiveErrHarness) archiveFilePath(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(h.dir, "*.jsonl"))
	require.NoError(t, err, "glob the store dir")
	require.Len(t, matches, 1, "setup must have created exactly one archive file")
	return matches[0]
}

// finalizeWithContent builds the exact chain AgentLoop.runTurn builds
// (pkg/agent/loop.go::AgentLoop.runTurn) with the response stage already
// complete, and runs the real finalizeTurn — the function whose AddMessage+
// Save seam is under test. The iteration starts in the completed status the
// conductor sets before finalize (see runTurn's TurnEndStatusCompleted
// default), so only a storage refusal may change it.
func (h *archiveErrHarness) finalizeWithContent(
	ts *turnState, content string,
) (turnResult, error, *agentLoopRunTurnIteration) {
	rt := &agentLoopRunTurn{al: h.al, ts: ts, turnCtx: context.Background()}
	rf := &agentLoopRunTurnFallbacks{rt: rt}
	ri := &agentLoopRunTurnIteration{rf: rf, turnStatus: TurnEndStatusCompleted}
	rq := &agentLoopRunTurnRequest{ri: ri}
	rr := &agentLoopRunTurnResponse{rq: rq}
	rx := &agentLoopRunTurnTools{ctx: context.Background(), rr: rr, finalContent: content}
	rc := &agentLoopRunTurnConductor{rx: rx}
	rz := &agentLoopRunTurnFinalize{rc: rc}
	res, err := rz.finalizeTurn()
	return res, err, ri
}

func newArchiveErrTurnState(agent *AgentInstance, key, userText string) *turnState {
	return newTurnState(agent, processOptions{
		SessionKey: key, UserMessage: userText, Channel: "scheduled",
		SkipInitialSteeringPoll: true,
	}, turnEventScope{agentID: agent.ID, sessionKey: key, turnID: key + "-turn"})
}

// The every-turn site: the final assistant reply must not vanish with a
// clean success when the archive refuses the append. Desired contract per
// finalizeTurn's own (currently dead) save-error branch and the checked
// admission twin: the error surfaces, the turn status records the error,
// and a session_save error event reaches the event bus.
func TestArchiveWriteError_FinalAssistantReply_TurnMustNotReportSuccess(t *testing.T) {
	h := newArchiveErrHarness(t, "arch-err-final")
	userText := "user asks the model a question"
	reply := "the final assistant reply for this turn"

	h.persistUserInput(t, userText)
	ts := newArchiveErrTurnState(h.agent, h.key, userText)

	archivePath := h.archiveFilePath(t)

	// Positive instrument check: the healthy archive currently accepts an
	// append-open (open only, no write — a canary byte would corrupt JSONL).
	healthy, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err, "healthy archive must accept an append-open before the swap")
	require.NoError(t, healthy.Close(), "close the probe handle")

	// Preserve the exact pre-failure bytes so the post-failure archive state
	// can be verified after the condition is removed.
	preserved, err := os.ReadFile(archivePath)
	require.NoError(t, err, "read archive bytes before the swap")

	// Apply the genuine filesystem condition: the archive path now holds a
	// directory, so any O_CREATE|O_WRONLY|O_APPEND open fails EISDIR
	// regardless of the effective uid (chmod 0444 would not bind under a
	// root runner).
	require.NoError(t, os.Remove(archivePath), "remove the archive file for the swap")
	require.NoError(t, os.Mkdir(archivePath, 0o755), "stand a directory in the archive's place")
	t.Cleanup(func() {
		// The swap is scenario state; TempDir cleanup would also handle it,
		// but restore the regular file so the in-test assertions below see
		// the real post-failure bytes.
		if err := os.RemoveAll(archivePath); err != nil {
			t.Fatalf("cleanup: remove swapped-in directory: %v", err)
		}
		if err := os.WriteFile(archivePath, preserved, 0o644); err != nil {
			t.Fatalf("cleanup: restore archive bytes: %v", err)
		}
	})

	// Capability probe: the instrument must ACTUALLY prevent the targeted
	// write. If an environment (e.g. privileged runner) can still open the
	// path for append, this scenario cannot test what it claims — fail
	// loudly instead of reporting a green that verified nothing.
	blocked, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		blocked.Close()
		t.Fatal("BLOCKED: archive path still accepts an append-open after the swap — " +
			"this environment bypasses the filesystem condition (privileged runner?), " +
			"so the test cannot inject a genuine write failure here")
	}
	var pathErr *fs.PathError
	require.ErrorAs(t, err, &pathErr, "refusal must be an fs path error, got %T", err)
	require.True(t,
		errors.Is(err, syscall.EISDIR) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM),
		"refusal must be a write-refusal errno (EISDIR for a directory at the path, "+
			"EACCES/EPERM for permission-family refusals on other platforms), got %v", err)

	sub := h.al.SubscribeEvents(16)
	defer h.al.UnsubscribeEvents(sub.ID)

	res, err, ri := h.finalizeWithContent(ts, reply)

	// 1. The storage failure must be visible to the caller — nil here is the
	// defect itself (loss reported as success).
	require.Error(t, err,
		"finalizeTurn must return the archive write failure; a nil error means the "+
			"final assistant reply was silently dropped while the turn reported success")

	// 2. The turn's recorded end status must be the error status — exactly
	// what finalizeTurn's save-error branch sets. The returned turnResult is
	// deliberately empty on that branch (return turnResult{}, err), so the
	// iteration state is the engine's end-status surface, not res.status.
	require.Equal(t, TurnEndStatusError, ri.turnStatus,
		"turn must end in the error status when the final reply cannot be persisted; "+
			"got %q — a completed/other status is a false success", ri.turnStatus)

	// 3. The surfaced signal a user-facing channel renders: an EventKindError
	// whose payload names the session_save stage (the dead branch's own
	// payload contract).
	events := collectEventStream(sub.C)
	errEvt, ok := findEvent(events, EventKindError)
	require.True(t, ok,
		"an EventKindError must be emitted when the final reply cannot be persisted — "+
			"this is the signal a user-facing channel renders; got events: %+v", events)
	errPayload, ok := errEvt.Payload.(ErrorPayload)
	require.True(t, ok, "error event payload must be ErrorPayload, got %T", errEvt.Payload)
	require.Equal(t, "session_save", errPayload.Stage,
		"error event must name the session_save stage per finalizeTurn's save-error contract")

	// 4. Restore the pre-failure bytes and verify the true storage outcome:
	// the refusal genuinely prevented the targeted write (no phantom success
	// data), and the earlier setup data is untouched.
	require.NoError(t, os.RemoveAll(archivePath), "remove swapped-in directory")
	require.NoError(t, os.WriteFile(archivePath, preserved, 0o644), "restore archive bytes")
	archive, readErr := h.store.ReadArchive(context.Background(), h.key)
	require.NoError(t, readErr, "archive must be readable again after the file is restored")
	require.Len(t, archive, 1,
		"archive must hold exactly the earlier user line: the assistant line was "+
			"refused by the filesystem and must not appear from a failed append")
	require.Equal(t, "user", archive[0].Role, "earlier setup data must survive untouched")
	require.Equal(t, userText, archive[0].Content)

	// No false success in the result either: the failed turn must not carry
	// the reply content forward as if it had been persisted.
	require.NotEqual(t, TurnEndStatusCompleted, res.status,
		"returned turn result must not claim completed status for a failed persist")
}

// Positive control: the identical harness with a HEALTHY archive must
// persist the final assistant reply and complete cleanly. This proves the
// harness, chain construction and instrument placement are sound — case 1's
// failure comes from the filesystem condition, not from the fixture.
func TestArchiveWriteError_PositiveControl_HealthyArchivePersistsFinalReply(t *testing.T) {
	h := newArchiveErrHarness(t, "arch-err-control")
	userText := "user asks the control question"
	reply := "control: final assistant reply persisted"

	h.persistUserInput(t, userText)
	ts := newArchiveErrTurnState(h.agent, h.key, userText)

	sub := h.al.SubscribeEvents(16)
	defer h.al.UnsubscribeEvents(sub.ID)

	res, err, ri := h.finalizeWithContent(ts, reply)

	require.NoError(t, err, "healthy archive: the final reply must persist without error")
	require.Equal(t, TurnEndStatusCompleted, ri.turnStatus,
		"healthy archive: turn must keep the completed status")
	require.Equal(t, TurnEndStatusCompleted, res.status,
		"healthy archive: returned result must carry the completed status")
	require.Equal(t, reply, res.finalContent,
		"healthy archive: result must carry exactly the finalized content")

	events := collectEventStream(sub.C)
	_, ok := findEvent(events, EventKindError)
	require.False(t, ok,
		"healthy archive: no error event may be emitted; got events: %+v", events)

	archive, readErr := h.store.ReadArchive(context.Background(), h.key)
	require.NoError(t, readErr, "healthy archive must stay readable")
	require.Len(t, archive, 2, "healthy archive must hold exactly user + assistant lines")
	require.Equal(t, "user", archive[0].Role)
	require.Equal(t, userText, archive[0].Content)
	require.Equal(t, "assistant", archive[1].Role)
	require.Equal(t, reply, archive[1].Content)
}
