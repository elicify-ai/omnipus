// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ListMountsTool is the read-only counterpart to request_mount (ADR-068 §4):
// it tells an agent which folders on the operator's machine are mounted into
// the workspace this turn is rooted in.
//
// # Why it exists
//
// request_mount shipped without one. An agent could ASK for access and could
// not enumerate what it already had, so its only way to find out was to
// attempt a write and read the refusal — or, worse, to ask the operator to
// approve a folder they had already approved. UAT defect 003 Issue 1 is
// exactly that, and ADR-068 §4 records it as the one genuinely missing piece
// of the mount surface rather than a symptom of the guard defects around it.
//
// # It is strictly read-only, and that is load-bearing
//
// This tool never creates, repairs, re-points or removes a mount, and it never
// writes to the mount store. Everything it reports comes from LoadMounts (the
// persisted grant list) and MountStatus (a live os.Stat of each host path,
// computed per call and never written back — see FR-8.5: a broken mount is
// reported broken, never silently re-bound to something that happens to exist
// at the same name). A discovery tool that mutated the thing it discloses
// would be a write grant obtained by asking a question.
//
// # What "permission level" means here, and why it is a constant
//
// ADR-068 §4 asks for a per-mount permission level. There is no such field on
// the Mount record, because permission is not per-mount data — it is a
// system-wide invariant that AllowedMountRoots states in its own contract: "A
// mount grants WRITE and nothing else — reads are already open under ADR-062
// regardless of this list." So every entry reports grants:"write", as a stated
// invariant rather than an invented field that no writer populates. Adding a
// real per-mount permission column is a store-schema change with its own
// migration question; it is not something a read-only tool may imply exists.
//
// # Why there is no approval timestamp
//
// ADR-068 §4 also asks for one. The store records none: Mount is {Name,
// HostPath} and the enclosing record is {workspace_id, mounts[]} — no time
// field anywhere on the path from CreateMount to disk. The field is therefore
// OMITTED rather than approximated. The two available substitutes are both
// wrong in ways that would read as fact: the work/<name> symlink's mtime
// describes the symlink (and is destroyed by any workspace restore), and the
// mount-store file's mtime is shared by every mount in the workspace and moves
// on every subsequent create. Reporting either as "approved at" would be a
// fabricated audit datum. Surfacing it needs a deliberate Mount.ApprovedAt
// schema addition with a back-compat story for stores written before it.
//
// # Status has two values, not three
//
// MountStatus returns "ok" or "broken" and nothing else; every failure mode
// (deleted, unmounted drive, foreign machine, symlink loop, permission error)
// collapses to "broken". There is no "revoked" state to report: revoking a
// mount DELETES it, so a revoked mount is simply absent from this list.
type ListMountsTool struct {
	BaseTool
	// homePath is $OMNIPUS_HOME — where the mount store lives. Injected at
	// construction, exactly as request_mount does it, so tests can point the
	// tool at a temp home without touching process-wide config.
	homePath string
}

// NewListMountsTool builds the tool. Like NewRequestMountTool it takes ONLY
// $OMNIPUS_HOME and deliberately NOT a workspace: an agent can belong to
// several workspaces, so a workspace captured at registration would be wrong
// for every turn on a different one. The target workspace is resolved from the
// turn at execution time (see Execute).
func NewListMountsTool(homePath string) *ListMountsTool {
	return &ListMountsTool{homePath: homePath}
}

func (t *ListMountsTool) Name() string { return "list_mounts" }

// Description is the LLM-facing contract, and it carries one line of policy
// deliberately: that an empty list does not mean "you cannot read there".
// Under the pre-ADR-068 bash guard a mount was the only way to read an outside
// path, so operators were creating WRITE grants to obtain READ access
// (ADR-068 §2.2). An agent that reads "no mounts" and concludes it must call
// request_mount to look at a file reproduces that mistake from the other side.
func (t *ListMountsTool) Description() string {
	return "List the folders on the operator's computer that are mounted into this workspace — " +
		"the write access you currently hold. Read-only; it changes nothing. Each entry gives the " +
		"mount's name (it appears in your workspace as work/<name>), the real folder it points at, " +
		"and whether that folder still exists on this machine (\"ok\") or has gone missing " +
		"(\"broken\"). A mount grants WRITE only: reading files outside your workspace never needs " +
		"one, so an empty list means you hold no write grants, not that you cannot read. Use " +
		"request_mount to ask the operator for a new one."
}

// Parameters: none. The workspace is resolved from the turn, never from a
// model-supplied argument — the same rule request_mount applies to a grant,
// applied here so an agent cannot enumerate another workspace's grants by
// naming its id.
func (t *ListMountsTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Scope reports ScopeGeneral. This tool reads back grants the operator has
// already approved and can do nothing else — the same reasoning that makes
// list_jobs ScopeGeneral. Widening visibility of a decision the operator
// already made is not itself a privilege.
func (t *ListMountsTool) Scope() ToolScope { return ScopeGeneral }

// Category is CategoryFilesystem, sitting beside request_mount: the pair is
// one surface (ask for a folder / see which folders you have).
func (t *ListMountsTool) Category() ToolCategory { return CategoryFilesystem }

// listMountsEntry is one row of the response.
//
// Field-for-field this is what the mount store actually holds plus what
// MountStatus computes live — nothing is derived from anything else, and
// nothing is inferred. See the type doc for why Grants is a constant and why
// there is no approved_at.
type listMountsEntry struct {
	// Name is the mount alias — a single path segment under work/.
	Name string `json:"name"`
	// HostPath is the real folder on the operator's machine, in the
	// realpath-resolved form recorded at approval time.
	HostPath string `json:"host_path"`
	// WorkPath is where that folder appears to the agent. Not in ADR-068's
	// list, but it is the path the agent actually types, and computing it
	// itself from name + work dir is a needless chance to get it wrong.
	WorkPath string `json:"work_path"`
	// Grants is always "write" — see the type doc.
	Grants string `json:"grants"`
	// Status is "ok" or "broken", computed live for this call.
	Status string `json:"status"`
}

// listMountsResponse is the JSON payload returned to the model, following
// list_jobs' convention of a marshalled struct rather than prose.
type listMountsResponse struct {
	// WorkspaceID is the workspace that actually answered, so the agent can
	// see which one it is being told about rather than assuming.
	WorkspaceID string `json:"workspace_id"`
	// WorkspaceResolvedFrom is "turn" when the turn carried a workspace id,
	// or "agent_membership" when it did not and the workspace was resolved
	// from the calling agent's CoreTeam membership instead (see Execute).
	WorkspaceResolvedFrom string `json:"workspace_resolved_from"`
	// Note restates the read/write asymmetry in the payload itself. It is a
	// fixed string and costs tokens on every call, which is deliberate: the
	// mistake it prevents (asking for a write grant in order to read) is the
	// one this tool is most likely to be used in the middle of.
	Note string `json:"note"`
	// Mounts is never null — a workspace with no mounts returns an empty
	// array, which is a normal, successful state and not an error.
	Mounts []listMountsEntry `json:"mounts"`
}

const listMountsNote = "A mount grants WRITE access to a folder outside this workspace. " +
	"Reading a file outside the workspace does not require a mount, so an empty list is not a reason to call request_mount."

// Execute reports the current mounts of the workspace this turn is rooted in.
//
// # Resolving the workspace the same way the write path does
//
// ToolWorkspaceID alone is NOT sufficient, and using it alone would make this
// tool lie about the exact thing it exists to disclose. A CLI/ProcessDirect
// turn (`omnipus <agent> "..."`) and a scheduled/heartbeat turn never set
// tools.WithWorkspaceID, yet their work dir IS re-rooted into the agent's
// CoreTeam workspace — and ResolveTurnFSPolicy (resolvepath.go), which is what
// actually decides the turn's write grants and what guardCommand consults for
// its mount roots, falls back to workspace.FindForAgentPreferring for exactly
// that case. An agent on such a turn would be told "you have no mounts" while
// bash was honouring three of them.
//
// So this resolves identically, in the same order, from the same helper. The
// two must never disagree: a discovery tool whose answer differs from the
// enforcement it describes is worse than no discovery tool.
func (t *ListMountsTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.homePath == "" {
		// Only reachable for the metadata-catalog instance, which is
		// constructed with an empty home and never executed. Fail closed
		// rather than resolving the mount store against a relative path.
		return ErrorResult("list_mounts: no data directory configured")
	}

	workspaceID := ToolWorkspaceID(ctx)
	resolvedFrom := "turn"
	if workspaceID == "" {
		if wsID, found := workspace.FindForAgentPreferring(t.homePath, ToolAgentID(ctx), ""); found {
			workspaceID = wsID
			resolvedFrom = "agent_membership"
		}
	}
	if workspaceID == "" {
		return ErrorResult("list_mounts: this turn has no workspace, so there are no mounts to list")
	}

	// SafeWorkDir validates the id against traversal before it is used to
	// build any path. LoadMounts refuses an unsafe id too, but it reports that
	// as an unreadable store, which is a different thing to say.
	workDir, err := workspace.SafeWorkDir(t.homePath, workspaceID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("list_mounts: %v", err))
	}

	mounts, ok := workspace.LoadMounts(t.homePath, workspaceID)
	if !ok {
		// LoadMounts distinguishes "this workspace has no mounts" (nil, true)
		// from "the grant list exists and cannot be trusted" (nil, false):
		// unsafe id, unreadable file, malformed JSON, id mismatch. Reporting
		// the second as the first would tell an agent it holds no grants when
		// the truth is that nobody can currently tell — and the agent's next
		// move would be to ask the operator to re-approve folders they already
		// approved. Keep them distinct.
		return ErrorResult("list_mounts: this workspace's mount record cannot be read " +
			"(missing, unreadable, or malformed) — this is NOT the same as having no mounts; " +
			"do not request new ones on the strength of it, and tell the operator")
	}

	entries := make([]listMountsEntry, 0, len(mounts))
	for _, m := range mounts {
		entries = append(entries, listMountsEntry{
			Name:     m.Name,
			HostPath: m.HostPath,
			WorkPath: filepath.Join(workDir, m.Name),
			// Constant, not a stored field. See the type doc.
			Grants: "write",
			// Live, per call, never persisted (FR-8.2/FR-8.5).
			Status: workspace.MountStatus(m),
		})
	}

	payload, err := json.Marshal(listMountsResponse{
		WorkspaceID:           workspaceID,
		WorkspaceResolvedFrom: resolvedFrom,
		Note:                  listMountsNote,
		Mounts:                entries,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("list_mounts: marshal response: %v", err))
	}
	return NewToolResult(string(payload))
}
