package agent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// SoulDefinition represents the resolved SOUL.md file linked to the agent.
type SoulDefinition struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// UserDefinition represents the resolved USER.md file — the user's own profile.
//
// It is GLOBAL: one file for the whole installation, the same for every agent
// and every workspace. See config.UserProfilePath. The previous comment said
// "linked to the workspace", which read as the user-facing Workspace but meant
// the agent's own directory — and that ambiguity is what kept this file from
// reaching any agent.
type UserDefinition struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// AgentContextDefinition captures the agent's soul and the global user profile.
type AgentContextDefinition struct {
	Soul *SoulDefinition `json:"soul,omitempty"`
	User *UserDefinition `json:"user,omitempty"`
}

// LoadAgentDefinition loads the agent's SOUL.md and the global USER.md profile.
func (cb *ContextBuilder) LoadAgentDefinition() AgentContextDefinition {
	return loadAgentDefinition(cb.workspace)
}

func loadAgentDefinition(workspace string) AgentContextDefinition {
	definition := AgentContextDefinition{}
	// USER.md is deliberately NOT resolved from `workspace`. It describes the
	// user, so it is global — see config.ReadUserProfile.
	definition.User = loadUserDefinition()

	soulPath := filepath.Join(workspace, "SOUL.md")
	if content, err := os.ReadFile(soulPath); err != nil {
		// M8: distinguish permission/IO errors from expected absence.
		if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF("agent", "Could not read SOUL.md",
				map[string]any{"path": soulPath, "error": err.Error()})
		}
	} else {
		definition.Soul = &SoulDefinition{
			Path:    soulPath,
			Content: string(content),
		}
	}

	return definition
}

func loadUserDefinition() *UserDefinition {
	path, content, err := config.ReadUserProfile()
	if err != nil {
		logger.WarnCF("agent", "Could not read USER.md",
			map[string]any{"path": config.UserProfilePath(), "error": err.Error()})
		return nil
	}
	if path == "" {
		return nil // no profile yet — normal on a fresh install
	}
	return &UserDefinition{Path: path, Content: content}
}

func relativeWorkspacePath(workspace, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	relativePath, err := filepath.Rel(workspace, path)
	if err == nil && relativePath != "." && !strings.HasPrefix(relativePath, "..") {
		return filepath.ToSlash(relativePath)
	}
	return filepath.Clean(path)
}

func uniquePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if slices.Contains(result, cleaned) {
			continue
		}
		result = append(result, cleaned)
	}
	return result
}
