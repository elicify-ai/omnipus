package commands

import (
	"context"
	"testing"
)

// U10a — command-table cut (FR-031 part; BDD-09.3 part, BDD-12.1; T11 part).
//
// Spec sources (oracles; NOT observed from the implementation):
//   - session-core-spec.md §"Command capabilities" — the row
//     "new/agents/list/show/switch/check/channel/start/old resume | Deleted
//     everywhere, no alias" lists exactly the names this cut removes, and the
//     row above it keeps "help/status/stop/cancel/stop-redirect/sessions/
//     workspace/tasks/recall/navigation; existing read-only model/skill/channel
//     lists".
//   - FR-031: "Command table MUST agree at palette and typed/server execution,
//     including stale-menu direct calls. Delete retired aliases; read-only lists
//     cannot select model/mutate config."
//   - DEL-05 (clearCommand old /new action + hidden /clear alias; old /agents
//     selector/list action), DEL-06 (deprecated /start /show /list /switch
//     /check; /channel alias/support).
//   - X3 interim (PLAN §12/§15): "/clear is absent from the table until U10b
//     ships the real /clear. No interim hybrid /clear."
//
// Every name below is a NEGATIVE case: the table must not offer it. The
// canonical survivors are asserted separately as the positive control (B).

// retiredCommandTableNames are the server-side command-table entries U10a cuts.
// The spec's capability row names them "Deleted everywhere, no alias"; the
// palette (SPA) and the typed/server dispatch must agree (FR-031), so the
// registry/executor must not resolve any of them.
var retiredCommandTableNames = []string{
	"new",     // DEL-05 — the old /new action (U10b re-adds only /clear)
	"clear",   // DEL-05 + X3 — /clear removed until U10b; currently a /new alias
	"agents",  // DEL-05 — old selector/list action; replaced by /switch-agent
	"start",   // DEL-06
	"show",    // DEL-06
	"list",    // DEL-06
	"switch",  // DEL-06
	"check",   // DEL-06
	"channel", // DEL-06 — deprecated singular alias/support
	"resume",  // DEL-05 — old client name (SPA-side; asserted here for parity)
}

// canonicalCommandTableNames are the survivors the capability table keeps.
// They must remain resolvable — this is the canonical positive control (B) that
// proves the cut did not over-delete.
var canonicalCommandTableNames = []string{
	"help", "model", "skills", "channels", "status", "config", "tasks",
	"cancel", "stop", "stop-redirect", "remember", "recall", "retrospective",
	"goal", "loop",
}

// TestSessionCoreU10a_RetiredNamesAbsentFromTable asserts no retired name is
// offered as a command definition Name or Alias. RED on the pre-cut code: the
// table still defines /new (with a /clear alias), /agents, /start, /show,
// /list, /switch and /check.
func TestSessionCoreU10a_RetiredNamesAbsentFromTable(t *testing.T) {
	defs := BuiltinDefinitions()

	retired := make(map[string]bool, len(retiredCommandTableNames))
	for _, n := range retiredCommandTableNames {
		retired[n] = true
	}

	for _, def := range defs {
		if retired[def.Name] {
			t.Errorf("BuiltinDefinitions still offers retired command /%s (FR-031: deleted everywhere, no alias)", def.Name)
		}
		for _, alias := range def.Aliases {
			if retired[alias] {
				t.Errorf("BuiltinDefinitions still offers retired alias /%s on /%s (FR-031/DEL-05: no hidden alias)", alias, def.Name)
			}
		}
	}
}

// TestSessionCoreU10a_RetiredNamesAbsentFromRegistry asserts the registry index
// (what palette/server resolution consults) cannot resolve a retired name.
// RED on the pre-cut code for the same names as above.
func TestSessionCoreU10a_RetiredNamesAbsentFromRegistry(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	for _, name := range retiredCommandTableNames {
		if def, found := reg.Lookup(name); found {
			t.Errorf("registry still resolves retired name %q → /%s (FR-031: deleted everywhere)", name, def.Name)
		}
	}
}

// TestSessionCoreU10a_RetiredNamesPassthroughInExecutor asserts a typed direct
// call of a retired name is not handled by the command table — it passes through
// as ordinary text (BDD-09.3: "retired names absent/refused"; "stale-menu direct
// calls" cannot select model/mutate config). RED on the pre-cut code.
func TestSessionCoreU10a_RetiredNamesPassthroughInExecutor(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), nil)
	for _, name := range retiredCommandTableNames {
		res := ex.Execute(context.Background(), Request{
			Channel: "webchat",
			Text:    "/" + name,
		})
		if res.Outcome != OutcomePassthrough {
			t.Errorf("executor handled retired /%s (outcome=%v, command=%q); want Passthrough (FR-031)", name, res.Outcome, res.Command)
		}
	}
}

// TestSessionCoreU10a_CanonicalSurvivorsPresent is the canonical positive
// control (B): the commands the capability table keeps must still be offered and
// executable. GREEN on the pre-cut code by design — it proves the cut is a
// targeted removal, not a table wipe. It must stay green after G.
func TestSessionCoreU10a_CanonicalSurvivorsPresent(t *testing.T) {
	defs := BuiltinDefinitions()
	reg := NewRegistry(defs)

	for _, name := range canonicalCommandTableNames {
		def, found := reg.Lookup(name)
		if !found {
			t.Errorf("canonical command /%s missing from the table (FR-031 capability table keeps it)", name)
			continue
		}
		if def.Name != name {
			t.Errorf("lookup %q resolved to /%s, want /%s", name, def.Name, name)
		}
	}
}
