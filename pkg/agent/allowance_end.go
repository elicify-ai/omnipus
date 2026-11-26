// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-26: ending timed allowances. pkg/agent sees both the
// allowance store (pkg/security) and the egress proxy (pkg/sandbox), so it
// is the one place that can end an agent's bash allowances AND revoke the
// agent's live egress tokens, so a running command opens no new connection
// to an address that is no longer allowed.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// bashTool is the tool whose allowances carry egress tokens.
const bashTool = "bash"

// PermissionChange is one changed stored permission: the tool name, MCP
// wildcard mcp_<server>_* or "skill:<key>" (security.EndFor's key), its old
// and new value, and the agents it affects (nil = every agent).
type PermissionChange struct {
	Key      string
	Old, New string
	AgentIDs []string
}

// EndAllowancesFor ends the running allowances that answer a changed
// permission (C-26 point 7), writes one approval.allowance_ended entry per
// ended allowance and, for bash, revokes the affected agents' live egress
// tokens. It runs after the change is durable and before the response. The
// returned error joins every audit write that failed; the allowances are
// ended either way.
func (al *AgentLoop) EndAllowancesFor(ctx context.Context, logger *audit.Logger, c PermissionChange) error {
	if c.Old == c.New {
		return nil
	}
	ended := al.approvalGrants.EndFor(c.AgentIDs, c.Key)
	// The entry names the changed key as its permission map stores it (C-26
	// Audit): a skill's bare key, without the store's "skill:" prefix.
	changedKey := strings.TrimPrefix(c.Key, security.AllowanceSubjectSkillPrefix)
	var errs []error
	revoked := map[string]bool{}
	for _, e := range ended {
		if err := audit.EmitAllowanceEnded(ctx, logger, audit.AllowanceEnded{
			AgentID: e.AgentID, Kind: e.Kind, Subject: e.Subject,
			Key: changedKey, Old: c.Old, New: c.New,
		}); err != nil {
			errs = append(errs, err)
		}
		if c.Key == bashTool && !revoked[e.AgentID] {
			revoked[e.AgentID] = true
			al.revokeAgentEgress(e.AgentID)
		}
	}
	return errors.Join(errs...)
}

// EndAgentAllowances drops every allowance of a deleted agent and revokes
// its live egress tokens (C-26 "End").
func (al *AgentLoop) EndAgentAllowances(agentID string) {
	for _, tok := range al.approvalGrants.ClearAgent(agentID) {
		al.sandboxEgressProxy.RevokeRunToken(tok)
	}
}

// allowanceSweepInterval is how often RunAllowanceExpirySweep looks for
// ended timed network allowances.
const allowanceSweepInterval = time.Second

// SweepEndedAllowances revokes the live egress tokens of every agent whose
// timed network allowance has ended (C-26, Dataset D17 row 9): a running
// command's next new connection is refused, and the command keeps running.
// The tokens stay registered, so the agent's next command re-pushes the
// addresses still allowed.
func (al *AgentLoop) SweepEndedAllowances() {
	for _, tok := range al.approvalGrants.SweepEndedNetwork() {
		al.sandboxEgressProxy.RevokeRunToken(tok)
	}
}

// RunAllowanceExpirySweep calls SweepEndedAllowances every
// allowanceSweepInterval until ctx ends.
func (al *AgentLoop) RunAllowanceExpirySweep(ctx context.Context) {
	ticker := time.NewTicker(allowanceSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			al.SweepEndedAllowances()
		}
	}
}

// revokeAgentEgress revokes every live egress token of agentID. The tokens
// stay registered in the store, so the agent's next command re-pushes the
// addresses still allowed onto its token.
func (al *AgentLoop) revokeAgentEgress(agentID string) {
	for _, tok := range al.approvalGrants.NetworkTokensForAgent(agentID) {
		al.sandboxEgressProxy.RevokeRunToken(tok)
	}
}

// endAllowancesOnReload ends the allowances whose stored permission a
// config reload changed (C-26 point 7, writer 4): a global tool or skill
// value ends them for every agent, an agent's own value for that agent.
// Writers that already ended theirs before the reload find nothing left to
// end, so nothing is audited twice. The actor of a reload is "system".
func (al *AgentLoop) endAllowancesOnReload(oldCfg, newCfg *config.Config) {
	if oldCfg == nil || newCfg == nil {
		return
	}
	var changes []PermissionChange
	changes = append(changes, DiffPermissions(nil, oldCfg.Sandbox.ToolPolicies, newCfg.Sandbox.ToolPolicies, "")...)
	changes = append(changes, DiffPermissions(nil, oldCfg.Sandbox.SkillPolicies, newCfg.Sandbox.SkillPolicies,
		security.AllowanceSubjectSkillPrefix)...)
	oldAgents := agentPermissionsByID(oldCfg)
	newAgents := agentPermissionsByID(newCfg)
	ids := make([]string, 0, len(oldAgents))
	for id := range oldAgents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		next, ok := newAgents[id]
		if !ok {
			continue // a removed agent is ended by its delete path
		}
		prev := oldAgents[id]
		changes = append(changes, DiffPermissions([]string{id}, prev.tools, next.tools, "")...)
		changes = append(changes, DiffPermissions([]string{id}, prev.skills, next.skills,
			security.AllowanceSubjectSkillPrefix)...)
	}
	for _, c := range changes {
		if err := al.EndAllowancesFor(context.Background(), al.auditLogger, c); err != nil {
			logger.ErrorCF("agent", "config reload ended allowances but their audit entries were not written",
				map[string]any{"key": c.Key, "error": err.Error()})
		}
	}
}

// agentPermissions is one agent's own stored tool and skill values.
type agentPermissions struct {
	tools, skills map[string]string
}

func agentPermissionsByID(cfg *config.Config) map[string]agentPermissions {
	out := make(map[string]agentPermissions, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		p := agentPermissions{tools: map[string]string{}, skills: map[string]string{}}
		if ac.Tools != nil {
			for name, v := range ac.Tools.Builtin.Policies {
				p.tools[name] = string(v)
			}
			if ac.Tools.Skills != nil {
				for key, v := range ac.Tools.Skills.Policies {
					p.skills[key] = string(v)
				}
			}
		}
		out[ac.ID] = p
	}
	return out
}

// DiffPermissions lists the keys whose value differs between two maps of
// stored values, as changes for agentIDs, each key given prefix.
func DiffPermissions(agentIDs []string, before, after map[string]string, prefix string) []PermissionChange {
	keys := make([]string, 0, len(before)+len(after))
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []PermissionChange
	for _, k := range keys {
		if before[k] != after[k] {
			out = append(out, PermissionChange{Key: prefix + k, Old: before[k], New: after[k], AgentIDs: agentIDs})
		}
	}
	return out
}

// SetAllowanceClock replaces the wall and monotonic clocks of the loop's
// allowance store (C-26: lengths on the monotonic clock, ends reported in
// wall time). It is the clock input of the loop's allowances; production
// keeps the real clocks.
func (al *AgentLoop) SetAllowanceClock(wall func() time.Time, mono func() time.Duration) {
	security.SetApprovalGrantStoreClock(al.ApprovalGrants(), wall, mono)
}
