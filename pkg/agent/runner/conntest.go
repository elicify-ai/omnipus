// Spec-4 FR-4.2 — per-runner connection test (health check).
//
// A connection test validates that an external-CLI runner can run, WITHOUT
// running any real work (no model tokens spent). It answers three questions, in
// order, and reports the FIRST that fails so the operator gets an actionable
// reason (US-6 AC-2 — "missing/un-authed binary → clear reason"):
//
//  1. Is the CLI binary present on PATH?              (missing-binary)
//  2. Does it run and report a version?               (handshake)
//  3. Are credentials available for it to authenticate? (unauthed)
//
// The "JSON handshake" is the CLI's own `--version` invocation: it proves the
// binary executes and lets us pin its version (FR-5.6 — the stream schema
// drifts across versions) without spending tokens. Auth is checked by probing
// for the credentials each CLI documents (an API-key env var OR the CLI's
// on-disk auth file) — again, no network call, no tokens.
//
// Distinguishing missing-binary from unauthed is a hard requirement (US-6): the
// two have different remedies (install vs `login`), so they MUST NOT collapse
// into one generic failure.

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// versionProbeTimeout bounds the `--version` handshake. A CLI that can't print
// its version in this window is treated as a failed handshake.
const versionProbeTimeout = 15 * time.Second

// FailureReason classifies a connection-test failure so callers (and the SPA)
// can render the right remedy. It is set on ConnectionTestResult via the
// Message, and exposed here for programmatic branching in tests.
type FailureReason string

const (
	// ReasonOK indicates the test passed.
	ReasonOK FailureReason = ""
	// ReasonMissingBinary indicates the CLI binary was not found on PATH.
	ReasonMissingBinary FailureReason = "missing-binary"
	// ReasonHandshakeFailed indicates the binary exists but did not run / report
	// a version (wrong binary, broken install, incompatible CLI).
	ReasonHandshakeFailed FailureReason = "handshake-failed"
	// ReasonUnauthenticated indicates the binary is present and runs but no
	// credentials were found for it to authenticate to its model provider.
	ReasonUnauthenticated FailureReason = "unauthenticated"
	// ReasonUnknownCLI indicates an unsupported CLI name was requested.
	ReasonUnknownCLI FailureReason = "unknown-cli"
)

// cliSpec describes how to health-check one external CLI: its binary name, the
// args that print its version, and a predicate that reports whether credentials
// are available without spending tokens.
type cliSpec struct {
	binary       string
	versionArgs  []string
	hasCredsFunc func() bool
	loginHint    string
}

// supportedCLIs maps the executor `cli` value to its health-check spec. The
// keys match config.ExecutorConfig.CLI ("claude-code", "codex", "opencode").
var supportedCLIs = map[string]cliSpec{
	"claude-code": {
		binary:      "claude",
		versionArgs: []string{"--version"},
		hasCredsFunc: func() bool {
			// Claude Code authenticates via an API key / auth token in the env,
			// or an OAuth credential file under its config dir (default
			// ~/.claude, overridable via CLAUDE_CONFIG_DIR).
			if anyEnvSet("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN") {
				return true
			}
			return claudeAuthFilePresent()
		},
		loginHint: "run `claude login` or set ANTHROPIC_API_KEY",
	},
	"codex": {
		binary:      "codex",
		versionArgs: []string{"--version"},
		hasCredsFunc: func() bool {
			if anyEnvSet("OPENAI_API_KEY") {
				return true
			}
			// Reuse the existing Codex CLI credential reader (~/.codex/auth.json,
			// overridable via CODEX_HOME).
			tok, _, _, err := providers.ReadCodexCliCredentials()
			return err == nil && tok != ""
		},
		loginHint: "run `codex login` or set OPENAI_API_KEY",
	},
	"opencode": {
		binary:      "opencode",
		versionArgs: []string{"--version"},
		hasCredsFunc: func() bool {
			if anyEnvSet("OPENAI_API_KEY", "ANTHROPIC_API_KEY") {
				return true
			}
			return opencodeAuthFilePresent()
		},
		loginHint: "run `opencode auth login` or set OPENAI_API_KEY / ANTHROPIC_API_KEY",
	},
}

// SupportedCLIs returns the sorted list of recognized external-CLI names.
func SupportedCLIs() []string {
	out := make([]string, 0, len(supportedCLIs))
	for k := range supportedCLIs {
		out = append(out, k)
	}
	// small fixed set; insertion order doesn't matter for callers, but keep it
	// deterministic for tests.
	sortStrings(out)
	return out
}

// TestConnection runs the binary-present → handshake → authed health check for
// the named CLI and returns a ConnectionTestResult. It NEVER runs real agent
// work. cli must be one of SupportedCLIs(); an unknown name fails with
// ReasonUnknownCLI. It validates the CLI's DEFAULT $PATH binary — to validate a
// configured ExecutorConfig.cli_path instead, use TestConnectionWithPath.
//
// This is the standalone implementation the per-CLI ExternalAgentRunner.Test
// methods (U2 drivers) delegate to, so the health-check logic lives in one
// place and the missing-binary vs unauthed distinction is uniform.
func TestConnection(ctx context.Context, cli string) ConnectionTestResult {
	return TestConnectionWithPath(ctx, cli, "")
}

// TestConnectionWithPath is TestConnection with an explicit cliPath override
// (ExecutorConfig.cli_path). When cliPath is non-empty it is the binary that is
// validated — an absolute path is checked on disk, a bare name is resolved via
// $PATH — so a configured custom path that does not exist correctly fails the
// test (missing-binary) instead of silently passing on the default $PATH binary.
// When cliPath is empty the CLI's default binary name is used (legacy behavior).
func TestConnectionWithPath(ctx context.Context, cli, cliPath string) ConnectionTestResult {
	spec, ok := supportedCLIs[cli]
	if !ok {
		return ConnectionTestResult{
			OK:     false,
			Reason: ReasonUnknownCLI,
			Message: fmt.Sprintf(
				"[%s] unknown external CLI %q (supported: %s)",
				ReasonUnknownCLI, cli, strings.Join(SupportedCLIs(), ", "),
			),
		}
	}

	// Resolve which binary to validate: the configured cli_path wins, else the
	// CLI's default name. resolveCLIBinary trims and applies the same precedence
	// the drivers use to build the actual exec target, so Test validates exactly
	// what a real run would exec.
	target := resolveCLIBinary(cliPath, spec.binary)

	// 1. Binary present? An absolute/relative path is honored as-is by LookPath
	//    (it stats the path directly); a bare name is resolved via $PATH.
	binPath, err := exec.LookPath(target)
	if err != nil {
		return ConnectionTestResult{
			OK:     false,
			Reason: ReasonMissingBinary,
			Message: fmt.Sprintf(
				"[%s] %q CLI not found at %q: install %s / fix cli_path and ensure it is executable on PATH",
				ReasonMissingBinary, cli, target, spec.binary,
			),
		}
	}

	// 2. Handshake — run `<binary> --version`, no real work, no tokens.
	version, hErr := probeVersion(ctx, binPath, spec.versionArgs)
	if hErr != nil {
		return ConnectionTestResult{
			OK:      false,
			Reason:  ReasonHandshakeFailed,
			Message: fmt.Sprintf("[%s] %q version handshake failed: %v", ReasonHandshakeFailed, cli, hErr),
		}
	}

	// 3. Credentials available?
	if spec.hasCredsFunc == nil || !spec.hasCredsFunc() {
		return ConnectionTestResult{
			OK:     false,
			Reason: ReasonUnauthenticated,
			Message: fmt.Sprintf(
				"[%s] %q is installed (v%s) but not authenticated: %s",
				ReasonUnauthenticated, cli, version, spec.loginHint,
			),
			CLIVersion: version,
		}
	}

	return ConnectionTestResult{
		OK:         true,
		Reason:     ReasonOK,
		Message:    fmt.Sprintf("%q ready: binary %s present, v%s, authenticated", cli, binPath, version),
		CLIVersion: version,
	}
}

// probeVersion runs the version command with a bounded timeout and returns a
// cleaned version string. Both stdout and stderr are considered (some CLIs
// print version to stderr).
func probeVersion(ctx context.Context, binPath string, args []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, binPath, args...)
	// SECURITY (env scrub — CRIT fix): this probe spawns a CALLER-SUPPLIED path
	// (the cli-validate create-parity endpoint reaches here), so it must NOT
	// inherit the full gateway env. Scrub to the runner env-allowlist: the CLI's
	// own credential env (ANTHROPIC_API_KEY, OPENAI_API_KEY, CLAUDE_CONFIG_DIR,
	// CODEX_HOME, …) is preserved so a legit `--version` handshake is unaffected,
	// but gateway secrets (OMNIPUS_MASTER_KEY, the bearer token, FLY_API_TOKEN, …)
	// are stripped — a `--version` probe cannot exfil them. This mirrors the
	// runtime dispatch path (external_dispatch.go, which also uses
	// sandbox.ScrubGatewayEnvForRunner) and additionally hardens the pre-existing
	// runner-test path.
	cmd.Env = sandbox.ScrubGatewayEnvForRunner()
	// Make the context timeout a HARD upper bound: WaitDelay caps how long Wait
	// blocks on still-open child I/O (pipes) after the deadline fires or the
	// process exits. Without it, a double-forking `--version` that leaves a
	// grandchild holding stdout/stderr open could pin the caller's in-flight
	// slot past the 15s context timeout. 3s is ample for a version banner to
	// flush after the process itself has been signaled.
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("version probe timed out after %s", versionProbeTimeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && text != "" {
			return "", fmt.Errorf("%w: %s", err, firstLine(text))
		}
		return "", err
	}
	v := extractVersion(text)
	if v == "" {
		return "", fmt.Errorf("could not parse version from output: %q", firstLine(text))
	}
	return v, nil
}

// extractVersion pulls a version-looking token out of `--version` output.
// CLIs print things like "claude 1.2.3", "codex-cli 0.4.0", "opencode v0.1.7".
// We return the first token that contains a digit and a dot, stripped of a
// leading 'v'. If none matches, return the whole first line (still a valid
// handshake signal — the binary ran).
func extractVersion(text string) string {
	if text == "" {
		return ""
	}
	line := firstLine(text)
	for _, tok := range strings.Fields(line) {
		t := strings.TrimPrefix(tok, "v")
		if strings.ContainsAny(t, "0123456789") && strings.Contains(t, ".") {
			return t
		}
	}
	return line
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// anyEnvSet reports whether any of the named env vars is set to a non-empty
// value.
func anyEnvSet(keys ...string) bool {
	for _, k := range keys {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

// claudeAuthFilePresent reports whether a Claude Code OAuth credential file
// exists under the config dir (CLAUDE_CONFIG_DIR override, else ~/.claude).
func claudeAuthFilePresent() bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".claude")
	}
	// Claude Code stores OAuth creds in .credentials.json under the config dir
	// (and historically a .claude.json at the home root). Accept either.
	candidates := []string{
		filepath.Join(dir, ".credentials.json"),
		filepath.Join(dir, "credentials.json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude.json"))
	}
	return anyFileNonEmpty(candidates...)
}

// opencodeAuthFilePresent reports whether opencode's auth.json exists. opencode
// stores credentials at ~/.local/share/opencode/auth.json (or under
// XDG_DATA_HOME when set).
func opencodeAuthFilePresent() bool {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return anyFileNonEmpty(filepath.Join(dataHome, "opencode", "auth.json"))
}

// anyFileNonEmpty reports whether any path exists and is a non-empty regular
// file.
func anyFileNonEmpty(paths ...string) bool {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// sortStrings is a tiny insertion sort to avoid importing sort for one small
// fixed slice in this file (the sandbox package already imports sort, but this
// package should not grow its import surface for a 3-element sort).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
