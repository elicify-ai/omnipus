// packages_t5_wired_red_test.go — wired publication and invisibility (epic #1094
// S1a, FR-036, FR-036a, FR-036b, FR-049, D7 row 10, SC-004).
//
// The seam tests keep a Reload stub that returns confirmed without reading the
// store. These tests do not: the reload they install re-reads the agent records
// into the live roster, and a pending id must still be absent afterwards.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// installRosterReload makes publication reload re-read the store. A function
// that returns true without that read does not satisfy these tests. When
// frames is non-nil, no agent_created frame may already have been sent.
func installRosterReload(t *testing.T, api *restAPI, frames func() []gen.AgentCreatedFrame) {
	t.Helper()
	packageT5.Reload = func() (bool, error) {
		if frames != nil {
			require.Empty(t, frames(), "FR-036: agent_created is sent only after the reload is confirmed")
		}
		err := populateAgentsListFromEntityStoreStrict(api.agentLoop.GetConfig(), api.homePath)
		if err != nil {
			return false, err
		}
		return true, nil
	}
}

func forceConfirmedReload(t *testing.T, api *restAPI) {
	t.Helper()
	require.NoError(t, populateAgentsListFromEntityStoreStrict(api.agentLoop.GetConfig(), api.homePath),
		"FR-049: the forced reload must complete and confirm")
}

func probePendingUnseen(t *testing.T, api *restAPI, id string) {
	t.Helper()
	forceConfirmedReload(t, api)
	requireAgentInvisible(t, api, id)
	requirePackageChatRefused(t, api, id)
	_, err := agentstore.New(api.homePath).PeekRevision(id)
	require.ErrorIs(t, err, entity.ErrNotFound, "FR-049: PeekRevision must not see a pending agent")
	seen := listAgentsToolIDs(t, api)
	require.NotContains(t, seen, id, "FR-049: list_agents must not include a pending agent")
}

func listAgentsToolIDs(t *testing.T, api *restAPI) []string {
	t.Helper()
	tool := tools.NewAgentListTool(func() []tools.AgentInfo {
		var infos []tools.AgentInfo
		for _, ac := range api.agentLoop.GetConfig().Agents.List {
			if ac.IsSystem() {
				continue
			}
			infos = append(infos, tools.AgentInfo{ID: ac.ID, Name: ac.Name, Type: "custom"})
		}
		return infos
	})
	result := tool.Execute(context.Background(), nil)
	require.False(t, result.IsError, "list_agents failed: %s", result.ForLLM)
	var infos []tools.AgentInfo
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &infos), "list_agents body %s", result.ForLLM)
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	return ids
}

func crashedPendingID(t *testing.T, home string) string {
	t.Helper()
	_, journal := t5OnlyJournal(t, home)
	require.NotEmpty(t, journal.AgentID, "the crashed import's journal names the pending agent")
	return journal.AgentID
}

// TestPackagesApply_NewMode_Wired is the confirmed-import BDD with a reload
// that re-reads the store, not a stub that returns true.
func TestPackagesApply_NewMode_Wired(t *testing.T) {
	api := packageAPI(t)
	frames := agentCreatedRecorder(api)
	usePackageSeams(t)
	installRosterReload(t, api, frames)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, validMainPackage(t))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.Equal(t, gen.PackageImportResultActivationActive, res.Activation, "a confirmed reload publishes the agent")
	id := res.Created[0].NewId
	require.Contains(t, listedAgentIDs(t, api), id, "FR-036: GET /api/v1/agents lists the agent after the reload")
	require.Contains(t, listAgentsToolIDs(t, api), id, "list_agents lists the agent after the reload")
	gotFrames := frames()
	require.Len(t, gotFrames, 1, "FR-036: exactly one agent_created frame, after the confirmed reload")
	// emitAgentCreated carries the id. WSHandler.broadcastAgentCreated stamps
	// the frame type on the wire; this hook is the call that reaches it.
	require.Equal(t, id, gotFrames[0].AgentId)
	requireOneRecoveredMain(t, api, ws, id)
}

// TestPackagesApply_MainChatScope_Wired is FR-036a: Y has no main chat, V's
// main chat record is damaged, and importing a Main must not repair either.
func TestPackagesApply_MainChatScope_Wired(t *testing.T) {
	api := packageAPI(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "ensureMainSession writes the loop's session store; a nil store cannot prove the main chat")
	usePackageSeams(t)
	installRosterReload(t, api, nil)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	y := createMainAgent(t, api, "Yarrow", "soul y")
	v := createMainAgent(t, api, "Vetch", "soul v")
	addTeamMember(t, api.homePath, ws, y)
	addTeamMember(t, api.homePath, ws, v)
	yId, err := session.MainSessionID(ws, y)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(store.BaseDir(), yId))
	require.True(t, os.IsNotExist(err), "fixture: Y has no main chat")
	vId, err := session.MainSessionID(ws, v)
	require.NoError(t, err)
	vMeta := filepath.Join(store.BaseDir(), vId, "meta.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(vMeta), 0o700))
	damaged := []byte("{\n")
	require.NoError(t, os.WriteFile(vMeta, damaged, 0o600))

	prev := stageUpload(t, api, validMainPackage(t))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.NotContains(t, string(got.Body), y, "Y must not produce a warning")
	require.NotContains(t, string(got.Body), v, "V must not produce a warning")
	requireOneRecoveredMain(t, api, ws, res.Created[0].NewId)
	_, err = os.Stat(filepath.Join(store.BaseDir(), yId))
	require.True(t, os.IsNotExist(err), "FR-036a: Y still has no main chat")
	gotV, err := os.ReadFile(vMeta)
	require.NoError(t, err)
	require.Equal(t, damaged, gotV, "FR-036a: V's damaged main chat is byte-identical")
}

// TestPackagesApply_NativeWorkerReachable_Wired is FR-036b. The delegation
// file is read with a hard error when it is missing, and the Main → worker
// edge is drawn through the workspace delegation PUT.
func TestPackagesApply_NativeWorkerReachable_Wired(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	installRosterReload(t, api, nil)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	lead := createMainAgent(t, api, "Lead", "leads the work")
	addTeamMember(t, api.homePath, ws, lead)
	rec := map[string]any{
		"id": specSourceID, "name": "Researcher-2", "type": "worker", "description": "researches",
	}
	pkg := buildAgentPackage(t, specSourceID, "Researcher-2", rec, []byte("Be brief.\n"), nil, 0)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	// FR-036b requires the result to say the worker is reached by delegation once
	// the owner draws a line from a Main. The user-journey quote is an example,
	// not a second required string.
	lower := strings.ToLower(string(got.Body))
	require.Contains(t, lower, "reached by delegation", "FR-036b: the result says how the worker is reached")
	require.Contains(t, lower, "main agent", "FR-036b: the result names a Main agent as the source of the line")
	res := decodeResult(t, got.Body)
	id := res.Created[0].NewId
	require.Contains(t, listedAgentIDs(t, api), id, "the confirmed reload lists the worker")
	if store := api.agentLoop.GetSessionStore(); store != nil {
		mainID, err := session.MainSessionID(ws, id)
		require.NoError(t, err)
		_, statErr := os.Stat(filepath.Join(store.BaseDir(), mainID, "meta.json"))
		require.True(t, os.IsNotExist(statErr), "FR-036b: a native worker has no main chat")
	}

	path := filepath.Join(api.homePath, "entities", "delegation", ws+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "FR-036b: the delegation store must be readable; a missing file must not skip the no-edge check")
	edges := decodeDelegationEdges(t, raw)
	for _, edge := range edges {
		if edge.To == id && edge.From != id {
			t.Fatalf("import drew an edge %s → %s; only the worker's self-row is allowed", edge.From, edge.To)
		}
	}

	body := `{"edges":[` +
		`{"from_agent":` + packageJSONString(lead) + `,"to_agent":` + packageJSONString(id) + `,"modes":["direct"]},` +
		`{"from_agent":` + packageJSONString(id) + `,"to_agent":` + packageJSONString(id) + `,"modes":["direct"]}]}`
	put := putDelegation(t, api, ws, body)
	require.Equal(t, http.StatusOK, put.Code, "the Team graph edit must accept Lead → worker; body=%s", put.Body)
	raw, err = os.ReadFile(path)
	require.NoError(t, err, "FR-036b: Lead's delegation record for W must be readable after the edit")
	edges = decodeDelegationEdges(t, raw)
	var linked bool
	for _, edge := range edges {
		if edge.From == lead && edge.To == id {
			linked = true
		}
	}
	require.True(t, linked, "FR-036b: after the owner draws the line, W's delegation record lists Lead → worker")
}

type delegEdge struct {
	From string `json:"from_agent"`
	To   string `json:"to_agent"`
}

func decodeDelegationEdges(t *testing.T, raw []byte) []delegEdge {
	t.Helper()
	var stored struct {
		Delegation []delegEdge `json:"delegation"`
	}
	require.NoError(t, json.Unmarshal(raw, &stored), "delegation store %s", raw)
	return stored.Delegation
}

// TestPackages_TwoHomeRoundTrip_Wired re-reads the destination roster after
// the import. The seam round-trip does not.
func TestPackages_TwoHomeRoundTrip_Wired(t *testing.T) {
	src := packageAPI(t)
	id := createMainAgent(t, src, "Writer", "\n  Answer in rhyme.\n\n")
	require.NoError(t, os.WriteFile(filepath.Join(src.homePath, "agents", id, "AGENT.md"), []byte("  rules\n"), 0o600))
	exp := callPackageExport(t, src, `{"agent_ids":["`+id+`"]}`)
	require.Equal(t, http.StatusOK, exp.Code, "body=%s", exp.Body)

	dst := packageAPI(t)
	usePackageSeams(t)
	installRosterReload(t, dst, nil)
	ws := createWorkspaceViaAPI(t, dst, "Research", "notes")
	prev := stageUpload(t, dst, exp.Body)
	got := callPackageApply(t, dst, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.Contains(t, listedAgentIDs(t, dst), res.Created[0].NewId, "the destination roster lists Writer after the confirmed reload")
	require.Equal(t, "Answer in rhyme.", string(fileBytes(t, filepath.Join(dst.homePath, "agents", res.Created[0].NewId, "SOUL.md"))))
	require.Equal(t, "  rules\n", string(fileBytes(t, filepath.Join(dst.homePath, "agents", res.Created[0].NewId, "AGENT.md"))))
}

// TestPackagesApply_WiredInvisibility is D7 row 10 at the four pre-commit
// points. Crash seams stop the apply; the membership and rename hooks can
// also continue into a confirmed publish.
func TestPackagesApply_WiredInvisibility(t *testing.T) {
	t.Run("after E4", func(t *testing.T) {
		api, got := crashApply(t, "E4")
		requireCrashStopped(t, got, "E4")
		probePendingUnseen(t, api, crashedPendingID(t, api.homePath))
	})
	t.Run("after the self-row", func(t *testing.T) {
		api, got := crashApply(t, "self-row")
		requireCrashStopped(t, got, "self-row")
		probePendingUnseen(t, api, crashedPendingID(t, api.homePath))
	})
	t.Run("after membership, fault, detail waits for the lock", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		packageT5.CrashAt = "after-membership"
		wiredDetailOrder = nil
		var detailFinished <-chan struct{}
		packageT5.AfterMembershipWrite = func(wsID, agentID string) {
			forceConfirmedReload(t, api)
			require.NotContains(t, listedAgentIDs(t, api), agentID, "a reload inside the lock must not list the pending agent")
			started, finished := startWorkspaceDetail(t, api, wsID)
			detailFinished = finished
			waitDetailBlocked(t, started, finished)
			wiredDetailOrder = append(wiredDetailOrder, "helper-returned")
		}
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		requireCrashStopped(t, got, "after-membership")
		waitDetailFinished(t, detailFinished)
		require.Equal(t, []string{"helper-returned", "detail-done"}, wiredDetailOrder,
			"the detail request completed only after the helper returned")
		require.Equal(t, http.StatusOK, wiredDetailCode, "the lock-taking workspace detail succeeds once the helper has returned")
		probePendingUnseen(t, api, crashedPendingID(t, api.homePath))
	})
	t.Run("before the rename, fault", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		var id string
		packageT5.PublishRename = func(from, to string) error {
			id = strings.TrimSuffix(filepath.Base(from), ".pending")
			probePendingUnseen(t, api, id)
			return os.ErrPermission
		}
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusInternalServerError, got.Code, "body=%s", got.Body)
		require.Contains(t, string(got.Body), omnpkg.CodeImportRolledBack)
		require.NotEmpty(t, id)
		probePendingUnseen(t, api, id)
	})
	t.Run("before the rename, confirmed publish", func(t *testing.T) {
		api := packageAPI(t)
		frames := agentCreatedRecorder(api)
		usePackageSeams(t)
		installRosterReload(t, api, frames)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		var id string
		packageT5.PublishRename = func(from, to string) error {
			id = strings.TrimSuffix(filepath.Base(from), ".pending")
			forceConfirmedReload(t, api)
			require.NotContains(t, listedAgentIDs(t, api), id, "the agent is invisible until the rename")
			return nil
		}
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		res := decodeResult(t, got.Body)
		require.Equal(t, id, res.Created[0].NewId)
		require.Contains(t, listedAgentIDs(t, api), id, "after the confirmed reload the agent is listed")
		require.NotEmpty(t, frames())
	})
}

// wiredDetailOrder records the membership hook and the workspace detail that
// must wait for it. These tests do not run in parallel.
var (
	wiredDetailOrder []string
	wiredDetailCode  int
)

func startWorkspaceDetail(t *testing.T, api *restAPI, wsID string) (<-chan struct{}, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	finished := make(chan struct{})
	api.beforeWorkspaceDetailPreparation = func() { close(started) }
	go func() {
		defer close(finished)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wsID, nil)
		req.URL.Path = "/api/v1/workspaces/" + wsID
		api.HandleWorkspaces(rec, req)
		wiredDetailCode = rec.Code
		wiredDetailOrder = append(wiredDetailOrder, "detail-done")
	}()
	return started, finished
}

func waitDetailFinished(t *testing.T, finished <-chan struct{}) {
	t.Helper()
	require.NotNil(t, finished, "the detail request was not started")
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the workspace detail request did not finish after the helper returned the lock")
	}
}

func waitDetailBlocked(t *testing.T, started, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the workspace detail request did not reach preparation while the import held the lock")
	}
	select {
	case <-finished:
		t.Fatal("the workspace detail request finished while the helper still held the lock")
	default:
	}
}
