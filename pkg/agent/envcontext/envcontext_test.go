// Package envcontext_test covers Fix A (env-awareness preamble) behavioral tests.
// Traces to: env-awareness-and-memory-spec.md (spec v7)
package envcontext_test

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dapicom-ai/omnipus/pkg/agent/envcontext"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// ---------------------------------------------------------------------------
// Mock Provider — lets tests inject controlled values without live runtime.
// ---------------------------------------------------------------------------

type mockProvider struct {
	platform       envcontext.Platform
	platformErr    error
	sandboxMode    string
	sandboxErr     error
	networkPolicy  envcontext.NetworkPolicy
	workspacePath  string
	omnipusHome    string
	activeWarnings []string
}

func (m *mockProvider) Platform() (envcontext.Platform, error) {
	return m.platform, m.platformErr
}

func (m *mockProvider) SandboxMode() (string, error) {
	return m.sandboxMode, m.sandboxErr
}

func (m *mockProvider) NetworkPolicy() envcontext.NetworkPolicy {
	return m.networkPolicy
}
func (m *mockProvider) WorkspacePath() string { return m.workspacePath }
func (m *mockProvider) OmnipusHome() string   { return m.omnipusHome }
func (m *mockProvider) ActiveWarnings() []string {
	return m.activeWarnings
}

// ---------------------------------------------------------------------------
// #49 — TestEnvironmentProvider_PlatformFromRuntime
// Traces to: env-awareness-and-memory-spec.md FR-043
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_PlatformFromRuntime(t *testing.T) {
	// Use the DefaultProvider wired with a minimal config and no backend.
	// We just need to verify Platform() returns GOOS and GOARCH from runtime.
	p := envcontext.NewDefaultProvider(minimalConfig(), nil, "/tmp/test-workspace")

	plat, err := p.Platform()
	// err is allowed (e.g. /proc/version missing on non-Linux) — what matters is
	// that GOOS and GOARCH are correct.
	if err != nil && runtime.GOOS == "linux" {
		// On Linux a read error is unexpected but not catastrophic for the test.
		t.Logf("Platform() returned error on linux: %v (non-fatal)", err)
	}

	if plat.GOOS != runtime.GOOS {
		t.Errorf("Platform.GOOS = %q, want %q", plat.GOOS, runtime.GOOS)
	}
	if plat.GOARCH != runtime.GOARCH {
		t.Errorf("Platform.GOARCH = %q, want %q", plat.GOARCH, runtime.GOARCH)
	}

	// On Linux, Kernel must be non-empty (or we got an error above).
	if runtime.GOOS == "linux" && err == nil && plat.Kernel == "" {
		t.Error("Platform.Kernel is empty on Linux with no error")
	}
	// On non-Linux, Kernel should be empty.
	if runtime.GOOS != "linux" && plat.Kernel != "" {
		t.Errorf("Platform.Kernel = %q, want empty on %s", plat.Kernel, runtime.GOOS)
	}
}

// ---------------------------------------------------------------------------
// #50 — TestEnvironmentProvider_SandboxModeFromBackend
// Traces to: env-awareness-and-memory-spec.md FR-044
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_SandboxModeFromBackend(t *testing.T) {
	tests := []struct {
		name     string
		backend  sandbox.SandboxBackend
		wantMode string
	}{
		{
			name:     "nil backend → off",
			backend:  nil,
			wantMode: "off",
		},
		{
			name:     "fallback backend → fallback",
			backend:  sandbox.NewFallbackBackend(),
			wantMode: "fallback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := envcontext.NewDefaultProvider(minimalConfig(), tc.backend, "/tmp/ws")
			mode, err := p.SandboxMode()
			if err != nil {
				t.Fatalf("SandboxMode() returned unexpected error: %v", err)
			}
			if mode != tc.wantMode {
				t.Errorf("SandboxMode() = %q, want %q", mode, tc.wantMode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// #51 — TestEnvironmentProvider_NetworkPolicyComputation
// Traces to: env-awareness-and-memory-spec.md FR-045
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_NetworkPolicyComputation(t *testing.T) {
	tests := []struct {
		name            string
		outboundAllowed bool
		wantInPreamble  string
	}{
		{
			name:            "outbound allowed → outbound-allowed in preamble",
			outboundAllowed: true,
			wantInPreamble:  "outbound-allowed",
		},
		{
			name:            "outbound denied → outbound-denied in preamble",
			outboundAllowed: false,
			wantInPreamble:  "outbound-denied",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &mockProvider{
				sandboxMode:   "fallback",
				networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: tc.outboundAllowed},
				workspacePath: "/workspace",
				omnipusHome:   "/home/.omnipus",
			}
			out := envcontext.Render(p, "")
			if !strings.Contains(out, tc.wantInPreamble) {
				t.Errorf("Render() output does not contain %q;\ngot:\n%s", tc.wantInPreamble, out)
			}
			// Differentiation: make sure allowed and denied produce different output.
			otherAllowed := !tc.outboundAllowed
			otherP := &mockProvider{
				sandboxMode:   "fallback",
				networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: otherAllowed},
				workspacePath: "/workspace",
				omnipusHome:   "/home/.omnipus",
			}
			otherOut := envcontext.Render(otherP, "")
			if out == otherOut {
				t.Error("Render() returns identical output for allowed and denied outbound — should differ")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// #52 — TestEnvironmentProvider_ActiveWarnings_DevModeBypass
// Traces to: env-awareness-and-memory-spec.md FR-049
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_ActiveWarnings_DevModeBypass(t *testing.T) {
	// Inject via mockProvider — tests the renderer's warning section directly.
	p := &mockProvider{
		sandboxMode:    "fallback",
		networkPolicy:  envcontext.NetworkPolicy{OutboundAllowed: false},
		workspacePath:  "/workspace",
		omnipusHome:    "/home/.omnipus",
		activeWarnings: []string{"dev_mode_bypass is ACTIVE — auth checks are relaxed. Do not assume strict auth."},
	}
	out := envcontext.Render(p, "")

	if !strings.Contains(out, "dev_mode_bypass is ACTIVE") {
		t.Errorf("preamble missing dev_mode_bypass warning; got:\n%s", out)
	}
	if !strings.Contains(out, "### Active warnings") {
		t.Errorf("preamble missing '### Active warnings' section; got:\n%s", out)
	}

	// Without the warning, section should be absent.
	pNoWarn := &mockProvider{
		sandboxMode:   "fallback",
		networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
		workspacePath: "/workspace",
		omnipusHome:   "/home/.omnipus",
	}
	outNoWarn := envcontext.Render(pNoWarn, "")
	if strings.Contains(outNoWarn, "### Active warnings") {
		t.Error("preamble should not contain '### Active warnings' when no warnings are active")
	}
}

// ---------------------------------------------------------------------------
// #54 — TestEnvironmentProvider_ActiveWarnings_WindowsFlockNoop
// Traces to: env-awareness-and-memory-spec.md FR-049
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_ActiveWarnings_WindowsFlockNoop(t *testing.T) {
	// Inject the Windows warning directly via mock — the renderer must include it.
	p := &mockProvider{
		sandboxMode:   "fallback",
		networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
		workspacePath: "/workspace",
		omnipusHome:   "/home/.omnipus",
		activeWarnings: []string{
			"running on Windows — pkg/fileutil.WithFlock is a no-op; concurrent memory writes rely on single-writer discipline.",
		},
	}
	out := envcontext.Render(p, "")
	if !strings.Contains(out, "WithFlock is a no-op") {
		t.Errorf("preamble missing Windows flock warning; got:\n%s", out)
	}

	// On a production run, the DefaultProvider only emits this on Windows.
	// Verify the DefaultProvider logic: if GOOS is not windows, the warning is absent.
	if runtime.GOOS != "windows" {
		dp := envcontext.NewDefaultProvider(minimalConfig(), nil, "/tmp/ws")
		warnings := dp.ActiveWarnings()
		for _, w := range warnings {
			if strings.Contains(w, "WithFlock is a no-op") {
				t.Errorf("DefaultProvider emitted Windows flock warning on non-windows GOOS=%s", runtime.GOOS)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// #55 — TestEnvironmentProvider_NoSecretLeakage
// Traces to: env-awareness-and-memory-spec.md FR-055
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_NoSecretLeakage(t *testing.T) {
	secrets := []struct {
		label  string
		secret string
	}{
		{"openai sk-", "sk-abcdefghijklmnopqrstuv"},
		{"openrouter sk-or-v1-", "sk-or-v1-abcdefghijklmnopqrstuvwxyz"},
		{"slack xoxb-", "xoxb-1234567890-abcdefghij"},
		{"slack xoxp-", "xoxp-1234567890-abcdefghij"},
		{"github ghp_", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijkl"},
		{"github gho_", "gho_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijkl"},
		{"aws akia", "AKIAIOSFODNN7EXAMPLE"},
		{"aws asia (sts)", "ASIAIOSFODNN7EXAMPLE"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"google oauth", "ya29.a0ARrdaM-SAMPLE_TOKEN_VALUE_abcdefghij"},
		{"bearer", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig"},
		{"email", "operator-alice@example.com"},
	}

	for _, s := range secrets {
		t.Run(s.label, func(t *testing.T) {
			// Inject the secret in each render-visible field to prove none of
			// the paths leak it through.
			for _, injectVia := range []string{"sandboxMode", "workspace", "warning"} {
				p := &mockProvider{
					sandboxMode:   "fallback",
					networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
					workspacePath: "/workspace",
					omnipusHome:   "/home/.omnipus",
				}
				switch injectVia {
				case "sandboxMode":
					p.sandboxMode = s.secret
				case "workspace":
					p.workspacePath = "/tmp/" + s.secret + "/.omnipus"
				case "warning":
					p.activeWarnings = []string{"operator note: " + s.secret}
				}
				out := envcontext.Render(p, "")
				if strings.Contains(out, s.secret) {
					t.Errorf("preamble contains raw secret via %s — must be redacted\nsecret: %q\npreamble:\n%s",
						injectVia, s.secret, out)
				}
			}
		})
	}
}

// TestEnvironmentProvider_LegitimatePathsNotOverRedacted pins the other half
// of FR-055: a workspace path that happens to contain an innocuous token like
// "secret" in a directory name must NOT be rewritten into [REDACTED]. The
// redactor is pattern-based, not keyword-based, so this should be a trivial
// pass — the test guards against future drift toward keyword filtering.
func TestEnvironmentProvider_LegitimatePathsNotOverRedacted(t *testing.T) {
	cases := []string{
		"/srv/app-secret-123/.omnipus",     // "secret" is a substring, not a pattern match
		"/home/alice/tokens-design-doc/ws", // "tokens" substring
		"/opt/key-material-only-the-name",  // "key-" prefix but too short for key-[…]{20,}
	}
	for _, p := range cases {
		mp := &mockProvider{
			sandboxMode:   "fallback",
			networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
			workspacePath: p,
			omnipusHome:   "/home/.omnipus",
		}
		out := envcontext.Render(mp, "")
		if !strings.Contains(out, p) {
			t.Errorf("preamble over-redacted legitimate path %q; got:\n%s", p, out)
		}
	}
}

// ---------------------------------------------------------------------------
// #56 — TestEnvironmentProvider_NoHostnameLeakage
// Traces to: env-awareness-and-memory-spec.md FR-055
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_NoHostnameLeakage(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		t.Skip("cannot determine hostname, skipping")
	}

	// The renderer must NOT emit the OS hostname. Workspace paths and home
	// directories may incidentally contain the hostname on some setups (e.g.
	// /Users/<hostname>/…), so we test with controlled, obviously non-hostname paths.
	p := &mockProvider{
		sandboxMode:   "fallback",
		networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: true},
		workspacePath: "/workspace/agent",
		omnipusHome:   "/home/omnipus",
	}
	out := envcontext.Render(p, "")

	// The hostname must not appear verbatim in the preamble.
	if strings.Contains(out, hostname) {
		t.Errorf("preamble contains OS hostname %q — must not leak;\ngot:\n%s", hostname, out)
	}
}

// ---------------------------------------------------------------------------
// #57 — TestEnvironmentProvider_GracefulFieldFailure
// Traces to: env-awareness-and-memory-spec.md FR-054, CRIT-005
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_GracefulFieldFailure(t *testing.T) {
	// SandboxMode returns an error → field shows <unknown>, build continues.
	p := &mockProvider{
		sandboxMode:   "", // will be overridden by the error path
		sandboxErr:    errors.New("backend unavailable"),
		networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
		workspacePath: "/workspace",
		omnipusHome:   "/home/.omnipus",
	}
	out := envcontext.Render(p, "")

	if out == "" {
		t.Fatal("Render() returned empty string — should return partial preamble even on field error")
	}
	if !strings.Contains(out, "<unknown>") {
		t.Errorf("preamble should contain '<unknown>' for failed field; got:\n%s", out)
	}
	if !strings.Contains(out, "## Environment") {
		t.Errorf("preamble should still contain '## Environment' header; got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// #60 — TestEnvironmentProvider_GetEnvironmentContext_Below2000Runes
// Traces to: env-awareness-and-memory-spec.md FR-050
// ---------------------------------------------------------------------------

func TestEnvironmentProvider_GetEnvironmentContext_Below2000Runes(t *testing.T) {
	// Feed many large warnings to stress the truncation path.
	bigWarnings := make([]string, 50)
	for i := range bigWarnings {
		bigWarnings[i] = strings.Repeat("W", 100)
	}

	p := &mockProvider{
		sandboxMode:    "fallback",
		networkPolicy:  envcontext.NetworkPolicy{OutboundAllowed: false},
		workspacePath:  "/workspace",
		omnipusHome:    "/home/.omnipus",
		activeWarnings: bigWarnings,
	}
	out := envcontext.Render(p, "")

	runes := utf8.RuneCountInString(out)
	// Spec says ≤ 2000 runes before the truncation marker; we allow the marker itself.
	const limit = 2000 + len("\n\n[env context truncated]")
	if runes > limit {
		t.Errorf("preamble has %d runes, exceeds limit %d", runes, limit)
	}

	// When truncated, the truncation marker must be present.
	if runes > 2000 {
		if !strings.Contains(out, "[env context truncated]") {
			t.Errorf("preamble is truncated but missing truncation marker; got:\n%s", out)
		}
	}
}

// ---------------------------------------------------------------------------
// #65 — TestRenderSandboxMode_AllStatusShapes
// Traces to: env-awareness-and-memory-spec.md (sandbox mode mapping table)
// ---------------------------------------------------------------------------

func TestRenderSandboxMode_AllStatusShapes(t *testing.T) {
	// We exercise the mapping via the render path: inject a mockProvider whose
	// SandboxMode() returns a known string.
	tests := []struct {
		name       string
		sandboxStr string
		wantIn     string
	}{
		{
			name:       "off",
			sandboxStr: "off",
			wantIn:     "off",
		},
		{
			name:       "fallback backend",
			sandboxStr: "fallback",
			wantIn:     "fallback",
		},
		{
			name:       "landlock ABI 3",
			sandboxStr: "landlock-abi-3",
			wantIn:     "landlock-abi-3",
		},
		{
			name:       "unknown shape",
			sandboxStr: "unknown",
			wantIn:     "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &mockProvider{
				sandboxMode:   tc.sandboxStr,
				networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
				workspacePath: "/workspace",
				omnipusHome:   "/home/.omnipus",
			}
			out := envcontext.Render(p, "")
			if !strings.Contains(out, tc.wantIn) {
				t.Errorf("Render() does not contain %q; got:\n%s", tc.wantIn, out)
			}
		})
	}

	// Differentiation: two different modes produce two different preambles.
	p1 := &mockProvider{
		sandboxMode:   "off",
		networkPolicy: envcontext.NetworkPolicy{},
		workspacePath: "/ws",
		omnipusHome:   "/h",
	}
	p2 := &mockProvider{
		sandboxMode:   "fallback",
		networkPolicy: envcontext.NetworkPolicy{},
		workspacePath: "/ws",
		omnipusHome:   "/h",
	}
	if envcontext.Render(p1, "") == envcontext.Render(p2, "") {
		t.Error("Render() returns same output for 'off' and 'fallback' sandbox modes — must differ")
	}
}

// ---------------------------------------------------------------------------
// TestRender_NilProvider
// Traces to: envcontext/provider.go — Render(nil) guard
// ---------------------------------------------------------------------------

func TestRender_NilProvider(t *testing.T) {
	out := envcontext.Render(nil, "")
	if out != "" {
		t.Errorf("Render(nil) = %q, want empty string", out)
	}
}

// ---------------------------------------------------------------------------
// TestRender_WorkspaceOverrideWins
// Traces to: env-awareness-and-memory-spec.md FR-046
// ---------------------------------------------------------------------------

func TestRender_WorkspaceOverrideWins(t *testing.T) {
	p := &mockProvider{
		sandboxMode:   "fallback",
		networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
		workspacePath: "/original/workspace",
		omnipusHome:   "/home/.omnipus",
	}
	override := "/override/workspace"
	out := envcontext.Render(p, override)

	if !strings.Contains(out, override) {
		t.Errorf("Render() does not contain override path %q; got:\n%s", override, out)
	}
	// Original workspace must not appear when overridden.
	if strings.Contains(out, "/original/workspace") {
		t.Errorf("Render() contains original workspace path when override is set; got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// TestRender_ContainsRequiredSections
// Smoke-level check that all required sections are present.
// ---------------------------------------------------------------------------

func TestRender_ContainsRequiredSections(t *testing.T) {
	p := &mockProvider{
		sandboxMode:   "fallback",
		networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: true},
		workspacePath: "/workspace/agent",
		omnipusHome:   "/home/.omnipus",
	}
	out := envcontext.Render(p, "")

	required := []string{
		"## Environment",
		"### Paths you can use",
		"### Paths you cannot use",
		"### Sandbox & network",
	}
	for _, section := range required {
		if !strings.Contains(out, section) {
			t.Errorf("preamble missing required section %q; got:\n%s", section, out)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// minimalConfig returns a zero-value *config.Config, which satisfies
// NewDefaultProvider's requirement for a non-nil config. The sandbox and
// gateway sub-structs carry zero values (e.g. DevModeBypass=false,
// AllowNetworkOutbound=false), which is fine for tests that don't need
// specific config values.
func minimalConfig() *config.Config {
	return &config.Config{}
}
