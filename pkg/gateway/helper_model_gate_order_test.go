package gateway

// #1537 Q11 A: spec API and Data item 3 / FR-009 / FR-013.
// Across turns: first notice. Within a turn: last publish. The projection
// retains one CURRENT value per type, not a historical list of same-type notices.
import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hmGateProject(t *testing.T, p *activeTurnProjection, turn, event string, retryOrdinal int) {
	t.Helper()
	meta, raw := hmProjectionFrame(t, event, retryOrdinal)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(raw, &frame))
	frame["turn_id"], frame["child_session_id"], frame["agent_id"] = turn, "child-"+turn, "agent-"+turn
	if event == "recovered" {
		frame["retries"] = max(1, retryOrdinal)
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	meta.turnID, meta.key = turn, fmt.Sprint(frame["type"])+":"+turn
	p.update(meta, raw, hmRoot)
}

func hmGateReplay(t *testing.T, p *activeTurnProjection) (turns, aNotices []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, raw := range projectionFrames(hmRoot, p.snapshot(), map[string]bool{}) {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		turn, ok := frame["turn_id"].(string)
		require.True(t, ok, "SETUP: each projected helper notice is addressed to its actual turn")
		if !seen[turn] {
			turns = append(turns, turn)
			seen[turn] = true
		}
		if turn == "A" {
			notice := fmt.Sprint(frame["state"])
			if frame["type"] == "provider_retry" {
				notice = fmt.Sprintf("retry:%v", frame["attempt"])
			}
			aNotices = append(aNotices, notice)
		}
	}
	return turns, aNotices
}

func TestHelperProjectionFirstNoticeOrderSurvivesLaterAUpdates(t *testing.T) {
	for _, later := range []string{"second-retry", "silence-then-retry"} {
		t.Run(later, func(t *testing.T) {
			var p activeTurnProjection
			for _, turn := range []string{"A", "B", "C", "D"} {
				hmGateProject(t, &p, turn, "retry", 0)
			}
			before, _ := hmGateReplay(t, &p)
			require.Equal(t, []string{"A", "B", "C", "D"}, before, "Q11 positive control: four turns first noticed in A/B/C/D order")
			wantEarlier := "recovered"
			if later == "second-retry" {
				hmGateProject(t, &p, "A", "recovered", 0)
			} else {
				hmGateProject(t, &p, "A", "waiting", 0)
				hmGateProject(t, &p, "A", "responding", 0)
				wantEarlier = "responding"
			}
			hmGateProject(t, &p, "A", "retry", 1) // explicit attempt 3 replaces A's earlier attempt 2
			turns, aNotices := hmGateReplay(t, &p)
			require.Equal(t, []string{wantEarlier, "retry:3"}, aNotices, "Q11 WITHIN A: later retry replays after the earlier status; no stale status wins")
			assert.Equal(t, []string{"A", "B", "C", "D"}, turns, "Q11 ACROSS turns: A's later notice must not move its first-notice position behind B/C/D")
			// Re-use the SAME status key: recovery must replay after retry.
			// CR1 below also pins status-before-retry creation order.
			hmGateProject(t, &p, "A", "recovered", 2)
			turns, aNotices = hmGateReplay(t, &p)
			require.Equal(t, []string{"retry:3", "recovered"}, aNotices, "Q11 WITHIN A: recovered after retry must replay LAST; stale retry cannot reappear")
			assert.Equal(t, []string{"A", "B", "C", "D"}, turns, "Q11 ACROSS turns: even recovery keeps A's first-notice position")
		})
	}
}

func TestHelperProjectionCR1SilenceRespondingRetryKeepsFirstNoticeOrder(t *testing.T) {
	var p activeTurnProjection
	hmGateProject(t, &p, "A", "waiting", 0) // t0: A first notices by silence, not a retry
	hmGateProject(t, &p, "B", "retry", 0)   // t1
	hmGateProject(t, &p, "A", "responding", 0)
	hmGateProject(t, &p, "A", "retry", 0) // t2: FR-013 clear before the 429 retry
	hmGateProject(t, &p, "C", "retry", 0) // t3
	hmGateProject(t, &p, "D", "retry", 0) // t4
	turns, aNotices := hmGateReplay(t, &p)
	require.Equal(t, []string{"responding", "retry:2"}, aNotices, "CR1 WITHIN A: FR-013 responding replays before its retry")
	assert.Equal(t, []string{"A", "B", "C", "D"}, turns, "CR1 ACROSS turns: first silence fixes A's row before B/C/D through reload")
	hmGateProject(t, &p, "A", "recovered", 1)
	turns, aNotices = hmGateReplay(t, &p)
	require.Equal(t, []string{"retry:2", "recovered"}, aNotices, "CR1 WITHIN A: recovered overwrites the earlier status but must replay after the stale retry")
	assert.Equal(t, []string{"A", "B", "C", "D"}, turns, "CR1 ACROSS turns: a recovered A remains first, never in the overflow")
}
