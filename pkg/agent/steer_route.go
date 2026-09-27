// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 identity — steered-session route pinning. A steered child's agent
// is pinned by its LifecycleRecord (SteerLauncher.Launch writes AgentID at
// launch; reconstructSteeredTurn rebuilds every turn under it). This file
// holds the one lookup resolveMessageRoute uses to make inbound routing obey
// that pin; see the intercept there for the failure it prevents.
package agent

import "strings"

// pinnedSteeredAgentID returns the LifecycleRecord-pinned agent for a steered
// child session, or "" when sessionID is blank, no lifecycle store is wired,
// no record exists, or the record is not steered (SteeredBy == nil — an
// ordinary root keeps today's dropdown/handoff/cascade routing). Store read
// errors also return "": a damaged record must not silently re-route a
// message that today's cascade would have delivered; classification-level
// repair stays with boot_sweep/SteerRecordClassifier.
func (al *AgentLoop) pinnedSteeredAgentID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	store := al.GetSessionLifecycleStore()
	if store == nil {
		return ""
	}
	rec, err := store.Load(sessionID)
	if err != nil || rec == nil || rec.SteeredBy == nil {
		return ""
	}
	return strings.TrimSpace(rec.AgentID)
}
