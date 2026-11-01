// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills spec r15 §6.1 item 1: the turn's skill authority. ResolveTurnFSPolicy
// is the only place HomeGuard.SkillFolders (the real paths of the agent's
// loadable set) and HomeGuard.SkillsReadAll (effective edit_skill ≠ deny) are
// filled. Both are read from the persisted agent record and the global
// ceiling on disk, so every path-taking tool, grep and the bash checks see
// the same answer the loader and edit_skill see.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// turnSkillAuthority is the per-turn skill read authority of one agent.
type turnSkillAuthority struct {
	folders []string
	readAll bool
}

// resolveTurnSkillAuthority computes the authority for agentID under home.
// It fails closed: no agent id, no persisted record, or a record, ceiling or
// skill stamp that cannot be read all yield no authority, so no skill folder
// is readable this turn and every skill path is refused by the predicate.
// The cause is logged with the agent id; it never widens access and it does
// not break the agent's ordinary file access outside skills.
func resolveTurnSkillAuthority(home, agentID string) turnSkillAuthority {
	if home == "" || agentID == "" {
		return turnSkillAuthority{}
	}
	rec, err := agentstore.New(home).Get(agentID)
	if err != nil {
		if !errors.Is(err, entity.ErrNotFound) {
			logger.WarnCF("tools", "skills authority: agent record unreadable; no skill folder is readable this turn",
				map[string]any{"agent_id": agentID, "error": err.Error()})
		}
		return turnSkillAuthority{}
	}
	ceiling, err := readGlobalToolCeiling(home)
	if err != nil {
		logger.WarnCF("tools", "skills authority: tool ceiling unreadable; no skill folder is readable this turn",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return turnSkillAuthority{}
	}
	pol := &ToolPolicyCfg{GlobalPolicies: ceiling}
	if rec.Tools != nil && len(rec.Tools.Builtin.Policies) > 0 {
		pol.Policies = rec.Tools.Builtin.Policies
	}
	folders, err := skills.LoadableFolders(filepath.Join(home, "skills"), rec.Skills, rec.SkillGrants)
	if err != nil {
		logger.WarnCF("tools", "skills authority: granted skill folders unreadable; no skill folder is readable this turn",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return turnSkillAuthority{}
	}
	return turnSkillAuthority{
		folders: folders,
		readAll: ResolveEffectivePolicy(pol, "edit_skill") != string(config.ToolPolicyDeny),
	}
}

// turnSkillFolders returns the real paths of the acting agent's loadable
// skill folders under the registered sandbox home, for the God floor's kernel
// grant (§6.2 item 1; the ordinary path reads them from the authored policy's
// HomeGuard.SkillFolders, filled by the same resolveTurnSkillAuthority). No
// registered home or no agent → none (fail closed).
func turnSkillFolders(ctx context.Context) []string {
	base, ok := sandbox.RegisteredTurnPolicyBase()
	if !ok || base.HomePath == "" {
		return nil
	}
	return resolveTurnSkillAuthority(base.HomePath, ToolAgentID(ctx)).folders
}

// readGlobalToolCeiling returns the global tool-policy ceiling as the next
// config load would see it: the persisted sandbox.tool_policies with the
// shipped default filled in for any key the file does not carry (what
// config.ReconcileToolPolicyCeiling adds on load). God mode is deliberately
// not applied: it can only lift a global deny, so ignoring it can only make
// SkillsReadAll stricter, never looser.
func readGlobalToolCeiling(home string) (map[string]config.ToolPolicy, error) {
	out := map[string]config.ToolPolicy{}
	for k, v := range config.DefaultToolPolicyCeiling() {
		out[k] = config.ToolPolicy(v)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("skills authority: read config: %w", err)
	}
	var doc struct {
		Sandbox struct {
			ToolPolicies map[string]string `json:"tool_policies"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("skills authority: parse config: %w", err)
	}
	for k, v := range doc.Sandbox.ToolPolicies {
		out[k] = config.ToolPolicy(v)
	}
	return out, nil
}
