// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Legacy agents.list strip (ADR-054 entity/config separation, D1/D2/§11
// checklist item 8).
//
// Agents are no longer entities inside config.json: each agent record now
// lives in its own file under $OMNIPUS_HOME/entities/agents/<id>.json
// (ADR-054 D2), owned and populated by the agent registry/store
// (pkg/agentstore, pkg/agent), never by pkg/config. `agents.defaults` is a
// SETTING and stays in config.json (D1) — only the `list` sub-key is retired.
//
// Per ADR-054 §11 checklist item 8, this must NOT be done by simply deleting
// the whole `agents` key or the whole AgentsConfig.List struct field:
//   - Deleting all of `Agents` would make "agents" an unrecognized top-level
//     key, which detectUnknownConfigFields (migration.go) stashes into
//     cfg.UnknownFields and re-emits verbatim on every save forever — the
//     exact ghost-key failure mode already reproduced live with a "heartbeat"
//     key. The 31KB legacy roster blob would round-trip in config.json
//     forever, negating this ADR's entire benefit.
//   - Removing only the List field would leave `agents` known but `list`
//     silently unrecognized-and-dropped by encoding/json with zero log
//     output — an operator-invisible, no-error data loss.
//
// This is a per-ADR "no migration, no back-compat" cutover (operator-
// accepted, see ADR-054 §10 Consequences / Negative): a legacy config.json's
// agents.list content is NOT carried forward into entities/ — it is dropped,
// loudly (a WARN log naming every dropped agent ID), both in memory and,
// best-effort, on disk.
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// stripLegacyAgentsList drops any agents.list content that survived from a
// legacy config.json.
//
// It must clear the in-memory cfg.Agents.List unconditionally so a legacy
// roster blob unmarshaled from an old config.json never leaks into a fresh
// boot as phantom agents (before the real per-entity roster, if any, is
// separately loaded from entities/agents/ by the agent registry) — AND
// best-effort rewrite the ON-DISK file to drop the same key. The on-disk
// rewrite matters because config.json's other write path,
// updateConfigJSONLocked (pkg/gateway/rest.go), operates on the raw file
// bytes directly (read whole map -> mutate a delta -> write whole map back)
// and never goes through this struct-level load path at all — so without an
// on-disk strip, ANY unrelated raw-map config write (e.g. a gateway.port
// change) would silently round-trip the stale agents.list blob back to disk
// forever, exactly like the config-level fix alone would for the
// struct-only path.
//
// agents.defaults is untouched (D1: it is a SETTING, stays in config.json).
// Idempotent (a clean config or a config with only agents.defaults is a
// no-op) and best-effort on the disk side: a write failure only logs — the
// in-memory clear already makes runtime behavior correct regardless of
// whether the on-disk self-heal succeeded (mirrors migrateCLITokenOutOfUsers's
// documented failure posture in cli_token_migration.go).
func stripLegacyAgentsList(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {
	if len(cfg.Agents.List) == 0 {
		return // nothing to strip — already-clean config, or fresh install
	}

	ids := make([]string, 0, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		ids = append(ids, a.ID)
	}
	logger.WarnF("config: dropping legacy agents.list from config.json — agents are now "+
		"per-entity records under entities/agents/ (ADR-054); no migration is performed "+
		"(operator-accepted, no back-compat) — these agent IDs are NOT carried forward "+
		"and must be recreated (via the Agents UI/API) if still needed", map[string]any{
		"dropped_agent_ids": ids,
		"count":             len(ids),
	})
	cfg.Agents.List = nil

	written, healErr := stripAgentsListOnDisk(cfgPath)
	if healErr != nil {
		logger.WarnF("failed to strip legacy agents.list from config.json on disk; runtime "+
			"behavior is still correct (in-memory state is clean), but the on-disk file could "+
			"not be self-healed and a future raw-map config write may round-trip the stale blob",
			map[string]any{
				"path":  cfgPath,
				"error": healErr.Error(),
			})
		return
	}
	if written != nil && onSelfHeal != nil {
		onSelfHeal(written)
	}
}

// stripAgentsListOnDisk rewrites config.json at path with agents.list
// removed — agents.defaults and every other key preserved byte-for-byte
// structure untouched (same raw-JSON-map read/patch/write technique as
// migrateCLITokenOnDisk in cli_token_migration.go).
//
// Returns (nil, nil) — a true no-op, no write performed — when the file has
// no agents section on disk, or the agents section has no list key to drop
// (already clean).
func stripAgentsListOnDisk(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config for agents.list strip: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("parse config for agents.list strip: %w", unmarshalErr)
	}
	agents, ok := m["agents"].(map[string]any)
	if !ok {
		return nil, nil // no agents section on disk — nothing to strip
	}
	if _, hasList := agents["list"]; !hasList {
		return nil, nil // already clean
	}
	delete(agents, "list")

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize config for agents.list strip: %w", err)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write config for agents.list strip: %w", writeErr)
	}
	return out, nil
}
