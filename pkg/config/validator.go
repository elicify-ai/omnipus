// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// fr001RemovedKeysMsg is the exact error message required by.
const fr001RemovedKeysMsg = "config error: agents.defaults.restrict_to_workspace and " +
	"agents.defaults.allow_read_outside_workspace have been removed; remove these keys " +
	"(under the agents.defaults object) and use cfg.Tools.AllowReadPaths and " +
	"cfg.Tools.AllowWritePaths regex arrays for path-specific allow-listing"

// validateRemovedKeys parses raw JSON bytes and returns an error if the config
// contains either of the two keys removed by. The check fires for ANY
// value (true, false, null) — key presence is sufficient. Callers must invoke
// this BEFORE struct unmarshal so that the check is not bypassed by JSON
// tag renaming.
func validateRemovedKeys(data []byte) error {
	// Navigate agents.defaults to check for the removed keys.
	// Parse failures at each level are intentionally ignored: the main JSON
	// unmarshal step surfaces malformed input; this pre-check only inspects
	// specific keys when the structure is parseable.
	top := unmarshalMapSilent(data)
	if top == nil {
		return nil
	}

	agentsRaw, ok := top["agents"]
	if !ok {
		return nil
	}

	agents := unmarshalMapSilent(agentsRaw)
	if agents == nil {
		return nil
	}

	defaultsRaw, ok := agents["defaults"]
	if !ok {
		return nil
	}

	defaults := unmarshalMapSilent(defaultsRaw)
	if defaults == nil {
		return nil
	}

	_, hasRestrict := defaults["restrict_to_workspace"]
	_, hasAllowRead := defaults["allow_read_outside_workspace"]

	if hasRestrict || hasAllowRead {
		return fmt.Errorf(fr001RemovedKeysMsg)
	}

	return nil
}

// unmarshalMapSilent attempts to unmarshal JSON bytes into a
// map[string]json.RawMessage. Returns nil on any parse failure; callers
// use nil to skip optional key checks (the main unmarshal step surfaces
// malformed JSON).
func unmarshalMapSilent(data []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// inlineGroupRe matches inline flag groups such as (?i), (?s), (?m), (?-i).
// It intentionally uses [^:] to exclude named/non-capturing groups (?:...) and
// lookaheads/lookbehinds which Go's RE2 engine rejects anyway, but the
// character class catches all flag sequences before Go's compiler sees them.
var inlineGroupRe = regexp.MustCompile(`\(\?[^:]`)

// knownBadPaths returns the set of paths that no AllowReadPaths/AllowWritePaths
// regex may match (FR-002a / ). The authoritative source is the fixture
// at tests/security/fixtures/known_bad_paths.json; see that file's
// sibling README.md for schema and extension guidance.
//
// The fixture is the source of truth in checked-out source trees. The hardcoded
// fallback below is a defense-in-depth safety net for build artifacts that do
// not include the tests/ directory (e.g. published binaries running tests in
// embedded mode); it MUST stay in sync with the fixture's invariants:
//
//	(a) the empty-string sentinel is always present (catches ^.* / ^\w*),
//	(b) every credential-bearing dotfile under a known home/system root is
//	 represented, with at least one user-home variant per kind so patterns
//	 like ^/home/[^/]+/\.aws/ are caught.
//
// If the fixture exists but is malformed JSON, the fallback is used and a
// build-time test (TestKnownBadPaths_FixtureLoads) ensures this never silently
// happens in CI.
func knownBadPaths() []string {
	// Locate the fixture relative to the binary's source tree.
	_, file, _, ok := runtime.Caller(0)
	if ok {
		// Walk up from pkg/config/ to the repo root.
		root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		fixturePath := filepath.Join(root, "tests", "security", "fixtures", "known_bad_paths.json")
		if raw, err := os.ReadFile(fixturePath); err == nil {
			var paths []string
			if jsonErr := json.Unmarshal(raw, &paths); jsonErr == nil && len(paths) > 0 {
				return paths
			}
		}
	}
	// Fallback: identical invariants to the fixture, kept for missing-tests-dir
	// build artifacts. Any new entry in known_bad_paths.json that exercises a
	// new threat class SHOULD also appear here.
	return []string{
		"",
		"/etc/passwd",
		"/etc/shadow",
		"/etc/sudoers",
		"/etc/gshadow",
		"/root/.ssh/id_rsa",
		"/root/.ssh/id_ed25519",
		"/root/.ssh/authorized_keys",
		"/root/.aws/credentials",
		"/root/.git-credentials",
		"/root/.claude.json",
		"/root/.npmrc",
		"/home/operator/.ssh/id_rsa",
		"/home/operator/.ssh/id_ed25519",
		"/home/operator/.ssh/authorized_keys",
		"/home/operator/.aws/credentials",
		"/home/operator/.git-credentials",
		"/home/operator/.claude.json",
		"/home/operator/.npmrc",
		"/var/lib/omnipus/credentials.json",
		"/var/lib/omnipus/master.key",
	}
}

// validateAllowPaths validates a list of path regex patterns per FR-002a.
// Rules (,, per ):
// 1. Each entry must start with a literal '^'.
// 2. Each entry must NOT contain inline flag groups (e.g. (?i), (?s)).
// 3. Each entry must be ASCII-printable only (0x20–0x7E).
// 4. Each entry must compile as a valid Go regexp.
// 5. After compilation, the pattern must NOT match any path in the known-bad
// set (: coverage-based, not literal-text-based).
//
// fieldName is used in error messages (e.g. "cfg.Tools.AllowReadPaths").
func validateAllowPaths(patterns []string, fieldName string) error {
	bad := knownBadPaths()

	for _, pat := range patterns {
		// Rule 1: must start with '^'.
		if len(pat) == 0 || pat[0] != '^' {
			return fmt.Errorf("config error: %s entry %q must start with ^", fieldName, pat)
		}

		// Rule 2: no inline flag groups.
		if inlineGroupRe.MatchString(pat) {
			return fmt.Errorf(
				"config error: %s entry %q must not contain inline flag groups (e.g. (?i))",
				fieldName,
				pat,
			)
		}

		// Rule 3: ASCII-only printable chars.
		for i, ch := range pat {
			if ch < 0x20 || ch > 0x7E {
				return fmt.Errorf(
					"config error: %s entry %q contains non-ASCII-printable character at index %d (0x%02x)",
					fieldName,
					pat,
					i,
					ch,
				)
			}
		}

		// Rule 4: must compile.
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("config error: %s entry %q is not a valid regexp: %w", fieldName, pat, err)
		}

		// Rule 5: must not match any known-bad path.
		for _, badPath := range bad {
			if re.MatchString(badPath) {
				return fmt.Errorf(
					"config error: %s entry %q is too permissive (matches %q); "+
						"each entry must start with ^ and not match the known-bad set",
					fieldName, pat, badPath,
				)
			}
		}
	}

	return nil
}

// validateBootConfig validates the fully-loaded Config struct against the
// constraints added by the path-sandbox-and-capability-tiers.
// Called after struct unmarshal and env-override application.
func validateBootConfig(cfg *Config) error {
	// --- FR-002a: AllowReadPaths and AllowWritePaths validation ---
	if err := validateAllowPaths(cfg.Tools.AllowReadPaths, "cfg.Tools.AllowReadPaths"); err != nil {
		return err
	}
	if err := validateAllowPaths(cfg.Tools.AllowWritePaths, "cfg.Tools.AllowWritePaths"); err != nil {
		return err
	}

	// --- : Numeric sandbox field bounds ---

	// Apply default then validate MaxConcurrentDevServers.
	if cfg.Sandbox.MaxConcurrentDevServers == 0 {
		cfg.Sandbox.MaxConcurrentDevServers = 2
	}
	if cfg.Sandbox.MaxConcurrentDevServers < 1 || cfg.Sandbox.MaxConcurrentDevServers > 100 {
		return fmt.Errorf(
			"config error: cfg.Sandbox.MaxConcurrentDevServers=%d is out of range [1, 100]",
			cfg.Sandbox.MaxConcurrentDevServers,
		)
	}

	// Apply default then validate MaxConcurrentBuilds.
	if cfg.Sandbox.MaxConcurrentBuilds == 0 {
		cfg.Sandbox.MaxConcurrentBuilds = 2
	}
	if cfg.Sandbox.MaxConcurrentBuilds < 1 || cfg.Sandbox.MaxConcurrentBuilds > 100 {
		return fmt.Errorf(
			"config error: cfg.Sandbox.MaxConcurrentBuilds=%d is out of range [1, 100]",
			cfg.Sandbox.MaxConcurrentBuilds,
		)
	}

	// Apply default then validate BuildStatic.TimeoutSeconds.
	if cfg.Tools.BuildStatic.TimeoutSeconds == 0 {
		cfg.Tools.BuildStatic.TimeoutSeconds = 300
	}
	if cfg.Tools.BuildStatic.TimeoutSeconds < 1 || cfg.Tools.BuildStatic.TimeoutSeconds > 3600 {
		return fmt.Errorf(
			"config error: cfg.Tools.BuildStatic.TimeoutSeconds=%d is out of range [1, 3600]",
			cfg.Tools.BuildStatic.TimeoutSeconds,
		)
	}

	// Apply default then validate BuildStatic.MemoryLimitBytes.
	if cfg.Tools.BuildStatic.MemoryLimitBytes == 0 {
		cfg.Tools.BuildStatic.MemoryLimitBytes = 536870912 // 512 MiB
	}
	const memMin uint64 = 67108864   // 64 MiB
	const memMax uint64 = 4294967295 // ~4 GiB
	if cfg.Tools.BuildStatic.MemoryLimitBytes < memMin || cfg.Tools.BuildStatic.MemoryLimitBytes > memMax {
		return fmt.Errorf(
			"config error: cfg.Tools.BuildStatic.MemoryLimitBytes=%d is out of range [%d, %d]",
			cfg.Tools.BuildStatic.MemoryLimitBytes, memMin, memMax,
		)
	}

	// Apply defaults for DevServerPortRange if unset, then validate.
	// FR-024 / type-design F-24: reject malformed ranges (min>max, out-of-bounds)
	// at boot rather than at first web_serve dev-mode tool call.
	if cfg.Sandbox.DevServerPortRange.IsZero() {
		cfg.Sandbox.DevServerPortRange = PortRange{18000, 18999}
	}
	if err := cfg.Sandbox.DevServerPortRange.Validate(); err != nil {
		return err
	}

	// Apply defaults for ServeWorkspace durations.
	if cfg.Tools.ServeWorkspace.MaxDurationSeconds == 0 {
		cfg.Tools.ServeWorkspace.MaxDurationSeconds = 86400 // 24 h
	}
	if cfg.Tools.ServeWorkspace.MinDurationSeconds == 0 {
		cfg.Tools.ServeWorkspace.MinDurationSeconds = 60
	}

	// Apply default EgressAllowList.
	if len(cfg.Sandbox.EgressAllowList) == 0 {
		cfg.Sandbox.EgressAllowList = []string{
			"registry.npmjs.org",
			"*.npmjs.org",
			"*.npmjs.com",
			"github.com",
			"raw.githubusercontent.com",
			"objects.githubusercontent.com",
			"nodejs.org",
		}
	}

	// Default workspace_shell_enabled to false (deny-by-default per hard constraint #6).
	// Absent key → false. Operators who want the tool must explicitly opt in:
	// {"sandbox": {"experimental": {"workspace_shell_enabled": true}}}.
	// Jim (core agent) also flips this to true in coreagent.SeedConfig.
	if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
		f := false
		cfg.Sandbox.Experimental.WorkspaceShellEnabled = &f
	}

	// Validate Tier3Commands: each entry must have ≥2 non-empty tokens after
	// strings.Fields. The baseline allow-list always uses "binary subcommand"
	// format (e.g. "next dev", "vite dev"). A single-token entry like "node"
	// would allow "node anything.js" — that is too broad and is never the
	// operator's intent. Reject at config-load time so the error surfaces at
	// boot rather than silently during a first Tier 3 invocation.
	//
	// Empty-string entries are also rejected: they are almost certainly a
	// config authoring mistake (e.g. a trailing comma in a JSON array).
	for i, entry := range cfg.Sandbox.Tier3Commands {
		tokens := strings.Fields(entry)
		switch {
		case len(tokens) == 0:
			return fmt.Errorf(
				"config error: cfg.Sandbox.Tier3Commands[%d] is empty or all-whitespace — remove or replace it",
				i,
			)
		case len(tokens) == 1:
			return fmt.Errorf(
				"config error: cfg.Sandbox.Tier3Commands[%d]=%q has only one token %q; "+
					"entries must specify \"binary subcommand\" (≥2 tokens, e.g. \"remix dev\")",
				i, entry, tokens[0],
			)
		}
	}

	// --- : AuthMismatchLogLevel ---
	if cfg.Gateway.AuthMismatchLogLevel == "" {
		cfg.Gateway.AuthMismatchLogLevel = "warn"
	}
	switch cfg.Gateway.AuthMismatchLogLevel {
	case "debug", "info", "warn":
		// valid
	default:
		return fmt.Errorf(
			"config error: cfg.Gateway.AuthMismatchLogLevel=%q is invalid; must be one of: debug, info, warn",
			cfg.Gateway.AuthMismatchLogLevel,
		)
	}

	return nil
}
