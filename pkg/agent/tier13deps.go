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

	// (The former GatewayPreviewBaseURL field was removed by ADR-044: /preview/
	// is served on the main gateway listener and WebServeTool now derives its
	// URL live via a getConfig accessor — see tools.NewWebServeTool and
	// loop.go's wireTier13DepsLocked, which passes al.GetConfig directly.)
}
