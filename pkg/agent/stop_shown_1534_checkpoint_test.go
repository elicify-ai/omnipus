package agent

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: /workspace/logs/1534/arch-decision.md §§2,4,5. Reflect ONLY the
// not-yet-implemented CancelScope.Shown data carrier, never a private state
// field or a replacement algorithm. The real claimActiveTurn consumes it.
// Missing implementation is a loud BLOCKED failure, not a compile error/skip.
func checkpoint1534Scope(t *testing.T, ts *turnState, messageID, turnID string, count int) CancelScope {
	t.Helper()
	scope := CancelScope{TurnOnly: true}
	field := reflect.ValueOf(&scope).Elem().FieldByName("Shown")
	if !field.IsValid() {
		t.Fatal("BLOCKED: CancelScope.Shown not implemented — required by #1534 architect decision §2 plumbing, §4 W5")
	}
	require.Equal(t, reflect.Pointer, field.Kind(), "§2: optional checkpoint carrier")
	value := reflect.New(field.Type().Elem())
	for _, item := range []struct{ name, text string }{{"MessageID", messageID}, {"TurnID", turnID}} {
		f := value.Elem().FieldByName(item.name)
		require.True(t, f.IsValid() && f.CanSet(), "checkpoint carrier must expose %s", item.name)
		f.SetString(item.text)
	}
	counter := value.Elem().FieldByName("CodePoints")
	require.True(t, counter.IsValid() && counter.CanSet(), "checkpoint carrier must expose CodePoints")
	counter.SetInt(int64(count))
	field.Set(value)
	_ = ts // identity comes from the checkpoint argument, not server state
	return scope
}

func checkpoint1534Claim(t *testing.T, ts *turnState, scope CancelScope) bool {
	t.Helper()
	rc := &agentLoopRequestCancel{ctx: context.Background(), activeTurn: ts, scope: scope}
	return rc.claimActiveTurn()
}

func checkpoint1534Keep(t *testing.T, ts *turnState, messageID string) (int, bool) {
	t.Helper()
	reader, ok := any(ts).(interface {
		stopKeepFor(messageID string) (int, bool)
	})
	if !ok {
		t.Fatal("BLOCKED: turnState.stopKeepFor not implemented — required by #1534 architect decision §2, §5 agent unit cases")
	}
	return reader.stopKeepFor(messageID)
}

func Test1534Shown_StopKeepForMessageOrder(t *testing.T) {
	for _, tc := range []struct {
		name, shownMessage, writer, shownTurn string
		count, want                           int
		limited, omit                         bool
	}{
		{"same_message", "middle", "middle", "same", 2, 2, true, false},
		{"later_minted_message", "middle", "last", "same", 2, 0, true, false},
		{"earlier_message_unlimited", "middle", "first", "same", 2, 0, false, false},
		{"unknown_message_unlimited", "never-minted", "last", "same", 2, 0, false, false},
		{"other_turn_unlimited", "middle", "middle", "other-turn", 2, 0, false, false},
		{"empty_record_cuts_every_unsaved_message", "", "last", "same", 0, 0, true, false},
		{"empty_record_unknown_turn", "", "first", "", 0, 0, true, false},
		{"no_shown_legacy_unlimited", "", "last", "same", 0, 0, false, true},
		{"count_zero_same_message", "middle", "middle", "same", 0, 0, true, false},
		{"count_one_same_message", "middle", "middle", "same", 1, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := &turnState{turnID: "turn-checkpoint", finishedChan: make(chan struct{})}
			ids := map[string]string{}
			for _, label := range []string{"first", "middle", "last"} {
				ids[label] = ts.nextRoundMessageID()
			}
			shownID := ids[tc.shownMessage]
			if tc.shownMessage == "never-minted" {
				shownID = "never-minted"
			}
			shownTurn := tc.shownTurn
			if shownTurn == "same" {
				shownTurn = ts.turnID
			}
			scope := CancelScope{TurnOnly: true}
			if !tc.omit {
				scope = checkpoint1534Scope(t, ts, shownID, shownTurn, tc.count)
			}
			require.True(t, checkpoint1534Claim(t, ts, scope))
			keep, limited := checkpoint1534Keep(t, ts, ids[tc.writer])
			assert.Equal(t, tc.limited, limited, "§2 must choose the checkpoint by turn and mint order")
			if tc.limited {
				assert.Equal(t, tc.want, keep)
			}
		})
	}
}

// Capture both supported logging paths: the requirement is a WARN diagnostic,
// not a choice between pkg/logger and slog. Both captures have positive controls.
func checkpoint1534Log(t *testing.T) func() string {
	t.Helper()
	readFile := captureLogFile(t, logger.WARN)
	slogCapture := captureSlogWarnings(t)
	logger.WarnCF("1534-test", "checkpoint-file-positive-control", nil)
	slog.Warn("checkpoint-slog-positive-control")
	readWarnings := func() string {
		var output strings.Builder
		for _, line := range strings.Split(readFile(), "\n") {
			if strings.Contains(line, `"level":"warn"`) {
				output.WriteString(line + "\n")
			}
		}
		for _, line := range slogCapture.matching("level=WARN ") {
			output.WriteString(line + "\n")
		}
		return output.String()
	}
	require.Contains(t, readWarnings(), "checkpoint-file-positive-control")
	require.Contains(t, readWarnings(), "checkpoint-slog-positive-control")
	return readWarnings
}

func Test1534Shown_InvalidCheckpointWarnsWithoutText(t *testing.T) {
	for _, tc := range []struct{ name, message, turn, warning string }{
		{"unknown_message", "unknown-message", "turn-warning", "stop_checkpoint_unknown_message"},
		{"other_turn", "", "different-turn", "stop_checkpoint_turn_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readLog := checkpoint1534Log(t)
			ts := &turnState{turnID: "turn-warning", finishedChan: make(chan struct{})}
			ts.setStopShown("DO_NOT_LOG_CHECKPOINT_BODY", false)
			id := ts.nextRoundMessageID()
			messageID := tc.message
			if messageID == "" {
				messageID = id
			}
			require.True(t, checkpoint1534Claim(t, ts, checkpoint1534Scope(t, ts, messageID, tc.turn, 2)))
			_, limited := checkpoint1534Keep(t, ts, id)
			assert.False(t, limited, "invalid identity must fall back to the receipt cut")
			assert.Contains(t, readLog(), tc.warning, "§2 requires a content-free diagnostic")
			assert.NotContains(t, readLog(), "DO_NOT_LOG_CHECKPOINT_BODY", "warning must include IDs/counts only, never answer text")
		})
	}
}

func Test1534Shown_ClaimCancelFirstClaimWins(t *testing.T) {
	ts := &turnState{turnID: "turn-first-claim", finishedChan: make(chan struct{})}
	id := ts.nextRoundMessageID()
	first := checkpoint1534Scope(t, ts, id, ts.turnID, 1)
	require.True(t, checkpoint1534Claim(t, ts, first))
	second := checkpoint1534Scope(t, ts, id, ts.turnID, 9)
	require.False(t, checkpoint1534Claim(t, ts, second))
	keep, limited := checkpoint1534Keep(t, ts, id)
	assert.True(t, limited)
	assert.Equal(t, 1, keep, "losing Stop cannot replace the first checkpoint")
}

func Test1534Shown_AdministrativeCancelIgnoresCheckpoint(t *testing.T) {
	ts := &turnState{turnID: "turn-administrative", finishedChan: make(chan struct{})}
	id := ts.nextRoundMessageID()
	scope := checkpoint1534Scope(t, ts, id, ts.turnID, 1)
	scope.TurnOnly = false
	require.True(t, checkpoint1534Claim(t, ts, scope))
	_, limited := checkpoint1534Keep(t, ts, id)
	assert.False(t, limited, "§2: administrative cancellation is not client Stop")
	assert.False(t, ts.stopRequested.Load())
}

// Recording transport edge, not an alternate finalizer/cutting implementation.
// It records the optional handoff in its exact order, with no trimming logic.
type checkpoint1534Streamer struct{ events []string }

func (*checkpoint1534Streamer) Update(context.Context, string) error { return nil }
func (*checkpoint1534Streamer) Cancel(context.Context)               {}
func (*checkpoint1534Streamer) SetMessageID(string)                  {}
func (s *checkpoint1534Streamer) SetStopLimit(keep int) {
	s.events = append(s.events, fmt.Sprintf("limit:%d", keep))
}
func (s *checkpoint1534Streamer) Finalize(_ context.Context, content string) error {
	s.events = append(s.events, "finalize:"+content)
	return nil
}

func Test1534Shown_FinalizeStreamerSetsLimitBeforeFinalize(t *testing.T) {
	probe := &checkpoint1534Streamer{}
	ts := &turnState{turnID: "turn-finalize", lastStreamer: probe, finishedChan: make(chan struct{})}
	id := ts.nextRoundMessageID()
	ts.stampStreamerMessageID(probe)
	require.True(t, checkpoint1534Claim(t, ts, checkpoint1534Scope(t, ts, id, ts.turnID, 3)))
	ts.SetFinalContent("abcUNSEEN")
	require.NoError(t, ts.finalizeStreamer(context.Background()))
	assert.Equal(t, []string{"limit:3", "finalize:abcUNSEEN"}, probe.events,
		"W2 must hand the limit to the real transport BEFORE Finalize; transport W1 owns the final cut")
}

func Test1534Shown_EndTurnAtStopCutsWholeAnswerAndModelMemory(t *testing.T) {
	for _, continued := range []bool{false, true} {
		t.Run(fmt.Sprintf("continued_%t", continued), func(t *testing.T) {
			h := wu22NewHarness(t, wu22HealthyProvider())
			h.ts.setLastStreamer(nil)
			id := h.ts.nextRoundMessageID()
			if continued {
				h.ts.appendToAccumulator("A😀")
				h.ts.markContinuationDispatched()
				require.Equal(t, id, h.ts.nextRoundMessageID(), "continuation keeps the whole wire message identity")
				h.ts.setStopShown("éUNSEEN", false)
			} else {
				h.ts.setStopShown("A😀éUNSEEN", false)
			}
			require.True(t, checkpoint1534Claim(t, h.ts, checkpoint1534Scope(t, h.ts, id, h.ts.turnID, 4)))
			rx := &agentLoopRunTurnTools{ctx: context.Background(), rr: h.responseStage()}
			cn := &agentLoopRunTurnConductorRunIterations{rc: &agentLoopRunTurnConductor{rx: rx}}
			assert.Equal(t, agentLoopRunTurnConductorRunIterationsBreakL1, cn.endTurnAtStop())
			assert.Equal(t, "A😀é", rx.finalContent, "W3 cuts after joining continuation, by code points")
			rz := &agentLoopRunTurnFinalize{rc: cn.rc}
			_, err := rz.finalizeTurn()
			require.NoError(t, err)
			assert.Equal(t, []providers.Message{{Role: "assistant", Content: "A😀é"}}, h.store.GetHistory(h.sid), "next-turn model memory must match the checkpoint")
			entries, err := h.store.ReadTranscript(h.sid)
			require.NoError(t, err)
			var bodies []string
			for _, entry := range entries {
				if entry.Role == "assistant" && entry.Content != "" {
					bodies = append(bodies, entry.Content)
				}
			}
			assert.Equal(t, []string{"A😀é"}, bodies, "W3 no-streamer transcript must have exactly the cut answer")
		})
	}
}

func Test1534Shown_PreserveTruncatedAccumulatorCutsWholeAnswer(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		t.Run(fmt.Sprintf("streamed_%t", streamed), func(t *testing.T) {
			h := wu22NewHarness(t, wu22HealthyProvider())
			if !streamed {
				h.ts.setLastStreamer(nil)
			}
			id := h.ts.nextRoundMessageID()
			h.ts.appendToAccumulator("A😀")
			h.ts.markContinuationDispatched()
			require.Equal(t, id, h.ts.nextRoundMessageID())
			h.ts.setStopShown("éUNSEEN", false)
			require.True(t, checkpoint1534Claim(t, h.ts, checkpoint1534Scope(t, h.ts, id, h.ts.turnID, 4)))
			h.al.preserveTruncatedAccumulator(h.ts)
			assert.Equal(t, "A😀é", h.ts.finalContent, "W4 applies the same whole-message cut")
			assert.False(t, h.ts.continuationUnresolved())
			if !streamed {
				entries, err := h.store.ReadTranscript(h.sid)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				assert.Equal(t, "A😀é", entries[0].Content)
				assert.Equal(t, id, entries[0].ID)
			}
		})
	}
}
