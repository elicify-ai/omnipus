// Issue #638 (gate handoff: CHECK surviving-mutant + pr-test-analyzer F1):
// ResolveServerHeaderRefs is the connect-time half of the MCP header-secret
// fix — addMCPServer/patchMCPServer store the Authorization/Cookie/
// Proxy-Authorization values in the encrypted credential store and persist
// only HeaderRefs into config.json, so NOTHING works at connect time unless
// these refs are resolved back into Headers in memory.
//
// This file pins the resolution contract at the unit level. Oracles are the
// function's own documented contract (pkg/mcp/manager.go::ResolveServerHeaderRefs
// doc comment and issue #638) and the established ResolveServerEnvRefs
// semantics it mirrors — never observed output:
//
//   - empty HeaderRefs is a no-op even with a nil resolver (back-compat for
//     literal-header servers added before the fix)
//   - non-empty HeaderRefs with a nil resolver is an ERROR (fail-closed):
//     connecting without the configured secret would surface as a baffling
//     remote 401/403, or a successful-looking connection to the wrong endpoint
//   - resolved refs merge into Headers and a ref OVERRIDES a same-named
//     literal (mirroring EnvRefs-over-Env), and the caller's config is not
//     mutated
//   - a resolver failure propagates wrapped, naming the header and the
//     credential-store key, so an operator can fix the store entry

package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestResolveServerHeaderRefs(t *testing.T) {
	t.Run("no HeaderRefs is a no-op even with a nil resolver (back-compat)", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			Enabled: true,
			Type:    "http",
			URL:     "https://127.0.0.1:1/mcp",
			Headers: map[string]string{"X-Literal": "unchanged"},
		}
		got, err := ResolveServerHeaderRefs(cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Headers["X-Literal"] != "unchanged" {
			t.Errorf("literal Headers must survive untouched, got: %+v", got.Headers)
		}
		if len(got.HeaderRefs) != 0 {
			t.Errorf("HeaderRefs = %+v, want untouched (empty)", got.HeaderRefs)
		}
	})

	t.Run("non-empty HeaderRefs with a nil resolver errors (fail-closed)", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			HeaderRefs: map[string]string{"Authorization": "mcp_srv_header_Authorization"},
		}
		_, err := ResolveServerHeaderRefs(cfg, nil)
		if err == nil {
			t.Fatal("expected error when HeaderRefs is non-empty and resolver is nil, got nil")
		}
		if !strings.Contains(err.Error(), "no credential resolver is configured") {
			t.Errorf("error = %q, want it to name the missing resolver", err.Error())
		}
		if !strings.Contains(err.Error(), "1 header credential reference") {
			t.Errorf("error = %q, want it to carry the ref count for diagnosis", err.Error())
		}
	})

	t.Run("resolves refs into Headers, overriding a same-named literal", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			Headers: map[string]string{
				"Keep":         "literal-value",
				"Authorization": "Bearer stale-literal",
			},
			HeaderRefs: map[string]string{
				"Authorization": "mcp_srv_header_Authorization",
				"Cookie":        "mcp_srv_header_Cookie",
			},
		}
		store := map[string]string{
			"mcp_srv_header_Authorization": "Bearer fresh-secret",
			"mcp_srv_header_Cookie":        "session=fresh-cookie",
		}
		resolve := func(refKey string) (string, error) {
			v, ok := store[refKey]
			if !ok {
				return "", fmt.Errorf("no such credential %q", refKey)
			}
			return v, nil
		}
		got, err := ResolveServerHeaderRefs(cfg, resolve)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Headers["Keep"] != "literal-value" {
			t.Errorf("Keep = %q, want the literal to survive the merge", got.Headers["Keep"])
		}
		if got.Headers["Authorization"] != "Bearer fresh-secret" {
			t.Errorf("Authorization = %q, want the ref value to override the stale literal", got.Headers["Authorization"])
		}
		if got.Headers["Cookie"] != "session=fresh-cookie" {
			t.Errorf("Cookie = %q, want the resolved ref value", got.Headers["Cookie"])
		}
		if len(got.Headers) != 3 {
			t.Errorf("merged Headers has %d entries (%+v), want exactly the 2 literals' union + 1 ref = 3", len(got.Headers), got.Headers)
		}
		// The caller's config must not be mutated — the merge happens on a
		// fresh map (an in-place write would leak the resolved secret back
		// into whatever struct the caller persists).
		if cfg.Headers["Authorization"] != "Bearer stale-literal" {
			t.Errorf("caller's cfg.Headers mutated: %+v", cfg.Headers)
		}
	})

	t.Run("a resolver failure is surfaced, naming the header and ref key", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			HeaderRefs: map[string]string{"Authorization": "mcp_srv_header_Authorization"},
		}
		resolve := func(string) (string, error) { return "", fmt.Errorf("credential store is locked") }
		_, err := ResolveServerHeaderRefs(cfg, resolve)
		if err == nil {
			t.Fatal("expected the resolver's error to propagate, got nil")
		}
		for _, want := range []string{"credential store is locked", "Authorization", "mcp_srv_header_Authorization"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), want)
			}
		}
	})
}
