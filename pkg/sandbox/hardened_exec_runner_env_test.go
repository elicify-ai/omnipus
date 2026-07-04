package sandbox

import (
	"os"
	"strings"
	"testing"
)

// envHas reports whether kv slice contains a key=... entry.
func envHas(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

// TestRunnerCredentialEnv_LeaksOnlyToRunnerPath is the core security assertion
// for Spec-4 FR-5.3: credential-bearing env keys reach the EXTERNAL-CLI RUNNER
// child (ScrubGatewayEnvForRunner) but are STRIPPED from the generic child env
// (ScrubGatewayEnv) used by workspace.shell / build / web_serve.
func TestRunnerCredentialEnv_LeaksOnlyToRunnerPath(t *testing.T) {
	credKeys := RunnerCredentialEnvKeys()
	if len(credKeys) == 0 {
		t.Fatal("RunnerCredentialEnvKeys() returned empty set")
	}

	for _, key := range credKeys {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "SECRET-"+key)

			generic := ScrubGatewayEnv()
			if envHas(generic, key) {
				t.Fatalf("SECURITY: credential key %q leaked into generic child env (ScrubGatewayEnv)", key)
			}

			runner := ScrubGatewayEnvForRunner()
			if !envHas(runner, key) {
				t.Fatalf("credential key %q did NOT reach the runner child env (ScrubGatewayEnvForRunner)", key)
			}
		})
	}
}

// TestRunnerCredentialEnv_DoesNotBroadenGeneric proves the runner allowlist is
// strictly additive on the runner path only — a NON-credential, NON-generic key
// is still stripped from both paths (no accidental broadening).
func TestRunnerCredentialEnv_DoesNotBroadenGeneric(t *testing.T) {
	const sneaky = "SOME_OTHER_SECRET"
	t.Setenv(sneaky, "nope")

	if envHas(ScrubGatewayEnv(), sneaky) {
		t.Fatalf("%q leaked into generic env", sneaky)
	}
	if envHas(ScrubGatewayEnvForRunner(), sneaky) {
		t.Fatalf("%q leaked into runner env (allowlist over-broad)", sneaky)
	}
}

// TestRunnerCredentialEnv_GenericKeysStillPresent confirms the runner path is a
// SUPERSET of the generic path: ordinary keys (PATH, HOME) still pass through.
func TestRunnerCredentialEnv_GenericKeysStillPresent(t *testing.T) {
	// PATH passes through regardless of value, but t.Setenv mutates the
	// PROCESS-GLOBAL env for the duration of this test. Clobbering PATH to a bare
	// "/usr/bin" breaks any parallel sibling test that spawns `sh`/`env` from a
	// different location (e.g. /bin), which surfaced as a flaky "sh -c env: exit 2"
	// in TestSpawnBackgroundChild_EnvMerging. Preserve resolvability by prepending
	// to the real PATH instead of replacing it.
	t.Setenv("PATH", "/usr/bin:"+os.Getenv("PATH"))
	t.Setenv("HOME", "/home/x")

	runner := ScrubGatewayEnvForRunner()
	if !envHas(runner, "PATH") || !envHas(runner, "HOME") {
		t.Fatalf("runner env missing generic keys PATH/HOME: %v", runner)
	}
}

// TestRunnerCredentialEnvKeys_ExactSet documents the exact security-sensitive
// keys allowlisted for runner children. If this set changes, the change is a
// deliberate security decision and the test must be updated alongside it.
func TestRunnerCredentialEnvKeys_ExactSet(t *testing.T) {
	got := RunnerCredentialEnvKeys()
	want := []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CONFIG_DIR",
		"CODEX_ACCESS_TOKEN",
		"CODEX_API_KEY",
		"CODEX_HOME",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("runner credential env allowlist changed:\n got=%v\nwant=%v", got, want)
	}
}
