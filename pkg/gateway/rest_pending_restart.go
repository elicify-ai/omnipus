//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strings"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// RestartGatedKeys is the authoritative list of config keys that require a
// process restart to take effect. Hot-reload keys (prompt_injection_level,
// rate_limits.*, ssrf.*, tool_policies.*, default_tool_policy) are
// deliberately excluded — changing them is picked up on the next request
// without a restart.
//
// Exported so tests and future refactors can reference the canonical list
// without duplicating it.
var RestartGatedKeys = []config.ConfigKey{
	config.SandboxModeKey,
	config.SandboxAuditLog,
	config.SandboxAllowedPaths,
	config.SessionDMScope,
	config.GatewayPort,
	config.GatewayUsers,
	// Preview listener fields require restart because changing a listener's bind
	// address or port mid-process races with active connections (FR-027b, MR-02).
	config.GatewayPreviewPort,
	config.GatewayPreviewHost,
	config.GatewayPreviewOrigin,
	config.GatewayPublicURL,
	config.GatewayPreviewListenerEnabled,
	config.ToolsWebServeWarmup,
}

// pendingRestartEntry is an alias for the generated type — same dotted key
// structure, same wire shape, prevents the local type from diverging.
type pendingRestartEntry = gen.PendingRestartEntry

// HandlePendingRestart handles GET /api/v1/config/pending-restart.
//
// Returns a JSON array of restart-required changes: config keys whose
// persisted (disk) value differs from the boot-time applied value. The diff
// is computed over RestartGatedKeys only — hot-reload keys are never included.
//
// A set-then-revert scenario (admin writes X→Y then Y→X before restart)
// correctly produces an empty array, clearing the UI banner without a restart.
//
// Admin-only: non-admin callers receive 403.
func (a *restAPI) HandlePendingRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to read persisted config")
		return
	}

	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to parse persisted config")
		return
	}

	var applied map[string]any
	if a.appliedConfig != nil {
		appliedRaw, marshalErr := json.Marshal(a.appliedConfig)
		if marshalErr != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to serialize applied config")
			return
		}
		if err := json.Unmarshal(appliedRaw, &applied); err != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to parse applied config")
			return
		}
	}

	diffs := make([]pendingRestartEntry, 0)
	for _, key := range RestartGatedKeys {
		pv := getAtPath(persisted, string(key))
		av := getAtPath(applied, string(key))
		if key == config.GatewayUsers {
			pv = normalizeUsersForDiff(pv)
			av = normalizeUsersForDiff(av)
		}
		if !reflect.DeepEqual(pv, av) {
			diffs = append(diffs, pendingRestartEntry{
				Key:            string(key),
				PersistedValue: pv,
				AppliedValue:   av,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(diffs); err != nil {
		// Header already written; can only log.
		slog.Error("pending-restart: encode failed", "error", err)
	}
}

// deepCopyConfig returns a deep copy of cfg via JSON round-trip. It is called
// exactly once at boot to produce the appliedConfig snapshot; the original cfg
// may be mutated by hot-reload afterward without affecting the snapshot.
// Returns (nil, nil) when cfg is nil. Returns a non-nil error when the
// JSON round-trip fails — callers must abort boot on error, otherwise the
// pending-restart diff compares persisted config against an empty map and
// incorrectly reports every gated key as pending.
func deepCopyConfig(cfg *config.Config) (*config.Config, error) {
	if cfg == nil {
		return nil, nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("pending-restart: failed to marshal boot config: %w", err)
	}
	var snapshot config.Config
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("pending-restart: failed to unmarshal boot config snapshot: %w", err)
	}
	return &snapshot, nil
}

// mustDeepCopyConfig is the boot-time wrapper for deepCopyConfig. It panics
// when the JSON round-trip fails, aborting boot. This is intentional: a
// corrupted appliedConfig snapshot would cause every restart-gated key to
// appear pending immediately after boot, which is misleading and would
// prevent the admin from ever clearing the restart banner.
func mustDeepCopyConfig(cfg *config.Config) *config.Config {
	snap, err := deepCopyConfig(cfg)
	if err != nil {
		// Panic here causes cmd/omnipus/main.go's recovery to write the
		// error to gateway_panic.log and exit non-zero.
		panic(fmt.Sprintf("pending-restart: boot snapshot failed: %v", err))
	}
	return snap
}

// normalizeUsersForDiff strips rotating credential fields from each user
// record so the pending-restart diff isn't triggered by routine session
// activity. session_token_hash and token_hash are re-issued on every login
// and persisted immediately, while applied_value reflects the boot snapshot
// — without this normalization, the banner fires on first login and never
// clears. Adding/removing a user, changing username, or changing role still
// produces a diff, since those are the structural changes that genuinely
// require a restart. password_hash is also stripped: a password change is
// audited via emitUserAudit and takes effect immediately on the next login,
// it is not a restart-gated event.
func normalizeUsersForDiff(v any) any {
	users, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(users))
	for _, u := range users {
		m, ok := u.(map[string]any)
		if !ok {
			out = append(out, u)
			continue
		}
		stripped := make(map[string]any, len(m))
		for k, val := range m {
			switch k {
			case "session_token_hash", "token_hash", "password_hash":
				continue
			}
			stripped[k] = val
		}
		out = append(out, stripped)
	}
	return out
}

// getAtPath extracts a value from a nested map[string]any using a dotted path
// such as "sandbox.mode" or "gateway.port". Returns nil when any path segment
// is missing or a non-map intermediate value is encountered.
func getAtPath(m map[string]any, dotted string) any {
	segments := strings.SplitN(dotted, ".", 2)
	if len(segments) == 0 {
		return nil
	}
	val, ok := m[segments[0]]
	if !ok {
		return nil
	}
	if len(segments) == 1 {
		return val
	}
	nested, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	return getAtPath(nested, segments[1])
}
