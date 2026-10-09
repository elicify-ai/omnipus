//go:build goolm && stdjson

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Scenario 5; MAJ-CW-005: obstruct the REAL atomic metadata writer while
// leaving reads available. No fake SessionStore, return injection or test hook.
func TestCWSlideR1_MetadataWriteFailureCannotInstallOrSendCandidate(t *testing.T) {
	h := cwR1New(t, 12_000)
	ts := h.turn("storage atomicity")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	b := agentContextBudget(h.agent)
	for _, id := range []string{"atomic-old-a", "atomic-old-b"} {
		h.append(t, cwR1Step(id, "", strings.Repeat("source ", b/3))...)
	}
	h.append(t, cwR1Step("atomic-newest", "", "small newest result")...)
	before := h.agent.Sessions.GetHistory(h.key)
	require.Greater(t, requestTokens(before, nil), b, "instrument: candidate must need persistent relief")
	live := cwR1Clone(t, before)
	meta, archive := h.meta(t), h.archive(t)
	r, p := cwR1OpenAI(t, 0)
	// Restore before cleanup removes the temporary directory, including on Fatal.
	t.Cleanup(func() { require.NoError(t, os.Chmod(h.dir, 0o700), "restore fixture permissions") })
	require.NoError(t, os.Chmod(h.dir, 0o500))
	probe := fileutil.WriteFileAtomic(filepath.Join(h.dir, "must-not-write.json"), []byte("{}"), 0o600)
	if !errors.Is(probe, os.ErrPermission) {
		t.Fatalf("BLOCKED: real metadata-write permission instrument did not fail with permission error (got %v) — required for MAJ-CW-005 storage-error proof", probe)
	}
	t.Logf("instrument: real atomic writer returned permission error: %v", probe)
	out, err := h.al.midTurnWindowCheck(ts, live, nil)
	// Never send the candidate when the real checkpoint returns an error.
	if err == nil {
		cwR1Send(t, cwR1Flow(h, ts, out, p))
	}
	require.ErrorIs(t, err, os.ErrPermission, "genuine storage failure must return, not disappear or become local size failure")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "storage error must preserve the original filesystem cause")
	require.Equal(t, before, live, "failed persistence cannot mutate the caller's already-installed live view")
	require.Equal(t, meta, h.meta(t), "Skip, anchor, exact projections and Count commit together or not at all")
	require.Equal(t, archive, h.archive(t), "failed candidate must not rewrite admitted archive evidence")
	require.Equal(t, before, h.agent.Sessions.GetHistory(h.key), "storage view cannot be half-installed")
	require.Empty(t, r.requests(t), "no provider send after a failed metadata commit")
}

// Scenario 6; MAJ-CW-005: start with a REAL persisted anchored/capped view,
// then make multiple advances and restore exactly that initial snapshot.
func TestCWSlideR1_AbortRestoresActualStartingSnapshot(t *testing.T) {
	for _, appendLines := range []bool{false, true} {
		name := "no_new_archive_line"
		if appendLines {
			name = "with_new_archive_lines"
		}
		t.Run(name, func(t *testing.T) {
			h := cwR1New(t, 80_000)
			h.cfg.Context.BuiltinSuccessCap = 1_200
			initial := h.turn("snapshot original user")
			h.append(t, providers.Message{Role: "user", Content: initial.userMessage})
			for _, id := range []string{"start-a", "start-b", "start-c", "start-d", "start-e", "start-f"} {
				h.append(t, cwR1Step(id, strings.Repeat("narration ", 160), strings.Repeat("source ", 100))...)
			}
			h.append(t, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{cwR1Call("start-newest")}})
			h.al.admitToolResult(initial, toolResultAdmission{Tool: "r1_tool", ToolCallID: "start-newest", Content: cwR1Unicode(4_000), ParallelN: 1})
			messages := h.al.assembleMessages(context.Background(), initial, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			r, p := cwR1OpenAI(t, 1)
			rr := cwR1Flow(h, initial, messages, p)
			cwR1Retry(t, rr)
			require.NoError(t, rr.rq.ri.rf.err)
			require.Len(t, r.requests(t), 2, "real forced slide establishes the starting anchored view")
			if cwR1Skip(t, h) == 0 {
				t.Fatal("BLOCKED: persisted nonzero Skip with original-user anchor not implemented — required by ADR-066 MAJ-CW-005 starting-snapshot fixture")
			}
			startWindow := h.agent.Sessions.GetHistory(h.key)
			cwR1AssertAnchor(t, startWindow, h.archive(t)[0].Message)
			startMeta, startArchive := h.meta(t), h.archive(t)
			startSkip := cwR1Skip(t, h)
			require.NotEmpty(t, h.agent.Sessions.Projection(h.key).Entries, "snapshot includes real projection records, not only a cursor")
			ts := h.turn(initial.userMessage) // The ONLY capture of the actual turn-start snapshot.
			live := h.al.assembleMessages(context.Background(), ts, startWindow, "", nil, nil)
			if appendLines {
				added := cwR1Step("during-aborted-turn", "new narration", "new result")
				h.append(t, added...)
				live = append(live, added...)
			}
			lastSkip, advances := startSkip, 0
			// Put B one token below the actual current candidate twice. Derive W
			// from the spec formula, rather than guess windows that may never fire.
			// There are five remaining older steps, so the floor is still reachable.
			for pass := 0; pass < 2; pass++ {
				cwR1SetBudget(t, h, requestTokens(live, nil)-1)
				live = h.check(t, ts, live)
				next := cwR1Skip(t, h)
				if next > lastSkip {
					advances++
				}
				lastSkip = next
			}
			require.Equal(t, 2, advances, "fixture made multiple actual post-snapshot Skip advances")
			require.NoError(t, ts.restoreSession(h.agent), "real abort restore returns visibly on storage failure")
			// FR-006 / DEL-12: the abort is NON-destructive. Skip, the anchor and
			// every exact projection field are restored; the archive KEEPS every
			// byte (count re-syncs to the physical length) and the aborted span is
			// recorded as a retained-but-excluded effect, asserted below.
			restoredMeta := h.meta(t)
			require.Equal(t, startMeta["skip"], restoredMeta["skip"], "abort restores Skip")
			require.Equal(t, startMeta["anchor_archive_line"], restoredMeta["anchor_archive_line"], "abort restores the anchor")
			require.Equal(t, startMeta["projection"], restoredMeta["projection"], "abort restores ALL exact projection fields; snapshot never refreshed")
			require.Equal(t, startSkip, cwR1Skip(t, h), "anchored slice length is NOT the Skip oracle")
			retained := h.archive(t)
			require.GreaterOrEqual(t, len(retained), len(startArchive), "FR-006: abort never drops retained archive bytes")
			require.Equal(t, startArchive, retained[:len(startArchive)], "undo never rewrites pre-turn archive evidence")
			restored := h.al.assembleMessages(context.Background(), h.turn(initial.userMessage), h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			h.agent.ContextWindow = 80_000 // Start projection is exact, not re-derived from the current admission clamp.
			startRequest := h.al.assembleMessages(context.Background(), initial, startWindow, "", nil, nil)
			require.Equal(t, cwR1Result(t, startRequest, "start-newest"), cwR1Result(t, restored, "start-newest"), "starting pressure/admission bytes are restored")
			cwR1AssertAnchor(t, restored, startArchive[0].Message)
		})
	}
}
