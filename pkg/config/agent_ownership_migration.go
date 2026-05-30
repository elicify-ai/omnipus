//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — agent ownership migration.
//
// migrateAgentOwnership assigns an OwnerUsername to every ownerless custom
// agent in cfg, picking the alphabetically-first admin from cfg.Gateway.Users.
// It also persists the change to cfgPath on disk (raw JSON, preserving all
// other fields).
//
// Rules (BDD dataset rows from path-sandbox-and-capability-tiers-spec.md):
//   - System and core agents are skipped.
//   - Agents that already have an OwnerUsername are skipped.
//   - If no admin exists, OwnerUsername stays "" and a WARN is emitted.
//   - Otherwise, the alphabetically-first admin is assigned.
//
// Persistence strategy: read cfgPath as raw JSON, update the matching
// agents.list entry's owner_username field, write back atomically.

package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"slices"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// migrateAgentOwnership assigns OwnerUsername to ownerless custom agents and
// persists the change to cfgPath. It mutates cfg in-place.
func migrateAgentOwnership(cfg *Config, cfgPath string) {
	// Collect sorted admin usernames.
	var admins []string
	for _, u := range cfg.Gateway.Users {
		if u.Role == UserRoleAdmin {
			admins = append(admins, u.Username)
		}
	}
	slices.Sort(admins)

	// Determine the alphabetically-first admin (may be empty).
	firstAdmin := ""
	if len(admins) > 0 {
		firstAdmin = admins[0]
	}

	// Track which agent IDs received an update so we can persist selectively.
	type update struct {
		id    string
		owner string
	}
	var updates []update

	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if IsSystemAgent(a) {
			// System and core agents must never receive an owner.
			continue
		}
		if a.OwnerUsername != "" {
			// Already owned — preserve.
			continue
		}
		if firstAdmin == "" {
			slog.Warn("migrateAgentOwnership: no admin available to assign as owner",
				"agent_id", a.ID)
			continue
		}
		slog.Info("migrateAgentOwnership: assigning owner",
			"agent_id", a.ID, "owner", firstAdmin)
		a.OwnerUsername = firstAdmin
		updates = append(updates, update{id: a.ID, owner: firstAdmin})
	}

	if len(updates) == 0 {
		return
	}

	// Persist changes to disk: read raw JSON, patch agents.list entries, write back.
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		slog.Error("migrateAgentOwnership: read config failed", "error", err)
		return
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		slog.Error("migrateAgentOwnership: parse config failed", "error", unmarshalErr)
		return
	}

	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		slog.Error("migrateAgentOwnership: agents section missing in config")
		return
	}
	list, _ := agents["list"].([]any)
	for _, u := range updates {
		for _, entry := range list {
			agentMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if agentMap["id"] == u.id {
				agentMap["owner_username"] = u.owner
				break
			}
		}
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		slog.Error("migrateAgentOwnership: marshal config failed", "error", err)
		return
	}
	if err := fileutil.WriteFileAtomic(cfgPath, out, 0o600); err != nil {
		slog.Error("migrateAgentOwnership: write config failed", "error", err)
	}
}
