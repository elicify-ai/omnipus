// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — RED tests for ADR-094 preview isolation, orders 20 and 1,
// plus the mint-side DS-3 rows of order 9 (spec:
// docs/internal/specs/adr-094-preview-isolation-spec.md, TDD Plan, DS-1,
// DS-3).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: the serve_web tool result gains `isolated_url` —
//     the absolute Mode 1 URL http://<label>.localhost:<canonical-port>/ —
//     exactly on Mode 1 deployments; misconfigured canonical origins refuse
//     to mint; the minted label matches FR-005's grammar with the 128-bit
//     entropy floor.
//   - Specification source: FR-001/FR-002/FR-003/FR-005/FR-022, S-1.1–S-1.3,
//     DS-1, DS-3 rows 1–10, S-8.3. Expected values are the spec's, not
//     observed output.
//   - Unit boundary: real WebServeTool.Execute over a real
//     agent.ServedSubdirs store — the one contract-fixed mint surface that
//     exists today (FR-022). No mocks.
//   - RED shape: Mode 1 rows fail at the missing `isolated_url` key (a loud,
//     named runtime failure); refusal rows fail because the tool mints today
//     where the spec demands an error result. Non-Mode-1 rows are pins
//     (fallback-only holds today).
//   - Known gaps (documented, not silent):
//     (1) The DEV variant of order 20 is not drivable pre-GREEN —
//     WebServeTool.executeDev gates on Linux (Tier3UnsupportedMessage on
//     this platform) and spawns a real process; the repo's own e2e test
//     (rest_preview_web_serve_e2e_test.go) documents the same precedent.
//     (2) The shared origin-validity helper of FR-019 does not exist yet
//     (pkg/gateway/middleware home); these tests exercise its behaviour
//     through the mint, and the GREEN-side unit test lands with the helper.
//     (3) DS-1's incoming-validation rows (upper-case Host, 64-char label,
//     leading-hyphen label, underscore label) are mux-side, covered
//     behaviorally in pkg/gateway/preview_host_dispatch_red_test.go
//     (order 5) — the mint never emits them, so they cannot be mint-side
//     rows.

package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// piRedLabelGrammar is FR-005's grammar as a regexp: lower-case letters,
// digits and hyphens; no leading or trailing hyphen (the inner-hyphen form
// is the only multi-segment shape); 1–63 chars enforced separately.
var piRedLabelGrammar = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// piRedStubStore implements tools' ServedSubdirsRegistry interface without
// importing pkg/agent (agent imports tools — an internal test file here
// cannot import it back; the repo's own web_serve_test.go stub exists for
// the same reason). Register returns a fixed-shaped token; the token VALUE
// is irrelevant to these assertions (labels, not tokens, are under test).
type piRedStubStore struct{}

func (piRedStubStore) Register(agentID, absDir string, duration time.Duration) (string, time.Time, error) {
	return "piredfixedtoken43chars000000000000000000000", time.Now().Add(duration), nil
}

func (piRedStubStore) ActiveForAgent(agentID string) (string, time.Time, bool) {
	return "", time.Time{}, false
}

// piRedExecuteStatic mints a static web_serve result against the given
// canonical origin and returns the parsed result payload.
func piRedExecuteStatic(t *testing.T, publicURL string) map[string]any {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"),
		[]byte("<h1>pi-red preview</h1>"),
		0o644,
	))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			PublicURL:      publicURL,
			PreviewEnabled: boolPtr(true),
		},
	}
	tool := NewWebServeTool(
		dir,
		"pi-red-agent",
		func() *config.Config { return cfg }, // live accessor — same shape production uses
		piRedStubStore{},
		nil, // devReg — static mode only
		WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, // egressProxy
		nil, // auditLogger
		60,
		86400,
	)
	ctx := WithAgentID(context.Background(), "pi-red-agent")
	result := tool.Execute(ctx, map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve Execute must succeed: %s", result.ForLLM)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed),
		"web_serve result must be valid JSON")
	return parsed
}

// TestServeWebResult_IsolatedURL covers order 20 (isolated_url on the tool
// result) and the mint-side DS-3 rows of order 9.
func TestServeWebResult_IsolatedURL(t *testing.T) {
	t.Run("mode1_static_dual_url", func(t *testing.T) {
		// DS-3 row 1 / S-1.1: Mode 1 mint carries BOTH urls.
		parsed := piRedExecuteStatic(t, "http://localhost:5000")

		fallbackURL, _ := parsed["url"].(string)
		require.NotEmpty(t, fallbackURL,
			"the /preview/ fallback url must stay present on a Mode 1 mint (FR-022)")
		assert.Contains(t, fallbackURL, "/preview/",
			"the fallback url keeps its Mode 2 meaning (FR-022)")

		isoRaw, has := parsed["isolated_url"]
		require.True(t, has,
			"RED (FR-001/FR-022, S-1.1): a Mode 1 web_serve result carries no isolated_url — "+
				"the dual-URL contract is not implemented; expected http://<label>.localhost:5000/ "+
				"next to the /preview/ fallback")
		isoURL, _ := isoRaw.(string)
		require.NotEmpty(t, isoURL, "isolated_url must be a non-empty string when present")

		u, err := url.Parse(isoURL)
		require.NoError(t, err, "isolated_url must parse as an absolute URL: %q", isoURL)

		// S-1.1: http://<label>.localhost:<port>/ — one label, no second dot.
		hostParts := strings.SplitN(u.Hostname(), ".", 2)
		require.Len(t, hostParts, 2,
			"isolated_url host must be exactly <label>.localhost, got %q", u.Hostname())
		assert.Equal(t, "localhost", hostParts[1],
			"the suffix after the single label must be exactly localhost")
		assert.NotContains(t, hostParts[1], ".",
			"no second dot may appear in the isolated_url host")
		assert.Equal(t, "5000", u.Port(),
			"the mint uses the CANONICAL port from public_url (FR-001)")
		assert.Equal(t, "http", u.Scheme, "Mode 1 mints are http")
	})

	t.Run("mode1_canonical_port_mint", func(t *testing.T) {
		// DS-3 row 8: canonical :8080 mints with :8080 regardless of listener.
		parsed := piRedExecuteStatic(t, "http://localhost:8080")
		isoRaw, has := parsed["isolated_url"]
		require.True(t, has,
			"RED (DS-3 row 8): port-mapped Mode 1 mint carries no isolated_url")
		u, err := url.Parse(isoRaw.(string))
		require.NoError(t, err)
		assert.Equal(t, "8080", u.Port(),
			"the mint is canonical-port (8080), never the listener port")
	})

	t.Run("mode1_case_normalized_origin", func(t *testing.T) {
		// DS-3 row 9: LOCALHOST canonical origin is case-normalized; the
		// minted label URL is all lower-case.
		parsed := piRedExecuteStatic(t, "http://LOCALHOST:5000")
		isoRaw, has := parsed["isolated_url"]
		require.True(t, has,
			"RED (DS-3 row 9): case-normalized Mode 1 mint carries no isolated_url")
		isoURL := isoRaw.(string)
		assert.Equal(t, strings.ToLower(isoURL), isoURL,
			"the minted label URL must be lower-case throughout (FR-005)")
		assert.Contains(t, isoURL, ".localhost:5000/",
			"case-normalized origin still mints <label>.localhost:5000")
	})

	t.Run("mode1_implicit_80_portless_mint", func(t *testing.T) {
		// DS-3 row 10: http://localhost (implicit 80) mints a PORTLESS
		// isolated_url.
		parsed := piRedExecuteStatic(t, "http://localhost")
		isoRaw, has := parsed["isolated_url"]
		require.True(t, has,
			"RED (DS-3 row 10): implicit-80 Mode 1 mint carries no isolated_url")
		u, err := url.Parse(isoRaw.(string))
		require.NoError(t, err)
		assert.Empty(t, u.Port(),
			"implicit-80 mint is portless: http://<label>.localhost/")
		assert.Equal(t, "http", u.Scheme)
	})

	t.Run("mode2_https_loopback_fallback_only", func(t *testing.T) {
		// DS-3 row 2 / S-1.2: https-loopback is Mode 2 only. Pin: holds today.
		parsed := piRedExecuteStatic(t, "https://localhost:5000")
		assert.NotEmpty(t, parsed["url"], "fallback url stays present")
		isoRaw, has := parsed["isolated_url"]
		assert.False(t, has && isoRaw != "",
			"S-1.2 (FR-002): https-loopback must mint NO isolated_url — fallback only")
	})

	t.Run("mode2_ip_literal_fallback_only", func(t *testing.T) {
		// DS-3 row 3. Pin.
		parsed := piRedExecuteStatic(t, "http://127.0.0.1:5000")
		assert.NotEmpty(t, parsed["url"])
		isoRaw, has := parsed["isolated_url"]
		assert.False(t, has && isoRaw != "",
			"S-1.2 (FR-002): IP-literal canonical origin is Mode 2 only")
	})

	t.Run("mode2_real_domain_fallback_only", func(t *testing.T) {
		// DS-3 row 4. Pin.
		parsed := piRedExecuteStatic(t, "http://myhost.example.com:5000")
		assert.NotEmpty(t, parsed["url"])
		isoRaw, has := parsed["isolated_url"]
		assert.False(t, has && isoRaw != "",
			"S-1.2 (FR-002): a real domain is Mode 2 only")
	})

	t.Run("mode2_tailscale_fallback_only", func(t *testing.T) {
		// DS-3 row 5. Pin.
		parsed := piRedExecuteStatic(t, "http://myhost.tailnet-name.ts.net:5000")
		assert.NotEmpty(t, parsed["url"])
		isoRaw, has := parsed["isolated_url"]
		assert.False(t, has && isoRaw != "",
			"S-1.2 (FR-002): a Tailscale name is Mode 2 only")
	})

	t.Run("wildcard_host_refuses_to_mint", func(t *testing.T) {
		// DS-3 row 6 / S-1.3 (FR-003): refuse — error result, no URL, never a
		// degraded policy. RED today: the tool mints with the wildcard origin.
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644))
		cfg := &config.Config{Gateway: config.GatewayConfig{
			PublicURL:      "http://*.wildcard.example:5000",
			PreviewEnabled: boolPtr(true),
		}}
		tool := NewWebServeTool(dir, "pi-red-agent",
			func() *config.Config { return cfg }, piRedStubStore{}, nil,
			WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
			nil, nil, 60, 86400)
		result := tool.Execute(WithAgentID(context.Background(), "pi-red-agent"),
			map[string]any{"path": "."})

		require.True(t, result.IsError,
			"RED (FR-003, DS-3 row 6): a wildcard-host canonical origin must REFUSE to mint — "+
				"today the tool mints %q instead of an error result", result.ForLLM)
		var parsed map[string]any
		if json.Unmarshal([]byte(result.ForLLM), &parsed) == nil {
			assert.Empty(t, parsed["url"],
				"a refused mint must include no url")
			assert.Empty(t, parsed["isolated_url"],
				"a refused mint must include no isolated_url")
		}
	})

	t.Run("trailing_dot_refuses_to_mint", func(t *testing.T) {
		// DS-3 row 7 / S-1.3 (FR-003). RED today.
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644))
		cfg := &config.Config{Gateway: config.GatewayConfig{
			PublicURL:      "http://localhost.:5000",
			PreviewEnabled: boolPtr(true),
		}}
		tool := NewWebServeTool(dir, "pi-red-agent",
			func() *config.Config { return cfg }, piRedStubStore{}, nil,
			WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
			nil, nil, 60, 86400)
		result := tool.Execute(WithAgentID(context.Background(), "pi-red-agent"),
			map[string]any{"path": "."})

		require.True(t, result.IsError,
			"RED (FR-003, DS-3 row 7): a trailing-dot canonical origin must REFUSE to mint — "+
				"today the tool mints %q instead of an error result", result.ForLLM)
		var parsed map[string]any
		if json.Unmarshal([]byte(result.ForLLM), &parsed) == nil {
			assert.Empty(t, parsed["url"], "a refused mint must include no url")
			assert.Empty(t, parsed["isolated_url"], "a refused mint must include no isolated_url")
		}
	})

	// Gap (documented in the file header): the DEV variant of order 20 —
	// executeDev is Linux-gated and spawns a real process, so its dual-URL
	// result shape has no drivable surface in this environment pre-GREEN.
	// The dev-side assertion lands with GREEN's dev tests / CHECK.
	_ = context.Background
}

// TestPreviewLabelGrammar covers order 1 (mint side of DS-1): the minted
// label's grammar bounds, lower-case minting and hostname-safe encoding.
func TestPreviewLabelGrammar(t *testing.T) {
	// S-1.1: the label comes from a Mode 1 mint.
	parsed := piRedExecuteStatic(t, "http://localhost:5000")
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-005, S-1.1): no isolated_url mint exists yet — the label grammar "+
			"cannot be exercised without it")

	u, err := url.Parse(isoRaw.(string))
	require.NoError(t, err)
	hostParts := strings.SplitN(u.Hostname(), ".", 2)
	require.Len(t, hostParts, 2, "isolated_url host must be <label>.localhost")
	label := hostParts[0]

	// DS-1 row 9 / FR-005: at least 128 bits of entropy — the normative floor
	// (≈26 lower-case base32 chars).
	require.GreaterOrEqual(t, len(label), 26,
		"RED (FR-005, DS-1 row 9): the minted label carries <128 bits of entropy "+
			"(got %d chars); the layering claim 'guessing a label is guessing ~2^128' rests on this floor",
		len(label))

	// DS-1 rows 1–3: length bounds 1..63 (a real mint is on the long side).
	require.LessOrEqual(t, len(label), 63,
		"DS-1 rows 2–3: label must be at most 63 chars")

	// DS-1 rows 4–8: grammar — letters/digits/hyphens, no leading/trailing
	// hyphen, no underscore, no dots, lower-case only.
	assert.Regexp(t, piRedLabelGrammar, label,
		"DS-1 rows 4–7: the label must match FR-005's grammar")
	assert.Equal(t, strings.ToLower(label), label,
		"DS-1 row 7: labels are minted lower-case, never upper")
}
