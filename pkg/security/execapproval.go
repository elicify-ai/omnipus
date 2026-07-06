// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/policy"
)

// GlobMatchResult is the result of matching a command against an exec allowlist.
type GlobMatchResult struct {
	Allowed    bool
	PolicyRule string
}

// MatchExecAllowlist checks whether command matches any of the glob patterns.
//
// Patterns support a single '*' wildcard that matches any sequence of characters
// (including spaces). An empty or nil pattern list denies everything.
//
// This function implements per-binary exec allowlist matching per US-7 / US-5.
func MatchExecAllowlist(command string, patterns []string) GlobMatchResult {
	if len(patterns) == 0 {
		binary := policy.FirstToken(command)
		return GlobMatchResult{
			Allowed:    false,
			PolicyRule: fmt.Sprintf("binary %q not in exec allowlist (empty allowlist)", binary),
		}
	}

	for _, pat := range patterns {
		if policy.MatchGlob(pat, command) {
			return GlobMatchResult{Allowed: true}
		}
	}

	binary := policy.FirstToken(command)
	return GlobMatchResult{
		Allowed:    false,
		PolicyRule: fmt.Sprintf("binary %q not in exec allowlist", binary),
	}
}

// --------------------------------------------------------------------------
// ExecApprovalManager — interactive CLI approval for exec commands (US-7)
// --------------------------------------------------------------------------

// ExecApprovalMode controls when the approval prompt is shown.
type ExecApprovalMode string

const (
	// ExecApprovalModeAsk shows a CLI prompt for every exec command not in the
	// persistent allowlist. Safe default for interactive use.
	ExecApprovalModeAsk ExecApprovalMode = "ask"

	// ExecApprovalModeOff skips the approval prompt entirely.
	// Use in automated / headless deployments.
	ExecApprovalModeOff ExecApprovalMode = "off"
)

// ExecApprovalConfig configures the approval manager.
type ExecApprovalConfig struct {
	// Mode is "ask" (default) or "off".
	Mode string
}

// ApprovalResult is returned by CheckApproval.
type ApprovalResult struct {
	// AutoApproved is true when the command matched a persistent allowlist pattern
	// and no interactive prompt was required.
	AutoApproved bool

	// Approved is true when the command is allowed (either auto or interactively).
	Approved bool

	// PolicyRule explains how the approval decision was made.
	PolicyRule string
}

// allowlistFile is the structure persisted to exec-allowlist.json.
type allowlistFile struct {
	Patterns []string `json:"patterns"`
}

// ExecApprovalManager manages persistent exec allowlist patterns and
// provides interactive CLI prompts per US-7.
type ExecApprovalManager struct {
	mode     ExecApprovalMode
	mu       sync.RWMutex
	patterns []string // in-memory allowlist
	filePath string   // optional: path to exec-allowlist.json for persistence
	reader   *bufio.Reader
}

// NewExecApprovalManager creates an approval manager with the given config.
// AllowlistPath is optional: if non-empty the patterns are loaded from (and saved to)
// that file. If empty, patterns live in memory only.
func NewExecApprovalManager(cfg ExecApprovalConfig) *ExecApprovalManager {
	mode := ExecApprovalMode(cfg.Mode)
	if mode == "" {
		mode = ExecApprovalModeAsk
	}
	return &ExecApprovalManager{
		mode:   mode,
		reader: bufio.NewReader(os.Stdin),
	}
}

// WithAllowlistFile configures a file path for persistent allowlist storage.
// Loads existing patterns from the file if it exists.
func (m *ExecApprovalManager) WithAllowlistFile(path string) error {
	m.filePath = path
	return m.load()
}

// load reads patterns from m.filePath.
func (m *ExecApprovalManager) load() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("execapproval: read %s: %w", m.filePath, err)
	}
	var af allowlistFile
	if err := json.Unmarshal(data, &af); err != nil {
		return fmt.Errorf("execapproval: parse %s: %w", m.filePath, err)
	}
	m.mu.Lock()
	m.patterns = af.Patterns
	m.mu.Unlock()
	return nil
}

// save persists current patterns to m.filePath atomically.
func (m *ExecApprovalManager) save() {
	if m.filePath == "" {
		return
	}
	m.mu.RLock()
	af := allowlistFile{Patterns: append([]string{}, m.patterns...)}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		slog.Error("execapproval: marshal allowlist", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.filePath), 0o700); err != nil {
		slog.Error("execapproval: mkdir", "path", m.filePath, "error", err)
		return
	}
	// Atomic write via temp file + rename
	tmp := m.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		slog.Error("execapproval: write temp", "path", tmp, "error", err)
		return
	}
	if err := os.Rename(tmp, m.filePath); err != nil {
		slog.Error("execapproval: rename", "src", tmp, "dst", m.filePath, "error", err)
	}
}

// PersistPattern adds a glob pattern to the allowlist and (if configured) saves to disk.
// Duplicate patterns are ignored.
func (m *ExecApprovalManager) PersistPattern(pattern string) {
	m.mu.Lock()
	for _, p := range m.patterns {
		if p == pattern {
			m.mu.Unlock()
			return
		}
	}
	m.patterns = append(m.patterns, pattern)
	m.mu.Unlock()

	slog.Info("execapproval: pattern persisted", "pattern", pattern)
	m.save()
}

// CheckApproval checks whether command needs an approval prompt.
//
// If mode is "off", approval is always granted without prompting.
// If the command matches a persistent allowlist pattern, it is auto-approved.
// Otherwise (mode == "ask"), a blocking CLI prompt is shown.
func (m *ExecApprovalManager) CheckApproval(command string) ApprovalResult {
	if m.mode == ExecApprovalModeOff {
		return ApprovalResult{
			Approved:   true,
			PolicyRule: "exec approval disabled (mode: off)",
		}
	}

	// Check persistent allowlist
	m.mu.RLock()
	patterns := append([]string{}, m.patterns...)
	m.mu.RUnlock()

	for _, pat := range patterns {
		if matchAllowlistPattern(pat, command) {
			return ApprovalResult{
				AutoApproved: true,
				Approved:     true,
				PolicyRule:   fmt.Sprintf("persistent allowlist match: %q", pat),
			}
		}
	}

	// Interactive prompt
	return m.prompt(command)
}

// matchAllowlistPattern reports whether command is covered by a persisted
// approval pattern.
//
// C6 (exec-approval consent loss): a wildcard-free pattern is treated as a
// BINARY ALLOWANCE — it matches when it equals the command's first token
// (i.e. "this binary, with any arguments or none"). This is what a user means
// when they pick "always allow" for a command: the previous implementation
// persisted "<binary> *" and matched only via policy.MatchGlob, but the glob
// "git *" does NOT match the bare invocation "git" (the literal space before
// the star has nothing to consume). So a user who approved "always allow" for
// "git status" was re-prompted on the next bare "git", and a user who approved
// a no-argument command like "htop" was re-prompted every single time — the
// granted consent was silently lost.
//
// Persisting the bare binary token (e.g. "git") and matching it against the
// command's first token honors the consent for both "git" and "git status"
// without the over-broad "git*" form (which would also match "github").
// Patterns that DO contain a wildcard keep their existing glob semantics, so
// hand-written or legacy "git *" / "npm run *" entries still work — and, for
// back-compat, a legacy "<binary> *" entry now also covers the bare binary.
func matchAllowlistPattern(pattern, command string) bool {
	// Glob patterns (and exact-string patterns) keep their existing semantics.
	if policy.MatchGlob(pattern, command) {
		return true
	}
	// Wildcard-free pattern → binary allowance: match the command's first token.
	if !strings.ContainsAny(pattern, "*?") {
		return policy.FirstToken(pattern) == policy.FirstToken(command)
	}
	// Back-compat: a legacy "<binary> *" pattern should also cover the bare
	// binary invocation ("git" for a persisted "git *"), which plain MatchGlob
	// misses because the literal space cannot be consumed by the trailing star.
	if base, ok := strings.CutSuffix(pattern, " *"); ok {
		return policy.FirstToken(base) == policy.FirstToken(command)
	}
	return false
}

// prompt shows a CLI approval prompt and returns the user's decision.
func (m *ExecApprovalManager) prompt(command string) ApprovalResult {
	fmt.Printf("\n[Exec Approval Required]\n")
	fmt.Printf("Command: %s\n", command)
	// Keys: Enter or 'y' = allow once (default), 'a' = always allow, anything else = deny.
	// Do NOT lowercase before matching — 'y'/'a' are distinct single-char keys with
	// no ambiguity. (A previous scheme used 'A'/'a' but ToLower collapsed them.)
	fmt.Printf("[y]es once / [d]eny / [a]lways allow (y/d/a): ")

	line, err := m.reader.ReadString('\n')
	if err != nil {
		slog.Warn("execapproval: stdin read error", "error", err)
		return ApprovalResult{
			Approved:   false,
			PolicyRule: "exec approval: stdin read failed, denying for safety",
		}
	}
	line = strings.TrimSpace(line)

	switch strings.ToLower(line) {
	case "", "y": // Allow once (default — Enter or 'y')
		return ApprovalResult{
			Approved:   true,
			PolicyRule: "exec approval: allowed once by user",
		}
	case "a", "always", "always allow": // Always allow
		// C6: persist the bare binary token as a binary allowance. Matching
		// (matchAllowlistPattern) treats a wildcard-free pattern as "this
		// binary with any arguments or none", so this single entry covers both
		// the bare invocation ("git") and the argument form ("git status").
		// The previous "<binary> *" glob form silently failed to match the bare
		// invocation, re-prompting the user despite their "always allow" choice.
		binary := policy.FirstToken(command)
		m.PersistPattern(binary)
		return ApprovalResult{
			AutoApproved: true,
			Approved:     true,
			PolicyRule:   fmt.Sprintf("exec approval: always allow binary %q added", binary),
		}
	default: // 'd' or anything else → deny
		return ApprovalResult{
			Approved:   false,
			PolicyRule: "exec approval: denied by user",
		}
	}
}
