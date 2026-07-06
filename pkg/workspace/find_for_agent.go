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

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

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
// Mirrors ResolveDefaultID's file-scanning pattern: entries are read via
// os.ReadDir, non-JSON entries and directories are skipped, and a
// malformed/unreadable workspace file is skipped rather than aborting the
// whole scan over one bad file.
func FindForAgent(home, agentID string) (string, bool) {
	if agentID == "" {
		return "", false
	}
	dir := dirFor(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}

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
		member := false
		for _, id := range w.CoreTeam {
			if id == agentID {
				member = true
				break
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

	if len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	if len(matches) > 1 {
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
