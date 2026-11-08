// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package thirdpartycli owns the person-set program path and standard
// parameters of the three external CLIs (Settings -> Third-party agents):
// the one exported path check every caller uses (the Settings save,
// cli-validate, every spawn, the connection check, the smoke test and the
// command preview) and the exact refusal texts (tool-parity spec r17 §3.5,
// §3.6, §3.11; T11 spec §3.2, founder Q28 A).
package thirdpartycli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// CLI keys, the values of the agent `cli` enum. One key set is used for the
// agent `cli`, the Settings rows and the error `field` (r17 §3.14 row 1).
const (
	CLIClaudeCode = "claude-code"
	CLICodex      = "codex"
	CLIOpenCode   = "opencode"
)

// CLIs lists the three CLI keys in the fixed validation order of a Settings
// save (r17 §3.4: claude-code, codex, opencode).
var CLIs = []string{CLIClaudeCode, CLICodex, CLIOpenCode}

// MaxPresetChars is the preset limit in characters (Unicode code points),
// never UTF-8 bytes (r17 §3.4).
const MaxPresetChars = 4096

// Check names carried by CheckError.Check and the error details.
const (
	CheckNotAbsolute           = "not_absolute"
	CheckNotFound              = "not_found"
	CheckNotRegularFile        = "not_regular_file"
	CheckNotExecutable         = "not_executable"
	CheckAgentWritableLocation = "agent_writable_location"
	CheckLocationUnverifiable  = "location_unverifiable"
	CheckWindowsExtension      = "windows_extension"
	CheckWindowsShortName      = "windows_short_name"
	CheckWindowsStream         = "windows_stream"

	CheckTooLong           = "too_long"
	CheckContainsNUL       = "contains_nul"
	CheckContainsLineBreak = "contains_line_break"
)

// pathReasons is the `<reason>` of the cli_path_invalid sentence (r17 §3.11).
var pathReasons = map[string]string{
	CheckNotAbsolute:           "it must be an absolute path",
	CheckNotFound:              "nothing exists at that path (or a link in it is broken)",
	CheckNotRegularFile:        "it is not a regular file",
	CheckNotExecutable:         "it is not executable for the Omnipus user",
	CheckAgentWritableLocation: "it is inside a folder agents can write to",
	CheckLocationUnverifiable:  locationUnverifiableReason,
	CheckWindowsExtension:      "it must end in .exe, .cmd or .bat",
	CheckWindowsShortName:      "short (8.3) names are not accepted; use the full name",
	CheckWindowsStream:         "alternate data streams are not accepted",
}

// locationUnverifiableReason names the real cause of a location_unverifiable
// refusal (founder Q37 A): the list of folders agents can write to was
// incomplete — a blank data folder, a workspace listing or mount record that
// could not be read, an unsafe workspace id, or a listed folder that could
// not be examined — not the program. The server log names the exact cause.
const (
	locationUnverifiableCause  = "the list of folders agents can write to could not be read in full"
	locationUnverifiableLog    = "Details are in the server log"
	locationUnverifiableReason = locationUnverifiableCause + ", so this location cannot be checked. " +
		locationUnverifiableLog
)

// presetReasons is the `<reason>` of the cli_preset_invalid sentence.
var presetReasons = map[string]string{
	CheckTooLong:           "they are longer than 4,096 characters",
	CheckContainsNUL:       "they contain a NUL character",
	CheckContainsLineBreak: "they must be one line",
}

// IsKnownCLI reports whether cli is one of the three CLI keys.
func IsKnownCLI(cli string) bool {
	for _, c := range CLIs {
		if c == cli {
			return true
		}
	}
	return false
}

// DisplayName is the name used in every text (r17 §3.6 item 6). An unknown
// key is returned unchanged so a text never loses the CLI it is about.
func DisplayName(cli string) string {
	switch cli {
	case CLIClaudeCode:
		return "Claude Code"
	case CLICodex:
		return "Codex"
	case CLIOpenCode:
		return "OpenCode"
	default:
		return cli
	}
}

// FilterName is the flag-filter key of a CLI: claude-code uses "claude"
// (r17 §3.7).
func FilterName(cli string) string {
	if cli == CLIClaudeCode {
		return "claude"
	}
	return cli
}

// CheckError is a failed path or preset check. Check is the check name, At
// the path element that failed (the link or the file; empty for a preset),
// Detail an optional operator-facing cause that never reaches a text.
type CheckError struct {
	CLI    string
	Check  string
	At     string
	Detail string
}

// Error implements error with the operator-facing summary.
func (e *CheckError) Error() string {
	if e.At == "" {
		return fmt.Sprintf("%s: check %s failed", e.CLI, e.Check)
	}
	return fmt.Sprintf("%s: check %s failed at %s", e.CLI, e.Check, e.At)
}

// IsPresetCheck reports whether the error came from ValidatePreset.
func (e *CheckError) IsPresetCheck() bool {
	_, ok := presetReasons[e.Check]
	return ok
}

// SaveRefusal is the exact `error` sentence of a refused Settings save
// (r17 §3.11 cli_path_invalid / cli_preset_invalid).
func (e *CheckError) SaveRefusal() string {
	if reason, ok := presetReasons[e.Check]; ok {
		return "The parameters for " + DisplayName(e.CLI) + " were not saved: " + reason + "."
	}
	return "The program path for " + DisplayName(e.CLI) + " was not saved: " + pathReasons[e.Check] + "."
}

// NoPathText is the run refusal when no program path is saved (r17 §3.11
// NoPath).
func NoPathText(cli string) string {
	name := DisplayName(cli)
	return "This agent cannot run: no program path is set for " + name +
		". A person can set it in Settings → Third-party agents → " + name + "."
}

// CheckFailedText is the run refusal when the saved path fails its checks
// (r17 §3.11 CheckFailed).
func CheckFailedText(cli, check, at string) string {
	if check == CheckLocationUnverifiable {
		// Moving or fixing the program would not help, so this refusal names
		// the real cause instead of "does not pass its checks".
		return "This agent cannot run: the location of the program set for " + DisplayName(cli) +
			" in Settings → Third-party agents cannot be checked (" + check + " at " + at + ") because " +
			locationUnverifiableCause + ". " + locationUnverifiableLog + "."
	}
	return "This agent cannot run: the program set for " + DisplayName(cli) +
		" in Settings → Third-party agents does not pass its checks (" + check + " at " + at +
		"). A person can fix it there."
}

// ValidatePreset applies the preset rules (r17 §3.4): at most 4,096
// characters, no NUL, no CR or LF. Any token is allowed; nothing is filtered.
func ValidatePreset(cli, preset string) error {
	switch {
	case utf8.RuneCountInString(preset) > MaxPresetChars:
		return &CheckError{CLI: cli, Check: CheckTooLong}
	case strings.ContainsRune(preset, 0):
		return &CheckError{CLI: cli, Check: CheckContainsNUL}
	case strings.ContainsAny(preset, "\r\n"):
		return &CheckError{CLI: cli, Check: CheckContainsLineBreak}
	}
	return nil
}
