//go:build goolm && stdjson

package agent

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), Part M.
// TestSkillPolicyResolution_DelegationAskPending is C-11 E5 (delegation's
// requested_skill) for the four Dataset D1 rows whose outcome is ask_pending:
// D1#3, D1#6, D1#9 and D1#10. Part L's E5 check (epCheckE5 in
// skill_entry_points_one_outcome_red_test.go) takes the allow, deny and
// not-granted rows; this file takes the rest of the table, on that file's world
// (newEPWorld), rows (epRows) and card reader (epOpenChecks).
//
// Oracle: docs/internal/specs/skills-tools-redesign-spec.md, and the order of
// team-lead of 2026-11-27 (Part M).
//
//	C-11 E5: "Delegation: `delegate` `requested_skill` →
//	  `pkg/agent/subturn_identity.go::resolveRequestedSkillForChild` | the
//	  instructions, in the child | decided for the **child** agent;
//	  `ask_pending` raises the card in the root chat (interactive) or refuses
//	  (unattended)".
//	Edge Cases: "`delegate` with `requested_skill` naming an Ask skill: the
//	  child's load raises the card in the root chat (#1263 routing); in an
//	  unattended chain it is refused."
//	C-12, decision table rows 2-4: one permission card, one trust card or one
//	  combined card; the header "Interactive (chat; `/name`; delegation, card in
//	  the root chat)".
//	FR-066: "a skill whose outcome is `deny` or `ask_pending` MUST NOT be loaded,
//	  injected, ... for that agent".
//	Dataset D1 rows 3, 6, 9 and 10 (BDD S-19, S-30, S-31): the outcome is
//	  ask_pending; the open check is the permission check for #3 and #10 and the
//	  trust check for #6 and #9 (God Mode floors the global value, never trust).
//	Order of 2026-11-27: "Interactive: the card is raised in the ROOT chat and
//	  the child gets no instructions before approval. Unattended chain: the
//	  delegation is refused."
//
// The two worlds. The entry point is the production launcher the delegate tool
// calls (SteerLauncher.Launch with requested_skill, then Dispatch for the child's
// turn), on the real loop holding the row's stored state.
//
//	interactive  the steering session is a chat. The child's scripted model never
//	             calls Skill: a card raised because the child's OWN model chose to
//	             call Skill is E1's rule (Part L), not E5's; E5 is the delegation
//	             consuming the outcome itself ("ask_pending raises the card"), so
//	             the card must come with no help from the child's model. (At this
//	             tree requested_skill gates only the grant: SteerLauncher.Launch;
//	             ForcedSkills is no longer written by delegate,
//	             loop_slash.go::activeSkillNames. The delegate tool's own
//	             description still promises that the child's first turn begins
//	             with the skill loaded; the code does not do that here.) The
//	             approver denies every card: the person never approves. Observed:
//	             the Skill cards, the session each was raised for, and every
//	             message any model got. An instrument shows what the same world
//	             records when the child's own model does call Skill (a card, found
//	             in the root chat by the same walk), so the walk is shown to read a
//	             real card of a real delegated child.
//	unattended   the steering session is a task root and the launch carries the
//	             posture a task turn's tool call carries (the pattern of
//	             steer_autodeny_inheritance_891_test.go). Observed: the launch's
//	             result and error, the children recorded and the cards raised.
//
// "In the root chat" is observed the way the gateway derives the approval
// frame's root_session_id (pkg/gateway/ws_tool_approval.go::approvalSessionContext):
// the card's acting session, followed up its persisted ParentSessionID chain,
// ends at the chat that delegated (epRootOf).
//
// What is deliberately NOT asserted, because the spec does not fix it (the
// report to team-lead lists each as UNVERIFIED):
//   - when the interactive card appears (at the launch gate, or when the child's
//     first turn starts and the delegation loads the requested skill for it; the
//     Edge Cases entry calls it "the child's load"), and how many cards a
//     delegation raises: all are "the card for the delegation". The cards are
//     read after the launch and the child's whole turn; at least one is
//     required, and every one raised must name exactly the open check and be
//     raised in the root chat. The only route NOT accepted is the one that needs
//     the child's own model to call Skill (above);
//   - what the delegation does once the person denies the card, including whether
//     the child's dispatch then succeeds (the interactive rows read the cards after
//     the dispatch, whatever it returned);
//   - how the instructions reach the child after an approval, and whether they do
//     (E5 says "the instructions, in the child" and names no mechanism); only
//     their absence in a world where nobody approves is asserted (FR-066);
//   - the words of the unattended refusal beyond naming the skill or saying that
//     it needs approval (C-12 words the skipped LOAD of an unattended run; E5's
//     own sentence is not fixed);
//   - "decided for the child, not for the steering agent": the child here is the
//     steering agent itself (self-delegation, which the launcher allows), as in
//     Part L's E5 check, so a decision taken for the wrong agent cannot be told
//     apart in this world.
//
// Compile note: like Part L's file, this one names no symbol that exists only in
// a tree carrying the backend. The approval card's open checks are read by
// reflection (epOpenChecks), and the stored skill keys go through the real
// config decoder; on a tree whose decoder does not take them (the landing base
// rejects the trust value "ask") every row ends with the world builder's BLOCKED
// message.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// epChainMaxHops is the hop bound of the gateway's walk up a ParentSessionID
// chain (pkg/gateway/ws_tool_approval.go, approvalSessionContext).
const epChainMaxHops = 16

// epAskRows are the Dataset D1 rows whose outcome is ask_pending, in table order.
func epAskRows() []epRow {
	var rows []epRow
	for _, row := range epRows {
		if row.want == "ask_pending" {
			rows = append(rows, row)
		}
	}
	return rows
}

// epDelegation is one delegation world: the loop of an epWorld wired for
// steering, the session that steers (the root) and the context a launch runs under.
type epDelegation struct {
	w        *epWorld
	launcher *SteerLauncher
	root     string          // the steering session
	ctx      context.Context // the launch context: plain, or the posture of an unattended chain
}

// newEPDelegation wires the world's loop for delegation the way the gateway does
// (the lifecycle and inbox stores, one genuine boot epoch) and creates the
// steering session: a chat (unattended false) or a task root (unattended true).
func newEPDelegation(t *testing.T, w *epWorld, unattended bool) *epDelegation {
	t.Helper()
	lifecycle := session.NewLifecycleStore(filepath.Join(w.home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(w.home, "session_messages"))
	w.al.SetSessionMessagingStores(inbox, lifecycle)
	mintGenuineBootEpochForLoop(t, w.al)
	d := &epDelegation{w: w, launcher: NewSteerLauncher(w.al), ctx: context.Background()}
	if unattended {
		d.root = taskRootSteeringSession(t, w.al, epAgent, "ep-e5-task")
		d.ctx = tools.WithAutoDenyAsk(d.ctx, true)
	} else {
		d.root = newTestSteeringSessionOwnedBy(t, w.al, "", epAgent)
	}
	require.Zero(t, epSteeredRecords(t, w.al), "fixture: no child exists before the launch")
	return d
}

// launch is the delegation: the same agent as the child, the one task, the skill
// the delegate tool's requested_skill names ("" for a delegation without one).
func (d *epDelegation) launch(requestedSkill string) (steer.LaunchResult, error) {
	return d.launcher.Launch(d.ctx, steer.LaunchRequest{
		SteeringSessionID: d.root,
		TargetAgentID:     epAgent,
		Task:              "Use the skill.",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "ep-e5-ask"},
		RequestedSkill:    requestedSkill,
	})
}

// runChild dispatches the launched child and, when the dispatch succeeded, waits
// for its turn to end, so everything the child's turn does (a card, a model
// request) has happened. It returns the dispatch's error: a world where the
// dispatch is refused has no turn to wait for.
func (d *epDelegation) runChild(res steer.LaunchResult) error {
	t := d.w.t
	t.Helper()
	if _, err := d.launcher.Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		return err
	}
	lifecycle := d.w.al.GetSessionLifecycleStore()
	waitFor(t, 15*time.Second, func() bool {
		rec, loadErr := lifecycle.Load(res.SessionID)
		return loadErr == nil && session.IsTerminalLifecycleState(rec.State)
	})
	return nil
}

// epRootOf walks the persisted ParentSessionID chain from sessionID and returns
// the session at its top: the root chat of a card raised for sessionID. It is the
// derivation the gateway applies to an approval frame's root_session_id
// (approvalSessionContext: a bounded, cycle-guarded walk; the session with no
// parent is the root). A session whose metadata cannot be read fails the test:
// the gateway would fall back to the acting session, which is not a pass for a
// helper's card.
func epRootOf(t *testing.T, al *AgentLoop, sessionID string) string {
	t.Helper()
	seen := map[string]bool{}
	sid := sessionID
	for hop := 0; hop < epChainMaxHops; hop++ {
		require.False(t, seen[sid], "the parent chain of session %q has a cycle at %q", sessionID, sid)
		seen[sid] = true
		meta, err := al.GetSessionStore().GetMeta(sid)
		require.NoError(t, err, "session %q on the chain of session %q has readable metadata", sid, sessionID)
		if meta.ParentSessionID == "" {
			return sid
		}
		sid = meta.ParentSessionID
	}
	t.Fatalf("the parent chain of session %q is longer than %d hops", sessionID, epChainMaxHops)
	return ""
}

// epCheckE5AskInteractive: a chat delegates with requested_skill naming a skill
// whose outcome for the child is ask_pending, and the person never approves. The
// child's model only answers "done": whatever raises the card is the delegation.
func epCheckE5AskInteractive(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{})
	d := newEPDelegation(t, w, false)

	res, err := d.launch(w.slug)
	var dispatchErr error
	if err == nil {
		dispatchErr = d.runChild(res)
	}

	cards := w.skillCards()
	require.NotEmpty(t, cards, "E5 ask_pending, interactive (%s): C-11 E5 and C-12 raise the %s card in the root chat for the delegation; the launch's error was %v, its child session %q, the dispatch's error was %v, and no Skill card was raised",
		row.name, row.openChecks()[0], err, res.SessionID, dispatchErr)
	for i, card := range cards {
		require.Equal(t, row.openChecks(), epOpenChecks(t, card), "E5: card %d names exactly the open check (C-12)", i)
		require.Equal(t, d.root, epRootOf(t, w.al, card.SessionID),
			"E5: card %d is raised in the root chat (#1263 routing): its session %q must lead up to the chat that delegated", i, card.SessionID)
	}
	require.NotContains(t, w.allRequestText(), epSentinel, "E5 ask_pending (FR-066): the child gets no instructions before the person approves")
}

// epCheckE5AskUnattended: a task run delegates with requested_skill naming a skill
// whose outcome for the child is ask_pending. A chain with no person never asks,
// so the delegation is refused (order of 2026-11-27).
func epCheckE5AskUnattended(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{})
	d := newEPDelegation(t, w, true)

	res, err := d.launch(w.slug)

	require.Error(t, err, "E5 ask_pending, unattended (%s): C-11 E5 refuses the delegation and an unattended chain never asks; the launch started child session %q instead", row.name, res.SessionID)
	require.Equal(t, steer.LaunchResult{}, res, "E5 ask_pending, unattended: a refused delegation starts no child")
	require.Zero(t, epSteeredRecords(t, w.al), "E5 ask_pending, unattended: a refused delegation records no child")
	require.Empty(t, w.approver.requests(), "E5 ask_pending, unattended: a chain with no person never asks, so no card (C-12)")
	// The words are not fixed for E5 (C-12 words the skipped load of an unattended
	// run). What ties the refusal to the skill's outcome, and not to some other
	// launch failure, is either the form of today's refusal of a requested_skill
	// (it names the skill: order of 2026-11-27, item b) or C-12's reason.
	msg := err.Error()
	require.True(t, strings.Contains(msg, w.slug) || strings.Contains(msg, "needs approval"),
		"E5 ask_pending, unattended: the refusal must name the skill %q or say it needs approval (C-12); got: %v", w.slug, err)
}

// epInstrumentAttendedChain is the instrument of the interactive rows: the chat
// chain launches a child that is NOT recorded unattended (a chain read as
// unattended would refuse the child's load without a card, a fixture defect
// that would look like the product's); the child's own load of an allowed skill
// puts the instructions in the child's model requests and raises no card (so
// "no instructions in any request" can tell a gate that works from a harness
// that never captures the child's requests); and the walk epRootOf finds the
// chat from the child, and from the chat itself.
func epInstrumentAttendedChain(t *testing.T) {
	t.Helper()
	w := newEPWorld(t, epRows[0], epOptions{skillCall: true}) // D1#1: allow
	d := newEPDelegation(t, w, false)

	res, err := d.launch(w.slug)
	require.NoError(t, err, "instrument: an allowed skill's delegation launches from the chat")
	rec, err := w.al.GetSessionLifecycleStore().Load(res.SessionID)
	require.NoError(t, err)
	require.False(t, rec.Unattended, "instrument: the child of a chat is not recorded unattended")
	require.NoError(t, d.runChild(res), "instrument: the launched child dispatches")

	require.Empty(t, w.approver.requests(), "instrument: an allowed skill raises no card")
	require.Contains(t, w.allRequestText(), epSentinel, "instrument: the child's own load of an allowed skill puts its instructions in the child's model requests")
	require.Equal(t, d.root, epRootOf(t, w.al, res.SessionID), "instrument: the walk finds the chat from the child")
	require.Equal(t, d.root, epRootOf(t, w.al, d.root), "instrument: the walk stops at the chat itself")
}

// epInstrumentChildLoadRoute is the instrument of the root-chat assertion: the
// card of a real delegated child is found in the root chat by the same walk, and
// carries the open check the reflection reads. In this world the child's OWN
// model calls Skill (E1's route, not E5's): it is what makes the interactive
// rows' "no card" a finding about the delegation and not about a walk or a card
// reader that could never have seen a card. Nobody approves, so no instructions
// reach the child.
func epInstrumentChildLoadRoute(t *testing.T) {
	t.Helper()
	row := epAskRows()[0] // D1#3: the permission check is open
	w := newEPWorld(t, row, epOptions{skillCall: true})
	d := newEPDelegation(t, w, false)

	res, err := d.launch("")
	require.NoError(t, err, "instrument: a delegation without requested_skill launches from the chat")
	require.NoError(t, d.runChild(res), "instrument: the launched child dispatches")

	cards := w.skillCards()
	require.Len(t, cards, 1, "instrument: the child's own load of an ask_pending skill raises one card")
	require.Equal(t, row.openChecks(), epOpenChecks(t, cards[0]), "instrument: the card names the open check")
	require.Equal(t, res.SessionID, cards[0].SessionID, "instrument: the child raises the card as itself")
	require.Equal(t, d.root, epRootOf(t, w.al, cards[0].SessionID), "instrument: the walk finds the root chat from a real card of a real child")
	require.NotContains(t, w.allRequestText(), epSentinel, "instrument: nobody approved, so no instructions reached the child")
}

// epInstrumentUnattendedChain is the instrument of the unattended rows: the same
// task-root chain launches an allowed skill (so the refusal of an ask_pending
// skill is the outcome's doing, not the chain's), and the child it launches is
// recorded unattended (so the chain is the unattended one the rows mean).
func epInstrumentUnattendedChain(t *testing.T) {
	t.Helper()
	w := newEPWorld(t, epRows[0], epOptions{}) // D1#1: allow
	d := newEPDelegation(t, w, true)

	res, err := d.launch(w.slug)
	require.NoError(t, err, "instrument: an allowed skill's delegation launches on the unattended chain")
	require.NotEmpty(t, res.SessionID, "instrument: a child session was started")
	require.Equal(t, 1, epSteeredRecords(t, w.al), "instrument: the child's steering edge is recorded")
	rec, err := w.al.GetSessionLifecycleStore().Load(res.SessionID)
	require.NoError(t, err)
	require.True(t, rec.Unattended, "instrument: the child of the task root is recorded unattended")
}

// epInstrumentRows pins the table the rows run over: the four rows the order
// names, in table order, two with the permission check open and two with the
// trust check open.
func epInstrumentRows(t *testing.T) {
	t.Helper()
	rows := epAskRows()
	prefixes := []string{"D1#3 ", "D1#6 ", "D1#9 ", "D1#10 "}
	require.Len(t, rows, len(prefixes), "the ask_pending rows of Dataset D1 are #3, #6, #9 and #10")
	open := map[string]int{}
	for i, row := range rows {
		require.True(t, strings.HasPrefix(row.name, prefixes[i]), "row %d is %s, got %q", i, strings.TrimSpace(prefixes[i]), row.name)
		open[row.openChecks()[0]]++
	}
	require.Equal(t, map[string]int{"permission": 2, "trust": 2}, open, "two permission rows (#3, #10) and two trust rows (#6, #9)")
}

// TestSkillPolicyResolution_DelegationAskPending (pkg/agent: C-11 E5, the
// ask_pending rows).
//
// Traces to: BDD S-19, S-30, S-31 (Dataset D1 rows 3, 6, 9, 10); spec C-11 E5,
// C-12 (decision table rows 2-4), FR-066, the Edge Cases entry for `delegate`
// with `requested_skill` naming an Ask skill.
func TestSkillPolicyResolution_DelegationAskPending(t *testing.T) {
	t.Run("instrument: the rows are the four ask_pending rows of Dataset D1", epInstrumentRows)
	t.Run("instrument: the chat chain's child is attended, its load shows the instructions, the walk finds the chat", epInstrumentAttendedChain)
	t.Run("instrument: the card of a child that calls Skill itself is found in the root chat", epInstrumentChildLoadRoute)
	t.Run("instrument: the task chain launches an allowed skill and records its child unattended", epInstrumentUnattendedChain)

	byMode := []struct {
		name  string
		check func(*testing.T, epRow)
	}{
		{"E5 interactive chain: ask_pending raises the card in the root chat and the child gets no instructions before approval", epCheckE5AskInteractive},
		{"E5 unattended chain: ask_pending refuses the delegation: no child, no card", epCheckE5AskUnattended},
	}
	for _, mode := range byMode {
		t.Run(mode.name, func(t *testing.T) {
			for _, row := range epAskRows() {
				t.Run(row.name, func(t *testing.T) { mode.check(t, row) })
			}
		})
	}
}
