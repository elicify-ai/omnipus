//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 13.
// TestSkillApprovalFrame_CarriesSkill: a Skill-load approval card names the
// skill (C-9) and carries the server-computed choices (C-24/C-26), live and
// after a reload.
//
// Oracles: skills-tools-redesign-spec.md C-9 (the `skill` object: key, name,
// display_name, revision, unverified, open_checks — every value
// server-derived from the pending approval; SessionStatePendingApproval
// gains the same so a card rebuilt after a reload is complete), C-24
// (offered_actions / allow_durations computed when the card is raised), C-26
// (a permission-open card offers Approve Once, Allow for 1 hour / 3 hours /
// 1 day, Deny), BDD S-10. The exact refusal behaviours are order 58
// (TestToolApprovals_TimedActions); the fixtures here pin only what the
// frame carries.
//
// Original RED reason: before the redesign the approval entry did not know
// skills and the two frame builders set neither `skill` nor
// `offered_actions`/`allow_durations`
// (pkg/gateway/ws_tool_approval.go::broadcastToolApprovalRequired and
// ::pendingApprovalSnapshot). Per the qa-lead RED rule the test fails LOUD
// for that missing behaviour.
//
// Fix round 4, part D item 7 (backend handoff-4, "Frame test CarriesSkill"):
// the first draft built the pending approval by hand,
// requestApproval(..., map{"skill": "deep-research", "action": "load"}, ...).
// Two things were wrong with that, both read in the code: the real Skill
// tool's argument is `name` (pkg/tools/skill.go::SkillTool.Parameters), and a
// production card gets its skill facts from the agent's request — the loop
// resolves the skill the call names and hands the facts to the registry
// (pkg/gateway/policy_approver.go, approvalExtras.Skill) — never from the
// tool arguments the registry is given. A hand-built entry therefore can never
// carry a skill object, whatever the frame builders do, so renaming the
// argument alone would not have made the test meaningful. The test now raises
// the card the way S-10 says it happens, a web-chat Skill load, through the
// real loop, the real policy approver and the real frame builder, and reads
// the live frame the connected client receives plus the reload snapshot. Only
// the model is scripted (skill_approval_native_fixture_red_test.go). Every
// oracle is unchanged except the display-name literal: the harness skill is
// notes-helper, whose metadata names it "Notes Helper"; the slug differs from
// the display name, so the value can only come from the metadata (S-10's
// "Deep Research" is the spec's example skill).

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"

	"github.com/stretchr/testify/require"
)

// frameSkillMarkdown is the notes-helper SKILL.md of the frame test: the native
// fixture skill plus a display name in its metadata.
const frameSkillMarkdown = "---\nname: notes-helper\ndescription: Native consent fixture.\nmetadata:\n  display_name: Notes Helper\n---\n\nF18-NOTES-R1-INSTRUCTIONS-9Z\n"

// nextApprovalRequiredFrame returns the next tool_approval_required frame the
// connection receives, skipping any other frame type, and fails the test if
// none arrives.
func nextApprovalRequiredFrame(t *testing.T, wc *wsConn) gen.ToolApprovalRequiredFrame {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case raw := <-wc.sendCh:
			var probe struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(raw, &probe))
			if probe.Type != string(gen.WsFrameTypeToolApprovalRequired) {
				continue
			}
			var frame gen.ToolApprovalRequiredFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			return frame
		case <-deadline:
			t.Fatal("no tool_approval_required frame reached the connected client")
		}
	}
}

// TestSkillApprovalFrame_CarriesSkill (order 13, S-10).
// Traces to: BDD S-10; spec C-9, C-24, C-26 (permission-open row).
func TestSkillApprovalFrame_CarriesSkill(t *testing.T) {
	// Trust Allow and permission Ask: for this plain, unverified folder only the
	// permission check is open (C-12 row 2), so the card is the permission card.
	h := newNativeSkillHarnessWithBody(t, "allow", "ask", frameSkillMarkdown)

	// One connected client. broadcastToolApprovalRequired reaches every
	// connection in the handler's session table (FR-073, single account); this
	// connection receives what the policy approver really sends.
	wc := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{}), userID: "skill-card"}
	h.handler.mu.Lock()
	if h.handler.sessions == nil {
		h.handler.sessions = make(map[string]*wsConn)
	}
	h.handler.sessions["skill-card"] = wc
	h.handler.mu.Unlock()

	// A real web-chat Skill load whose effective permission is Ask: the card is
	// raised by the loop, never inserted by the test (S-10).
	run := h.begin(t)
	entry := h.pending(t, run)
	frame := nextApprovalRequiredFrame(t, wc)
	require.Equal(t, "Skill", frame.ToolName, "S-10: the approval card is for tool Skill")

	// C-9: the card names the skill.
	require.NotNil(t, frame.Skill, "C-9: the frame carries the skill object; S-10 names the skill on the card")
	require.Equal(t, "notes-helper", frame.Skill.Key, "C-23 key")
	require.Equal(t, "notes-helper", frame.Skill.Name, "the slug")
	require.Equal(t, "Notes Helper", frame.Skill.DisplayName, "the display name, from the skill's metadata (C-9)")
	require.NotEmpty(t, frame.Skill.Revision, "the installed folder's ContentRevision (C-5)")
	require.True(t, frame.Skill.Unverified, "a plain installed folder has no registry hash, so it is unverified (C-5)")
	require.Contains(t, frame.Skill.OpenChecks, "permission",
		"C-12 row 2: the permission check is open")

	// C-24/C-26: the choices are server-computed on the frame.
	require.Equal(t,
		[]string{"allow_once", "allow_for", "deny"},
		frame.OfferedActions,
		"C-26 permission card: Approve Once, Allow for, Deny — no always allow (S-10)")
	require.Equal(t, []string{"1h", "3h", "1d"}, frame.AllowDurations,
		"C-26: the three time choices (S-10)")

	// C-9: the reload snapshot carries the same, so a rebuilt card is
	// complete.
	snap := h.handler.pendingApprovalSnapshot(entry)
	require.NotNil(t, snap.Skill, "C-9: SessionStatePendingApproval carries the same skill object")
	require.Equal(t, frame.Skill.Key, snap.Skill.Key, "the snapshot's skill matches the live frame")
	require.Equal(t, frame.OfferedActions, snap.OfferedActions, "a rebuilt card offers exactly what the server accepts (C-24)")
	require.Equal(t, frame.AllowDurations, snap.AllowDurations, "the snapshot carries the time choices")

	// End the waiting call: Deny, so the turn finishes and nothing loads.
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	h.finish(t, run, false)
}

// TestSkillApprovalFrame_NonSkillCardUnchanged guards the C-9 scope: a
// plain tool card carries no skill object.
func TestSkillApprovalFrame_NonSkillCardUnchanged(t *testing.T) {
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	entry, accepted := reg.requestApproval("tc-bash-1", "bash",
		map[string]any{"command": "echo hi"}, "mia", "sess-2", "turn-2")
	require.True(t, accepted)
	t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
	go func() { <-entry.resultCh }()
	handler.approvalRegV2 = reg
	snap := handler.pendingApprovalSnapshot(entry)
	require.Nil(t, snap.Skill, "C-9: no skill object on a non-skill card")
	_, err := json.Marshal(snap)
	require.NoError(t, err)
}
