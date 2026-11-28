// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package programs is the connected command-line programs feature (#1960,
// spec docs/internal/specs/cli-credentials-s1-s2-spec.md, slice S1): the
// program catalog (presets as data files plus custom entries), the token and
// entry validation rules, the account label rule, the source masking writer
// and programs.Attach, the only way to add program variables to a child
// (invariant I-1).
package programs

// ProgramEntry is the stored definition of one program, preset or custom.
// It is the catalog record; the connection state, switches and revision of
// the REST view are composed by the gateway from the generated wire type.
type ProgramEntry struct {
	ID             string                `json:"id"`
	Kind           string                `json:"kind"` // KindPreset | KindCustom
	DisplayName    string                `json:"display_name"`
	ProgramWords   []string              `json:"program_words"`
	TokenVariables []string              `json:"token_variables"`
	NetworkSites   []string              `json:"network_sites"`
	Git            *ProgramGit           `json:"git,omitempty"`
	IdentityCheck  *ProgramIdentityCheck `json:"identity_check,omitempty"`
	Revision       int                   `json:"revision"`
	// CheckRevision increments only when a check-relevant field changes
	// (identity_check or token_variables, spec R3-M3).
	CheckRevision int `json:"check_revision,omitempty"`
}

// Entry kinds.
const (
	KindPreset = "preset"
	KindCustom = "custom"
)

// ProgramGit is the git site of an entry: canonical host and the username
// git sends with the token (default x-access-token).
type ProgramGit struct {
	Host     string `json:"host"`
	Username string `json:"username"`
}

// ProgramIdentityCheck is the account check (FR-005): literal args, never
// shell-parsed; timeout 1–30 s (default 20); output capped at 4,096 bytes.
type ProgramIdentityCheck struct {
	ProgramWord    string   `json:"program_word"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxOutputBytes int      `json:"max_output_bytes"`
}

// CustomRequest is the create or edit payload of a custom entry.
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
	// (name=value).
	Env []string
	// Tokens are every current or retired token value the child's output
	// must be masked against.
	Tokens []string
}

// Fixed bounds of the identity check (contract ProgramIdentityCheck).
const (
	DefaultCheckTimeoutSeconds = 20
	MaxCheckTimeoutSeconds     = 30
	CheckMaxOutputBytes        = 4096
)
