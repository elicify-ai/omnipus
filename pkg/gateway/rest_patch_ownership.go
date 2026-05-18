//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — patchAgentOwnership handler.
//
// Implements PATCH /api/v1/agents/<id> for the ownership field only.
//
// BDD scenarios: #79b (non-admin → 403), #79c (unknown user → 400),
// #79d (empty without header → 400, empty with header → 200).
// Traces to: path-sandbox-and-capability-tiers-spec.md

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// patchAgentOwnership handles PATCH /api/v1/agents/<agentID> for the
// owner_username field. Only admins may call this endpoint.
//
// Request body: {"owner_username": "<username>"}
//
// Rules:
//   - Caller must be admin → 403 if not.
//   - agentID must exist in config → 404 if not.
//   - System and core agents have no owner → 400 if targeted.
//   - owner_username="" requires X-Confirm-Demote: 1 header → 400 without it.
//   - owner_username must exist in cfg.Gateway.Users when non-empty → 400 if not.
//   - On success: persists via safeUpdateConfigJSON and returns 200.
func (a *restAPI) patchAgentOwnership(w http.ResponseWriter, r *http.Request, agentID string) {
	// Auth: admin only.
	requester, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || requester == nil || requester.Role != config.UserRoleAdmin {
		jsonErr(w, http.StatusForbidden, "admin only: patchAgentOwnership requires admin role")
		return
	}

	// Decode body.
	var body gen.AgentOwnershipUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "AgentOwnershipUpdateRequest", &body, validateEnabled) {
		return
	}

	// Find agent in live config.
	cfg := a.agentLoop.GetConfig()
	foundIdx := -1
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			foundIdx = i
			break
		}
	}
	if foundIdx == -1 {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}

	found := cfg.Agents.List[foundIdx]

	// System and core agents have no owner.
	if config.IsSystemAgent(&found) {
		jsonErr(w, http.StatusBadRequest, "system agents have no owner")
		return
	}

	newOwner := ""
	if body.OwnerUsername != nil {
		newOwner = *body.OwnerUsername
	}

	// Clearing the owner requires explicit confirmation header to prevent accidents.
	if newOwner == "" {
		if r.Header.Get("X-Confirm-Demote") != "1" {
			jsonErr(w, http.StatusBadRequest,
				"clearing owner_username requires X-Confirm-Demote: 1 header")
			return
		}
	} else {
		// Verify the target user exists.
		userExists := false
		for _, u := range cfg.Gateway.Users {
			if u.Username == newOwner {
				userExists = true
				break
			}
		}
		if !userExists {
			jsonErr(w, http.StatusBadRequest, "owner_username does not exist")
			return
		}
	}

	// Persist via safeUpdateConfigJSON (holds configMu, raw-JSON read-modify-write).
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		if agents == nil {
			return fmt.Errorf("agents section missing in config.json")
		}
		list, _ := agents["list"].([]any)
		for _, entry := range list {
			agentMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if agentMap["id"] == agentID {
				if newOwner == "" {
					delete(agentMap, "owner_username")
				} else {
					agentMap["owner_username"] = newOwner
				}
				return nil
			}
		}
		return fmt.Errorf("agent %q not found in agents.list during persist", agentID)
	}); err != nil {
		slog.Error("rest: patchAgentOwnership: save config failed",
			"agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	jsonOK(w, gen.AgentOwnerUpdateResponse{
		Success:       true,
		AgentId:       agentID,
		OwnerUsername: newOwner,
	})
}
