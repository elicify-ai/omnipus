// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

// executor_settings.go — the external worker's own settings and its read
// model (tool-parity spec r17 §3.8, §3.11, §7.5 AG-04/AG-08..AG-13, §7.6
// T-EXEC). REST and the agent tools validate env_overrides and cli_args with
// the same rules and read the program status with the same function, so the
// two surfaces cannot drift.

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// EnvMask replaces every env_overrides value on every read (EX-01).
const EnvMask = "••••••"

const (
	maxEnvEntries   = 64
	maxEnvNameChars = 128
	maxEnvValChars  = 8192
	// MaxCLIArgsChars bounds executor.cli_args (EX-04).
	MaxCLIArgsChars = 4096
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Executor field refusals for a REST agent body (r17 §3.11, §7.11), keyed by
// the executor property a client may not send.
const (
	KindRefusalText    = "executor.kind is derived from the agent type and cannot be set."
	CLILockedText      = "executor.cli is locked after create; create a new agent to switch CLIs."
	CLIPathRefusalText = "cli_path cannot be set on an agent. The program for the chosen CLI is set by a person " +
		"in Settings → Third-party agents."
)

// NativeRuntimeRefusalText is the r17 §3.9 reason the executor settings are
// not editable on an agent that is not an external-CLI worker.
const NativeRuntimeRefusalText = "Only external-CLI workers have a runtime environment."

// ExecutorFieldRefusal returns the §3.11/§7.11 refusal text for an executor
// property a client may not send, or "" for any other property.
func ExecutorFieldRefusal(field string) string {
	switch field {
	case "kind":
		return KindRefusalText
	case "cli":
		return CLILockedText
	case "cli_path":
		return CLIPathRefusalText
	}
	return ""
}

// refusalError is an error whose message is a normative sentence that must be
// returned byte for byte (including its closing full stop).
type refusalError string

func (r refusalError) Error() string { return string(r) }

// ValidateEnvName checks one env_overrides name: the pattern, the length and
// the reserved OMNIPUS_ prefix.
func ValidateEnvName(name string) error {
	if strings.HasPrefix(strings.ToUpper(name), "OMNIPUS_") {
		return fmt.Errorf("env_overrides key %q is reserved: OMNIPUS_* is gateway-internal", name)
	}
	if !envNamePattern.MatchString(name) || utf8.RuneCountInString(name) > maxEnvNameChars {
		return fmt.Errorf("env_overrides key %q must match ^[A-Za-z_][A-Za-z0-9_]*$ and be at most %d characters",
			name, maxEnvNameChars)
	}
	return nil
}

// ValidateEnvOverrides checks a whole env_overrides map (create, or the
// merged result of an update): at most 64 entries, each name valid, each
// value at most 8,192 characters and never the read mask. Names are checked
// in sorted order so a refusal is deterministic.
func ValidateEnvOverrides(env map[string]string) error {
	if len(env) > maxEnvEntries {
		return fmt.Errorf("env_overrides has %d entries; at most %d are allowed", len(env), maxEnvEntries)
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ValidateEnvName(name); err != nil {
			return err
		}
		value := env[name]
		if value == EnvMask {
			// The §7.11 text ends with a full stop, byte for byte.
			return refusalError(fmt.Sprintf(
				"env_overrides value for %q is the masked placeholder; send the real value or leave the name out.", name))
		}
		if utf8.RuneCountInString(value) > maxEnvValChars {
			return fmt.Errorf("env_overrides value for %q is longer than %d characters", name, maxEnvValChars)
		}
	}
	return nil
}

// maxEnvRemoveNames bounds env_overrides_remove (EX-03).
const maxEnvRemoveNames = 64

// EnvMergeOutcome names what an update did to the stored environment, by
// outcome; every list is sorted and never nil. It carries names only.
type EnvMergeOutcome struct {
	Added, Replaced, Removed, AbsentRemoved []string
}

// MergeEnvOverrides applies an update's env_overrides (merge: each supplied
// name added or replaced, {} a no-op) and env_overrides_remove (names
// removed; a name not stored is reported, not refused) to the stored map
// and validates the merged result with the create rules (EX-02, EX-03,
// FR-061). A name both set and removed is refused. stored is never
// modified; the merged map is nil when it ends empty.
func MergeEnvOverrides(stored, set map[string]string, remove []string) (map[string]string, EnvMergeOutcome, error) {
	out := EnvMergeOutcome{Added: []string{}, Replaced: []string{}, Removed: []string{}, AbsentRemoved: []string{}}
	if len(remove) > maxEnvRemoveNames {
		return nil, out, fmt.Errorf("env_overrides_remove has %d names; at most %d are allowed", len(remove), maxEnvRemoveNames)
	}
	removeSorted := append([]string(nil), remove...)
	sort.Strings(removeSorted)
	for _, name := range removeSorted {
		if _, both := set[name]; both {
			return nil, out, refusalError(fmt.Sprintf("env_overrides and env_overrides_remove both name %q.", name))
		}
	}
	// The supplied names are checked before any merge work, so a refusal
	// names the caller's own key and an oversized set (caller input) is
	// refused before it sizes anything; the merged whole is checked against
	// the entry limit last.
	if err := ValidateEnvOverrides(set); err != nil {
		return nil, out, err
	}
	// The capacity is a hint bounded by the entry limit, never by caller input.
	merged := make(map[string]string, maxEnvEntries)
	for name, value := range stored {
		merged[name] = value
	}
	for _, name := range removeSorted {
		if _, ok := merged[name]; ok {
			delete(merged, name)
			out.Removed = append(out.Removed, name)
		} else {
			out.AbsentRemoved = append(out.AbsentRemoved, name)
		}
	}
	setNames := make([]string, 0, len(set))
	for name := range set {
		setNames = append(setNames, name)
	}
	sort.Strings(setNames)
	for _, name := range setNames {
		if _, ok := merged[name]; ok {
			out.Replaced = append(out.Replaced, name)
		} else {
			out.Added = append(out.Added, name)
		}
		merged[name] = set[name]
	}
	if err := ValidateEnvOverrides(merged); err != nil {
		return nil, out, err
	}
	if len(merged) == 0 {
		merged = nil
	}
	return merged, out, nil
}

// ValidateCLIArgs checks executor.cli_args: one string of at most 4,096
// characters, stored as typed (EX-04).
func ValidateCLIArgs(args string) error {
	if utf8.RuneCountInString(args) > MaxCLIArgsChars {
		return fmt.Errorf("cli_args must be at most %d characters", MaxCLIArgsChars)
	}
	return nil
}

// MaskEnv returns the read view of env_overrides: the names, each value
// replaced by EnvMask. A nil or empty map returns nil.
func MaskEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for name := range env {
		out[name] = EnvMask
	}
	return out
}

// rootsIncomplete, as an entry of an agent-writable list, says the list could
// not be completed, so ValidateProgramPath refuses every path. A missing root
// would otherwise silently turn "refuse" into "allow". NUL cannot occur in a
// real path, so no real entry can equal it.
const rootsIncomplete = "\x00agent-writable-roots-incomplete"

// AgentWritableRoots lists the agent-writable locations a program path must
// stay outside beyond $OMNIPUS_HOME: the gateway's home folder (every
// workspace root lives under it), the sandbox's read+write grants
// (sandbox.allowed_paths) and the host folder of every workspace mount (T11
// §5). It is read on every call, so a mount added after a program was saved
// counts at the next check. The Settings save, every run, the read model and
// the agent tools all use this one list. When the mounts cannot be listed
// completely the failure is logged and the list carries rootsIncomplete.
func AgentWritableRoots(home string, cfg *config.Config) []string {
	out := []string{home}
	if cfg != nil {
		out = append(out, cfg.Sandbox.AllowedPaths...)
	}
	mounts, err := workspaceMountRoots(home)
	if err != nil {
		slog.Error("third-party program check: every program path is refused until this is fixed", "error", err)
		return append(out, rootsIncomplete)
	}
	return append(out, mounts...)
}

// workspaceMountRoots returns the host folder of every mount of every
// workspace, or an error when a workspace listing or a mount record cannot be
// read in full.
func workspaceMountRoots(home string) ([]string, error) {
	if strings.TrimSpace(home) == "" {
		home = config.OmnipusHomeDir()
	}
	ids, err := workspace.ListWorkspaceIDs(home)
	if err != nil {
		return nil, fmt.Errorf("the workspaces could not be listed: %w", err)
	}
	var roots []string
	for _, id := range ids {
		mounts, ok, dropped := workspace.LoadMountsWithDropStatus(home, id)
		if !ok || dropped {
			return nil, fmt.Errorf("the mount record of workspace %q could not be read in full", id)
		}
		for _, m := range mounts {
			roots = append(roots, m.HostPath)
		}
	}
	return roots, nil
}

// PresetUseNote is the plain-text note on the audit entry of every run or test
// message that applies preset parameters (T11 §4 Audit).
const PresetUseNote = "person preset used; flag filter bypassed for preset parameters"

// PresetAuditWithheld stands in for preset text in an audit entry when the
// configuration is missing: the entry says so rather than holding clear text.
const PresetAuditWithheld = "[not recorded: the sensitive-data filter is unavailable]"

// PresetAuditFilter returns the function preset text goes through on its way
// into an audit entry (T11 §4 Audit). It obeys tools.filter_sensitive_data the
// way pkg/agent's filterExternalString does: switch on, the config's
// sensitive-data replacer with no short-content fast path; switch off, the text
// as typed. Only the audit copy is filtered; the worker gets the preset as
// typed. With no config the switch cannot be read, so nothing is known to allow
// clear text: every non-empty value becomes PresetAuditWithheld and the failure
// is logged. The same applies to a missing replacer with the switch on.
func PresetAuditFilter(cfg *config.Config) func(string) string {
	if cfg != nil && !cfg.Tools.IsFilterSensitiveDataEnabled() {
		return func(s string) string { return s }
	}
	var replacer *strings.Replacer
	if cfg != nil {
		replacer = cfg.SensitiveDataReplacer()
	}
	if replacer == nil {
		slog.Error("audit: sensitive-data filter unavailable; preset text withheld from the audit entry")
		return func(s string) string {
			if s == "" {
				return ""
			}
			return PresetAuditWithheld
		}
	}
	return replacer.Replace
}

// ProgramWarning returns the exact text a run of an external worker on cli
// would be refused with right now, given the program path a person saved in
// Settings, or "" when a run would start (r17 §3.8, §3.11). The path is
// checked with the same ValidateProgramPath the Settings save and the spawn
// use.
func ProgramWarning(cli, settingsPath string, agentWritable ...string) string {
	_, refusal := RunProgram(cli, settingsPath, agentWritable...)
	return refusal
}

// RunProgram is the spawn-time program decision (r17 §3.6): with no saved
// path the refusal is NoPath; otherwise ValidateProgramPath runs again and a
// failure is CheckFailed. On success resolvedPath is the checked file, the
// one the driver must exec. There is no fallback to any other program.
func RunProgram(cli, settingsPath string, agentWritable ...string) (resolvedPath, refusal string) {
	if strings.TrimSpace(settingsPath) == "" {
		return "", NoPathText(cli)
	}
	resolved, err := ValidateProgramPath(cli, settingsPath, agentWritable...)
	if err != nil {
		var ce *CheckError
		if errors.As(err, &ce) {
			return "", CheckFailedText(cli, ce.Check, ce.At)
		}
		return "", CheckFailedText(cli, CheckNotFound, settingsPath)
	}
	return resolved, ""
}
