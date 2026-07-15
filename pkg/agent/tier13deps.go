// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — Tier13Deps carrier for web_serve (static + dev modes),
// build_static, and related tool wiring.
//
// Tier13Deps bundles the shared infrastructure instances that are created once
// at gateway boot and passed down into every NewAgentInstance call. Keeping
// them in a single struct avoids threading six extra parameters through the
// existing function call chain.

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// Tier13Deps carries the shared singletons required to register the Tier 1
// (web_serve static mode), Tier 2 (build_static), and Tier 3 (web_serve dev
// mode) tools for non-system agents.
//
// All fields are nullable — a nil registry / proxy means the corresponding
// tool is not registered (graceful degradation when the gateway skips Tier
// 2/3 setup, e.g. in unit tests that only need Tier 1).
type Tier13Deps struct {
	// ServedSubdirs is the process-wide web_serve static-mode registration map.
	// Non-nil when the gateway has initialized it at boot.
	ServedSubdirs *ServedSubdirs

	// EgressProxy is the shared Tier 2 / Tier 3 egress HTTP/HTTPS proxy.
	// Non-nil when sandbox.NewEgressProxy succeeded at boot.
	EgressProxy *sandbox.EgressProxy

	// DevServerRegistry is the process-wide web_serve dev-mode registration map.
	// Non-nil when the gateway has initialized it at boot.
	DevServerRegistry *sandbox.DevServerRegistry

	// GatewayPreviewBaseURL is REMOVED (preview-on-main-listener v5, ADR-044
	// D2, FR-003/FR-005). There is no more separate preview listener and no
	// more boot-frozen preview base URL: /preview/ is served on the SAME main
	// gateway listener as everything else, and WebServeTool now takes a live
	// `getConfig func() *config.Config` accessor (see tools.NewWebServeTool)
	// so it builds its URL from middleware.CanonicalGatewayOrigin(cfg) fresh
	// on every call and reads gateway.preview_enabled live — see
	// pkg/agent/loop.go's wireTier13DepsLocked, which now passes al.GetConfig
	// directly instead of reading a field off this struct.
	//
	// bash's background-session mode ("bash" — ADR-036 unified the retired
	// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it) never used
	// this field: the equivalent port-exposure/preview-URL capability was
	// dropped, not ported, when workspace_shell_bg was merged (ADR-036 §3.1)
	// — bash's background mode is a plain run-in-background + poll/kill
	// capability with no preview URL.
}
