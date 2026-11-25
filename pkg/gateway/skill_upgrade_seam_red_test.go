// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Seam to the one-time skill-permissions upgrade (C-19), shared by the two
// gateway tests that must run the real upgrade: test 67's S-131 end-to-end
// subtest (permissions_upgrade_unfinished_red_test.go) and the C-19
// waiting-agent test (skill_upgrade_waiting_agent_red_test.go).
//
// Why a seam and not an import: the upgrade lives in pkg/skillperm, which does
// not exist at this base. A test file that imported it would stop the whole
// pkg/gateway test binary from compiling on any tree without the backend. The
// pack rule is that a missing named seam is a loud BLOCKED test, never an
// undefined-symbol compile (see l1_wu10_setup_offer_turn_test.go).
//
// What binds it: the backend's bindings file for this package
// (skills_tools_bindings_test.go) assigns the named production entry, one line:
//
//	skillUpgradeRunOf = skillperm.Run
//
// tests/helpers/nativebinding refuses anything that is not a direct, named
// function of pkg/skillperm (no closure, no test code), so the seam cannot be
// satisfied by a test-side stand-in that computes the upgrade's result.
//
// It is typed any because the entry's options type cannot be named here;
// runSkillUpgrade checks the shape it needs and fails BLOCKED when the bound
// function does not have it.

import (
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// skillUpgradeRunOf is nil until the backend binds pkg/skillperm.Run: every
// test that needs the real upgrade then fails BLOCKED, not with a panic.
var skillUpgradeRunOf any

// runSkillUpgrade runs the real upgrade once over home and returns its error.
// beforeAgentWrite, when not nil, is the upgrade's own write hook: an error it
// returns for an agent id is that agent's failed record write (the spec's
// example is a read-only file). It is a fault injection point, not a product
// action: it computes nothing and decides nothing.
func runSkillUpgrade(t *testing.T, home string, beforeAgentWrite func(agent string) error) error {
	t.Helper()
	nativebinding.Require(t, "pkg/skillperm", skillUpgradeRunOf,
		"C-19: the one-time skill-permissions upgrade entry (the backend binds skillUpgradeRunOf = skillperm.Run)")
	fn := reflect.ValueOf(skillUpgradeRunOf)
	ft := fn.Type()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if ft.NumIn() != 2 || ft.In(0).Kind() != reflect.String || ft.In(1).Kind() != reflect.Struct ||
		ft.NumOut() != 1 || ft.Out(0) != errorType {
		t.Fatalf("BLOCKED: the bound upgrade entry has the signature %s, want func(home string, options <struct>) error — required by C-19", ft)
	}
	options := reflect.New(ft.In(1)).Elem()
	if beforeAgentWrite != nil {
		hook := options.FieldByName("BeforeAgentWrite")
		if !hook.IsValid() || !reflect.TypeOf(beforeAgentWrite).AssignableTo(hook.Type()) {
			t.Fatalf("BLOCKED: the upgrade options have no BeforeAgentWrite func(agent string) error — required by C-19 (a failed agent write must be injectable at the upgrade's own write step)")
		}
		hook.Set(reflect.ValueOf(beforeAgentWrite))
	}
	out := fn.Call([]reflect.Value{reflect.ValueOf(home).Convert(ft.In(0)), options})
	if out[0].IsNil() {
		return nil
	}
	return out[0].Interface().(error)
}
