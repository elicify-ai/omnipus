// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agent"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// HandleBrowserInspect implements POST /api/v1/browser/inspect (ADR-039
// D-B3): resolve the DOM element at a device-pixel point on an agent's live
// browser tab, so the SPA can attach the element's text/HTML as extra
// context when a user annotates a spot in the Live Browser panel.
//
// New, disjoint file (ADR-039 wave BE-2) — deliberately does not touch
// browser_ws.go or live.go, which the sibling navigate+SSRF work (BE-1)
// owns; the only shared surface is BrowserManager.InspectPoint
// (pkg/tools/browser/inspect.go, also a new file) and
// AgentLoop.BrowserManagerForAgent, both pre-existing/new read-only entry
// points.
//
// Best-effort by design, mirroring the ADR: every outcome short of a genuine
// request-validation failure (empty agent_id/session_id, negative
// coordinates, malformed JSON) is reported as a 200 OK with ok=false +
// reason — never a 404/500 — because this endpoint is purely additive to the
// annotate-and-discuss flow (D-B1/B2's cropped-image path still delivers the
// feature on its own). Gated behind authentication (withAuth, wired in
// registerAdditionalEndpoints) and, like the live-view WS
// (pkg/gateway/browser_ws.go), behind tools.browser.live_view_enabled so an
// operator who disabled the live browser feature entirely can't use this
// sibling surface to probe a tab anyway.
func (a *restAPI) HandleBrowserInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.agentLoop.GetConfig()
	if !cfg.Tools.Browser.LiveViewEnabled {
		jsonErr(w, http.StatusForbidden, "live browser view is disabled (tools.browser.live_view_enabled=false)")
		return
	}

	var req gen.BrowserInspectRequest
	if !decodeAndValidate(w, r, "BrowserInspectRequest", &req, cfg.Gateway.ValidateInbound) {
		return
	}

	// Defense-in-depth: contracts/components/schemas/BrowserInspectRequest.yaml
	// already declares session_id/agent_id minLength:1 and x/y minimum:0, but
	// that schema is only enforced above when gateway.validate_inbound=true
	// (default false — see decodeAndValidate's doc comment). This endpoint's
	// own request-shape validation must hold unconditionally, so it is
	// re-checked here regardless of that operator setting.
	if req.AgentId == "" {
		jsonErr(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if req.SessionId == "" {
		jsonErr(w, http.StatusBadRequest, "session_id is required")
		return
	}
	if req.X < 0 || req.Y < 0 {
		jsonErr(w, http.StatusBadRequest, "x and y must be >= 0")
		return
	}

	mgr, outcome := a.agentLoop.BrowserManagerForAgent(r.Context(), req.AgentId, "")
	if outcome != agent.BrowserResolveOK {
		reason := browserResolveReason(outcome, req.AgentId)
		jsonOK(w, gen.BrowserInspectResponse{Ok: false, Reason: &reason})
		return
	}

	result, err := mgr.InspectPoint(float64(req.X), float64(req.Y))
	if err != nil {
		// InspectPoint reserves a non-nil error for a genuine infrastructure
		// failure (couldn't even resolve a tab session) — worth a log line,
		// but still reported to the caller as a soft ok:false per the ADR's
		// best-effort contract, not a 5xx.
		slog.Warn("browser inspect: InspectPoint failed",
			"error", err, "agent_id", req.AgentId, "session_id", req.SessionId)
		reason := fmt.Sprintf("inspect failed: %s", err)
		jsonOK(w, gen.BrowserInspectResponse{Ok: false, Reason: &reason})
		return
	}

	if !result.Ok {
		reason := "no element could be resolved at that point"
		jsonOK(w, gen.BrowserInspectResponse{Ok: false, Reason: &reason})
		return
	}

	jsonOK(w, gen.BrowserInspectResponse{
		Ok:   true,
		Tag:  &result.Tag,
		Text: &result.Text,
		Html: &result.Html,
	})
}
