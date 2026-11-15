package gateway

// #1537 orders 1c/1d: spec FR-008/013 and API and Data reload rules.
// Isolate projection behavior from the missing producer/copy implementation.
import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func hmProjectionFrame(t *testing.T, event string, n int) (hubFrameMeta, []byte) {
	t.Helper()
	kind, key, state := "provider_status", "provider_status:helper-turn", event
	if event == "retry" {
		kind, key = "provider_retry", "provider_retry:helper-turn"
	}
	f := map[string]any{"type": kind, "session_id": hmRoot, "turn_id": "helper-turn", "child_session_id": hmChild, "agent_id": hmAgent, "sent_at": hmSent.AddDate(0, 0, n).Format("2006-01-02T15:04:05Z")}
	if event != "retry" {
		f["state"] = state
	} else {
		f["attempt"] = n + 2
	}
	raw, err := json.Marshal(f)
	require.NoError(t, err)
	return hubFrameMeta{kind: hubKindItem, key: key, turnID: "helper-turn"}, raw
}

func TestProjectionKeepsHelperCopyAcrossRootDone(t *testing.T) {
	for _, ending := range []string{"done", "stop", "new-main-turn"} {
		t.Run(ending, func(t *testing.T) {
			var p activeTurnProjection
			m, raw := hmProjectionFrame(t, "retry", 0)
			p.update(m, raw, hmRoot)
			p.update(hubFrameMeta{kind: hubKindItem, key: "provider_retry:root-turn", turnID: "root-turn"}, []byte(`{"type":"provider_retry","turn_id":"root-turn"}`), hmRoot)
			if ending == "stop" {
				p.forgetTurn("root-turn")
			} else {
				p.update(hubFrameMeta{kind: hubKindDone, turnID: "root-turn"}, []byte(`{"type":"done","turn_id":"root-turn"}`), hmRoot)
			}
			if ending == "new-main-turn" {
				p.update(hubFrameMeta{kind: hubKindToken, turnID: "new-root", messageID: "new-root-message", content: "new answer"}, nil, hmRoot)
			}
			var helpers []projSnapshotItem
			for _, it := range p.snapshot() {
				if it.turnID == "helper-turn" {
					helpers = append(helpers, it)
				}
			}
			require.Len(t, helpers, 1, "FR-008: %s must retain exactly the helper copy on reload", ending)
			require.JSONEq(t, string(raw), string(helpers[0].start))
			p.forgetTurn("helper-turn")
			for _, it := range p.snapshot() {
				require.NotEqual(t, "helper-turn", it.turnID, "only the helper end releases its projection")
			}
		})
	}
}

func TestProjectionReplaysProviderItemsInLastPublishOrder(t *testing.T) {
	cases := []struct {
		events []string
		want   string
	}{
		{[]string{"waiting", "retry", "recovered"}, "recovered"},
		{[]string{"retry", "waiting", "recovered"}, "recovered"},
		{[]string{"recovered", "waiting", "retry"}, "retry"},
		{[]string{"waiting", "recovered", "retry"}, "retry"},
		{[]string{"retry", "recovered", "waiting"}, "waiting"},
		{[]string{"recovered", "retry", "waiting"}, "waiting"},
		{[]string{"retry", "recovered", "tool", "retry"}, "retry"},
		{[]string{"waiting", "responding", "retry"}, "retry"},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("sequence-%d", i), func(t *testing.T) {
			var p activeTurnProjection
			for n, event := range tc.events {
				if event == "tool" {
					p.update(hubFrameMeta{kind: hubKindToolStart, key: "tool-1", turnID: "helper-turn"}, []byte(`{"type":"tool_call_start","call_id":"tool-1"}`), hmRoot)
				} else {
					m, raw := hmProjectionFrame(t, event, n)
					p.update(m, raw, hmRoot)
				}
			}
			frames := projectionFrames(hmRoot, p.snapshot(), map[string]bool{})
			last := ""
			for _, raw := range frames {
				var f map[string]any
				require.NoError(t, json.Unmarshal(raw, &f))
				if f["type"] == "provider_retry" {
					last = "retry"
				}
				if f["type"] == "provider_status" {
					state, ok := f["state"].(string)
					require.True(t, ok, "projected status has a string state")
					last = state
				}
			}
			require.Equal(t, tc.want, last, "FR-008: replay final notice equals live final notice for %v", tc.events)
		})
	}
}

func TestHelperProjectionOtherItemsKeepCreationOrder(t *testing.T) {
	var p activeTurnProjection
	for _, key := range []string{"media:a", "error:b", "media:a"} {
		raw, _ := json.Marshal(map[string]any{"type": "media", "id": key})
		p.update(hubFrameMeta{kind: hubKindItem, key: key, turnID: "t"}, raw, hmRoot)
	}
	items := p.snapshot()
	require.Len(t, items, 2)
	require.Equal(t, []string{"media:a", "error:b"}, []string{items[0].id, items[1].id}, "provider-only reorder must not reorder media/errors")
}
