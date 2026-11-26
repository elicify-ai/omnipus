// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills & Tools C-25: the project skill folders of every mount the turn
// can reach, gated by their C-11 outcome for the acting agent under key
// project:<W>/<slug>. Computed once per turn in ResolveTurnFSPolicy, beside
// the installed loadable set; fspolicy.IsCarveOut and the bash operand
// check enforce it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// errProjectSkillGated is the class of a C-25 refusal.
var errProjectSkillGated = errors.New("project skill gated")

// projectSkillGateError carries the exact refusal text of one gate.
type projectSkillGateError struct{ gate fspolicy.ProjectSkillGate }

func (e *projectSkillGateError) Error() string { return e.gate.Reason }

func (e *projectSkillGateError) Unwrap() error { return errProjectSkillGated }

// ProjectSkillAskText is the C-25 refusal of a project skill whose outcome
// is still ask_pending for the agent.
func ProjectSkillAskText(slug string) string {
	return slug + " is a project skill this agent may not use yet. Approve it when it is loaded, or change its permission in Skills & Tools."
}

// projectSkillGates lists the gated project skill folders under mountRoots
// for agentID in workspaceID. A home with no skill values on either side
// keeps the pre-upgrade rule (no gate). An unreadable mount, record or
// setting fails closed for the folders it covers and is logged.
func projectSkillGates(home, agentID, workspaceID string, mountRoots []string) []fspolicy.ProjectSkillGate {
	if home == "" || agentID == "" || workspaceID == "" || len(mountRoots) == 0 {
		return nil
	}
	settings, err := config.ReadPersistedSkillSettings(home)
	if err != nil {
		logger.WarnCF("tools", "project skill gate: skill settings unreadable; every project skill folder is closed this turn",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return allProjectSkillGates(mountRoots, fspolicy.ProjectSkillGateDeny, "skill settings unreadable")
	}
	var agentSkills *config.AgentSkillsCfg
	if _, statErr := os.Stat(agentstore.RecordsDir(home)); statErr == nil {
		if rec, getErr := agentstore.New(home).Get(agentID); getErr == nil && rec.Tools != nil {
			agentSkills = rec.Tools.Skills
		}
	}
	if settings.Policies == nil && (agentSkills == nil || len(agentSkills.Policies) == 0) {
		return nil
	}
	var gates []fspolicy.ProjectSkillGate
	for _, root := range mountRoots {
		found, _ := skills.DiscoverProjectSkills(filepath.Base(root), root)
		for _, ps := range found {
			folder := filepath.Dir(ps.Path)
			key := config.ProjectSkillKey(workspaceID, ps.ID)
			d, err := projectSkillDecision(home, settings, agentSkills, key, folder)
			if err != nil {
				logger.WarnCF("tools", "project skill gate: decision unreadable; the folder is closed this turn",
					map[string]any{"agent_id": agentID, "skill": key, "error": err.Error()})
				d = SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyPermission}
			}
			if skillUsableWithoutCard(agentID, key, d) {
				continue
			}
			gate := fspolicy.ProjectSkillGate{Folder: folder, Slug: ps.ID, Outcome: fspolicy.ProjectSkillGateDeny}
			if d.Outcome == SkillOutcomeAskPending {
				gate.Outcome, gate.Reason = fspolicy.ProjectSkillGateAsk, ProjectSkillAskText(ps.ID)
			} else {
				gate.Reason = ProjectSkillRefusalText(ps.ID, WorkspaceLabel(home, workspaceID), d.Reason)
			}
			gates = append(gates, gate)
		}
	}
	return gates
}

// WorkspaceLabel is the name a person knows workspace id by: its record's
// name, or the id itself when the record is unreadable or has no name.
func WorkspaceLabel(home, id string) string {
	if name, _, ok := workspace.LoadTitle(home, id); ok && strings.TrimSpace(name) != "" {
		return name
	}
	return id
}

// projectSkillDecision is C-11 for one project skill: granted by the
// mount, always unverified (ST-Q8 A), trust approval per revision.
func projectSkillDecision(home string, settings config.PersistedSkillSettings, agentSkills *config.AgentSkillsCfg, key, folder string) (SkillDecision, error) {
	approved := false
	revision, err := skills.ContentRevision(folder)
	if err != nil {
		return SkillDecision{}, fmt.Errorf("revision of %s: %w", key, err)
	}
	if settings.Trust == config.SkillTrustAsk {
		skillsDir := filepath.Join(home, "skills")
		if approved, err = skills.TrustApproved(skillsDir, key, revision); err != nil {
			return SkillDecision{}, err
		}
	}
	in := SkillDecisionInput{
		Key: key, Granted: true, Global: config.ToolPolicy(settings.Policies[key]),
		Trust: settings.Trust, Verified: false, GodMode: settings.GodMode, TrustApproved: approved,
	}
	if agentSkills != nil {
		in.Agent = agentSkills.Policies[key]
	}
	d := ResolveSkillDecision(in)
	d.Revision = revision
	return d, nil
}

// allProjectSkillGates closes every project skill folder under the roots.
func allProjectSkillGates(mountRoots []string, outcome, why string) []fspolicy.ProjectSkillGate {
	var gates []fspolicy.ProjectSkillGate
	for _, root := range mountRoots {
		found, _ := skills.DiscoverProjectSkills(filepath.Base(root), root)
		for _, ps := range found {
			gates = append(gates, fspolicy.ProjectSkillGate{
				Folder: filepath.Dir(ps.Path), Slug: ps.ID, Outcome: outcome,
				Reason: fmt.Sprintf("%s cannot be used: %s.", ps.ID, why),
			})
		}
	}
	return gates
}

// projectSkillOperandRefusal is the bash operand leg of C-25: a read of a
// gated folder is refused (an editor may read), a run is refused for
// everyone.
func projectSkillOperandRefusal(p string, use pathUseVerdict, policy fspolicy.FSPolicy) (string, bool) {
	check := fspolicy.ProjectSkillRunGate
	if use.readOnly && !use.exec {
		check = fspolicy.ProjectSkillReadGate
	}
	g, ok := check(p, policy)
	if !ok {
		if resolved, err := resolvePathAgainstExistingAncestor(p); err == nil && resolved != p {
			g, ok = check(resolved, policy)
		}
	}
	if !ok {
		return "", false
	}
	return "Command blocked: " + g.Reason, true
}

// appLevelGateWarned remembers, per gated folder, the last turn that was
// warned about, so the warning is logged once per turn (bounded by the
// number of folders).
var appLevelGateWarned sync.Map

// warnAppLevelOnlyGates logs, once per turn and folder, that a gate is
// enforced by the app only: no kernel sandbox policy is installed in this
// process (Windows, Linux before 5.13, a sandbox that failed to start). It
// is never reported as kernel-enforced.
func warnAppLevelOnlyGates(turnID string, gates []fspolicy.ProjectSkillGate) {
	if len(gates) == 0 || sandbox.TurnPolicyBaseInstalled() {
		return
	}
	for _, g := range gates {
		if prev, ok := appLevelGateWarned.Load(g.Folder); ok && prev == turnID {
			continue
		}
		appLevelGateWarned.Store(g.Folder, turnID)
		logger.WarnCF("tools", "project skill gate is app-level only this turn: no kernel sandbox is enforcing, so bash can reach the folder through a path the operand check cannot see",
			map[string]any{"folder": g.Folder, "skill": g.Slug, "turn_id": turnID})
	}
}
