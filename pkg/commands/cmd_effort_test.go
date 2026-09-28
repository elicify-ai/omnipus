// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cmd_effort_test.go — RED tests for WP-H's backend half
// (thinking-reasoning-spec.md §9.5 "`/effort` slash command", §16 test 39,
// FR-023): /effort registered with Surfaces [Web, CLI, Channel]; on CLI and
// messenger channels it reads/patches SessionMeta.ReasoningEffort for
// commands.Runtime.SessionID through NEW conversation-effort callbacks on
// pkg/commands/runtime.go::Runtime; "default" clears; and the handler MUST
// NOT call Runtime.SwitchModel / ApplyAgentModel / any agent-config writer
// (the §2.2 explicit non-path). Web never writes the session field
// server-side (D23/D30 — the web palette reads/sets the per-message picker
// value only).
//
// Boundaries: real command registry + executor; the handler's declared seam
// (Runtime func-field callbacks) wired to a REAL session.UnifiedStore — the
// same MetaPatch path the production wiring test (pkg/agent tier) proves, so
// persistence assertions go to disk-backed state; rt.SwitchModel is a
// counting SPY (test 39's "zero agent-config writer calls" proof), which is
// exactly the seam buildCommandsRuntime wires to al.ApplyAgentModel.
//
// RED status at time of writing: Runtime.GetConversationEffort,
// Runtime.SetConversationEffort and the registered "effort" command DO NOT
// EXIST yet — this file is expected to fail to compile, naming them
// verbatim. That compile failure is the RED evidence.
package commands

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// effortFixture builds a registry + Runtime wired the way the agent loop's
// buildCommandsRuntime wires /effort per spec §9.5: conversation-effort
// callbacks backed by a REAL UnifiedStore, addressed by rt.SessionID, with
// a counting spy on SwitchModel (the agent-config-writer seam). Returns the
// registry, the runtime, the store (for disk-truth assertions), the session
// id, and the spy counter.
func newEffortFixture(t *testing.T) (*Registry, *Runtime, *session.UnifiedStore, string, *int) {
	t.Helper()
	reg := NewRegistry(BuiltinDefinitions())
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	meta, err := store.NewSession(session.SessionTypeChannel, "telegram", "agent-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sid := meta.ID

	calls := 0
	rt := &Runtime{
		SessionID: func() string { return sid },
		// The real persistence wiring per §9.5: read/patch through MetaPatch
		// on the real store — the same shape the production wiring must use.
		GetConversationEffort: func() (string, error) {
			m, err := store.GetMeta(sid)
			if err != nil {
				return "", err
			}
			return m.ReasoningEffort, nil
		},
		SetConversationEffort: func(value string) error {
			v := value
			return store.SetMeta(sid, session.MetaPatch{ReasoningEffort: &v})
		},
		// Test 39's spy: rt.SwitchModel is the only agent-config-writer seam
		// reachable from a commands handler — buildCommandsRuntime wires it
		// to al.ApplyAgentModel. Counting, never executing.
		SwitchModel: func(value string) (string, error) {
			calls++
			return "", fmt.Errorf("SPY: /effort must never call SwitchModel (got %q)", value)
		},
	}
	return reg, rt, store, sid, &calls
}

// runEffort executes text as a CLI-origin request and returns the reply.
func runEffort(t *testing.T, reg *Registry, rt *Runtime, text string) string {
	t.Helper()
	ex := NewExecutor(reg, rt)
	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    text,
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("%q: outcome=%v, want %v", text, res.Outcome, OutcomeHandled)
	}
	return reply
}

// TestEffortCommand_IsRegisteredForWebCliAndChannelSurfaces (spec §9.5: "the
// definition uses Surfaces: [Web, CLI, Channel]") — the command exists under
// the canonical name "effort", carries a handler, and is surfaced exactly
// where /model is (the D27 template).
func TestEffortCommand_IsRegisteredForWebCliAndChannelSurfaces(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	def, ok := reg.Lookup("effort")
	if !ok {
		t.Fatal(`Lookup("effort") not found — /effort must be a registered builtin command`)
	}
	if def.Name != "effort" {
		t.Fatalf(`Lookup("effort").Name=%q, want "effort"`, def.Name)
	}
	if def.Handler == nil {
		t.Fatal("/effort must carry a Handler (agent-delivery is the /remember shape, not this command's)")
	}
	want := []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel}
	if !reflect.DeepEqual(def.Surfaces, want) {
		t.Fatalf("Surfaces=%v, want %v (spec §9.5)", def.Surfaces, want)
	}
}

// TestEffortCommand_BareInvocationReportsCurrentOrDefault (spec §9.5: "/effort
// alone reads and reports the current value (or 'Default')") — with a stored
// value the reply carries it verbatim; with nothing stored it reports
// "Default"; neither touches the agent config.
func TestEffortCommand_BareInvocationReportsCurrentOrDefault(t *testing.T) {
	reg, rt, store, sid, spy := newEffortFixture(t)

	// Nothing stored yet → "Default".
	reply := runEffort(t, reg, rt, "/effort")
	if !strings.Contains(reply, "Default") {
		t.Fatalf("/effort with nothing stored: reply=%q, want it to report Default", reply)
	}

	// Store a value, ask again → the value comes back verbatim.
	effort := "high"
	if err := store.SetMeta(sid, session.MetaPatch{ReasoningEffort: &effort}); err != nil {
		t.Fatalf("SetMeta setup: %v", err)
	}
	reply = runEffort(t, reg, rt, "/effort")
	if !strings.Contains(reply, "high") {
		t.Fatalf("/effort with 'high' stored: reply=%q, want it to report the stored value", reply)
	}
	if *spy != 0 {
		t.Fatalf("SwitchModel spy fired %d times across read operations — /effort must never write agent config", *spy)
	}
}

// TestEffortCommand_LevelArgumentStoresPerSessionValue (spec §9.5: "/effort
// <level> sets it") — the stored value is the session's, visible through the
// same MetaPatch path a restart read would take.
func TestEffortCommand_LevelArgumentStoresPerSessionValue(t *testing.T) {
	reg, rt, store, sid, spy := newEffortFixture(t)

	reply := runEffort(t, reg, rt, "/effort medium")
	if !strings.Contains(reply, "medium") {
		t.Fatalf("/effort medium: reply=%q, want it to confirm the new level", reply)
	}
	got, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ReasoningEffort != "medium" {
		t.Fatalf("stored effort after '/effort medium' = %q, want %q", got.ReasoningEffort, "medium")
	}
	if *spy != 0 {
		t.Fatalf("SwitchModel spy fired %d times on set — /effort must never write agent config", *spy)
	}
}

// TestEffortCommand_DefaultArgumentClearsStoredValue (spec §9.5: "default
// clears it") — after a stored value, /effort default empties the session's
// field without touching the agent config.
func TestEffortCommand_DefaultArgumentClearsStoredValue(t *testing.T) {
	reg, rt, store, sid, spy := newEffortFixture(t)

	effort := "high"
	if err := store.SetMeta(sid, session.MetaPatch{ReasoningEffort: &effort}); err != nil {
		t.Fatalf("SetMeta setup: %v", err)
	}
	reply := runEffort(t, reg, rt, "/effort default")
	if !strings.Contains(strings.ToLower(reply), "default") {
		t.Fatalf("/effort default: reply=%q, want a default-clearing confirmation", reply)
	}
	got, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("stored effort after '/effort default' = %q, want empty (cleared)", got.ReasoningEffort)
	}
	if *spy != 0 {
		t.Fatalf("SwitchModel spy fired %d times on clear — /effort must never write agent config", *spy)
	}
}

// TestEffortCommand_NeverCallsAgentConfigWriter (spec §16 test 39: "spies
// prove zero agent-config writer/ApplyAgentModel calls (D30)") — the read,
// set and clear operations in sequence, with the SwitchModel spy asserted
// zero after EVERY operation and after all of them together.
func TestEffortCommand_NeverCallsAgentConfigWriter(t *testing.T) {
	reg, rt, store, sid, spy := newEffortFixture(t)

	if got := runEffort(t, reg, rt, "/effort"); !strings.Contains(got, "Default") {
		t.Fatalf("/effort read: reply=%q, want Default report", got)
	}
	if *spy != 0 {
		t.Fatalf("spy fired on read: %d calls", *spy)
	}

	if got := runEffort(t, reg, rt, "/effort high"); !strings.Contains(got, "high") {
		t.Fatalf("/effort high: reply=%q, want confirmation of high", got)
	}
	if *spy != 0 {
		t.Fatalf("spy fired on set: %d calls", *spy)
	}

	if got := runEffort(t, reg, rt, "/effort default"); got == "" {
		t.Fatal("/effort default must reply")
	}
	if *spy != 0 {
		t.Fatalf("spy fired on clear: %d calls", *spy)
	}

	// And the stored state really went through its lifecycle.
	got, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("stored effort after set+clear = %q, want empty", got.ReasoningEffort)
	}
	if *spy != 0 {
		t.Fatalf("SwitchModel spy fired %d times total — /effort must never write agent config (test 39)", *spy)
	}
}

// TestEffortCommand_WebchatRequestNeverWritesSessionField (spec §9.5/D23/D30:
// "On web, the slash palette reads or sets the current model picker's
// per-message value; no server session field is written") — even if a
// webchat-origin /effort reaches the server handler, the session field must
// stay untouched: web is picker-state, never stored conversation state.
func TestEffortCommand_WebchatRequestNeverWritesSessionField(t *testing.T) {
	reg, rt, store, sid, spy := newEffortFixture(t)

	ex := NewExecutor(reg, rt)
	// Reply: nil is deliberate — the executor substitutes a no-op sink for a
	// nil Reply (executor.go::executeDefinition nil-guard), and this test's
	// oracle is the untouched session field plus the zero spy count, not the
	// reply text (D23/D30 says nothing about what the server replies).
	res := ex.Execute(context.Background(), Request{
		Channel: "webchat",
		Text:    "/effort high",
		Reply:   nil,
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("webchat /effort high: outcome=%v, want %v", res.Outcome, OutcomeHandled)
	}
	if res.Err != nil {
		t.Fatalf("webchat /effort high: handler error %v — the webchat branch must reply and succeed", res.Err)
	}
	got, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("webchat /effort wrote session state: stored=%q, want empty — "+
			"web is per-message picker state (D23/D30), never a server session write", got.ReasoningEffort)
	}
	if *spy != 0 {
		t.Fatalf("SwitchModel spy fired %d times on webchat request", *spy)
	}
}

// TestEffortCommand_UnwiredRuntimeRepliesUnavailable (the /model D27
// template's nil-guard convention, pkg/commands/cmd_model.go::modelHandler):
// a Runtime without the conversation-effort callbacks must get the package's
// standard unavailable reply, never a nil-callback panic.
func TestEffortCommand_UnwiredRuntimeRepliesUnavailable(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	rt := &Runtime{SessionID: func() string { return "session-1" }}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/effort",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/effort on unwired runtime: outcome=%v, want %v", res.Outcome, OutcomeHandled)
	}
	if reply != unavailableMsg {
		t.Fatalf("/effort on unwired runtime: reply=%q, want %q (the /model nil-guard convention)", reply, unavailableMsg)
	}
}
