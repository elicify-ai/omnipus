// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"fmt"
	"slices"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// referencePrefix starts the credential-store reference of every program
// token. One reference per entry: an entry has one credential per
// installation (FR-006), handed to every token variable of the entry.
const referencePrefix = credentials.ProgramReferencePrefix

// TokenReference returns the credential-store reference of an entry's token.
func TokenReference(id string) string { return referencePrefix + id }

// EntryIDForReference returns the entry id of a program token reference and
// true, or "" and false when ref is not one.
func EntryIDForReference(ref string) (string, bool) {
	id, ok := strings.CutPrefix(ref, referencePrefix)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// Entries returns every program entry of cfg: the embedded presets, then the
// person's custom entries in their stored order. A stored entry whose kind is
// not custom is ignored: presets are data in the catalog, never config.
func Entries(cfg *config.Config) ([]ProgramEntry, error) {
	presets, err := Presets()
	if err != nil {
		return nil, err
	}
	out := append([]ProgramEntry(nil), presets...)
	if cfg == nil {
		return out, nil
	}
	for _, c := range cfg.Programs.Entries {
		if c.Kind != KindCustom {
			continue
		}
		out = append(out, entryFromConfig(c))
	}
	return out, nil
}

// Lookup returns the entry with the given id.
func Lookup(cfg *config.Config, id string) (ProgramEntry, bool, error) {
	entries, err := Entries(cfg)
	if err != nil {
		return ProgramEntry{}, false, err
	}
	for _, e := range entries {
		if e.ID == id {
			return e, true, nil
		}
	}
	return ProgramEntry{}, false, nil
}

// OwnerName names the program that owns a credential reference, for the
// program-owned refusal "<reference> belongs to the program <name>. …". It
// returns "" when the reference is not a known program's token reference.
func OwnerName(cfg *config.Config, ref string) string {
	id, ok := EntryIDForReference(ref)
	if !ok {
		return ""
	}
	if e, found, err := Lookup(cfg, id); err == nil && found {
		return e.DisplayName
	}
	return ""
}

// ProgramOwnedMessage is the refusal of a generic credential writer aimed at
// a program's stored reference (FR-048, message table).
func ProgramOwnedMessage(cfg *config.Config, ref string) string {
	name := OwnerName(cfg, ref)
	if name == "" {
		name = "that stored it"
	}
	return ref + " belongs to the program " + name + ". Change it in Settings → Programs."
}

func entryFromConfig(c config.ProgramEntryConfig) ProgramEntry {
	e := ProgramEntry{
		ID:             c.ID,
		Kind:           KindCustom,
		DisplayName:    c.DisplayName,
		ProgramWords:   append([]string(nil), c.ProgramWords...),
		TokenVariables: append([]string(nil), c.TokenVariables...),
		NetworkSites:   append([]string(nil), c.NetworkSites...),
		Revision:       int(c.Revision),
	}
	if c.Git != nil {
		e.Git = &ProgramGit{Host: c.Git.Host, Username: c.Git.Username}
	}
	if c.IdentityCheck != nil {
		e.IdentityCheck = &ProgramIdentityCheck{
			ProgramWord:    c.IdentityCheck.ProgramWord,
			Args:           append([]string(nil), c.IdentityCheck.Args...),
			TimeoutSeconds: c.IdentityCheck.TimeoutSeconds,
			MaxOutputBytes: c.IdentityCheck.MaxOutputBytes,
		}
	}
	return e
}

// ConfigFromRequest builds the stored custom entry from a normalized request.
func ConfigFromRequest(id string, req CustomRequest, revision int64) config.ProgramEntryConfig {
	c := config.ProgramEntryConfig{
		ID:             id,
		Kind:           KindCustom,
		DisplayName:    req.DisplayName,
		ProgramWords:   append([]string(nil), req.ProgramWords...),
		TokenVariables: append([]string(nil), req.TokenVariables...),
		NetworkSites:   append([]string(nil), req.NetworkSites...),
		Revision:       revision,
	}
	if req.Git != nil {
		c.Git = &config.ProgramGitConfig{Host: req.Git.Host, Username: req.Git.Username}
	}
	if req.IdentityCheck != nil {
		c.IdentityCheck = &config.ProgramIdentityCheckConfig{
			ProgramWord:    req.IdentityCheck.ProgramWord,
			Args:           append([]string(nil), req.IdentityCheck.Args...),
			TimeoutSeconds: req.IdentityCheck.TimeoutSeconds,
			MaxOutputBytes: req.IdentityCheck.MaxOutputBytes,
		}
	}
	return c
}

// NewCustomID derives a new entry id from a display name: lower-case letters
// and digits joined by "-", made unique against existing ids.
func NewCustomID(displayName string, existing []ProgramEntry) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(displayName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "program"
	}
	if len(base) > 48 {
		base = strings.Trim(base[:48], "-")
	}
	taken := func(id string) bool {
		return slices.ContainsFunc(existing, func(e ProgramEntry) bool { return e.ID == id })
	}
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

// EnabledAgentIDs lists the agents that have the entry switched on, in
// roster order.
func EnabledAgentIDs(cfg *config.Config, id string) []string {
	out := []string{}
	if cfg == nil {
		return out
	}
	for _, a := range cfg.Agents.List {
		if slices.Contains(a.Programs, id) {
			out = append(out, a.ID)
		}
	}
	return out
}

// AgentName returns the agent's display name, or its id.
func AgentName(cfg *config.Config, agentID string) string {
	if cfg != nil {
		for _, a := range cfg.Agents.List {
			if a.ID == agentID && a.Name != "" {
				return a.Name
			}
		}
	}
	return agentID
}
