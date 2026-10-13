// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// taskMainResolver is the gateway's agent.MainSessionResolver (session-core U6,
// D-U6.1): it answers which (workspace, agent) pairs currently own a visible
// main, using the same eligibility the REST surface applies
// (mainSessionPairEligible) and get-or-creating the main through the same path
// (ensureMainSession). A workspace that does not exist, or a pair that is not
// eligible, is "no main" (false, nil); anything that cannot be READ is an error,
// never "not eligible".
type taskMainResolver struct{ api *restAPI }

// EligibleMain implements agent.MainSessionResolver.
func (r taskMainResolver) EligibleMain(workspaceID, agentID string) (string, bool, error) {
	if r.api == nil || r.api.agentLoop == nil {
		return "", false, errors.New("main resolver: the gateway is not wired")
	}
	ws, err := readWorkspaceFile(r.api.homePath, workspaceID)
	if err != nil {
		if errors.Is(err, errWorkspaceNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("main resolver: read workspace %q: %w", workspaceID, err)
	}
	if !mainSessionPairEligible(r.api.agentLoop.GetConfig(), ws, agentID) {
		return "", false, nil
	}
	id, err := r.api.ensureMainSession(ws.ID, agentID)
	if err != nil {
		return "", false, fmt.Errorf("main resolver: main of %q in workspace %q: %w", agentID, workspaceID, err)
	}
	if want, werr := session.MainSessionID(ws.ID, agentID); werr != nil || want != id {
		return "", false, fmt.Errorf("main resolver: computed main id mismatch for %q/%q", workspaceID, agentID)
	}
	return id, true, nil
}
