package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFakeBin writes an executable shell script named bin into dir that prints
// the given version text and exits 0. Returns nothing; skips on Windows where
// the shebang trick doesn't apply.
func writeFakeBin(t *testing.T, dir, bin, versionOut string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary handshake test uses a POSIX shell script")
	}
	p := filepath.Join(dir, bin)
	script := "#!/bin/sh\necho '" + versionOut + "'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolatePATH sets PATH to exactly dir for the test, so only fakes we install
// are found. It also clears credential env vars so auth probing is deterministic
// unless a test sets them.
func isolatePATH(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY", "CLAUDE_CONFIG_DIR", "CODEX_HOME",
		"XDG_DATA_HOME",
	} {
		t.Setenv(k, "")
	}
	// Point HOME at an empty dir so on-disk auth-file probes find nothing.
	t.Setenv("HOME", t.TempDir())
}

func TestTestConnection_UnknownCLI(t *testing.T) {
	res := TestConnection(context.Background(), "bogus-cli")
	if res.OK {
		t.Fatal("unknown CLI should not be OK")
	}
	if !strings.Contains(res.Message, string(ReasonUnknownCLI)) {
		t.Fatalf("message lacks unknown-cli reason: %q", res.Message)
	}
}

func TestTestConnection_MissingBinary(t *testing.T) {
	dir := t.TempDir() // empty PATH dir → no claude binary
	isolatePATH(t, dir)

	res := TestConnection(context.Background(), "claude-code")
	if res.OK {
		t.Fatal("missing binary should not be OK")
	}
	if !strings.Contains(res.Message, string(ReasonMissingBinary)) {
		t.Fatalf("want missing-binary reason, got: %q", res.Message)
	}
}

func TestTestConnection_PresentButUnauthenticated(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "claude", "claude 1.2.3 (fake)")
	isolatePATH(t, dir) // clears creds → unauthenticated

	res := TestConnection(context.Background(), "claude-code")
	if res.OK {
		t.Fatalf("present-but-unauthed should not be OK: %q", res.Message)
	}
	if !strings.Contains(res.Message, string(ReasonUnauthenticated)) {
		t.Fatalf("want unauthenticated reason, got: %q", res.Message)
	}
	// The version handshake still succeeded, so CLIVersion must be set — this is
	// the distinction the spec requires (handshake OK, auth not).
	if res.CLIVersion != "1.2.3" {
		t.Fatalf("CLIVersion = %q, want 1.2.3", res.CLIVersion)
	}
}

func TestTestConnection_FullyHealthy(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "claude", "claude v2.0.1")
	isolatePATH(t, dir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-fake") // now authed

	res := TestConnection(context.Background(), "claude-code")
	if !res.OK {
		t.Fatalf("healthy runner should be OK, got: %q", res.Message)
	}
	if res.CLIVersion != "2.0.1" {
		t.Fatalf("CLIVersion = %q, want 2.0.1", res.CLIVersion)
	}
}

func TestTestConnection_Codex_AuthViaEnv(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "codex", "codex-cli 0.4.0")
	isolatePATH(t, dir)
	t.Setenv("OPENAI_API_KEY", "sk-fake")

	res := TestConnection(context.Background(), "codex")
	if !res.OK {
		t.Fatalf("codex should be OK with OPENAI_API_KEY: %q", res.Message)
	}
	if res.CLIVersion != "0.4.0" {
		t.Fatalf("CLIVersion = %q, want 0.4.0", res.CLIVersion)
	}
}

func TestTestConnection_Opencode_AuthViaFile(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "opencode", "opencode v0.1.7")
	isolatePATH(t, dir)

	// Place an opencode auth.json under XDG_DATA_HOME.
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	authDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res := TestConnection(context.Background(), "opencode")
	if !res.OK {
		t.Fatalf("opencode should be OK with auth.json present: %q", res.Message)
	}
}

// TestTestConnectionWithPath_CustomPathMissing closes the false-green gap: when
// a custom cli_path is configured but does not exist, the test must FAIL with
// missing-binary even though the DEFAULT binary IS on PATH. Before the fix, Test
// validated only the default $PATH binary, so a broken custom path passed.
func TestTestConnectionWithPath_CustomPathMissing(t *testing.T) {
	dir := t.TempDir()
	// The DEFAULT "claude" binary IS present and authed on PATH …
	writeFakeBin(t, dir, "claude", "claude v2.0.1")
	isolatePATH(t, dir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-fake")

	// … but the agent is configured with a custom cli_path that does NOT exist.
	missing := filepath.Join(dir, "nonexistent-claude")
	res := TestConnectionWithPath(context.Background(), "claude-code", missing)
	if res.OK {
		t.Fatalf("custom cli_path that does not exist must FAIL the test (false-green guard); got OK: %q", res.Message)
	}
	if res.Reason != ReasonMissingBinary {
		t.Fatalf("want missing-binary reason for bad cli_path, got %q (%q)", res.Reason, res.Message)
	}
}

// TestTestConnectionWithPath_CustomPathHonored verifies a valid custom cli_path
// is the binary actually validated (its version is reported).
func TestTestConnectionWithPath_CustomPathHonored(t *testing.T) {
	dir := t.TempDir()
	isolatePATH(t, dir) // no default "claude" on PATH at all
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-fake")

	// Custom binary at an explicit absolute path with a distinctive version.
	custom := filepath.Join(dir, "my-claude")
	script := "#!/bin/sh\necho 'claude 9.9.9 (custom)'\n"
	if err := os.WriteFile(custom, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	res := TestConnectionWithPath(context.Background(), "claude-code", custom)
	if !res.OK {
		t.Fatalf("valid custom cli_path should be OK, got: %q", res.Message)
	}
	if res.CLIVersion != "9.9.9" {
		t.Fatalf("custom cli_path binary version must be reported; CLIVersion = %q, want 9.9.9", res.CLIVersion)
	}
}

func TestSupportedCLIs(t *testing.T) {
	got := SupportedCLIs()
	want := map[string]bool{"claude-code": true, "codex": true, "opencode": true}
	if len(got) != len(want) {
		t.Fatalf("SupportedCLIs = %v, want 3 entries", got)
	}
	for _, c := range got {
		if !want[c] {
			t.Fatalf("unexpected CLI %q", c)
		}
	}
}

func TestExtractVersion(t *testing.T) {
	cases := map[string]string{
		"claude 1.2.3":        "1.2.3",
		"opencode v0.1.7":     "0.1.7",
		"codex-cli 0.4.0":     "0.4.0",
		"version 10.20.30-rc": "10.20.30-rc",
		"no-version-here":     "no-version-here",
	}
	for in, want := range cases {
		if got := extractVersion(in); got != want {
			t.Errorf("extractVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
