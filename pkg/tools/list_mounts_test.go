// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// list_mounts (ADR-068 §4) is the read-only counterpart to request_mount. The
// properties worth pinning are the ones that make it trustworthy rather than
// merely present: it must not error on the ordinary "no mounts" state, it must
// report a mount whose folder has gone missing rather than hiding it, it must
// never present an unreadable grant list as an empty one, and it must not
// touch the mount store while answering.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'ListMounts' -p 1 ./pkg/tools/

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newListMountsFixture builds a home containing one real workspace record (not
// just a directory — CreateMount loads the record before it grants anything)
// whose CoreTeam contains agentID, so the membership-fallback path in Execute
// can be exercised from the same fixture.
func newListMountsFixture(t *testing.T, wsID, agentID string) (home string) {
	t.Helper()
	home = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces", wsID, "work"), 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	rec := map[string]any{
		"id": wsID, "name": "List Mounts Test", "status": "active",
		"created_at": now, "updated_at": now,
		"core_team": []string{agentID},
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", wsID+".json"), data, 0o600))
	return home
}

// newListMountsTarget returns a real folder outside the workspace, in the
// realpath-resolved form CreateMount will store (on macOS t.TempDir() hands
// back a path under /var, which is a symlink to /private/var — comparing
// against the unresolved form would fail for a reason that has nothing to do
// with what is under test).
func newListMountsTarget(t *testing.T) (raw, resolved string) {
	t.Helper()
	raw = t.TempDir()
	resolved, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)
	return raw, filepath.Clean(resolved)
}

// decodeListMounts parses a successful response payload.
func decodeListMounts(t *testing.T, res *ToolResult) listMountsResponse {
	t.Helper()
	require.False(t, res.IsError, "expected success, got error: %s", res.ForLLM)
	var out listMountsResponse
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload was not JSON: %s", res.ForLLM)
	return out
}

// TestListMounts_NoMountsIsSuccessWithAnEmptyList pins the state every fresh
// workspace is in. A workspace with no mounts has no mount-store file at all,
// which LoadMounts reports as (nil, true) — a normal state, not a failure. An
// error here would teach an agent that having no write grants is a fault
// condition, and the obvious "fix" for it is to ask the operator to approve a
// folder it does not actually need (ADR-068 §2.2).
func TestListMounts_NoMountsIsSuccessWithAnEmptyList(t *testing.T) {
	const wsID = "ws-list-mounts-empty"
	home := newListMountsFixture(t, wsID, "agent-list-mounts")

	res := NewListMountsTool(home).Execute(WithWorkspaceID(context.Background(), wsID), nil)

	out := decodeListMounts(t, res)
	assert.Equal(t, wsID, out.WorkspaceID)
	assert.Equal(t, "turn", out.WorkspaceResolvedFrom)
	assert.Empty(t, out.Mounts, "a workspace with no mounts must report no mounts")
	// Never null: an empty array is what "no grants" looks like on the wire.
	assert.Contains(t, res.ForLLM, `"mounts":[]`)
	// The payload must say, in the empty case above all, that reading needs no
	// mount — otherwise the empty list reads as "you cannot look outside".
	assert.Contains(t, strings.ToLower(out.Note), "does not require a mount")
}

// TestListMounts_ReportsEveryMountWithHostPathWorkPathAndGrant covers the
// ordinary populated case: every approved folder is listed once, with the
// resolved host path the operator actually approved, the work/<name> location
// the agent will type, and the grant level.
func TestListMounts_ReportsEveryMountWithHostPathWorkPathAndGrant(t *testing.T) {
	const wsID = "ws-list-mounts-many"
	home := newListMountsFixture(t, wsID, "agent-list-mounts")
	rawNotes, notes := newListMountsTarget(t)
	rawPhotos, photos := newListMountsTarget(t)

	_, _, err := workspace.CreateMount(home, wsID, "notes", rawNotes)
	require.NoError(t, err)
	_, _, err = workspace.CreateMount(home, wsID, "photos", rawPhotos)
	require.NoError(t, err)

	res := NewListMountsTool(home).Execute(WithWorkspaceID(context.Background(), wsID), nil)

	out := decodeListMounts(t, res)
	require.Len(t, out.Mounts, 2, "both approved folders must be listed")

	byName := map[string]listMountsEntry{}
	for _, m := range out.Mounts {
		byName[m.Name] = m
	}
	require.Contains(t, byName, "notes")
	require.Contains(t, byName, "photos")

	assert.Equal(t, notes, byName["notes"].HostPath)
	assert.Equal(t, photos, byName["photos"].HostPath)
	assert.Equal(t, filepath.Join(home, "workspaces", wsID, "work", "notes"), byName["notes"].WorkPath)
	assert.Equal(t, filepath.Join(home, "workspaces", wsID, "work", "photos"), byName["photos"].WorkPath)
	for name, m := range byName {
		assert.Equalf(t, "ok", m.Status, "%q resolves on disk, so its status must be ok", name)
		// The permission level ADR-068 §4 asks for. It is a system-wide
		// invariant (a mount grants write and nothing else), not a stored
		// per-mount column, and it must be reported as that constant rather
		// than as something the store decides.
		assert.Equalf(t, "write", m.Grants, "%q must report the write-only grant", name)
	}

	// ADR-068 §4 also asks for an approval timestamp. The store records none
	// (Mount is {name, host_path}; the record adds only workspace_id), so the
	// field is deliberately ABSENT rather than approximated from a symlink or
	// file mtime. This assertion exists so that a later change which starts
	// emitting one has to be deliberate: if Mount ever gains a real
	// ApprovedAt, delete this line together with that change.
	assert.NotContains(t, res.ForLLM, "approved_at",
		"no approval time is persisted anywhere, so none may be reported")
}

// TestListMounts_BrokenMountIsListedAsBroken is the case that decides whether
// this tool is useful when something is wrong. When the approved folder no
// longer resolves — deleted, on an unmounted drive, restored onto a different
// machine — the mount still EXISTS as a grant and writes through it will fail.
// Dropping it from the list would leave the agent unable to see why, and would
// invite it to re-request a folder it already has. It must be listed, with the
// status saying what happened.
func TestListMounts_BrokenMountIsListedAsBroken(t *testing.T) {
	const wsID = "ws-list-mounts-broken"
	home := newListMountsFixture(t, wsID, "agent-list-mounts")
	rawGone, gone := newListMountsTarget(t)
	rawLive, _ := newListMountsTarget(t)

	_, _, err := workspace.CreateMount(home, wsID, "gone", rawGone)
	require.NoError(t, err)
	_, _, err = workspace.CreateMount(home, wsID, "live", rawLive)
	require.NoError(t, err)

	// The folder disappears AFTER approval — exactly the FR-8.2 scenario.
	require.NoError(t, os.RemoveAll(rawGone))

	res := NewListMountsTool(home).Execute(WithWorkspaceID(context.Background(), wsID), nil)

	out := decodeListMounts(t, res)
	require.Len(t, out.Mounts, 2, "a broken mount is still a grant and must still be listed")

	byName := map[string]listMountsEntry{}
	for _, m := range out.Mounts {
		byName[m.Name] = m
	}
	assert.Equal(t, "broken", byName["gone"].Status)
	assert.Equal(t, "ok", byName["live"].Status, "one broken mount must not taint the others")
	// FR-8.5: reporting a mount broken must never re-point or forget its
	// recorded host path — that path is what the operator approved.
	assert.Equal(t, gone, byName["gone"].HostPath)
}

// TestListMounts_UnreadableRecordIsNotReportedAsNoMounts keeps the two states
// LoadMounts deliberately distinguishes distinct all the way to the model.
// "You have no mounts" and "your grant list is corrupt" lead to opposite next
// moves: the first says ask for what you need, the second says stop and tell
// the operator. Collapsing them means an agent asks for folders it has already
// been given, and the operator re-approves grants that were never revoked.
func TestListMounts_UnreadableRecordIsNotReportedAsNoMounts(t *testing.T) {
	const wsID = "ws-list-mounts-corrupt"
	home := newListMountsFixture(t, wsID, "agent-list-mounts")

	storePath, err := workspace.MountStorePath(home, wsID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o700))
	require.NoError(t, os.WriteFile(storePath, []byte("{ this is not json"), 0o600))

	res := NewListMountsTool(home).Execute(WithWorkspaceID(context.Background(), wsID), nil)

	require.True(t, res.IsError, "an unreadable grant list must not succeed as an empty one")
	lower := strings.ToLower(res.ForLLM)
	assert.Contains(t, lower, "cannot be read")
	assert.Contains(t, lower, "not the same as having no mounts")
}

// TestListMounts_ResolvesWorkspaceFromAgentMembershipWhenTheTurnCarriesNone
// mirrors ResolveTurnFSPolicy, which is what actually computes the turn's
// write grants. A CLI or scheduled turn never sets tools.WithWorkspaceID even
// though its work dir IS re-rooted into the agent's CoreTeam workspace, and
// the enforcement path falls back to FindForAgentPreferring for exactly that
// case. If this tool looked only at the turn's workspace id it would answer
// "no mounts" while bash was honouring them — a discovery tool contradicting
// the thing it describes.
func TestListMounts_ResolvesWorkspaceFromAgentMembershipWhenTheTurnCarriesNone(t *testing.T) {
	const wsID = "ws-list-mounts-cli"
	const agentID = "agent-list-mounts"
	home := newListMountsFixture(t, wsID, agentID)
	rawNotes, notes := newListMountsTarget(t)
	_, _, err := workspace.CreateMount(home, wsID, "notes", rawNotes)
	require.NoError(t, err)

	// No WithWorkspaceID — only the agent identity, as a CLI turn carries.
	ctx := WithAgentID(context.Background(), agentID)
	res := NewListMountsTool(home).Execute(ctx, nil)

	out := decodeListMounts(t, res)
	assert.Equal(t, wsID, out.WorkspaceID)
	assert.Equal(t, "agent_membership", out.WorkspaceResolvedFrom,
		"the fallback must say it was used, so the answer is not mistaken for a turn-scoped one")
	require.Len(t, out.Mounts, 1)
	assert.Equal(t, notes, out.Mounts[0].HostPath)
}

// TestListMounts_RefusesWhenNoWorkspaceCanBeResolved is the fail-closed end of
// the same fallback: an agent belonging to no workspace gets a refusal that
// says so, not an empty list that looks like a workspace with no grants.
func TestListMounts_RefusesWhenNoWorkspaceCanBeResolved(t *testing.T) {
	home := newListMountsFixture(t, "ws-list-mounts-other", "someone-else")

	res := NewListMountsTool(home).Execute(context.Background(), nil)

	require.True(t, res.IsError)
	assert.Contains(t, strings.ToLower(res.ForLLM), "no workspace")
}

// TestListMounts_MetadataInstanceRefusesRatherThanReadingARelativePath guards
// the catalog instance, which is constructed with an empty home
// (GeneralBuiltinMetadata) and documented as never executed. If that invariant
// ever breaks, it must fail loudly rather than resolve "entities/mounts/…"
// against whatever the process working directory happens to be.
func TestListMounts_MetadataInstanceRefusesRatherThanReadingARelativePath(t *testing.T) {
	res := NewListMountsTool("").Execute(WithWorkspaceID(context.Background(), "ws-any"), nil)

	require.True(t, res.IsError)
	assert.Contains(t, strings.ToLower(res.ForLLM), "no data directory")
}

// TestListMounts_NeverMutatesMountState is the read-only guarantee, asserted
// rather than asserted-in-a-comment. A tool that could alter the grant list
// while answering a question about it would be a write grant obtained by
// asking — including the tempting "repair" of dropping or re-pointing a broken
// mount, which FR-8.5 forbids precisely because the recorded path is what the
// operator approved.
func TestListMounts_NeverMutatesMountState(t *testing.T) {
	const wsID = "ws-list-mounts-readonly"
	home := newListMountsFixture(t, wsID, "agent-list-mounts")
	rawGone, _ := newListMountsTarget(t)
	rawLive, _ := newListMountsTarget(t)
	_, _, err := workspace.CreateMount(home, wsID, "gone", rawGone)
	require.NoError(t, err)
	_, _, err = workspace.CreateMount(home, wsID, "live", rawLive)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(rawGone), "include a broken mount: repair is the tempting mutation")

	storePath, err := workspace.MountStorePath(home, wsID)
	require.NoError(t, err)
	before, err := os.ReadFile(storePath)
	require.NoError(t, err)
	workDir := filepath.Join(home, "workspaces", wsID, "work")
	entriesBefore, err := os.ReadDir(workDir)
	require.NoError(t, err)

	res := NewListMountsTool(home).Execute(WithWorkspaceID(context.Background(), wsID), nil)
	require.False(t, res.IsError, res.ForLLM)

	after, err := os.ReadFile(storePath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the mount store must be byte-identical after a read")

	entriesAfter, err := os.ReadDir(workDir)
	require.NoError(t, err)
	require.Len(t, entriesAfter, len(entriesBefore), "work/ must gain and lose nothing")
	for i := range entriesAfter {
		assert.Equal(t, entriesBefore[i].Name(), entriesAfter[i].Name())
	}
	// The broken mount's symlink is still there, still pointing where the
	// operator approved — not deleted, not recreated as a real directory
	// (FR-8.3).
	target, err := os.Readlink(filepath.Join(workDir, "gone"))
	require.NoError(t, err, "the broken mount's symlink must survive being listed")
	assert.NotEmpty(t, target)
}

// TestListMounts_ToolIdentity pins the surface the policy seed and the catalog
// key on. The name is the join between pkg/tools, the coverage universe
// (pkg/gateway buildKnownBuiltinToolNames), coreagent's static catalog and the
// global ceiling in pkg/config — a rename that misses any one of those ships
// the tool denied-by-default on every install, which is the trap
// recall_conversation_meta.go documents.
func TestListMounts_ToolIdentity(t *testing.T) {
	tool := NewListMountsTool("")
	assert.Equal(t, "list_mounts", tool.Name())
	assert.Equal(t, ScopeGeneral, tool.Scope())
	assert.Equal(t, CategoryFilesystem, tool.Category())
	assert.NotEmpty(t, tool.Description())
	params := tool.Parameters()
	assert.Equal(t, "object", params["type"], "no arguments: the workspace comes from the turn, never the model")
	assert.Empty(t, params["properties"])
}

// TestListMounts_IsInTheGeneralBuiltinCatalog is the targeted guard that the
// tool reached the metadata catalog pkg/gateway's buildKnownBuiltinToolNames
// walks. request_mount is asserted alongside it because the pair being blind
// together is precisely what a two-sided ElementsMatch drift test cannot see.
func TestListMounts_IsInTheGeneralBuiltinCatalog(t *testing.T) {
	present := map[string]bool{}
	for _, tool := range GeneralBuiltinMetadata() {
		present[tool.Name()] = true
	}
	assert.True(t, present["list_mounts"],
		"list_mounts must be in GeneralBuiltinMetadata, or it ships with no seeded tool policy "+
			"and is denied-by-default on every install")
	assert.True(t, present["request_mount"], "request_mount must still be catalogued alongside it")
}
