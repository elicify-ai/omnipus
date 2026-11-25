// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// I-2 / S-111 (ruling-check-delta "S-111 writers"): a /goal restate changes
// the goal's definition, so the restating message's provenance is joined into
// the goal record in the same write — for the marker-only restate as for the
// prose restate. An unresolved restating message makes the record unresolved;
// it is never skipped.
func TestGoalRestate_JoinsTheRestatingMessagesProvenance(t *testing.T) {
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	rows := []struct {
		name    string
		restate string
		writer  bus.Provenance
		want    []string
	}{
		{"marker restate from Telegram joins connector:telegram", "/goal [search: 2] [tests pass]", telegram, []string{"connector:telegram", "web"}},
		{"marker restate from an unresolved message makes the goal unresolved", "/goal [search: 2] [tests pass]", bus.UnresolvedProvenance(), nil},
		{"prose restate from Telegram joins connector:telegram", "/goal ship the release notes instead", telegram, []string{"connector:telegram", "web"}},
		{"prose restate from an unresolved message makes the goal unresolved", "/goal ship the release notes instead", bus.UnresolvedProvenance(), nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			agentInst, _ := al.GetRegistry().GetAgent("native-agent")
			agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
				"bash": config.ToolPolicyAllow, "search_web": config.ToolPolicyAllow,
			}})
			store, sid := newGoalTestSession(t, al, agentInst.ID)
			opts := processOptions{
				TranscriptStore: store, TranscriptSessionID: sid,
				Channel: "webchat", ChatID: "chat-s111-restate", SessionKey: "sk-s111", UserInitiated: true,
			}
			if matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
				bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts); !matched || handled {
				t.Fatalf("activation: matched=%v handled=%v", matched, handled)
			}
			// Given: the active goal records {web}.
			before := goalRecordForSession(t, sid)
			if _, err := resolveGoalRecordStore().Update(before.GoalID, goal.Write{Mutate: func(cur *goal.Goal) error {
				cur.Provenance = []string{"web"}
				return nil
			}}); err != nil {
				t.Fatalf("seed the goal's {web} provenance: %v", err)
			}

			// When: the restating message carries the row's provenance.
			opts.InputOrigin = inputOrigin{provenance: row.writer}
			if matched, _, reply := al.applyGoalCommandPrompt(context.Background(),
				bus.InboundMessage{Content: row.restate, UserInitiated: true}, agentInst, &opts); !matched {
				t.Fatalf("restate not matched (reply %q)", reply)
			}

			// Then: the same goal record carries the join.
			after, err := resolveGoalRecordStore().Get(before.GoalID)
			if err != nil {
				t.Fatalf("reload the goal record: %v", err)
			}
			if after.Prompt == before.Prompt {
				t.Fatalf("instrument: the restate did not change the goal's prompt (%q)", after.Prompt)
			}
			if !reflect.DeepEqual(after.Provenance, row.want) {
				t.Fatalf("goal provenance after the restate = %v, want %v", after.Provenance, row.want)
			}
		})
	}
}
