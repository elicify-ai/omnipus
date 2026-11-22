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
// The approval entry does not know skills at this commit and the two frame
// builders set neither `skill` nor `offered_actions`/`allow_durations`
// (pkg/gateway/ws_tool_approval.go::broadcastToolApprovalRequired and
// ::pendingApprovalSnapshot). Per the qa-lead RED rule the test fails LOUD
// for that missing behaviour.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"

	"github.com/stretchr/testify/require"
)

// installFrameSkillFixture writes deep-research into the loop's skills
// directory so the server can derive the skill facts from storage.
func installFrameSkillFixture(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "skills", "deep-research")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := "---\nname: deep-research\ndescription: Multi-step web research with cited sources.\nmetadata:\n  display_name: Deep Research\n---\nBody."
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644))
}

// TestSkillApprovalFrame_CarriesSkill (order 13, S-10).
// Traces to: BDD S-10; spec C-9, C-24, C-26 (permission-open row).
func TestSkillApprovalFrame_CarriesSkill(t *testing.T) {
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, cfg := newTestWSHandlerForModelName(t, msgBus)
	installFrameSkillFixture(t, cfg.Agents.Defaults.Home)

	reg := newApprovalRegistryV2(64, 300*time.Second)
	// A Skill load whose effective permission is Ask: the pending approval
	// is for tool "Skill" with the load arguments; every skill fact on the
	// card is server-derived (C-9).
	entry, accepted := reg.requestApproval("tc-skill-load-1", "Skill",
		map[string]any{"skill": "deep-research", "action": "load"},
		"mia", "sess-skill-1", "turn-skill-1")
	require.True(t, accepted)
	t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
	go func() { <-entry.resultCh }()
	handler.approvalRegV2 = reg

	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{}), userID: "skill-card"}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions["skill-card"] = wc
	handler.mu.Unlock()

	handler.broadcastToolApprovalRequired(entry)
	var frame gen.ToolApprovalRequiredFrame
	select {
	case raw := <-wc.sendCh:
		require.NoError(t, json.Unmarshal(raw, &frame))
	case <-time.After(2 * time.Second):
		t.Fatal("no tool_approval_required frame")
	}

	// C-9: the card names the skill.
	require.NotNil(t, frame.Skill, "C-9: the frame carries the skill object; S-10 names the skill on the card")
	require.Equal(t, "deep-research", frame.Skill.Key, "C-23 key")
	require.Equal(t, "deep-research", frame.Skill.Name, "the slug")
	require.Equal(t, "Deep Research", frame.Skill.DisplayName, "the display name (C-9)")
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
	snap := handler.pendingApprovalSnapshot(entry)
	require.NotNil(t, snap.Skill, "C-9: SessionStatePendingApproval carries the same skill object")
	require.Equal(t, frame.Skill.Key, snap.Skill.Key, "the snapshot's skill matches the live frame")
	require.Equal(t, frame.OfferedActions, snap.OfferedActions, "a rebuilt card offers exactly what the server accepts (C-24)")
	require.Equal(t, frame.AllowDurations, snap.AllowDurations, "the snapshot carries the time choices")
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
