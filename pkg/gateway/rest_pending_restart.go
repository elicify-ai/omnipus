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
	"reflect"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// RestartGatedKeys is the authoritative list of config keys that require a
// process restart to take effect. Hot-reload keys (prompt_injection_level,
// rate_limits.*, ssrf.*, tool_policies.*, default_tool_policy) are
// deliberately excluded — changing them is picked up on the next request
// without a restart.
//
// FR-105/FR-106: gateway.users is intentionally excluded. Auth reads
// GetConfig() live on every request (configSnapshotMiddleware, auth.go:133),
// and HandleCompleteOnboarding call refreshConfigAndRewireServices
// immediately after writing config.json — so user additions are hot and never
// require a restart. Including gateway.users caused a spurious restart banner
// on every fresh install (US-3 / SC-104).
//
// Exported so tests and future refactors can reference the canonical list
// without duplicating it.
var RestartGatedKeys = []config.ConfigKey{
	config.SandboxModeKey,
	config.SandboxAuditLog,
	config.SandboxAllowedPaths,
	// O14: sandbox.god_mode / god_mode_allowed are DELIBERATELY NOT listed
	// here, even though a config-only enable needs a restart to take effect.
	// They are the only restart-gated keys that can ALSO apply live (any boot
	// where god mode was already available — legacy --allow-god-mode, or any
	// boot after the first UI grant), and this endpoint diffs against a
	// boot-frozen appliedConfig snapshot that is never refreshed for a live
	// toggle. Listing them would make the generic "restart to apply" banner
	// falsely claim the kernel sandbox is inactive right after a live enable
	// (and the inverse after a live disable) — dangerous misinformation for
	// the highest-blast-radius switch. The dedicated GodModeControl surface
	// owns this signal accurately instead: the POST's restart_required flag
	// opens GatewayRestartModal, and the toggle's own note is availability-
	// driven. See rest_god_mode.go.
	config.SessionDMScope,
	// Changing the bind host re-binds the listener (like the port), which can
	// only happen safely on restart — so it is restart-gated like gateway.port.
	config.GatewayHost,
	config.GatewayPort,
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
// Gated by adminWrap (withAuth → RequireNotBypass); dev_mode_bypass returns 503.
func (a *restAPI) HandlePendingRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Load the on-disk config through the SAME path boot used to produce
	// appliedConfig, so restart-gated fields that get a computed default at boot
	// but are absent from config.json on disk do NOT surface as phantom diffs on
	// a clean install (regression: session.dm_scope, gateway.preview_host,
	// gateway.preview_port, tools.run_in_workspace.warmup_timeout_seconds).
	//
	// LoadConfig starts from DefaultConfig() (which seeds Session.DMScope =
	// "per-channel-peer") and unmarshals the JSON over it with omitempty tags, so
	// the dm_scope default is already applied here exactly as at boot. The
	// gateway-specific normalization (preview defaults, warmup timeout) is applied
	// separately at boot (gateway.go ~1248-1252) and is mirrored below.
	//
	// LoadConfig (not LoadConfigWithStore) is correct: every RestartGatedKey is a
	// non-secret field. Post-boot the on-disk config is already at CurrentVersion,
	// so LoadConfig takes the `case CurrentVersion` branch and does NOT defer a
	// migration SaveConfig (only the legacy `case 0` path does) — reading is
	// side-effect-free.
	persistedCfg, err := config.LoadConfig(a.configPath())
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to read persisted config")
		return
	}
	// Mirror the boot-time gateway normalization that produced appliedConfig.
	if err = persistedCfg.Gateway.ValidateAndApplyPreviewDefaults(); err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to normalize persisted config")
		return
	}
	persistedCfg.Tools.ApplyWarmupTimeoutDefault()

	persistedRaw, err := json.Marshal(persistedCfg)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to serialize persisted config")
		return
	}
	var persisted map[string]any
	if err := json.Unmarshal(persistedRaw, &persisted); err != nil {
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
