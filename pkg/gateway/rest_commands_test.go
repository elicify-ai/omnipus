//go:build !cgo

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// newMinimalCommandsAPI builds a restAPI with no agentLoop.
// Tests inject a config into the request context directly to avoid the
// agentLoop.GetConfig() fallback in withAuth.
func newMinimalCommandsAPI(t *testing.T) *restAPI {
	t.Helper()
	return &restAPI{
		allowedOrigin: "http://localhost:3000",
	}
}

// injectBypassConfig injects a config with DevModeBypass=true into the request
// context, which bypasses bearer auth (no agentLoop needed).
func injectBypassConfig(r *http.Request) *http.Request {
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = true
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, cfg)
	return r.WithContext(ctx)
}

// injectAuthConfig injects a config with DevModeBypass=false and no users,
// meaning no auth is possible (requests without a bearer env var get 401).
func injectNoAuthConfig(r *http.Request) *http.Request {
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = false
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, cfg)
	return r.WithContext(ctx)
}

// doCommandsRequest sends a GET to /api/v1/commands?surface=<surface>,
// injecting auth config to avoid nil agentLoop panics. If authed=true, the
// request is treated as authenticated (bypass mode); if authed=false, bypass is
// off and no users are configured, so 401 results.
func doCommandsRequest(t *testing.T, api *restAPI, surface string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/commands"
	if surface != "" {
		url += "?surface=" + surface
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if authed {
		req = injectBypassConfig(req)
	} else {
		req = injectNoAuthConfig(req)
	}
	w := httptest.NewRecorder()
	api.withAuth(api.HandleListCommands)(w, req)
	return w
}

// parseCommandsResponse decodes the JSON body from the recorder into []gen.SlashCommand.
func parseCommandsResponse(t *testing.T, w *httptest.ResponseRecorder) []gen.SlashCommand {
	t.Helper()
	var cmds []gen.SlashCommand
	if err := json.Unmarshal(w.Body.Bytes(), &cmds); err != nil {
		t.Fatalf("failed to parse /commands response: %v\nbody: %s", err, w.Body.String())
	}
	return cmds
}

// TestHandleListCommands_Web verifies surface=web returns exactly 5 commands (SC-001, TDD #10).
func TestHandleListCommands_Web(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "web", true)

	if w.Code != http.StatusOK {
		t.Fatalf("web: status=%d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	cmds := parseCommandsResponse(t, w)
	if len(cmds) != 5 {
		t.Errorf("web: expected 5 commands, got %d: %v", len(cmds), commandNames(cmds))
	}

	wantNames := map[string]bool{"clear": true, "help": true, "model": true, "skill": true, "cancel": true}
	for _, c := range cmds {
		if !wantNames[c.Name] {
			t.Errorf("web: unexpected command %q", c.Name)
		}
		delete(wantNames, c.Name)
	}
	for missing := range wantNames {
		t.Errorf("web: missing command %q", missing)
	}
}

// TestHandleListCommands_CLI verifies surface=cli returns all 11 commands (SC-002, TDD #10).
func TestHandleListCommands_CLI(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "cli", true)

	if w.Code != http.StatusOK {
		t.Fatalf("cli: status=%d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	cmds := parseCommandsResponse(t, w)
	if len(cmds) != 11 {
		t.Errorf("cli: expected 11 commands, got %d: %v", len(cmds), commandNames(cmds))
	}

	allCanonical := []string{"clear", "help", "model", "skill", "cancel", "agents", "tasks", "skills", "channels", "status", "config"}
	nameSet := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		nameSet[c.Name] = true
	}
	for _, want := range allCanonical {
		if !nameSet[want] {
			t.Errorf("cli: missing command %q in response", want)
		}
	}
}

// TestHandleListCommands_Channel verifies surface=channel returns all 11 commands (SC-002, TDD #10).
func TestHandleListCommands_Channel(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "channel", true)

	if w.Code != http.StatusOK {
		t.Fatalf("channel: status=%d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	cmds := parseCommandsResponse(t, w)
	if len(cmds) != 11 {
		t.Errorf("channel: expected 11 commands, got %d: %v", len(cmds), commandNames(cmds))
	}
}

// TestHandleListCommands_DefaultSurface verifies no surface param defaults to web (US-3/AS-3, FR-015).
func TestHandleListCommands_DefaultSurface(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	// No surface param.
	w := doCommandsRequest(t, api, "", true)

	if w.Code != http.StatusOK {
		t.Fatalf("default: status=%d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	cmds := parseCommandsResponse(t, w)
	if len(cmds) != 5 {
		t.Errorf("default surface (web): expected 5 commands, got %d: %v", len(cmds), commandNames(cmds))
	}
}

// TestHandleListCommands_UnknownSurface verifies unknown surface defaults to web (FR-015, lenient).
func TestHandleListCommands_UnknownSurface(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "bogus", true)

	if w.Code != http.StatusOK {
		t.Fatalf("bogus surface: status=%d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	cmds := parseCommandsResponse(t, w)
	if len(cmds) != 5 {
		t.Errorf("unknown surface should default to web (5 cmds), got %d: %v", len(cmds), commandNames(cmds))
	}
}

// TestHandleListCommands_Unauth verifies 401 when no bypass and no bearer token (US-3/AS-6, TDD #10).
func TestHandleListCommands_Unauth(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	// authed=false: config injected with DevModeBypass=false, no users configured.
	// The OMNIPUS_BEARER_TOKEN env var is unset in this test, so 401 results.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	w := doCommandsRequest(t, api, "web", false)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauth: status=%d, want 401\nbody: %s", w.Code, w.Body.String())
	}
}

// TestHandleListCommands_MethodNotAllowed verifies POST returns 405.
func TestHandleListCommands_MethodNotAllowed(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands", nil)
	req = injectBypassConfig(req)
	w := httptest.NewRecorder()
	api.withAuth(api.HandleListCommands)(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status=%d, want 405\nbody: %s", w.Code, w.Body.String())
	}
}

// TestHandleListCommands_DeliveryFields verifies delivery field values (FR-007, US-3/AS-5, TDD #10).
func TestHandleListCommands_DeliveryFields(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "web", true)
	cmds := parseCommandsResponse(t, w)

	clientCmds := map[string]bool{"clear": true, "help": true, "model": true, "cancel": true}
	agentCmds := map[string]bool{"skill": true}

	for _, c := range cmds {
		if clientCmds[c.Name] {
			if c.Delivery != gen.SlashCommandDeliveryClient {
				t.Errorf("%q: Delivery=%q, want %q", c.Name, c.Delivery, gen.SlashCommandDeliveryClient)
			}
		} else if agentCmds[c.Name] {
			if c.Delivery != gen.SlashCommandDeliveryAgent {
				t.Errorf("%q: Delivery=%q, want %q", c.Name, c.Delivery, gen.SlashCommandDeliveryAgent)
			}
		}
	}
}

// TestHandleListCommands_AliasesNotEntries verifies alias names do not appear as separate entries (US-3/AS-4).
func TestHandleListCommands_AliasesNotEntries(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "cli", true)
	cmds := parseCommandsResponse(t, w)

	aliasNames := map[string]bool{
		"subagents": true,
		"use":       true,
		"reload":    true,
		"show":      true,
		"list":      true,
		"switch":    true,
		"check":     true,
		"start":     true,
	}
	for _, c := range cmds {
		if aliasNames[c.Name] {
			t.Errorf("alias/deprecated %q must not appear as a standalone entry", c.Name)
		}
	}
}

// TestHandleListCommands_CancelAvailableWhileStreaming verifies the AvailableWhileStreaming field.
func TestHandleListCommands_CancelAvailableWhileStreaming(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "web", true)
	cmds := parseCommandsResponse(t, w)

	for _, c := range cmds {
		if c.Name == "cancel" {
			if c.AvailableWhileStreaming == nil || !*c.AvailableWhileStreaming {
				t.Errorf("cancel: AvailableWhileStreaming must be true, got %v", c.AvailableWhileStreaming)
			}
		} else {
			if c.AvailableWhileStreaming != nil && *c.AvailableWhileStreaming {
				t.Errorf("%q: AvailableWhileStreaming must be nil/false for non-cancel commands", c.Name)
			}
		}
	}
}

// TestDefToSlashCommand_Mapper tests the mapper function directly (TDD #7).
func TestDefToSlashCommand_Mapper(t *testing.T) {
	def := commands.Definition{
		Name:        "skill",
		Description: "Force a specific installed skill for one request",
		Usage:       "/skill <name> [message]",
		Aliases:     []string{"use"},
		Surfaces:    []commands.Surface{commands.SurfaceWeb, commands.SurfaceCLI, commands.SurfaceChannel},
		Delivery:    commands.DeliveryAgent,
	}

	sc := defToSlashCommand(def)

	if sc.Name != "skill" {
		t.Errorf("Name=%q, want %q", sc.Name, "skill")
	}
	if sc.Label != "/skill" {
		t.Errorf("Label=%q, want %q", sc.Label, "/skill")
	}
	if sc.Description != def.Description {
		t.Errorf("Description mismatch")
	}
	if sc.Delivery != gen.SlashCommandDeliveryAgent {
		t.Errorf("Delivery=%q, want %q", sc.Delivery, gen.SlashCommandDeliveryAgent)
	}
	if sc.Aliases == nil || len(*sc.Aliases) != 1 || (*sc.Aliases)[0] != "use" {
		t.Errorf("Aliases=%v, want [use]", sc.Aliases)
	}
	if sc.Usage == nil || *sc.Usage != "/skill <name> [message]" {
		t.Errorf("Usage=%v, want /skill <name> [message]", sc.Usage)
	}
	// Not cancel, so AvailableWhileStreaming should be nil.
	if sc.AvailableWhileStreaming != nil {
		t.Errorf("AvailableWhileStreaming should be nil for non-cancel")
	}
}

// TestParseSurface tests the query parameter parser.
func TestParseSurface(t *testing.T) {
	cases := []struct {
		param string
		want  commands.Surface
	}{
		{"web", commands.SurfaceWeb},
		{"cli", commands.SurfaceCLI},
		{"channel", commands.SurfaceChannel},
		{"", commands.SurfaceWeb},      // absent → web
		{"bogus", commands.SurfaceWeb}, // unknown → web
	}
	for _, tc := range cases {
		got := parseSurface(tc.param)
		if got != tc.want {
			t.Errorf("parseSurface(%q)=%v, want %v", tc.param, got, tc.want)
		}
	}
}

// commandNames extracts just the name slice for error messages.
func commandNames(cmds []gen.SlashCommand) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name
	}
	return names
}
