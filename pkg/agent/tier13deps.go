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
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
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

	// GatewayBaseURL is the base URL (scheme + host + port) of the running
	// gateway's MAIN listener, e.g. "http://localhost:5000".
	//
	// Deprecated: kept for one release for replay safety on transcripts that
	// embedded URLs minted before the two-port topology landed (FR-021). Use
	// GatewayPreviewBaseURL for new tool URL emission. Will be removed after
	// 2026-Q3.
	GatewayBaseURL string

	// GatewayPreviewBaseURL is the base URL of the gateway's PREVIEW
	// listener, e.g. "http://localhost:3001" or "https://preview.acme.com".
	// Sourced from cfg.Gateway.PreviewOrigin when set, otherwise computed
	// from cfg.Gateway.Host + cfg.Gateway.PreviewPort at boot.
	//
	// web_serve and workspace.shell_bg use this to build absolute preview
	// URLs returned in tool results — web_serve emits /preview/<agent>/<token>/,
	// workspace.shell_bg emits /dev/<agent>/<token>/. The preview origin is
	// browser-cross-origin to the SPA's main origin, providing the T-01
	// mitigation (parent.localStorage access throws SecurityError).
	GatewayPreviewBaseURL string
}
