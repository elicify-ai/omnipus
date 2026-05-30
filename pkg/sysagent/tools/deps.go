// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package systools implements the 35 exclusive system.* tools for the
// Omnipus system agent per BRD Appendix D §D.4.
package systools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// Deps bundles all shared dependencies for system tools.
//
// FR-050 / C1 atomic-pointer contract
//
// The reload path in pkg/agent/loop.go::ReloadProviderAndConfig satisfies FR-050
// via the "rebuild" pattern rather than the "atomic swap" pattern:
//
//  1. ReloadProviderAndConfig calls wireSysagentDepsLocked(newRegistry, deps).
//  2. wireSysagentDepsLocked calls AllTools(deps, nil) which constructs fresh tool
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
	// SaveConfig persists the current config to ConfigPath.
	//
	// Deprecated: use SaveConfigLocked when called from within WithConfig.
	// SaveConfig is kept for backward compatibility with existing tests that
	// wire it directly. When both are set, WithConfig uses SaveConfigLocked.
	SaveConfig func() error
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
	// nil (WithConfig falls back to SaveConfig in that case).
	SaveConfigLocked func(cfg *config.Config) error
	// CredStore is the encrypted credential store.
	CredStore *credentials.Store
	// ReloadFunc triggers a hot-reload of the agent loop so newly created agents
	// become available immediately. Nil in tests or when not wired.
	ReloadFunc func() error
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
				return fmt.Errorf("fn error: %w; also: restore failed: %v", fnErr, restoreErr)
			}
			return fnErr
		}

		// Persist. SaveConfigLocked is preferred (no mutex acquired — caller
		// already holds al.mu). Fall back to SaveConfig for tests that only
		// wire the legacy field.
		var saveErr error
		if d.SaveConfigLocked != nil {
			saveErr = d.SaveConfigLocked(cfg)
		} else if d.SaveConfig != nil {
			saveErr = d.SaveConfig()
		}
		if saveErr != nil {
			// Roll back in-memory state on disk write failure.
			if restoreErr := restoreConfig(cfg, snapshotJSON); restoreErr != nil {
				return fmt.Errorf("save error: %w; also: restore failed: %v", saveErr, restoreErr)
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

// readEntity reads a per-entity JSON file from dir/<id>.json.
func readEntity(dir, id string, v any) error {
	if err := validateID(id); err != nil {
		return fmt.Errorf("read entity: %w", err)
	}
	path := entityPath(dir, id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("NOT_FOUND: %s", id)
		}
		return fmt.Errorf("read entity %s: %w", id, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse entity %s: %w", id, err)
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
func deleteEntity(dir, id string) error {
	if err := validateID(id); err != nil {
		return fmt.Errorf("delete entity: %w", err)
	}
	path := entityPath(dir, id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete entity %s: %w", id, err)
	}
	return nil
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
