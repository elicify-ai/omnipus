//go:build goolm && stdjson

package agent

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), Part N.
// TestSkillPolicyResolution_DelegationAllowedInstructions is C-11 E5 (delegation's
// requested_skill) for the five Dataset D1 rows whose outcome is allow: D1#1, #7,
// #8, #11 and #12. Part L's E5 check (epCheckE5 in
// skill_entry_points_one_outcome_red_test.go) asserts for these rows only that the
// delegation launches; Part M (skill_delegation_ask_pending_red_test.go) takes the
// ask_pending rows. This file asks what an allowed requested_skill is for: the
// child gets the skill's instructions, with no help from its own model. It runs on
// Part L's world (newEPWorld), rows (epRows) and Part M's delegation world
// (newEPDelegation).
//
// Oracle: docs/internal/specs/skills-tools-redesign-spec.md, the delegate tool's own
// contract, and the order of team-lead of 2026-11-27 (Part N).
//
//	C-11 E5: "Delegation: `delegate` `requested_skill` →
//	  `pkg/agent/subturn_identity.go::resolveRequestedSkillForChild` | the
//	  instructions, in the child | decided for the **child** agent; ...".
//	C-11, its lead sentence: "Every entry point below consumes that outcome; none
//	  computes its own."
//	The delegate tool (pkg/tools/delegate.go, the requested_skill parameter): "the
//	  exact slug of a skill you want the delegation target to start its first turn
//	  with already loaded. This is a hard request, not a hint ... if the target is
//	  granted it, its first turn begins with the skill loaded and the result names
//	  it".
//	Order of 2026-11-27, the ruling on M-2 and M-3: "requested_skill is a load that
//	  the delegation does for the child, and that load consumes the one outcome";
//	  "for each allowed E5 row, the skill's instructions are in the child's first
//	  model request while the child's model never calls Skill".
//
// The world. The entry point is the production launcher the delegate tool calls
// (SteerLauncher.Launch with requested_skill, then Dispatch for the child's turn),
// on the real loop holding the row's stored state, on both chains Part M uses:
//
//	interactive  the steering session is a chat;
//	unattended   the steering session is a task root and the launch carries the
//	             posture a task turn's tool call carries (the pattern of
//	             steer_autodeny_inheritance_891_test.go). An allowed skill needs no
//	             person, so the unattended chain must deliver the instructions too.
//
// The child's scripted model never calls Skill: it answers "done" to its first
// request. Whatever puts the skill's instructions in front of the child therefore
// comes from the delegation itself. Observed: the text of every model request of
// the world. Nothing but the child's turn calls the model (the steering session is
// created and never run), so the first request is the child's; the test checks
// that it carries the task the delegation gave the child (epChildFirstRequest).
// The instructions are the sentinel line in the skill's body (epSentinel); the
// skill's name and description (its menu entry, E2) never contain it, so a menu
// listing cannot stand in for the instructions.
//
// Instruments, so that a pass or a failure here is about the delegation and not
// about the reader: the rows are the five allow rows; a child whose model calls
// Skill itself gets the instructions in its SECOND request and not in its first
// (E1's route, which is what an implementation that leaves the load to the child
// would give); a skill stored at the Full level puts its instructions in the child's
// first request (E8's route), so the reader can see instructions there.
//
// What is deliberately NOT asserted, because the spec does not fix it (the report to
// team-lead lists each as UNVERIFIED):
//   - where in the first request the instructions sit (the system prompt, a seeded
//     Skill result, a user message) and by what mechanism they get there: E5 says
//     "the instructions, in the child" and names none;
//   - whether they appear again in later requests, or how often;
//   - that the delegation's own result names the skill (the delegate tool's "and the
//     result names it"): that is the delegate tool's reply, not the launch;
//   - "decided for the child, not for the steering agent": the child here is the
//     steering agent itself (self-delegation, which the launcher allows), as in
//     Parts L and M, so a decision taken for the wrong agent cannot be told apart in
//     this world;
//   - that the audit log records the delegation's load.
//
// Compile note: like Parts L and M, this file names no symbol that exists only in a
// tree carrying the backend. On a tree whose config decoder does not take the stored
// skill keys (the landing base rejects the trust value "ask") every row ends with the
// world builder's BLOCKED message.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// epDelegatedTask is the task epDelegation.launch gives every child
// (skill_delegation_ask_pending_red_test.go): what makes a model request the child's.
const epDelegatedTask = "Use the skill."

// epAllowRows are the Dataset D1 rows whose outcome is allow, in table order.
func epAllowRows() []epRow {
	var rows []epRow
	for _, row := range epRows {
		if row.want == "allow" {
			rows = append(rows, row)
		}
	}
	return rows
}

// epChildFirstRequest returns the text of the first model request of a delegation
// world and the number of messages in it. In such a world nothing but the child's
// turn calls the model, so the first request is the child's; it is checked to carry
// the delegated task, so a request of some other session cannot pass for it. The
// text is not given to the assertion as a whole: a failing assertion would print it.
func epChildFirstRequest(t *testing.T, w *epWorld) (string, int) {
	t.Helper()
	requests := w.provider.AllRequests()
	require.NotEmpty(t, requests, "fixture: the child's turn sent no model request")
	first := w.requestText(0)
	require.True(t, strings.Contains(first, epDelegatedTask),
		"fixture: the first model request must be the child's, so it must carry the delegated task %q", epDelegatedTask)
	return first, len(requests[0])
}

// epChainName names the chain of a delegation world in messages.
func epChainName(unattended bool) string {
	if unattended {
		return "unattended"
	}
	return "interactive"
}

// epCheckE5AllowedInstructions: a chat or a task run delegates with requested_skill
// naming a skill whose outcome for the child is allow. The child's model only
// answers "done": whatever puts the instructions in its first request is the
// delegation.
func epCheckE5AllowedInstructions(unattended bool) func(*testing.T, epRow) {
	chain := epChainName(unattended)
	return func(t *testing.T, row epRow) {
		t.Helper()
		w := newEPWorld(t, row, epOptions{})
		d := newEPDelegation(t, w, unattended)

		res, err := d.launch(w.slug)
		require.NoError(t, err, "E5 allowed, %s chain (%s): an allowed, granted skill's delegation launches", chain, row.name)
		require.NoError(t, d.runChild(res), "E5 allowed, %s chain (%s): the launched child's turn runs", chain, row.name)

		require.Empty(t, w.skillCards(), "E5 allowed, %s chain (%s): an allowed skill needs no card (C-11 E1 \"allowed → loads\", C-12)", chain, row.name)
		first, messages := epChildFirstRequest(t, w)
		require.True(t, strings.Contains(first, epSentinel),
			"E5 allowed, %s chain (%s): C-11 E5 gives the child \"the instructions\" and the delegate tool says its first turn begins with the skill loaded; the child's model never called Skill, and the child's first model request (%d messages; it lists the skill: %t) holds no instructions of %q",
			chain, row.name, messages, strings.Contains(first, w.slug), w.slug)
	}
}

// epInstrumentAllowRows pins the table the rows run over: the five allow rows of
// Dataset D1, in table order, all granted.
func epInstrumentAllowRows(t *testing.T) {
	t.Helper()
	rows := epAllowRows()
	prefixes := []string{"D1#1 ", "D1#7 ", "D1#8 ", "D1#11 ", "D1#12 "}
	require.Len(t, rows, len(prefixes), "the allow rows of Dataset D1 are #1, #7, #8, #11 and #12")
	for i, row := range rows {
		require.True(t, strings.HasPrefix(row.name, prefixes[i]), "row %d is %s, got %q", i, strings.TrimSpace(prefixes[i]), row.name)
		require.True(t, row.granted, "%s: an allowed row is granted to the agent", row.name)
	}
}

// epInstrumentChildOwnLoad is the instrument of the first-request reading: a
// delegation that names no skill, to a child whose model calls Skill itself (an
// allowed skill). The child's first request is read as the child's and holds no
// instructions; they reach the child in its SECOND request, as the result of its own
// Skill call (E1's route). So a leaf that finds the instructions in the FIRST request
// of a child whose model calls nothing has found the delegation's load and not the
// child's own.
func epInstrumentChildOwnLoad(unattended bool) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		w := newEPWorld(t, epRows[0], epOptions{skillCall: true}) // D1#1: allow
		d := newEPDelegation(t, w, unattended)

		res, err := d.launch("")
		require.NoError(t, err, "instrument: a delegation that names no skill launches")
		rec, err := w.al.GetSessionLifecycleStore().Load(res.SessionID)
		require.NoError(t, err)
		require.Equal(t, unattended, rec.Unattended, "instrument: the child is recorded unattended exactly on the task chain")
		require.NoError(t, d.runChild(res), "instrument: the launched child's turn runs")

		first, _ := epChildFirstRequest(t, w)
		require.False(t, strings.Contains(first, epSentinel), "instrument: a delegation that names no skill gives the child no instructions in its first request")
		require.GreaterOrEqual(t, len(w.provider.AllRequests()), 2, "instrument: the child's own Skill call is answered by a second model request")
		require.True(t, strings.Contains(w.requestText(1), epSentinel),
			"instrument: the instructions reach a child that calls Skill itself in its second request (E1's route)")
		require.Empty(t, w.skillCards(), "instrument: an allowed skill's load raises no card")
	}
}

// epInstrumentFullLevel is the instrument of the reader: a skill stored at the Full
// level puts its instructions in the child's first request (C-11 E8, "the
// instructions, from the first turn"), with no requested_skill and no Skill call, so
// the first-request reading can see instructions where the delegation's load would
// put them.
func epInstrumentFullLevel(unattended bool) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		w := newEPWorld(t, epRows[0], epOptions{visibility: "full"}) // D1#1: allow, stored at the Full level
		d := newEPDelegation(t, w, unattended)

		res, err := d.launch("")
		require.NoError(t, err, "instrument: a delegation that names no skill launches")
		require.NoError(t, d.runChild(res), "instrument: the launched child's turn runs")

		first, _ := epChildFirstRequest(t, w)
		require.True(t, strings.Contains(first, epSentinel),
			"instrument: a skill stored at the Full level puts its instructions in the child's first model request (E8)")
	}
}

// TestSkillPolicyResolution_DelegationAllowedInstructions (pkg/agent: C-11 E5, the
// allow rows).
//
// Traces to: spec C-11 E5 and its lead sentence, C-11 E1 ("allowed → loads"), C-11
// E8 (the Full level, as the instrument), C-12, FR-066 (every entry point E1–E9
// uses the one outcome), the delegate tool's requested_skill contract; Dataset D1
// rows 1, 7, 8, 11 and 12. The spec has no BDD scenario of its own for a delegation
// whose requested_skill is allowed.
func TestSkillPolicyResolution_DelegationAllowedInstructions(t *testing.T) {
	t.Run("instrument: the rows are the five allow rows of Dataset D1", epInstrumentAllowRows)
	t.Run("instrument: interactive chain, a child that calls Skill itself gets the instructions in its second request, not its first", epInstrumentChildOwnLoad(false))
	t.Run("instrument: unattended chain, a child that calls Skill itself gets the instructions in its second request, not its first", epInstrumentChildOwnLoad(true))
	t.Run("instrument: interactive chain, a Full skill puts its instructions in the child's first request", epInstrumentFullLevel(false))
	t.Run("instrument: unattended chain, a Full skill puts its instructions in the child's first request", epInstrumentFullLevel(true))

	byChain := []struct {
		name  string
		check func(*testing.T, epRow)
	}{
		{"E5 allowed, interactive chain: the child's first model request holds the skill's instructions", epCheckE5AllowedInstructions(false)},
		{"E5 allowed, unattended chain: the child's first model request holds the skill's instructions", epCheckE5AllowedInstructions(true)},
	}
	for _, chain := range byChain {
		t.Run(chain.name, func(t *testing.T) {
			for _, row := range epAllowRows() {
				t.Run(row.name, func(t *testing.T) { chain.check(t, row) })
			}
		})
	}
}
