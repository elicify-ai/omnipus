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
//
// AgentsConfig.List now carries `json:"-"` (Bug 1 fix, see its doc comment on
// config.go): loadConfig's own unmarshal can therefore never populate
// cfg.Agents.List from a legacy config.json's "agents.list" key in the first
// place — the field is always empty at this point regardless of what's on
// disk. Detecting "is there legacy content to strip" must therefore read the
// raw file bytes directly (stripAgentsListOnDisk below), not inspect
// cfg.Agents.List.
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// coreAgentIDs mirrors pkg/coreagent's CoreAgentID roster (the CoreAgentID
// constants IDMia/IDJim/IDAva/IDRay/IDJudge in pkg/coreagent/core.go).
// pkg/coreagent imports pkg/config (coreagent.SeedConfig operates on
// *config.Config), so pkg/config cannot import pkg/coreagent's constants
// without creating an import cycle — these five literal IDs are deliberately
// mirrored here instead. Keep this set in sync with pkg/coreagent/core.go's
// CoreAgentID roster if it is ever extended.
//
// Why the distinction matters: a dropped core ID needs ZERO operator action
// — coreagent.SeedConfig re-creates any missing core agent (Locked=true)
// moments after this strip runs on every boot, and core IDs cannot be
// created via POST /api/v1/agents in the first place (the create endpoint
// rejects locked/core IDs). A dropped CUSTOM ID is real, operator-authored
// data loss: it is gone for good unless the operator recreates it by hand,
// and even then the recreated agent gets a brand-new server-derived ID
// (agent creation has no client-supplied `id` field), so it will NOT reclaim
// its old ID — any binding, mailbox, or workspace core_team entry naming the
// old ID is left dangling.
var coreAgentIDs = map[string]bool{
	"mia":   true,
	"jim":   true,
	"ava":   true,
	"ray":   true,
	"judge": true,
}

// stripLegacyAgentsList drops any agents.list content that survived from a
// legacy config.json, both defensively in memory and, best-effort, on disk —
// then logs an accurate WARN split by core vs custom agent IDs (see
// coreAgentIDs's doc comment for why the two cases need different operator
// guidance).
//
// Clearing cfg.Agents.List here is defense-in-depth only: the field's
// json:"-" tag already guarantees loadConfig's unmarshal never populated it
// from config.json to begin with, so this assignment is normally a no-op.
// The real detection and on-disk self-heal happens in stripAgentsListOnDisk,
// which reads the raw file bytes directly — the only way to see legacy
// content now that the typed field can't carry it.
//
// updateConfigJSONLocked (pkg/gateway/rest.go), config.json's OTHER write
// path, operates on the raw file bytes directly (read whole map -> mutate a
// delta -> write whole map back) and never goes through this struct-level
// load path at all — so without the on-disk strip below, ANY unrelated
// raw-map config write (e.g. a gateway.port change) would silently
// round-trip a legacy agents.list blob back to disk forever.
//
// Idempotent and best-effort on the disk side: a write failure only logs —
// in-memory state is already correct regardless of whether the on-disk
// self-heal succeeded (mirrors migrateCLITokenOutOfUsers's documented
// failure posture in cli_token_migration.go).
func stripLegacyAgentsList(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {
	cfg.Agents.List = nil

	coreIDs, customIDs, written, err := stripAgentsListOnDisk(cfgPath)
	if err != nil {
		logger.WarnF("failed to strip legacy agents.list from config.json on disk; runtime "+
			"behavior is still correct (in-memory state is clean), but the on-disk file could "+
			"not be self-healed and a future raw-map config write may round-trip the stale blob",
			map[string]any{
				"path":  cfgPath,
				"error": err.Error(),
			})
		return
	}
	if written == nil {
		return // already clean — no agents.list key on disk, nothing stripped
	}

	if len(coreIDs) > 0 {
		logger.WarnF("config: dropping legacy agents.list entries for core agent IDs from "+
			"config.json — no operator action needed: core agents (mia/jim/ava/ray/judge) are "+
			"auto-reseeded moments after boot by coreagent.SeedConfig, and cannot be created via "+
			"POST /api/v1/agents anyway (locked, core-agent IDs are rejected there)", map[string]any{
			"dropped_core_agent_ids": coreIDs,
			"count":                  len(coreIDs),
		})
	}
	if len(customIDs) > 0 {
		logger.WarnF("config: dropping legacy agents.list entries for custom agent IDs from "+
			"config.json — agents are now per-entity records under entities/agents/ (ADR-054); "+
			"no migration is performed (operator-accepted, no back-compat) — these agent IDs are "+
			"NOT carried forward and must be recreated (via the Agents UI/API) if still needed. "+
			"A recreated agent is assigned a NEW server-derived ID (agent creation has no "+
			"client-supplied id field), so it will NOT reclaim its old ID — any binding, mailbox, "+
			"or workspace core_team entry naming the old ID is left dangling and needs manual repair",
			map[string]any{
				"dropped_custom_agent_ids": customIDs,
				"count":                    len(customIDs),
			})
	}

	if onSelfHeal != nil {
		onSelfHeal(written)
	}
}

// stripAgentsListOnDisk rewrites config.json at path with agents.list
// removed — agents.defaults and every other key/byte-for-byte structure
// preserved untouched (same raw-JSON-map read/patch/write technique as
// migrateCLITokenOnDisk in cli_token_migration.go) — and reports which agent
// IDs were dropped, split into core (coreAgentIDs) vs custom, so the caller
// can log accurate, split guidance instead of one blanket "must be
// recreated" WARN that is false for core agents.
//
// Returns (nil, nil, nil, nil) — a true no-op, no write performed — when the
// file has no agents section on disk, or the agents section's `list` key is
// absent, not a JSON array, or a JSON array with zero elements. This
// deliberately mirrors the pre-existing in-memory gate every caller of this
// function used to rely on (a legacy `"list": []` unmarshaled into a
// zero-length — but non-nil — Go slice, which the old `len(...) == 0` guard
// treated identically to "no list key at all"): a present-but-empty list is
// left untouched on disk, same as before. Several sibling migrations'
// pinning tests (e.g. cli_token_migration_test.go's NoOpNoRewrite cases)
// fix a `"list": []` fixture byte-for-byte across an unrelated self-heal, so
// this no-write case must hold exactly.
func stripAgentsListOnDisk(path string) (coreIDs, customIDs []string, written []byte, err error) {
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, nil, nil, fmt.Errorf("read config for agents.list strip: %w", readErr)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return nil, nil, nil, fmt.Errorf("parse config for agents.list strip: %w", unmarshalErr)
	}
	agents, ok := m["agents"].(map[string]any)
	if !ok {
		return nil, nil, nil, nil // no agents section on disk — nothing to strip
	}
	rawList, hasList := agents["list"]
	if !hasList {
		return nil, nil, nil, nil // already clean
	}
	listArr, isArr := rawList.([]any)
	if !isArr || len(listArr) == 0 {
		return nil, nil, nil, nil // empty/non-array list — treated as already clean, matches legacy in-memory gate
	}

	for _, entry := range listArr {
		em, isMap := entry.(map[string]any)
		if !isMap {
			continue
		}
		id, _ := em["id"].(string)
		if id == "" {
			continue
		}
		if coreAgentIDs[id] {
			coreIDs = append(coreIDs, id)
		} else {
			customIDs = append(customIDs, id)
		}
	}
	delete(agents, "list")

	out, marshalErr := json.MarshalIndent(m, "", "  ")
	if marshalErr != nil {
		return nil, nil, nil, fmt.Errorf("serialize config for agents.list strip: %w", marshalErr)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, nil, nil, fmt.Errorf("write config for agents.list strip: %w", writeErr)
	}
	return coreIDs, customIDs, out, nil
}
