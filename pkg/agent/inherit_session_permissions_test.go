// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestInheritSessionPermissions_DelegateOwnOffSwitchSurvivesInheritance is
// the regression test for review finding #6 (MEDIUM, 2026-09-23 security
// fix lane): a delegate whose OWN agent config sets
// AutoApproveDisabled=true must never end up running in Auto because it
// inherited a parent chat's Auto-on per-chat modifier.
//
// On b7dc66acf, inheritSessionPermissions unconditionally copied the
// parent's SessionModeStore modifier onto the child session
// (al.SessionModes().InheritFrom), and ResolveAutoApprove consulted that
// chat modifier BEFORE the agent's own off-switch (`if chat != nil {
// return *chat }` ran first) — so a delegate with its own
// AutoApproveDisabled=true still resolved Auto=true once it inherited an
// Auto-on modifier from its parent's chat.
func TestInheritSessionPermissions_DelegateOwnOffSwitchSurvivesInheritance(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	const delegateID = "jim-delegate"
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:                  delegateID,
		AutoApproveDisabled: true,
	})

	const parentSessionID = "parent-session"
	const childSessionID = "child-session"

	// The parent's chat explicitly turned Auto ON for its own conversation
	// — a human made that choice for the PARENT's own agent, not for
	// whatever the parent later delegates to.
	al.SessionModes().Set(parentSessionID, true)

	al.inheritSessionPermissions(parentSessionID, testDefaultAgentID, childSessionID, delegateID)

	// The delegate's own off-switch must be the floor: it must not have
	// received the parent's Auto-on modifier.
	if v, ok := al.SessionModes().Get(childSessionID); ok {
		t.Errorf("child session inherited a per-chat modifier (%v) despite its own agent's "+
			"AutoApproveDisabled=true — finding #6 regression", v)
	}

	// End-to-end: the child's own resolution must come out Auto=false.
	childChat, ok := al.SessionModes().Get(childSessionID)
	var chatPtr *bool
	if ok {
		chatPtr = &childChat
	}
	assert.False(t, ResolveAutoApprove(cfg, delegateID, chatPtr),
		"delegate with AutoApproveDisabled=true resolved Auto=true after inheriting its parent's chat modifier")
}

// TestInheritSessionPermissions_OrdinaryDelegateStillInherits is the
// positive control: a delegate with NO off-switch of its own must still
// inherit the parent's per-chat modifier exactly as before this fix.
func TestInheritSessionPermissions_OrdinaryDelegateStillInherits(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	const delegateID = "ordinary-delegate"
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: delegateID})

	const parentSessionID = "parent-session-2"
	const childSessionID = "child-session-2"

	al.SessionModes().Set(parentSessionID, true)
	al.inheritSessionPermissions(parentSessionID, testDefaultAgentID, childSessionID, delegateID)

	v, ok := al.SessionModes().Get(childSessionID)
	if !ok || !v {
		t.Fatalf("ordinary delegate (no off-switch) did not inherit the parent's Auto-on modifier: got (%v, %v), want (true, true)", v, ok)
	}
}
