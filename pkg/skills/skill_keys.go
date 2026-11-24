// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

// Skills & Tools C-23: one known-skill-key rule for every writer of a skill
// permission (global PATCH, REST agent create and update, create_agent,
// set_agent_permissions).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const projectKeyPrefix = "project:"

// SkillKeyScope is what the key rule reads. AgentWorkspaces is nil for the
// global writer and the set of workspaces the agent is mounted on for a
// per-agent writer.
type SkillKeyScope struct {
	SkillsDir string
	// ProjectSlugs returns the project shelf of workspace W as skill keys
	// (lower case); ok is false when W does not exist.
	ProjectSlugs func(workspaceID string) (slugs []string, ok bool)
	// StoredGlobalKeys are the keys sandbox.skill_policies already holds.
	StoredGlobalKeys map[string]bool
	// AgentWorkspaces is nil for the global writer.
	AgentWorkspaces map[string]bool
}

// ErrUnknownSkillKey marks a key the rule refuses; its text is the 400 body.
var ErrUnknownSkillKey = errors.New("unknown skill key")

type skillKeyError struct{ text string }

func (e *skillKeyError) Error() string { return e.text }
func (e *skillKeyError) Unwrap() error { return ErrUnknownSkillKey }

// CheckSkillPolicyKey returns the stored form of key (installed ids lower
// case) or the refusal for an unknown key.
func CheckSkillPolicyKey(scope SkillKeyScope, key string) (string, error) {
	refuse := func() (string, error) {
		if scope.AgentWorkspaces == nil {
			return "", &skillKeyError{text: fmt.Sprintf("`%s` is not a known skill.", key)}
		}
		return "", &skillKeyError{text: fmt.Sprintf("`%s` is not a skill this agent can use.", key)}
	}
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || trimmed != key {
		return refuse()
	}
	if rest, ok := strings.CutPrefix(key, projectKeyPrefix); ok {
		ws, slug, found := strings.Cut(rest, "/")
		if !found || ws == "" || !ValidSlug(slug) {
			return refuse()
		}
		stored := projectKeyPrefix + ws + "/" + SkillKey(slug)
		if scope.AgentWorkspaces != nil && !scope.AgentWorkspaces[ws] {
			return refuse()
		}
		if scope.StoredGlobalKeys[stored] {
			return stored, nil
		}
		if scope.ProjectSlugs != nil {
			if slugs, ok := scope.ProjectSlugs(ws); ok {
				for _, s := range slugs {
					if SkillKey(s) == SkillKey(slug) {
						return stored, nil
					}
				}
			}
		}
		return refuse()
	}
	if !ValidSlug(key) && !ValidSlug(SkillKey(key)) {
		return refuse()
	}
	if IsBuiltin(key) {
		return SkillKey(key), nil
	}
	if scope.SkillsDir != "" {
		if _, present, err := ResolveInstalledSlug(scope.SkillsDir, key); err == nil && present {
			return SkillKey(key), nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("check skill %q: %w", key, err)
		}
	}
	return refuse()
}

// CheckSkillPolicyMap checks every key of a request map and refuses a skill
// named twice (letter case). It returns the map with stored keys.
func CheckSkillPolicyMap[V any](scope SkillKeyScope, in map[string]V) (map[string]V, error) {
	out := make(map[string]V, len(in))
	for key, v := range in {
		stored, err := CheckSkillPolicyKey(scope, key)
		if err != nil {
			return nil, err
		}
		if _, dup := out[stored]; dup {
			return nil, &skillKeyError{text: fmt.Sprintf("`%s` is listed twice.", key)}
		}
		out[stored] = v
	}
	return out, nil
}

// InstalledSkillKeys lists the installed skill folders under skillsDir as
// keys (built-ins included when present on disk).
func InstalledSkillKeys(skillsDir string) ([]string, error) {
	entries, err := os.ReadDir(skillsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || !ValidSlug(e.Name()) {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(skillsDir, e.Name(), "SKILL.md")); statErr == nil {
			out = append(out, SkillKey(e.Name()))
		}
	}
	return out, nil
}
