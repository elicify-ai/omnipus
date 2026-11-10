package commands

// effort_red_commands_test.go — RED tests for the messenger / web `/effort`
// command (train T7, Landing 1, test-order item 23 "T-M1 /effort handler").
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-36 (outline, reply table), S-37, S-38, S-39, S-40, S-41, S-42,
// S-70; FR-023 (registration: web delivery `client` + messenger surfaces only,
// exact matching, conversation-scoped on messengers, never SwitchModel or an
// agent writer, server web branch replies the chip hint and changes nothing).
//
// RED reason: /effort is not registered today. The spec's existing-seams table
// states it for the current tree: "pkg/commands/builtin.go::BuiltinDefinitions
// (27) … | Extended | No `/effort`" — Registry.Lookup("effort") fails and the
// Executor passes every /effort text through to the model (OutcomePassthrough).
// Every behavioral test below therefore fails first at an explicit lookup
// guard, so today's failure reason is honest (command not found), never a
// silently-passed execution.
//
// Round-4 binding: Runtime now exposes the required effort callbacks. The
// fixture supplies their external capability data and a REAL UnifiedStore,
// and binds each request to its conversation. It never supplies a command
// reply or reimplements validation. The original exact reply strings and
// SwitchModel zero-call assertions remain. Full loop/store restart coverage
// also lives in pkg/agent; this file separately reopens its real binding store.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// The spec's exact reply strings (effort-levels-spec-r10.md, S-36 examples
// table lines and S-70), with {model} = effortTestModel. Never edit these to
// match an implementation; they are the oracle.
const (
	effortTestModel = "test-model"

	// S-36 row 1 — bare /effort, model with levels none,low,medium,high,
	// current High.
	wantEffortStatusHigh = "Effort for test-model: High. Options: Provider default, Off, low, medium, high. /effort reset returns to the agent's setting."
	// S-36 row 2 — /effort max on a model whose levels are low,medium,high.
	wantEffortMaxUnoffered = "test-model doesn't offer max. Options: Provider default, low, medium, high. Nothing changed."
	// S-36 row 7 — /effort default sets Provider default.
	wantEffortDefaultSet = "Effort for test-model: Provider default (the provider decides)."
	// S-36 row 8 — /effort reset with the agent's setting Medium.
	wantEffortReset = "Effort for test-model: back to the agent's setting (Medium)."
	// S-36 row 9 — an input that is not a valid effort level (33 chars).
	wantEffortInvalid = "That isn't a valid effort level."
	// S-70 — the server's web branch reply (omnipus run arrives as webchat).
	wantEffortWebBranch = "Use the effort chip next to the model in the web chat. Effort isn't available in omnipus run yet."
)

// effortRedLookup guards every behavioral test: without the command in the
// registry the spec behaviors do not exist, and the failure must say so.
func effortRedLookup(t *testing.T, reg *Registry, scenario string) Definition {
	t.Helper()
	def, ok := reg.Lookup("effort")
	if !ok {
		t.Fatalf("RED (%s): Registry.Lookup(%q) not found — /effort is not registered in BuiltinDefinitions yet (spec seam table: \"No /effort\"); the scenario's behavior cannot exist", scenario, "effort")
	}
	return def
}

// Only the command runtime's external dependencies are supplied here: served
// capability data and real session-store reads/writes. The registry, parser,
// handler, value validation and exact reply generation remain production code.
type effortRedBinding struct {
	runtime       *Runtime
	store         *session.UnifiedStore
	conversations map[string]string
	levels        []string
	switchCalls   int
	closed        bool
}

func newEffortRedBinding(t *testing.T, store *session.UnifiedStore, conversations map[string]string) *effortRedBinding {
	t.Helper()
	if store == nil {
		var err error
		store, err = session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
		if err != nil {
			t.Fatalf("command fixture session store: %v", err)
		}
		conversations = make(map[string]string)
		for _, chatID := range []string{"conv-a", "conv-b"} {
			meta, createErr := store.NewSession(session.SessionTypeChat, "telegram", "mia")
			if createErr != nil {
				t.Fatalf("command fixture conversation %s: %v", chatID, createErr)
			}
			high := "high" // S-36 row 1's explicit initial conversation value.
			if patchErr := store.SetMeta(meta.ID, session.MetaPatch{ReasoningEffort: &high}); patchErr != nil {
				t.Fatalf("command fixture effort seed: %v", patchErr)
			}
			conversations[chatID] = meta.ID
		}
	}
	f := &effortRedBinding{store: store, conversations: conversations, levels: []string{"none", "low", "medium", "high"}}
	t.Cleanup(func() { f.close(t) })
	f.runtime = &Runtime{
		GetModelInfo: func() (string, string) { return effortTestModel, "openrouter" },
		SessionID:    func() string { return "conv-a" },
		SwitchModel: func(string) (string, error) {
			f.switchCalls++
			return "previous-model", nil
		},
	}
	f.runtime.GetEffortState = func() (EffortState, error) {
		id, found := f.conversations[f.runtime.SessionID()]
		if !found {
			return EffortState{}, fmt.Errorf("command fixture: undeclared conversation %q", f.runtime.SessionID())
		}
		meta, err := f.store.GetMeta(id)
		if err != nil {
			return EffortState{}, err
		}
		return EffortState{Model: effortTestModel, Known: true, Reasons: true, Levels: f.levels,
			Current: meta.ReasoningEffort, AgentSetting: "medium"}, nil
	}
	f.runtime.SetConversationEffort = func(value string) error {
		id, found := f.conversations[f.runtime.SessionID()]
		if !found {
			return fmt.Errorf("command fixture: undeclared conversation %q", f.runtime.SessionID())
		}
		return f.store.SetMeta(id, session.MetaPatch{ReasoningEffort: &value})
	}
	return f
}

func (f *effortRedBinding) close(t *testing.T) {
	t.Helper()
	if f.closed {
		return
	}
	if err := f.store.Close(); err != nil {
		t.Fatalf("command fixture close: %v", err)
	}
	f.closed = true
}

func newEffortRedRuntime(t *testing.T) (*Runtime, *int) {
	t.Helper()
	f := newEffortRedBinding(t, nil, nil)
	return f.runtime, &f.switchCalls
}

// runEffort executes one command text on one surface/conversation and returns
// the reply text and the executor outcome.
func runEffort(t *testing.T, ex *Executor, channel, chatID, text string) (string, Outcome) {
	t.Helper()
	// Runtime is per-request in the product. Bind this fixture's current
	// conversation before invoking the real executor; do not key all calls to A.
	ex.rt.SessionID = func() string { return chatID }
	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel:  channel,
		ChatID:   chatID,
		SenderID: "effort-red",
		Text:     text,
		Reply:    func(text string) error { reply = text; return nil },
	})
	if res.Err != nil {
		t.Fatalf("execute %q for %s: %v", text, chatID, res.Err)
	}
	return reply, res.Outcome
}

// TestT_CMD_Effort_S36_RegisteredWithSpecContract pins the registration
// contract: the command exists under the exact name, and per FR-023 it is
// registered for web (delivery client) and messenger surfaces ONLY — never a
// terminal/CLI registration (S-70's premise: omnipus run arrives as the web
// surface; "no terminal /effort registration", spec line 682).
// Traces to: effort-levels-spec-r10.md S-36, FR-023, S-41 (delivery client).
func TestT_CMD_Effort_S36_RegisteredWithSpecContract(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	def := effortRedLookup(t, reg, "S-36")

	if def.Name != "effort" {
		t.Fatalf("Lookup(\"effort\").Name=%q, want \"effort\"", def.Name)
	}
	if def.Description == "" {
		t.Fatal("Lookup(\"effort\").Description is empty — every builtin command documents itself (spec pins no exact description string; it must not be empty)")
	}
	if def.Hidden {
		t.Fatal("/effort must not be Hidden — it is a canonical, visible command (US-6)")
	}

	// FR-023: web (delivery client) and messenger surfaces only.
	if !def.AllowsSurface(SurfaceWeb) {
		t.Errorf("Surfaces=%v must allow SurfaceWeb — the server web branch answers omnipus run arrivals (S-70)", def.Surfaces)
	}
	if !def.AllowsSurface(SurfaceChannel) {
		t.Errorf("Surfaces=%v must allow SurfaceChannel — messenger conversations are the command's primary surface (S-36)", def.Surfaces)
	}
	if def.AllowsSurface(SurfaceCLI) {
		t.Errorf("Surfaces=%v must NOT allow SurfaceCLI — no terminal /effort registration (spec line 682, S-70)", def.Surfaces)
	}
	if got := def.EffectiveDelivery(); got != DeliveryClient {
		t.Errorf("EffectiveDelivery()=%q, want %q (FR-023: web delivery client, like /model)", got, DeliveryClient)
	}

	// Surface gating is enforced by the Executor, not just metadata: a CLI
	// arrival passes through to the model, a messenger arrival is handled.
	rt, spy := newEffortRedRuntime(t)
	ex := NewExecutor(reg, rt)

	if _, outcome := runEffort(t, ex, "cli", "term", "/effort high"); outcome != OutcomePassthrough {
		t.Errorf("/effort high on the cli surface: outcome=%v, want=%v (no terminal registration — the text must fall through to the agent)", outcome, OutcomePassthrough)
	}
	if _, outcome := runEffort(t, ex, "telegram", "conv-a", "/effort"); outcome != OutcomeHandled {
		t.Errorf("/effort on a messenger surface: outcome=%v, want=%v (registered for messenger surfaces)", outcome, OutcomeHandled)
	}
	if *spy != 0 {
		t.Errorf("SwitchModel spy called %d times during registration-contract checks, want 0", *spy)
	}

	// Differentiation control: /model is the precedent the spec cites
	// ("delivery `client`, like /model") — it must still resolve as such, so
	// the /effort assertions cannot be satisfied by breaking /model.
	modelDef, ok := reg.Lookup("model")
	if !ok {
		t.Fatal("Lookup(\"model\") not found — control premise broken")
	}
	if modelDef.EffectiveDelivery() != DeliveryClient {
		t.Errorf("/model EffectiveDelivery()=%q, want %q — the spec's /effort web delivery is defined as \"/model, like\"", modelDef.EffectiveDelivery(), DeliveryClient)
	}
}

// TestT_CMD_Effort_S37_BareEffortRepliesExactStatus pins the bare /effort
// status reply (S-36 row 1: model with levels none,low,medium,high, current
// High) and its "changes nothing" side.
// Traces to: effort-levels-spec-r10.md S-36 (row 1), US-6 AC1.
func TestT_CMD_Effort_S37_BareEffortRepliesExactStatus(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	effortRedLookup(t, reg, "S-37")

	rt, spy := newEffortRedRuntime(t)
	ex := NewExecutor(reg, rt)

	reply, outcome := runEffort(t, ex, "telegram", "conv-a", "/effort")
	if outcome != OutcomeHandled {
		t.Fatalf("bare /effort: outcome=%v, want=%v", outcome, OutcomeHandled)
	}
	if reply != wantEffortStatusHigh {
		t.Fatalf("bare /effort reply=%q, want exactly %q (S-36 row 1)", reply, wantEffortStatusHigh)
	}
	if *spy != 0 {
		t.Errorf("SwitchModel spy called %d times by bare /effort, want 0 — a status query changes nothing (US-6 AC1)", *spy)
	}

	// Differentiation: the reply must be the effort status, not a leaked
	// /model status — the two bare commands look similar on the wire.
	modelReply, _ := runEffort(t, ex, "telegram", "conv-a", "/model")
	if modelReply == reply {
		t.Fatalf("bare /effort and bare /model replied identically (%q) — the effort status must name effort, options and /effort reset", reply)
	}
}

// TestT_CMD_Effort_S38_SetIsConversationScoped pins the spec-pinned SET and
// CLEAR confirmations (S-36 rows 7 and 8) and the conversation scope: a set in
// conversation A leaves conversation B's status byte-identical (S-37's Then,
// "channels are communication, not steering"), and no agent-wide write
// happens (S-40's seam; FR-023 "never calling SwitchModel or an agent writer").
// Traces to: effort-levels-spec-r10.md S-38, S-37, S-36 (rows 7-8), FR-023.
func TestT_CMD_Effort_S38_SetIsConversationScoped(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	effortRedLookup(t, reg, "S-38")

	rt, spy := newEffortRedRuntime(t)
	ex := NewExecutor(reg, rt)

	// Snapshot conversation B before A changes anything.
	beforeB, outcome := runEffort(t, ex, "telegram", "conv-b", "/effort")
	if outcome != OutcomeHandled {
		t.Fatalf("bare /effort in conv-b: outcome=%v, want=%v", outcome, OutcomeHandled)
	}

	// /effort default in conversation A — the spec's exact SET confirmation.
	replyA, _ := runEffort(t, ex, "telegram", "conv-a", "/effort default")
	if replyA != wantEffortDefaultSet {
		t.Fatalf("/effort default reply=%q, want exactly %q (S-36 row 7)", replyA, wantEffortDefaultSet)
	}

	// /effort reset in conversation A — the spec's exact CLEAR confirmation;
	// the "(Medium)" comes from the agent's configured effort (harness note:
	// the GREEN capability seam must supply it, per S-36 row 8's premise).
	replyReset, _ := runEffort(t, ex, "telegram", "conv-a", "/effort reset")
	if replyReset != wantEffortReset {
		t.Fatalf("/effort reset reply=%q, want exactly %q (S-36 row 8)", replyReset, wantEffortReset)
	}

	// Conversation scope: B's status is byte-identical after A's set and clear.
	afterB, _ := runEffort(t, ex, "telegram", "conv-b", "/effort")
	if afterB != beforeB {
		t.Fatalf("conv-b status changed across conv-a's /effort set+clear: before=%q after=%q — a messenger /effort touches only its conversation (S-37)", beforeB, afterB)
	}

	// Conversation scope, other direction: A's status must differ from B's
	// while A holds the set value — A was reset above, so re-set in A only.
	_, _ = runEffort(t, ex, "telegram", "conv-a", "/effort default")
	statusA, _ := runEffort(t, ex, "telegram", "conv-a", "/effort")
	if statusA == afterB {
		t.Fatalf("conv-a status (%q) equals conv-b status after a set in A only — the set must be observable in A's own status", statusA)
	}

	if *spy != 0 {
		t.Errorf("SwitchModel spy called %d times by /effort set+reset forms, want 0 — never the agent-wide model switch (S-40, FR-023)", *spy)
	}
}

// TestT_CMD_Effort_S39_ConversationValueSurvivesRestart pins S-39: the
// conversation effort outlives a gateway restart. A restart rebuilds the
// command registry (the store is not part of it); whatever the set wrote must
// still be readable in conversation A afterwards.
// Traces to: effort-levels-spec-r10.md S-39 (persistence via SessionMeta
// "reasoning_effort" / MetaPatch — spec data table line "messenger
// conversation value").
func TestT_CMD_Effort_S39_ConversationValueSurvivesRestart(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	effortRedLookup(t, reg, "S-39")
	original := newEffortRedBinding(t, nil, nil)
	before := NewExecutor(reg, original.runtime)
	reply, outcome := runEffort(t, before, "telegram", "conv-a", "/effort default")
	if outcome != OutcomeHandled || reply != wantEffortDefaultSet {
		t.Fatalf("set before restart: outcome=%v reply=%q, want handled and %q", outcome, reply, wantEffortDefaultSet)
	}
	root := original.store.BaseDir()
	original.close(t)
	reopened, err := session.NewUnifiedStore(root)
	if err != nil {
		t.Fatalf("reopen command conversation store: %v", err)
	}
	rebuilt := newEffortRedBinding(t, reopened, original.conversations)
	if rebuilt.runtime == original.runtime || rebuilt.store == original.store {
		t.Fatal("restart must replace both the Runtime and the session-store instance")
	}
	after := NewExecutor(NewRegistry(BuiltinDefinitions()), rebuilt.runtime)
	status, outcome := runEffort(t, after, "telegram", "conv-a", "/effort")
	want := "Effort for test-model: Provider default. Options: Provider default, Off, low, medium, high. /effort reset returns to the agent's setting."
	if outcome != OutcomeHandled || status != want {
		t.Fatalf("status after restart: outcome=%v reply=%q, want handled and %q", outcome, status, want)
	}
	meta, err := reopened.GetMeta(rebuilt.conversations["conv-a"])
	if err != nil {
		t.Fatalf("read restored conversation: %v", err)
	}
	if meta.ReasoningEffort != "provider_default" {
		t.Errorf("restored effort=%q, want provider_default", meta.ReasoningEffort)
	}
	if original.switchCalls != 0 || rebuilt.switchCalls != 0 {
		t.Errorf("agent-wide SwitchModel calls before/after restart=%d/%d, want 0/0", original.switchCalls, rebuilt.switchCalls)
	}
	// The full agent-loop and store restart is additionally covered by
	// TestT_CMD_S39_RealLoopAndStoreRestartKeepsConversationEffort.
}

// TestT_CMD_Effort_S40_NeverCallsSwitchModel pins S-40 exactly: with a spy on
// Runtime.SwitchModel (the agent-wide writer seam reachable from the command
// runtime — the loop's SwitchModel closure writes the agent's model config),
// NO /effort form ever calls it. Every accepted, refused and invalid form is
// sent, including the web-surface arrival.
// Traces to: effort-levels-spec-r10.md S-40, FR-023, S-37 (config
// byte-identical at this seam's altitude).
func TestT_CMD_Effort_S40_NeverCallsSwitchModel(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	effortRedLookup(t, reg, "S-40")

	rt, spy := newEffortRedRuntime(t)
	ex := NewExecutor(reg, rt)

	forms := []struct {
		channel string
		chatID  string
		text    string
	}{
		{"telegram", "conv-a", "/effort"},
		{"telegram", "conv-a", "/effort high"},
		{"telegram", "conv-a", "/effort max"},
		{"telegram", "conv-a", "/effort default"},
		{"telegram", "conv-a", "/effort DEFAULT"},
		{"telegram", "conv-a", "/effort reset"},
		{"telegram", "conv-a", "/effort off"},
		{"telegram", "conv-a", "/effort on"},
		{"telegram", "conv-a", "/effort bogus"},
		{"telegram", "conv-a", "/effort "},
		{"telegram", "conv-a", "/effort  high"},
		{"telegram", "conv-a", "/effort " + strings.Repeat("x", 33)},
		{"webchat", "web-1", "/effort high"},
	}

	for _, f := range forms {
		if _, outcome := runEffort(t, ex, f.channel, f.chatID, f.text); outcome != OutcomeHandled {
			t.Errorf("%q on %s: outcome=%v, want=%v (the command must handle the text, not pass it to the model)", f.text, f.channel, outcome, OutcomeHandled)
		}
		if *spy != 0 {
			t.Fatalf("SwitchModel spy called %d times after %q — /effort never calls the agent-wide model switch (S-40)", *spy, f.text)
		}
	}
}

// TestT_CMD_Effort_S41_WebDeliveryClientNoServerRecord pins S-41's two
// observables: (a) the composer-side mechanism — web delivery `client`, like
// /model, so the SPA handles /effort locally and never ships it as a message;
// (b) the server-side observable when the text DOES arrive as a web message
// (omnipus run): the server web branch answers and writes no record —
// observable here as the chip-hint reply with no SwitchModel call.
// Traces to: effort-levels-spec-r10.md S-41, FR-023, S-70 (branch reply).
func TestT_CMD_Effort_S41_WebDeliveryClientNoServerRecord(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	def := effortRedLookup(t, reg, "S-41")

	if got := def.EffectiveDelivery(); got != DeliveryClient {
		t.Errorf("EffectiveDelivery()=%q, want %q — the web composer handles /effort locally (S-41 \"delivery `client`, like /model\")", got, DeliveryClient)
	}
	if !def.AllowsSurface(SurfaceWeb) || !def.AllowsSurface(SurfaceChannel) {
		t.Errorf("Surfaces=%v must allow web and messenger (FR-023)", def.Surfaces)
	}
	if def.AllowsSurface(SurfaceCLI) {
		t.Errorf("Surfaces=%v must not allow the CLI surface (no terminal registration)", def.Surfaces)
	}

	rt, spy := newEffortRedRuntime(t)
	ex := NewExecutor(reg, rt)

	// A model offering max; the text arrives as a webchat message anyway
	// (omnipus run path): the web branch answers, nothing is written.
	reply, _ := runEffort(t, ex, "webchat", "web-1", "/effort max")
	if reply != wantEffortWebBranch {
		t.Fatalf("webchat /effort max reply=%q, want exactly %q (S-70 web branch; S-41 \"no server record is written\")", reply, wantEffortWebBranch)
	}
	if *spy != 0 {
		t.Errorf("SwitchModel spy called %d times by the web branch, want 0 — the web branch changes nothing", *spy)
	}
}

// TestT_CMD_Effort_S42_UnknownUnofferedAndInputForms pins S-42's input forms
// and the invalid-value rejection, with the differentiation pair the spec
// builds: an UNKNOWN level name and a KNOWN-but-unoffered level get different
// exact texts (S-36 row 9 vs row 2).
// Traces to: effort-levels-spec-r10.md S-42, S-36 (rows 1, 2, 7, 9), FR-048
// (/effort checks the value against the catalog).
func TestT_CMD_Effort_S42_UnknownUnofferedAndInputForms(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	effortRedLookup(t, reg, "S-42")

	fixture := newEffortRedBinding(t, nil, nil)
	rt, spy := fixture.runtime, &fixture.switchCalls
	ex := NewExecutor(reg, rt)

	// Unknown level (the task's /effort bogus; S-36 row 9's invalid class —
	// the row's literal example is the 33-character input).
	replyBogus, _ := runEffort(t, ex, "telegram", "conv-a", "/effort bogus")
	if replyBogus != wantEffortInvalid {
		t.Fatalf("/effort bogus reply=%q, want exactly %q (S-36 row 9)", replyBogus, wantEffortInvalid)
	}
	reply33, _ := runEffort(t, ex, "telegram", "conv-a", "/effort "+strings.Repeat("x", 33))
	if reply33 != wantEffortInvalid {
		t.Fatalf("/effort <33 chars> reply=%q, want exactly %q (S-36 row 9)", reply33, wantEffortInvalid)
	}

	// Differentiation: a KNOWN level the model doesn't offer is refused with
	// the options list, NOT the invalid-level text (S-36 row 2).
	// S-36 row 2's premise differs from row 1: this capability has no Off.
	fixture.levels = []string{"low", "medium", "high"}
	replyMax, _ := runEffort(t, ex, "telegram", "conv-a", "/effort max")
	if replyMax != wantEffortMaxUnoffered {
		t.Fatalf("/effort max reply=%q, want exactly %q (S-36 row 2 — known-but-unoffered differs from unknown)", replyMax, wantEffortMaxUnoffered)
	}
	if replyMax == wantEffortInvalid {
		t.Fatal("/effort max was answered with the invalid-level text — unoffered and unknown must stay distinct refusals")
	}

	// Return to row 1's advertised options for the following High status.
	fixture.levels = []string{"none", "low", "medium", "high"}

	// S-42 form 1: "/effort DEFAULT" — /effort lower-cases the input first
	// (boundary table row 10), so the reserved catalog name sets Provider
	// default with row 7's exact confirmation.
	replyDEFAULT, _ := runEffort(t, ex, "telegram", "conv-a", "/effort DEFAULT")
	if replyDEFAULT != wantEffortDefaultSet {
		t.Fatalf("/effort DEFAULT reply=%q, want exactly %q (S-42: lower-cased first, sets Provider default)", replyDEFAULT, wantEffortDefaultSet)
	}

	// S-42 form 2: "/effort  high" (extra space) is treated as "high" — the
	// same conversation's status afterwards is row 1's pinned current-High
	// text (the set made High current).
	_, _ = runEffort(t, ex, "telegram", "conv-a", "/effort  high")
	statusHigh, _ := runEffort(t, ex, "telegram", "conv-a", "/effort")
	if statusHigh != wantEffortStatusHigh {
		t.Fatalf("conv-a status after \"/effort  high\"=%q, want exactly %q (S-42: extra space treated as high, so current is High)", statusHigh, wantEffortStatusHigh)
	}

	// S-42 form 3: "/effort <empty after trim>" behaves like bare /effort —
	// literal differential equality in the same runtime.
	bare, _ := runEffort(t, ex, "telegram", "conv-a", "/effort")
	emptyArg, _ := runEffort(t, ex, "telegram", "conv-a", "/effort ")
	if emptyArg != bare {
		t.Fatalf("/effort <empty after trim> reply=%q, bare /effort reply=%q — they must be identical (S-42 form 3)", emptyArg, bare)
	}

	if *spy != 0 {
		t.Errorf("SwitchModel spy called %d times across S-42 forms, want 0", *spy)
	}
}

// TestT_CMD_Effort_S70_WebBranchRepliesChipHint pins S-70 exactly: the text
// "/effort high" sent from the terminal client reaches the server's WEB branch
// (omnipus run arrives as webchat — spec seam table), which replies the exact
// chip-hint sentence and changes nothing. The messenger surface must NOT
// answer with the web-branch text.
// Traces to: effort-levels-spec-r10.md S-70, FR-023 (server web branch),
// ambiguity 6.
func TestT_CMD_Effort_S70_WebBranchRepliesChipHint(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	effortRedLookup(t, reg, "S-70")

	rt, spy := newEffortRedRuntime(t)
	ex := NewExecutor(reg, rt)

	reply, outcome := runEffort(t, ex, "webchat", "run-task", "/effort high")
	if outcome != OutcomeHandled {
		t.Fatalf("webchat /effort high: outcome=%v, want=%v (the web branch answers, it does not pass the text to the model)", outcome, OutcomeHandled)
	}
	if reply != wantEffortWebBranch {
		t.Fatalf("webchat /effort high reply=%q, want exactly %q (S-70)", reply, wantEffortWebBranch)
	}
	if *spy != 0 {
		t.Errorf("SwitchModel spy called %d times by the web branch, want 0 — S-70: \"changes nothing\"", *spy)
	}

	// Differentiation: the same text on a messenger surface must not produce
	// the web-branch reply — messengers get the S-36 rows (pinned in the
	// sibling tests), the chip hint is web-only.
	channelReply, _ := runEffort(t, ex, "telegram", "conv-a", "/effort high")
	if channelReply == wantEffortWebBranch {
		t.Fatalf("messenger /effort high replied with the web-branch chip hint (%q) — the web branch is web-only (S-70 vs S-36)", channelReply)
	}
}
