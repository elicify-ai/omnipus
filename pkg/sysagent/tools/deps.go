// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package systools implements the 33 exclusive system.* tools for the
// Omnipus system agent per BRD Appendix D §D.4.
package systools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Deps bundles all shared dependencies for system tools.
//
// FR-050 / C1 atomic-pointer contract
//
// The reload path in pkg/agent/loop.go::ReloadProviderAndConfig satisfies FR-050
// via the "rebuild" pattern rather than the "atomic swap" pattern:
//
//  1. ReloadProviderAndConfig calls wireSysagentDepsLocked(newRegistry, deps).
//  2. wireSysagentDepsLocked calls AllTools(deps) which constructs fresh tool
//     instances that capture the new *Deps pointer directly.
//  3. The new instances are registered on the new registry, which is then swapped
//     in atomically under al.mu (write lock).
//
// This guarantees that any Execute call after the lock is released sees the new
// deps because the tool instance itself is new.  There is no window where a tool
// holds a stale *Deps and the registry has already been swapped — the registry
// swap and the tool reconstruction are under the same al.mu write lock.
//
// Current atomic.Pointer usage:
//   - pkg/agent/instance.go: AgentInstance.toolPolicy  atomic.Pointer[tools.ToolPolicyCfg]
//   - Deps.Live (below): atomic.Pointer[Deps] enabling future hot-swap of deps without
//     a full registry rebuild (FR-050 structural guarantee).
//
// Tools that need future hot-swap semantics call deps.Live.Load() instead of using
// the *Deps directly; the field is already wired so adding Live support to a new
// tool is a one-line change.
type Deps struct {
	// Live is an optional atomic pointer to the current Deps snapshot.
	// When non-nil and loaded value is non-nil, tools that opt into hot-swap
	// semantics call d.Live.Load() to read the current deps without requiring a
	// full registry rebuild. The field satisfies the FR-050 structural contract:
	// presence of atomic.Pointer[Deps] on this type is detectable by reflection
	// (TestRegistry_ToolDepsContract asserts this).
	//
	// Reload path: ReloadProviderAndConfig rebuilds tool instances; Live is an
	// additive structural guarantee for forward-compatibility, not a runtime
	// dependency today.
	Live *atomic.Pointer[Deps]
	// Home is the ~/.omnipus/ data directory path.
	Home string
	// ConfigPath is the path to config.json.
	ConfigPath string
	// GetCfg returns the current in-memory config. It is always called at
	// invocation time so that hot-reloaded configs (via agentLoop.SwapConfig)
	// are visible to system tools without restarting. The returned pointer must
	// not be retained across calls.
	//
	// NOTE: callers must not mutate the returned config outside of WithConfig.
	// All mutations must go through WithConfig to be serialized with al.mu.
	GetCfg func() *config.Config
	// MutateConfig acquires the agent loop write lock and calls fn with the
	// live *config.Config pointer. This serializes sysagent mutations with
	// REST readers that hold the agent loop RLock via GetConfig. The gateway
	// wires this to AgentLoop.MutateConfig. In tests without an AgentLoop,
	// provide a simple mutex-based implementation.
	MutateConfig func(fn func(*config.Config) error) error
	// SaveConfigLocked persists cfg to disk. The caller MUST already hold
	// al.mu via MutateConfig — this function does NOT acquire any mutex.
	// Keeping it lock-free breaks the AB-BA deadlock:
	//
	//   REST path (old):     gatewayConfigMu → al.mu
	//   Sysagent path (old): al.mu → gatewayConfigMu
	//
	// With SaveConfigLocked, the sysagent path never acquires gatewayConfigMu,
	// so there is no second mutex and therefore no deadlock.
	//
	// The gateway wires this to a closure that calls config.SaveConfig directly.
	// Tests that do not need real persistence can set it to a no-op or leave it
	// nil (WithConfig will return an error in that case).
	SaveConfigLocked func(cfg *config.Config) error
	// CredStore is the encrypted credential store.
	CredStore *credentials.Store
	// ReloadFunc triggers a hot-reload of the agent loop so newly created agents
	// become available immediately. Nil in tests or when not wired.
	//
	// AgentCreateTool/AgentUpdateTool prefer UpsertAgentFastFunc below when it
	// is wired (issue #571, sysagent half) and fall back to this full reload
	// only when UpsertAgentFastFunc is nil. AgentDeleteTool always uses this
	// field — see UpsertAgentFastFunc's own doc comment for why delete is
	// deliberately NOT fast-pathed.
	ReloadFunc func() error
	// ReconcileMCP triggers live MCP reconciliation (connect/disconnect servers
	// and re-sync every agent's tool registry plus the central MCPRegistry)
	// after a config mutation that adds, removes, or edits an MCP server.
	// Without this, add_mcp_server / remove_mcp_server only persist
	// config.json — the live pkg/mcp.Manager and central registry are
	// untouched until the next full gateway reload. Nil in tests or when not
	// wired — callers must nil-check before use.
	//
	// The gateway wires this to AgentLoop.ReconcileMCP.
	ReconcileMCP func(ctx context.Context) error
	// MCPStatus reports live per-server connection status so mcp tool results
	// can be honest about whether a server actually connected rather than
	// just echoing the config write. status is one of "connected", "error",
	// or "disconnected"; toolCount is the number of tools currently
	// registered for that server in the central MCP registry (0 when unset);
	// errMsg carries the last connect error when status is "error" (empty
	// otherwise). Nil in tests or when not wired — callers must nil-check
	// before use.
	//
	// The gateway wires this to AgentLoop.MCPServerStatus.
	MCPStatus func(name string) (status string, toolCount int, errMsg string)
	// UpsertAgentFastFunc publishes a single agent create/update into the
	// live AgentRegistry without restarting channels/cron/schedulers/the plan
	// engine (issue #571's sysagent-facing half — the agent-facing
	// counterpart of pkg/gateway/rest.go's fastAgentUpsert, which already
	// does this for the REST create/update handlers). Given the just-
	// created/updated agent's ID, the wired implementation must swap ONLY
	// that agent's *AgentInstance into the live registry
	// (AgentLoop.UpsertAgentFast) and rebuild the resolver/default-agent
	// override so the agent is immediately resolvable via
	// GetAgent/ResolveRoute/GetDefaultAgent — not merely present in a map
	// (see pkg/agent/registry.go's UpsertAgentFast doc comment for the two
	// traps that requires closing).
	//
	// Returns nil on success. The wired implementation is expected to fall
	// back to the full reload path itself on failure (including when a full
	// reload is already in flight and must be waited out rather than raced)
	// and return that fallback's error, if any — so AgentCreateTool/
	// AgentUpdateTool have a single hot-reload call site instead of
	// duplicating the "try fast, fall back to full" decision here.
	//
	// Nil in tests or when not wired: AgentCreateTool/AgentUpdateTool fall
	// back to ReloadFunc (the pre-existing full-reload behavior) in that
	// case, never a silent no-op. The production gateway wires this next to
	// ReloadFunc; see gateway.go where sysAgentDeps is constructed.
	UpsertAgentFastFunc func(agentID string) error
	// SkillsLoader provides access to the locally installed skills tree
	// (workspace, global, and builtin skill directories). Nil in tests or when
	// not wired — callers must nil-check before use.
	SkillsLoader *skills.SkillsLoader
	// RegistryManager coordinates remote skill registries (ClawHub, etc.) for
	// search and install-by-slug operations. Nil when no registries are
	// configured or when not wired — callers must nil-check before use.
	RegistryManager *skills.RegistryManager
	// SkillInstaller downloads and installs skills from GitHub or a registry
	// into the operator workspace. Nil in tests or when not wired — callers
	// must nil-check before use.
	SkillInstaller *skills.SkillInstaller
	// SkillWriter authors and versions skills (system.skill.create / edit). It
	// is rooted at the global (user) skills directory so editing a built-in
	// produces a user override rather than mutating the shipped built-in in
	// place. Nil in tests or when not wired — callers must nil-check before use.
	SkillWriter *skills.SkillWriter
	// DelegationDeny, when non-nil, applies the full FR-6.2 delegation policy
	// (trust set + mode("task") + depth) to a cross-workspace task assignment or
	// reassignment. It is the SAME gate the plain create_task / update_task tools
	// enforce (pkg/tools/task.go), resolved dynamically from the live config for
	// the CALLING agent — because the sysagent in_workspace tools are registered
	// once on a central registry and cannot bind a per-agent checker at
	// construction time.
	//
	// callerAgentID is the acting agent (tools.ToolAgentID(ctx)); targetAgentID is
	// the agent_id being assigned. A nil return ALLOWS; a non-nil *tools.DelegationDenial
	// DENIES (carrying the structured reason + policy axis the SPA renders).
	//
	// When nil (tests / standalone), the gate is SKIPPED (fail-open ONLY when
	// unwired — matching how the plain tools no-op when their checker is unset).
	// The production gateway MUST wire this; see gateway.go where sysAgentDeps is
	// constructed.
	DelegationDeny func(ctx context.Context, callerAgentID, targetAgentID string) *tools.DelegationDenial

	// ResolveBashPolicy resolves an assignee agent's effective "bash" tool
	// policy (ADR-049 D2 rule 5, FR-017/052, review r1 major M5) — the SAME
	// gate the plain create_task tool enforces (pkg/tools/task.go), needed so
	// create_task_in_workspace can reject a create whose criteria are ALL
	// kind=check when that policy is deny or ask (structurally unsatisfiable:
	// the machine check could never even run). Returns ok=false when the
	// assignee agent cannot be resolved at all.
	//
	// FAIL CLOSED, not open, when nil (tests / standalone): an unwired
	// checker is a configuration error, never a permission grant — mirrors
	// the plain create_task tool's own bashPolicyChecker discipline
	// (pkg/tools/task.go SetBashPolicyChecker), NOT DelegationDeny's
	// documented fail-open-when-unwired convention above (a separate,
	// pre-existing policy axis this fix does not touch). The production
	// gateway MUST wire this; see gateway.go where sysAgentDeps is
	// constructed.
	ResolveBashPolicy func(assigneeAgentID string) (policy string, ok bool)

	// ListSessions returns all sessions across all stores (shared + legacy per-agent),
	// deduplicating entries that appear in both. Errors are per-store and non-fatal;
	// the slice may be partial on error. Nil when not wired — the get_usage tool
	// returns a USAGE_UNAVAILABLE error when this field is nil, because a nil
	// dependency in production is a wiring bug that must fail loudly rather than
	// returning an empty report indistinguishable from genuine zero usage.
	//
	// ADR-057 U9 (FR-092/FR-098) changed AgentLoop.ListAllSessions' signature to
	// (limit, offset int, parentSessionID string, flat bool) (session.SessionListPage,
	// []error) — paginated and hierarchy-aware. This field's own zero-arg shape is
	// intentionally UNCHANGED (ADR-057 U25, W16i, [grill2 C2-1]): get_usage always
	// wants the whole session set for a correct aggregate and has no pagination
	// concept of its own, so widening this field to mirror ListAllSessions'
	// parameters would add knobs no caller can usefully vary. The production
	// gateway wires this via u25AllSessionsForUsage (pkg/gateway/gateway.go),
	// which calls ListAllSessions(0, 0, "", true) — flat=true is load-bearing, not
	// a default of convenience: FR-104 warns that a roots-only listing silently
	// drops delegated children's token spend from the merged set, which would
	// make get_usage under-report real spend with a green build.
	ListSessions func() ([]*session.UnifiedMeta, []error)

	// PlanStore, when non-nil, backs create_task_in_workspace's optional
	// plan_id linkage arg (ADR-052 FR-002): validates the same-workspace FK
	// and rejects linking to a terminal (done/failed) plan. Mirrors the
	// plain create_task tool's SetPlanStore (pkg/tools/task.go). A nil
	// PlanStore with a non-empty plan_id arg fails closed — never a silent
	// no-op linkage. The production gateway wires this to the same
	// *plan.Store instance passed to the plain create_task tool.
	PlanStore *plan.Store
}

// clearMaps recursively walks v and zeros every map field it finds. Called
// before json.Unmarshal in restoreConfig because Unmarshal into a non-nil map
// merges rather than replaces — so a fn that added a map key would leave that
// key present after rollback if the map were not cleared first.
func clearMaps(v reflect.Value) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if !t.Field(i).IsExported() {
				continue
			}
			clearMaps(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			clearMaps(v.Index(i))
		}
	case reflect.Map:
		// Replace the map with a fresh empty one of the same type.
		// Unmarshal will populate it from the snapshot.
		v.Set(reflect.MakeMap(v.Type()))
	}
}

// restoreConfig clears all map fields on cfg and then unmarshals snapshotJSON
// into it. Clearing maps first ensures that map entries added by fn are fully
// removed rather than leaving orphaned keys (stdlib json.Unmarshal into a
// non-nil map merges, not replaces). Returns an error if unmarshal fails so
// callers can surface the divergence rather than silently serving corrupt state.
func restoreConfig(cfg *config.Config, snapshotJSON []byte) error {
	clearMaps(reflect.ValueOf(cfg))
	if err := json.Unmarshal(snapshotJSON, cfg); err != nil {
		slog.Error("sysagent: restoreConfig failed — config in-memory may diverge from snapshot",
			"error", err, "snapshot_bytes", len(snapshotJSON))
		return fmt.Errorf("restore config from snapshot: %w", err)
	}
	return nil
}

// WithConfig acquires the agent loop write lock via MutateConfig, takes a
// full-config snapshot, calls fn to apply the mutation, persists via
// SaveConfigLocked (or SaveConfig as fallback), and rolls back the entire
// config on either fn error or save error.
//
// Serialization: MutateConfig holds al.mu (write lock) for the duration of
// the call. REST readers acquire al.mu as RLock via AgentLoop.GetConfig, so
// reads and writes are never concurrent — eliminating the data race on
// cfg.Agents.List reported in Blocker 1.
//
// Rollback: a JSON-encoded snapshot is taken before fn runs. On failure,
// clearMaps zeroes all map fields before Unmarshal so that map entries added
// by fn are fully removed rather than leaving orphaned keys.
//
// Use this for all sysagent tool paths that mutate any part of the config.
func (d *Deps) WithConfig(fn func(*config.Config) error) error {
	return d.MutateConfig(func(cfg *config.Config) error {
		// Snapshot the JSON-serializable fields before mutation so any mutation
		// can be rolled back without copying the sync.RWMutex field.
		var snapshotBuf bytes.Buffer
		if err := json.NewEncoder(&snapshotBuf).Encode(cfg); err != nil {
			return fmt.Errorf("WithConfig: failed to snapshot config: %w", err)
		}
		snapshotJSON := snapshotBuf.Bytes()

		if fnErr := fn(cfg); fnErr != nil {
			// Roll back in-memory state to pre-mutation snapshot.
			if restoreErr := restoreConfig(cfg, snapshotJSON); restoreErr != nil {
				return fmt.Errorf("fn error: %w; also: restore failed: %w", fnErr, restoreErr)
			}
			return fnErr
		}

		// Persist. The caller MUST have wired SaveConfigLocked (the
		// gateway does this at boot; tests must wire it explicitly).
		// Returning an error here surfaces the misconfiguration loudly
		// rather than silently no-op'ing the write.
		if d.SaveConfigLocked == nil {
			return fmt.Errorf("sysagent: SaveConfigLocked not wired on Deps — gateway must call WithDeps() at boot")
		}
		saveErr := d.SaveConfigLocked(cfg)
		if saveErr != nil {
			// Roll back in-memory state on disk write failure.
			if restoreErr := restoreConfig(cfg, snapshotJSON); restoreErr != nil {
				return fmt.Errorf("save error: %w; also: restore failed: %w", saveErr, restoreErr)
			}
			return saveErr
		}
		return nil
	})
}

// validateID rejects entity IDs containing path separators, "..", or null bytes
// to prevent path traversal attacks on the entity storage.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid id: %q", id)
	}
	return nil
}

// writeEntity atomically writes a per-entity JSON file to dir/<id>.json.
// Uses an advisory flock as defense-in-depth for multi-process scenarios.
func writeEntity(dir, id string, v any) error {
	if err := validateID(id); err != nil {
		return fmt.Errorf("write entity: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entity %s: %w", id, err)
	}
	path := entityPath(dir, id)
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// deleteEntity removes dir/<id>.json.
// A missing file is treated as success (idempotent delete).
//
// M5: the removal is performed under the SAME per-path advisory flock that
// writeEntity holds (fileutil.WithFlock). Without it the delete — and any
// cascade that calls deleteEntity (project/workspace delete sweeping its
// tasks and pins) — races a concurrent writeEntity on the same file: the
// writer's temp-file + rename could land after os.Remove and silently
// resurrect a just-deleted entity, or the reader observes a half-written
// file. Sharing the lock domain serializes delete against write per entity.
func deleteEntity(dir, id string) error {
	if err := validateID(id); err != nil {
		return fmt.Errorf("delete entity: %w", err)
	}
	path := entityPath(dir, id)
	// Fast idempotent path: if the file (or its directory) does not exist there
	// is nothing to delete and nothing to race. Skipping the flock here also
	// avoids fileutil.WithFlock's O_CREATE re-creating a lock file inside a
	// missing/just-removed directory (which would both fail to open AND
	// resurrect an empty entity).
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	// The file exists: acquire the SAME per-path advisory flock writeEntity
	// holds, then remove under it. This serializes delete against concurrent
	// writeEntity so a writer's temp-file + rename cannot land after os.Remove
	// and resurrect the entity. The os.IsNotExist re-check inside the lock keeps
	// the delete idempotent if another locked deleter removed the file first.
	return fileutil.WithFlock(path, func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete entity %s: %w", id, err)
		}
		return nil
	})
}

// listEntities reads all JSON files in dir and unmarshals them into a slice of T.
func listEntities[T any](dir string) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	var result []T
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 6 || e.Name()[len(e.Name())-5:] != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(entityPath(dir, id))
		if err != nil {
			slog.Warn("sysagent: skipping unreadable entity file", "file", e.Name(), "error", err)
			continue
		}
		var v T
		if err := json.Unmarshal(data, &v); err != nil {
			slog.Warn("sysagent: skipping corrupt entity file", "file", e.Name(), "error", err)
			continue
		}
		result = append(result, v)
	}
	return result, nil
}

func entityPath(dir, id string) string {
	return dir + "/" + id + ".json"
}

// nowISO returns the current UTC time as an ISO 8601 string.
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// successJSON marshals v and returns it as a string for ForLLM.
func successJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return `{"success":true}`
	}
	return string(b)
}

// stringSliceArg coerces a JSON-decoded tool argument into a []string.
// JSON arrays decode to []any; non-string elements are skipped. Returns nil
// for nil/empty/non-array inputs so callers can leave the field unset.
func stringSliceArg(v any) []string {
	raw, ok := v.([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringMapArg coerces a JSON-decoded tool argument into a map[string]string.
// JSON objects decode to map[string]any; non-string values are skipped. Returns
// nil for nil/empty/non-object inputs so callers can leave the field unset.
func stringMapArg(v any) map[string]string {
	raw, ok := v.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, e := range raw {
		if s, ok := e.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// errorJSON returns a consistent error response per D.10.1.
func errorJSON(code, message, suggestion string) string {
	b, err := json.MarshalIndent(map[string]any{
		"success": false,
		"error": map[string]any{
			"code":       code,
			"message":    message,
			"suggestion": suggestion,
		},
	}, "", "  ")
	if err != nil {
		return `{"success":false,"error":{"code":"` + code + `","message":"` + message + `"}}`
	}
	return string(b)
}
