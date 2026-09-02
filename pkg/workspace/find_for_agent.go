// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// isImplicitMember reports whether agentID is IMPLICITLY a member of every
// workspace (operator decision, 2026-07-21: "make the judge a member of
// every workspace, keep it simple" — ADR-052's Judge/verifier fix). System
// Agents (coreagent.IsSystemAgentID; today: the Judge, ADR-049 D3) are
// permanently barred from ever being explicitly listed in a workspace's
// core_team (pkg/gateway/rest_workspaces.go's validateCoreTeamMembers 400s
// any attempt to add one) — rather than special-casing that as a refusal
// bypass at the call site, this is the ONE place membership itself is
// decided: a System Agent counts as a match for every workspace that
// exists, exactly as if it were listed in every core_team. Everything
// downstream (FindForAgent's ambiguous-membership tie-break,
// FindForAgentPreferring's preferred-id fast path, and every caller of
// either) sees a uniform, positive membership result — no separate
// exemption/bypass branch anywhere else in the codebase.
func isImplicitMember(agentID string) bool {
	return coreagent.IsSystemAgentID(coreagent.CoreAgentID(agentID))
}

// teamRecord is the minimal subset of the on-disk workspace JSON FindForAgent
// reads. Mirrors the package convention (see `record` in default.go and
// `delegationRecord` in delegation.go) of a per-reader minimal struct rather
// than mirroring the full storedWorkspace shape.
type teamRecord struct {
	ID       string   `json:"id"`
	CoreTeam []string `json:"core_team,omitempty"`
}

// FindForAgent scans every persisted workspace under home (~/.omnipus) and
// returns the id of the first (in sorted-id order, for determinism) whose
// CoreTeam contains agentID. Returns ("", false) when agentID is empty, the
// workspaces directory cannot be read, or no workspace claims this agent.
//
// This is the reverse lookup of Workspace.CoreTeam: "which workspace does
// this agent belong to" (agent identity → workspace), used to re-root a
// turn's filesystem at the workspace's shared directory regardless of
// whether the turn itself carries a channel-bound workspace_id. See
// pkg/agent/loop.go's runTurn and pkg/agent/external_dispatch.go's
// runExternalCLISubTurn.
//
// No uniqueness constraint prevents an agent ID from appearing in more than
// one workspace's CoreTeam simultaneously — nothing enforces or even detects
// this today (pkg/gateway/rest_workspaces.go's create/update handlers and
// pkg/sysagent/tools/workspace.go's tool-driven mutation both replace
// CoreTeam wholesale with no cross-workspace uniqueness check). When that
// happens, the first match in sorted-id order wins, and a WARN is logged
// naming every workspace that claims the agent, so the ambiguity is visible
// rather than silently arbitrary. This is a real, reachable state (e.g. an
// operator adds the same agent to two workspaces' core teams via two
// separate PUTs), not a defensive-only guard.
//
// A System Agent (isImplicitMember) is a match for EVERY workspace file
// found, regardless of its CoreTeam contents — it is an implicit member of
// all of them by design (see isImplicitMember's doc comment). It therefore
// hits the exact same "more than one match" tie-break every genuinely
// ambiguous multi-membership agent hits (sorted-first id wins) — no separate
// resolution path. The one difference: the ambiguous-membership WARN is
// suppressed for an implicit member, since having more than one match is the
// NORMAL, EXPECTED state for it (fires on literally every call once a second
// workspace exists) rather than the anomaly that WARN exists to surface for
// an ordinary agent.
//
// Mirrors ResolveDefaultID's file-scanning pattern: entries are read via
// os.ReadDir, non-JSON entries and directories are skipped, and a
// malformed/unreadable workspace file is skipped rather than aborting the
// whole scan over one bad file.
func FindForAgent(home, agentID string) (string, bool) {
	matches, implicit := findAllForAgent(home, agentID)
	if len(matches) == 0 {
		return "", false
	}
	if len(matches) > 1 && !implicit {
		logger.WarnCF(
			"workspace",
			"agent belongs to more than one workspace's core team; using the first by sorted id order",
			map[string]any{
				"agent_id":   agentID,
				"workspaces": strings.Join(matches, ","),
				"chosen":     matches[0],
			},
		)
	}
	return matches[0], true
}

// FindAllForAgent returns EVERY workspace whose CoreTeam claims agentID, in
// sorted id order, plus whether the agent is an implicit member of all of them
// (a System Agent, for which multi-membership is the normal state rather than
// an ambiguity).
//
// FindForAgent answers "which one" by applying a sorted-first tie-break; this
// answers "how many, and which", for the caller that must REFUSE rather than
// tie-break. The browser's ResolveBrowsingKey is that caller (ADR-072 FR-033):
// choosing arbitrarily between two workspaces there would silently pick which
// set of live logins a turn acts with, which is not a filesystem-rerooting
// question and must not inherit the filesystem answer.
func FindAllForAgent(home, agentID string) (ids []string, implicitMember bool) {
	return findAllForAgent(home, agentID)
}

// findAllForAgent is the shared scan FindForAgent and FindAllForAgent both
// read. It logs nothing — the ambiguity WARN belongs to the caller that
// actually resolves an ambiguity, and FindAllForAgent's callers report it
// their own way.
func findAllForAgent(home, agentID string) ([]string, bool) {
	if agentID == "" {
		return nil, false
	}
	dir := dirFor(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}

	implicit := isImplicitMember(agentID)

	var matches []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		var w teamRecord
		if jerr := json.Unmarshal(data, &w); jerr != nil {
			continue
		}
		member := implicit
		if !member {
			for _, id := range w.CoreTeam {
				if id == agentID {
					member = true
					break
				}
			}
		}
		if !member {
			continue
		}
		wsID := w.ID
		if wsID == "" {
			wsID = strings.TrimSuffix(e.Name(), ".json")
		}
		matches = append(matches, wsID)
	}

	sort.Strings(matches)
	return matches, implicit
}

// FindForAgentPreferring resolves the same question as FindForAgent — which
// workspace's CoreTeam does agentID belong to — but first checks
// preferredWsID directly when it is non-empty, disambiguating FindForAgent's
// documented ambiguous-membership case (an agent on more than one workspace's
// core team) in favor of the CALLER'S OWN notion of "the current one" (e.g.
// the current turn's channel-bound workspace_id) instead of FindForAgent's
// arbitrary sorted-first pick.
//
// Falls back to FindForAgent unchanged when preferredWsID is empty, invalid,
// or the agent is not actually a member of THAT SPECIFIC workspace's
// CoreTeam — so a caller with no turn-bound workspace (e.g. a delegated
// sub-turn, which spawnSubTurn never threads a workspace_id into) sees no
// behavior change from FindForAgent.
//
// For a System Agent (isImplicitMember), preferredWsID resolving to any
// real, readable workspace file is itself sufficient — its CoreTeam is never
// consulted, since implicit membership already covers it. This is what makes
// preferredWsID the genuine "which workspace does THIS adjudication root in"
// selector for the Judge (ADR-052 JudgeCriteriaInput.WorkspaceID, threaded
// via the verifier's turn ctx — pkg/agent/workspace_reroot.go): the SAME
// preferredWsID parameter every other caller already uses to pick among its
// own multiple memberships, not a second mechanism.
//
// Note: a successful preferredWsID match returns directly, WITHOUT running
// FindForAgent's full scan — so FindForAgent's own ambiguous-membership WARN
// (logged when an agent is found in more than one workspace) does NOT fire on
// this fast path, even though the ambiguity the WARN exists to surface is
// still real. This trades that operator-visible signal for avoiding a full
// directory scan on every turn for every CoreTeam member; the scan (and its
// WARN) still runs normally whenever preferredWsID is absent or doesn't
// resolve.
func FindForAgentPreferring(home, agentID, preferredWsID string) (string, bool) {
	if agentID == "" {
		return "", false
	}
	if safeID(preferredWsID) {
		data, err := os.ReadFile(filepath.Join(dirFor(home), preferredWsID+".json"))
		if err == nil {
			if isImplicitMember(agentID) {
				return preferredWsID, true
			}
			var w teamRecord
			if json.Unmarshal(data, &w) == nil {
				for _, id := range w.CoreTeam {
					if id == agentID {
						return preferredWsID, true
					}
				}
			}
		}
	}
	return FindForAgent(home, agentID)
}

// titleRecord is the minimal subset of the on-disk workspace JSON LoadTitle
// reads — mirrors teamRecord's convention of a per-reader minimal struct.
type titleRecord struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// LoadTitle reads a workspace's human-readable Name and Description for an
// already-resolved id (e.g. the id FindForAgent/FindForAgentPreferring
// returned) — the short identity to show an agent alongside its raw
// workspace id, since a ULID alone means nothing to it. Returns ok=false when
// id is unsafe or the workspace file is missing/unreadable/malformed; does
// NOT itself re-verify CoreTeam membership (that is the caller's job).
func LoadTitle(home, id string) (name, description string, ok bool) {
	if !safeID(id) {
		return "", "", false
	}
	data, err := os.ReadFile(filepath.Join(dirFor(home), id+".json"))
	if err != nil {
		return "", "", false
	}
	var w titleRecord
	if json.Unmarshal(data, &w) != nil {
		return "", "", false
	}
	return w.Name, w.Description, true
}
