// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// ProgramsConfig is the connected command-line programs section of
// config.json (connected-programs spec, S1). Preset entries come from the
// embedded catalog (pkg/programs/presets); Entries holds the person's custom
// entries. Tokens are never stored here: they live in the credential store as
// program-owned entries (invariant I-3).
type ProgramsConfig struct {
	// Entries are the custom entries ("kind": "custom"). An entry with kind
	// "preset" is ignored: presets are data in the embedded catalog.
	Entries []ProgramEntryConfig `json:"entries,omitempty"`
	// WebChatAllowed lets turns that come from the web chat, or from no
	// starting message at all, use connected programs (founder Q1 = A).
	// Absent = false.
	WebChatAllowed bool `json:"web_chat_allowed,omitempty"`
}

// ProgramEntryConfig is one stored program entry.
type ProgramEntryConfig struct {
	ID             string                      `json:"id"`
	Kind           string                      `json:"kind"`
	DisplayName    string                      `json:"display_name"`
	ProgramWords   []string                    `json:"program_words"`
	TokenVariables []string                    `json:"token_variables"`
	NetworkSites   []string                    `json:"network_sites,omitempty"`
	Git            *ProgramGitConfig           `json:"git,omitempty"`
	IdentityCheck  *ProgramIdentityCheckConfig `json:"identity_check,omitempty"`
	// Revision increments on every edit of the entry (optimistic
	// concurrency of edits only; FR-043).
	Revision int64 `json:"revision,omitempty"`
}

// ProgramGitConfig is a program's git login (host and username).
type ProgramGitConfig struct {
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
}

// ProgramIdentityCheckConfig is a program's account check recipe.
type ProgramIdentityCheckConfig struct {
	ProgramWord    string   `json:"program_word"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
}
