// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestInheritSessionPermissions_DelegateInheritsParentModifier: a delegate
// inherits its parent's per-chat Auto-approve modifier, with no per-agent
// exception.
func TestInheritSessionPermissions_DelegateInheritsParentModifier(t *testing.T) {
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
		t.Fatalf("delegate did not inherit the parent's Auto-on modifier: got (%v, %v), want (true, true)", v, ok)
	}
}
