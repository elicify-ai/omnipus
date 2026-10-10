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
//   - X3 interim (PLAN §12/§15): the U10a cut removed /clear from the table
//     until U10b shipped the real /clear. U10b has now shipped it (FR-030),
//     so /clear is a canonical survivor again — it is NOT in the retired list.
//
// Every name below is a NEGATIVE case: the table must not offer it. The
// canonical survivors are asserted separately as the positive control (B).

// retiredCommandTableNames are the server-side command-table entries U10a cuts.
// The spec's capability row names them "Deleted everywhere, no alias"; the
// palette (SPA) and the typed/server dispatch must agree (FR-031), so the
// registry/executor must not resolve any of them.
//
// /clear is deliberately absent from this list: U10b restored it as a real
// canonical command (FR-030/031, founder ruling 2026-10-09). It is asserted as
// a canonical survivor below instead.
var retiredCommandTableNames = []string{
	"new",     // DEL-05 — the old /new action (U10b re-adds only /clear)
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
// proves the cut did not over-delete. /clear is included: U10b shipped it.
var canonicalCommandTableNames = []string{
	"help", "model", "clear", "skills", "channels", "status", "config", "tasks",
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

// TestSessionCoreU10a_NewRetiredFromServerCommandTable states the founder
// ruling of 2026-10-09 on its own: "/new is RETIRED — the server must not
// expose it as a web command; starting an extra chat is the local 'New chat'
// action." RED on the pre-cut code, where the table still defines /new (with a
// /clear alias). Since U10b, /clear is a canonical table entry in its own right
// (asserted by canonicalCommandTableNames); its semantics are additionally
// asserted at the mechanism level (pkg/memory clear-mechanism test).
func TestSessionCoreU10a_NewRetiredFromServerCommandTable(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())
	if def, found := reg.Lookup("new"); found {
		t.Errorf("/new must be retired from the server command table (founder 2026-10-09): still resolves to /%s", def.Name)
	}
	for _, def := range BuiltinDefinitions() {
		if def.Name == "new" {
			t.Errorf("BuiltinDefinitions still defines a server /new command (founder 2026-10-09: retired)")
		}
		for _, a := range def.Aliases {
			if a == "new" {
				t.Errorf("/new must not survive as an alias on /%s (retired, no hidden alias)", def.Name)
			}
		}
	}
}

// TestSessionCoreU10a_UnknownCommandPassthroughReportsParsedName restores the
// name-extraction assertion the U10a cut lost with the deleted
// show_list_handlers_test.go::TestShowListHandlers_ChannelPolicy.
//
// U10A-CHECK-ADJUDICATION.md ruled that test MIXED: its `/show channel` half was
// a justified retirement (of a removed command) but its `/foo` passthrough pair
// asserted LIVE behaviour — `Outcome == OutcomePassthrough` AND
// `Command == "foo"` (the command name extracted from unknown text). The
// outcome half is re-covered by builtin_test.go::TestBuiltinNoSkillOrUseCommand;
// the `Command` name-extraction half is NOT re-covered anywhere — the
// replacement deliberately leaves the Command field unasserted. This re-homes
// the lost oracle.
//
// Oracle (executor contract, not observed output):
//   - executor.go::Execute — a well-formed slash command that is not in the
//     table is passed through, and the returned ExecuteResult reports the
//     parsed command name in its Command field (the `!found` branch).
//   - request.go::parseCommandName — accepts "/name" and normalizes the name
//     to lowercase, so "/foo" parses to "foo".
//   - FR-031 — typed/server execution agrees with the table: an unknown name is
//     not handled by the command table, it passes through as ordinary text.
func TestSessionCoreU10a_UnknownCommandPassthroughReportsParsedName(t *testing.T) {
	const unknown = "foo"

	reg := NewRegistry(BuiltinDefinitions())
	if def, found := reg.Lookup(unknown); found {
		t.Fatalf("test precondition broken: %q must be unknown, but the table resolves it to /%s", unknown, def.Name)
	}

	ex := NewExecutor(reg, nil)
	res := ex.Execute(context.Background(), Request{Channel: "webchat", Text: "/" + unknown})

	if res.Outcome != OutcomePassthrough {
		t.Errorf("executor /%s outcome=%v, want %v (unknown command must pass through — FR-031)", unknown, res.Outcome, OutcomePassthrough)
	}
	if res.Command != unknown {
		t.Errorf("executor /%s Command=%q, want %q — the passthrough result must report the parsed command name (U10a restore; executor.go::Execute)", unknown, res.Command, unknown)
	}
}
