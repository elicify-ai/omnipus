// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED stubs — connected command-line programs, slice S1 (#1960).
//
// qa-lead RED scaffolding sanctioned by the dispatch brief ("add minimal
// empty stubs ONLY if the spec says where they live"). The spec names this
// package: invariant I-1 ("one function, programs.Attach(cmd, set), is the
// only way to add program variables to a child"), invariant I-3
// (program-scoped store methods "only ... called from pkg/programs") and
// FR-048. The masking writer (B0) and the catalog ("presets are data files")
// belong to the same domain. Types carry the CONTRACT schema names
// (ProgramEntry, ProgramGit, ProgramIdentityCheck, ProgramConnection,
// ProgramCustomRequest → CustomRequest).
//
// Symbol names the spec does not pin are inventions listed in
// /workspace/logs/1960/s1-red/test-disputes.md; GREEN may rename them and
// reconcile with qa-lead (same test author). Every behaviour-bearing body is
// EMPTY (no-op / zero value / passthrough), so the S1 tests fail with real
// expected-vs-actual instead of not compiling.

package programs

import (
	"io"
	"io/fs"
	"os/exec"
)

// ProgramEntry mirrors contract schema `ProgramEntry` (S1): the person-facing
// record of one connected program, preset or custom.
type ProgramEntry struct {
	ID             string                `json:"id"`
	Kind           string                `json:"kind"` // "preset" | "custom"
	DisplayName    string                `json:"display_name"`
	ProgramWords   []string              `json:"program_words"`
	TokenVariables []string              `json:"token_variables"`
	NetworkSites   []string              `json:"network_sites"`
	Git            *ProgramGit           `json:"git,omitempty"`
	IdentityCheck  *ProgramIdentityCheck `json:"identity_check,omitempty"`
	Revision       int                   `json:"revision"`
}

// ProgramGit mirrors contract schema `ProgramGit`: host is canonical
// (lower-case, no trailing dot, no port or scheme); username defaults to
// x-access-token.
type ProgramGit struct {
	Host     string `json:"host"`
	Username string `json:"username"`
}

// ProgramIdentityCheck mirrors contract schema `ProgramIdentityCheck`
// (FR-005): literal args, never shell-parsed; timeout 1–30 s (default 20);
// output capped at 4,096 bytes after masking.
type ProgramIdentityCheck struct {
	ProgramWord    string   `json:"program_word"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxOutputBytes int      `json:"max_output_bytes"`
}

// CustomRequest mirrors contract schema `ProgramCustomRequest` (the create and
// edit payload of a custom entry).
type CustomRequest struct {
	DisplayName    string                `json:"display_name"`
	ProgramWords   []string              `json:"program_words"`
	TokenVariables []string              `json:"token_variables"`
	NetworkSites   []string              `json:"network_sites"`
	Git            *ProgramGit           `json:"git,omitempty"`
	IdentityCheck  *ProgramIdentityCheck `json:"identity_check,omitempty"`
	Revision       int                   `json:"revision,omitempty"` // edit only
}

// ResolvedPrograms is the `set` of programs.Attach(cmd, set): the resolved
// environment additions and the token values to mask (I-1).
type ResolvedPrograms struct {
	// Env are the additional environment variables for the child
	// (name=value), never containing a token outside its own variable.
	Env []string
	// Tokens are every current or retired token value the child's output
	// must be masked against.
	Tokens []string
}

// CatalogFromFS loads the program catalog from preset data files
// (FR-001: "Presets are data files shipped in the binary"; S-37: adding one
// data file lists a preset with no Go change).
//
// RED stub: always empty.
func CatalogFromFS(fsys fs.FS) ([]ProgramEntry, error) {
	return nil, nil
}

// ValidateTokenValue enforces the program token rules (FR-003, dataset D-1):
// 8–16,384 bytes, surrounding whitespace trimmed, inner line breaks and NUL
// refused, empty refused. It returns the trimmed value to store.
//
// RED stub: passthrough, no rule.
func ValidateTokenValue(raw string) (string, error) {
	return raw, nil
}

// ValidateGit canonicalises and validates one custom entry's git section
// (contract `ProgramGit`): host canonical — lower-case, no trailing dot, no
// port or scheme; username ^[A-Za-z0-9._@-]{1,64}$, default x-access-token.
// It returns the canonical form to store.
//
// RED stub: passthrough, no rule, no default.
func ValidateGit(git ProgramGit) (ProgramGit, error) {
	return git, nil
}

// ValidateCustomRequest validates a create or edit of a custom entry against
// dataset D-2 and the scope boundaries (FR-019): display-name length,
// duplicate names (409 class), word count and banned shells, variable count
// and protected/reserved names, site rules, identity-check bounds and the
// 32-entry cap.
//
// RED stub: always accepts.
func ValidateCustomRequest(req CustomRequest, existing []ProgramEntry) error {
	return nil
}

// AccountLabel derives the account label from the MASKED first non-empty
// stdout line of the account check (FR-046): 1–128 printable characters; a
// line containing [FILTERED] or an 8-or-more-byte fragment of any current or
// retired token ends the check check_failed with the label-carries-token
// message and NO label.
//
// RED stub: always empty label, no error.
func AccountLabel(program, maskedOutput string, tokens []string) (string, error) {
	return "", nil
}

// NewMaskingWriter wraps w in the program masking writer of invariant I-1
// (B0): every maximal run of bytes covered by protected fragments — 8-or-more-
// byte substrings of any token in tokens — is replaced by one "[FILTERED]";
// at most 7 trailing bytes are held back, and Close releases only those ≤ 7
// bytes, never a longer held prefix.
//
// RED stub: passthrough writer (no masking) — the honest empty behaviour.
func NewMaskingWriter(w io.Writer, tokens []string) io.WriteCloser {
	return nopWriteCloser{w}
}

type nopWriteCloser struct{ w io.Writer }

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n nopWriteCloser) Close() error                { return nil }

// Attach adds program variables to a child and installs one masking writer
// per output stream (invariant I-1). It is the ONLY way to add program
// variables to a child; the spawn guard (S-106) enforces the call sites.
//
// RED stub: no-op.
func Attach(cmd *exec.Cmd, set ResolvedPrograms) {}
