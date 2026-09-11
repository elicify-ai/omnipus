// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"reflect"
	"testing"
)

// goalStructField looks up a field of Goal by exact name via reflection,
// returning the field and whether it was found. Used by the two
// "deleted field" tests below so they fail loudly (not silently pass) if
// the field is ever reintroduced under any name.
func goalStructField(name string) (reflect.StructField, bool) {
	return reflect.TypeOf(Goal{}).FieldByName(name)
}

// goalJSONTags returns the json tag (name portion, before any comma option)
// of every field on Goal, so a test can assert a wire key is absent
// regardless of which Go field name it might have been reintroduced under.
func goalJSONTags(t *testing.T) map[string]bool {
	t.Helper()
	typ := reflect.TypeOf(Goal{})
	tags := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := tag
		if idx := indexComma(tag); idx >= 0 {
			name = tag[:idx]
		}
		tags[name] = true
	}
	return tags
}

func indexComma(s string) int {
	for i, r := range s {
		if r == ',' {
			return i
		}
	}
	return -1
}

// TestGoalRouteSessionKeyRemoved proves GOAL-FR-032, S-37: the pre-existing
// goal_route_session_key field — written by the old
// pkg/agent/goal_triggers.go::recordGoalRouting and read by nothing — does
// not survive onto the goal record, under any Go field name or wire key.
func TestGoalRouteSessionKeyRemoved(t *testing.T) {
	if _, ok := goalStructField("RouteSessionKey"); ok {
		t.Error("Goal has a RouteSessionKey field — GOAL-FR-032 requires it deleted, not carried forward")
	}
	if _, ok := goalStructField("SessionKey"); ok {
		t.Error("Goal has a SessionKey field — GOAL-FR-032 requires the session key deleted, not renamed")
	}
	tags := goalJSONTags(t)
	for _, forbidden := range []string{"route_session_key", "session_key", "goal_route_session_key"} {
		if tags[forbidden] {
			t.Errorf("Goal has a %q wire field — GOAL-FR-032 requires the session key deleted", forbidden)
		}
	}
}

// TestGoalRouteAgentFoldsIntoOwner proves GOAL-FR-033, S-37: the
// pre-existing goal_route_agent_id field does not survive as a separate
// routing field — the goal's owner reference (OwnerKind/OwnerID,
// GOAL-FR-002) is what the engine derives a routing agent from instead.
func TestGoalRouteAgentFoldsIntoOwner(t *testing.T) {
	if _, ok := goalStructField("RouteAgentID"); ok {
		t.Error("Goal has a RouteAgentID field — GOAL-FR-033 requires it folded into the owner reference, not persisted separately")
	}
	if _, ok := goalStructField("AgentID"); ok {
		t.Error("Goal has a bare AgentID field — GOAL-FR-033 requires agent identity to come from OwnerKind/OwnerID")
	}
	tags := goalJSONTags(t)
	for _, forbidden := range []string{"route_agent_id", "agent_id", "goal_route_agent_id"} {
		if tags[forbidden] {
			t.Errorf("Goal has a %q wire field — GOAL-FR-033 requires the routing agent id folded into owner_kind/owner_id", forbidden)
		}
	}

	// The owner reference that GOAL-FR-033 says the agent identity folds
	// into MUST itself be present and populated (GOAL-FR-002).
	g := newTestGoal(t, "session", "sess-owner-1")
	if g.OwnerKind == "" {
		t.Error("OwnerKind is empty — nothing for the routing agent id to fold into")
	}
	if g.OwnerID != "sess-owner-1" {
		t.Errorf("OwnerID = %q, want %q", g.OwnerID, "sess-owner-1")
	}
}

// TestGoalRouteChannelAndChatIDSurvive proves GOAL-FR-034, S-37: the two
// surviving routing fields exist with the exact wire keys
// contracts/components/schemas/Goal.yaml declares, and SetRoute/HasRoute
// round-trip them correctly — including the "both required together" rule
// dispatchGoalAsyncFollowUp's abort-without-either behaviour depends on.
func TestGoalRouteChannelAndChatIDSurvive(t *testing.T) {
	tags := goalJSONTags(t)
	if !tags["route_channel"] {
		t.Error(`Goal has no "route_channel" wire field — GOAL-FR-034 requires it to survive`)
	}
	if !tags["route_chat_id"] {
		t.Error(`Goal has no "route_chat_id" wire field — GOAL-FR-034 requires it to survive`)
	}

	cases := []struct {
		name         string
		channel      string
		chatID       string
		wantHasRoute bool
	}{
		{name: "neither set", channel: "", chatID: "", wantHasRoute: false},
		{name: "channel only", channel: "telegram", chatID: "", wantHasRoute: false},
		{name: "chat id only", channel: "", chatID: "12345", wantHasRoute: false},
		{name: "both set", channel: "telegram", chatID: "12345", wantHasRoute: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newTestGoal(t, "session", "sess-1")
			g.SetRoute(c.channel, c.chatID)
			if g.RouteChannel != c.channel {
				t.Errorf("RouteChannel = %q, want %q", g.RouteChannel, c.channel)
			}
			if g.RouteChatID != c.chatID {
				t.Errorf("RouteChatID = %q, want %q", g.RouteChatID, c.chatID)
			}
			if got := g.HasRoute(); got != c.wantHasRoute {
				t.Errorf("HasRoute() = %v, want %v", got, c.wantHasRoute)
			}
		})
	}
}

// TestRecordRoutingLostWritesWhenEmpty proves S-39's happy path: a goal
// with no LatestReason yet gets the routing-lost note written, and reports
// that it wrote.
func TestRecordRoutingLostWritesWhenEmpty(t *testing.T) {
	g := newTestGoal(t, "session", "sess-1")
	if g.LatestReason != "" {
		t.Fatalf("precondition: LatestReason = %q, want empty", g.LatestReason)
	}
	wrote := g.RecordRoutingLost()
	if !wrote {
		t.Error("RecordRoutingLost() = false, want true (LatestReason was empty)")
	}
	if g.LatestReason != RoutingLostReason {
		t.Errorf("LatestReason = %q, want %q", g.LatestReason, RoutingLostReason)
	}
}

// TestRecordRoutingLostDoesNotOverwriteFresherReason proves S-39: a real,
// more informative reason already on the record (e.g. a fresh judge
// verdict reason from the same settle pass) is never stomped by a
// routing-lost note.
func TestRecordRoutingLostDoesNotOverwriteFresherReason(t *testing.T) {
	g := newTestGoal(t, "session", "sess-1")
	const freshReason = "criterion 2 of 3 unmet: missing test coverage"
	g.LatestReason = freshReason

	wrote := g.RecordRoutingLost()
	if wrote {
		t.Error("RecordRoutingLost() = true, want false — a fresher reason must not be overwritten")
	}
	if g.LatestReason != freshReason {
		t.Errorf("LatestReason = %q, want unchanged %q", g.LatestReason, freshReason)
	}
}

// TestRecordRoutingLostNoChurnWhenAlreadySet proves S-39's second clause:
// calling RecordRoutingLost again while LatestReason already holds the
// identical routing-lost note is a no-op (no churn) — it does not, for
// instance, re-warn or reset any other field.
func TestRecordRoutingLostNoChurnWhenAlreadySet(t *testing.T) {
	g := newTestGoal(t, "session", "sess-1")
	g.LatestReason = RoutingLostReason

	wrote := g.RecordRoutingLost()
	if wrote {
		t.Error("RecordRoutingLost() = true, want false — an identical existing note must not churn")
	}
	if g.LatestReason != RoutingLostReason {
		t.Errorf("LatestReason = %q, want unchanged %q", g.LatestReason, RoutingLostReason)
	}
}
