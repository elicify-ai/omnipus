package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// probeEnvStub writes a stub CLI that dumps its OWN process environment to
// envFile (one KEY=VALUE per line) and then prints a version-looking token so
// detectCLIVersion's handshake succeeds. The probe runs it as the child
// process, so envFile captures exactly the env the probe child inherited.
func probeEnvStub(t *testing.T, envFile string) string {
	t.Helper()
	body := "#!/bin/sh\n" +
		"env > '" + envFile + "'\n" +
		"printf 'claude 1.2.3\\n'\n" +
		"exit 0\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "stub-cli")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("probeEnvStub: %v", err)
	}
	return path
}

// TestDetectCLIVersion_ChildEnvScrubbed is the security regression test for the
// version-probe env leak (u5b finding). Before the fix, detectCLIVersion exec'd
// the caller-supplied binary with cmd.Env == nil, so the `--version` child
// inherited the ENTIRE gateway environment — OMNIPUS_MASTER_KEY and every other
// gateway secret. The fix sets cmd.Env = sandbox.ScrubGatewayEnvForRunner(),
// the same policy every other external-CLI runner launch uses.
//
// This test drives the probe DIRECTLY (not through a driver's Run path) so the
// env snapshot is the PROBE child's env, and asserts:
//
//  1. OMNIPUS_MASTER_KEY is NOT present in the probe child env.
//  2. A sentinel gateway-only secret is NOT present either.
//  3. The probe actually ran (version parsed) AND a legitimately-allowlisted
//     runner credential (ANTHROPIC_API_KEY) DID reach the child — the positive
//     control proving the env snapshot instrument would have seen a leak.
func TestDetectCLIVersion_ChildEnvScrubbed(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Founder-accepted platform gap: issue #1256 (2026-10-09) accepts the
		// missing Windows equivalents for this landing and tracks them. The
		// fixture is a POSIX shell stub; the Windows replacement proof is owed.
		t.Skip("stub uses a POSIX shell script (Windows equivalent owed — founder-accepted gap, issue #1256)")
	}

	// Gateway secrets set in the PARENT (gateway) env — these MUST NOT reach the
	// probe child. Plus an allowlisted runner credential that SHOULD reach it.
	const sentinelKey = "OMNIPUS_TEST_PROBE_SENTINEL_SECRET"
	t.Setenv("OMNIPUS_MASTER_KEY", "deadbeef00000000000000000000000000000000000000000000000000000000")
	t.Setenv(sentinelKey, "must-not-leak")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-allowlisted-key")
	// PATH must survive so the stub's `env` / `sh` resolve in the child.
	t.Setenv("PATH", os.Getenv("PATH"))

	envFile := filepath.Join(t.TempDir(), "probe.env")
	stub := probeEnvStub(t, envFile)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ver, err := detectCLIVersion(ctx, stub)
	if err != nil {
		t.Fatalf("detectCLIVersion(%q) error = %v", stub, err)
	}
	if ver != "1.2.3" {
		t.Fatalf("detectCLIVersion parsed version = %q, want %q (the probe did not run as expected)", ver, "1.2.3")
	}

	childEnv := readChildEnvKeys(t, envFile)

	// (1) Positive control — the probe ran AND the env snapshot works. An
	// allowlisted runner credential must reach the child intact; if this fails,
	// the assertion instrument is blind and (2)/(3) below prove nothing.
	if v := childEnv["ANTHROPIC_API_KEY"]; v != "sk-ant-allowlisted-key" {
		t.Fatalf("instrument check failed: allowlisted ANTHROPIC_API_KEY did not reach the probe child "+
			"(got %q) — the env snapshot cannot be trusted to detect a leak", v)
	}

	// (2) The gateway master key must NOT be in the probe child env.
	if v, ok := childEnv["OMNIPUS_MASTER_KEY"]; ok {
		t.Fatalf("SECURITY LEAK: OMNIPUS_MASTER_KEY reached the --version probe child (value=%q); "+
			"the gateway master key must never be inherited by a caller-supplied binary", v)
	}
	// (3) The sentinel gateway secret must NOT be in the probe child env either.
	if v, ok := childEnv[sentinelKey]; ok {
		t.Fatalf("SECURITY LEAK: gateway secret %q reached the --version probe child (value=%q)", sentinelKey, v)
	}
}
