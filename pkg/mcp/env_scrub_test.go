package mcp

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// envSliceToMap parses a KEY=value env slice into a map for assertions.
func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		if idx := strings.Index(e, "="); idx > 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}

// TestBuildStdioServerEnv_ScrubsMasterKey proves the C7 secret-scrub at the
// integration level: the env actually handed to a spawned stdio MCP server
// EXCLUDES OMNIPUS_MASTER_KEY (and other gateway secrets) even when they are
// present in the gateway process environment, while a legitimately-allowlisted
// var (PATH) still passes through. This guards the full buildStdioServerEnv path
// — not just the sandbox.ScrubGatewayEnv primitive in isolation.
func TestBuildStdioServerEnv_ScrubsMasterKey(t *testing.T) {
	// Inject gateway secrets into the process env. t.Setenv restores them after
	// the test, and forbids t.Parallel — exactly what we want for an env test.
	t.Setenv("OMNIPUS_MASTER_KEY", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	t.Setenv("OMNIPUS_KEY_FILE", "/secret/master.key")
	t.Setenv("OMNIPUS_BEARER_TOKEN", "super-secret-bearer")
	// A benign, allowlisted var must survive the scrub.
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")

	env, err := buildStdioServerEnv("test-server", config.MCPServerConfig{
		Command: "some-mcp-server",
	})
	if err != nil {
		t.Fatalf("buildStdioServerEnv: %v", err)
	}
	m := envSliceToMap(env)

	for _, secret := range []string{"OMNIPUS_MASTER_KEY", "OMNIPUS_KEY_FILE", "OMNIPUS_BEARER_TOKEN"} {
		if v, present := m[secret]; present {
			t.Errorf("gateway secret %q LEAKED into spawned MCP server env (value=%q) — C7 scrub failed", secret, v)
		}
	}

	if m["PATH"] == "" {
		t.Error("expected allowlisted PATH to pass through the scrub, but it was missing")
	}
}

// TestBuildStdioServerEnv_ConfigEnvOverridesButCannotReintroduceSecretByAccident
// proves that explicit per-server config env is honored (the documented escape
// hatch) and is layered ON TOP of the scrubbed base — i.e. the scrub does not
// drop legitimately-declared values.
func TestBuildStdioServerEnv_ConfigEnvIsHonored(t *testing.T) {
	t.Setenv("OMNIPUS_MASTER_KEY", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	env, err := buildStdioServerEnv("test-server", config.MCPServerConfig{
		Command: "some-mcp-server",
		Env: map[string]string{
			"MY_SERVER_TOKEN": "explicitly-declared",
		},
	})
	if err != nil {
		t.Fatalf("buildStdioServerEnv: %v", err)
	}
	m := envSliceToMap(env)

	if got := m["MY_SERVER_TOKEN"]; got != "explicitly-declared" {
		t.Errorf("expected explicitly-declared config env var to be present, got %q", got)
	}
	// The gateway secret must STILL be absent even with an explicit env block.
	if _, present := m["OMNIPUS_MASTER_KEY"]; present {
		t.Error("OMNIPUS_MASTER_KEY leaked despite explicit (unrelated) config env — C7 scrub failed")
	}
}
