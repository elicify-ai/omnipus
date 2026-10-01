package agent

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// A cancellation belongs to one assistant position, not every reuse of its id.
// These positions are internal view identities, never persisted or sent as JSON.
type restartCallIdentity struct {
	assistantIndex int
	toolCallID     string
}

type restartToolGroup struct {
	assistantIndex int
	calls          map[string]bool // true once the declared result is present
	results        []int
	markers        map[int]string
	valid          bool
}

func newRestartToolGroup(index int, calls []providers.ToolCall) *restartToolGroup {
	g := &restartToolGroup{assistantIndex: index, calls: make(map[string]bool, len(calls)),
		markers: make(map[int]string), valid: true}
	for _, call := range calls {
		if _, duplicate := g.calls[call.ID]; call.ID == "" || duplicate {
			g.valid = false
		}
		g.calls[call.ID] = false
	}
	return g
}

func (g *restartToolGroup) addResult(index int, id string) {
	seen, declared := g.calls[id]
	if !declared || seen {
		g.valid = false
		return
	}
	g.calls[id] = true
	g.results = append(g.results, index)
}

func (g *restartToolGroup) finish(omitted map[int]bool, cancelled map[restartCallIdentity]bool) {
	if g == nil || !g.valid || len(g.markers) == 0 {
		return
	}
	incomplete := false
	for _, seen := range g.calls {
		incomplete = incomplete || !seen
	}
	if !incomplete {
		return // Completed or malformed groups are never recovery-filtered.
	}
	omitted[g.assistantIndex] = true
	for _, index := range g.results {
		omitted[index] = true
	}
	for index, id := range g.markers {
		omitted[index] = true
		cancelled[restartCallIdentity{assistantIndex: g.assistantIndex, toolCallID: id}] = true
	}
}

// restartCancellationID recognizes only the existing ungraceful-recovery record.
// A graceful-shutdown record or JSON-looking prose grants no group authorization.
func restartCancellationID(msg providers.Message) string {
	if msg.Role != "system" {
		return ""
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &record); err != nil {
		return ""
	}
	kind, _ := record["type"].(string)
	reason, _ := record["reason"].(string)
	id, _ := record["tool_call_id"].(string)
	if kind != "turn_canceled_restart" || reason != "ungraceful_shutdown_recovery" {
		return ""
	}
	return id
}

// restartCancellations scans one history in its original order. Every user or
// newer assistant closes the previous candidate; ids are never searched across
// that boundary. Invalid groups remain visible to validateWindowGroups even if
// they contain a marker, including when a later result exposes a duplicate.
func restartCancellations(count int, messageAt func(int) providers.Message) (map[int]bool, map[restartCallIdentity]bool) {
	omitted := make(map[int]bool)
	cancelled := make(map[restartCallIdentity]bool)
	var group *restartToolGroup
	for index := 0; index < count; index++ {
		msg := messageAt(index)
		switch msg.Role {
		case "user", "assistant":
			group.finish(omitted, cancelled)
			group = nil
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				group = newRestartToolGroup(index, msg.ToolCalls)
			}
		case "tool":
			if group != nil {
				group.addResult(index, msg.ToolCallID)
			}
		case "system":
			if group == nil || !group.valid {
				continue
			}
			id := restartCancellationID(msg)
			if seen, declared := group.calls[id]; id != "" && declared && !seen {
				group.markers[index] = id
			}
		}
	}
	group.finish(omitted, cancelled)
	return omitted, cancelled
}

// recoveryWindowHistory derives the model-only recovery view from the SAME
// atomic snapshot used for Skip, anchor and projections. Scan the full archive
// before selecting its retained suffix, so an evicted assistant can still own
// its cancellation. Filter message/index pairs without renumbering or mutating
// any archive record or captured metadata. Unrelated controls stay in place.
func recoveryWindowHistory(snap memory.WindowSnapshot) ([]providers.Message, []int) {
	omitted, _ := restartCancellations(len(snap.Archive), func(i int) providers.Message {
		return snap.Archive[i].Message
	})
	history, lines := memory.WindowHistory(snap)
	kept, keptLines := history[:0], lines[:0]
	for i, msg := range history {
		if !omitted[lines[i]] {
			kept = append(kept, msg)
			keptLines = append(keptLines, lines[i])
		}
	}
	return kept, keptLines
}
