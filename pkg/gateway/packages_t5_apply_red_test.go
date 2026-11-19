// packages_t5_apply_red_test.go — RED tests for POST /packages/import/{id}/apply
// (epic #1094, T-5). Expected values are spec revision 7. The handler is the
// 501 stub, so each case fails on the spec status (or on recovery's error)
// rather than on an undefined symbol.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

const (
	codeModeUnavailable = "import_mode_not_available"
	codeWorkspaceMiss   = "workspace_not_found"
	codeNoDefaultWS     = "default_workspace_missing"
	codeAuditFailed     = "audit_failed"
)

// TestPackagesApply_NewMode is TDD row 25: confirmed New-mode import (D14,
// D6, D5, FR-031, FR-032, FR-041, FR-036a, SC-008).
func TestPackagesApply_NewMode(t *testing.T) {
	t.Run("named workspace, new id, staging gone, one audit", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		before := configBytes(t, api.homePath)
		info, err := os.Stat(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "FR-041 body=%s", got.Body)
		res := decodeResult(t, got.Body)
		require.Equal(t, gen.PackageImportResultActivationActive, res.Activation)
		require.Equal(t, gen.PackageImportResultMode(gen.PackageImportApplyRequestModeNew), res.Mode)
		require.Len(t, res.Created, 1)
		require.Equal(t, "agent", res.Created[0].Type)
		require.Equal(t, specSourceID, res.Created[0].SourceId)
		require.Equal(t, "Writer", res.Created[0].Name, "D6 row 1: no clash")
		require.NotEqual(t, specSourceID, res.Created[0].NewId, "FR-031 new UUID")
		require.Equal(t, ws, res.Created[0].WorkspaceId)
		_, statErr := os.Stat(filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
		require.True(t, os.IsNotExist(statErr), "success deletes the staging folder")
		require.Equal(t, before, configBytes(t, api.homePath), "FR-034 config.json bytes unchanged")
		after, err := os.Stat(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		require.True(t, after.ModTime().Equal(info.ModTime()), "SC-008 config.json mtime unchanged")
		require.Contains(t, readAudit(t, api.homePath), res.Created[0].NewId)
	})

	t.Run("D14-5 no workspace and no default is 409", func(t *testing.T) {
		api := packageAPI(t)
		prev := stageUpload(t, api, validMainPackage(t))
		er := requirePackageStatus(t, callPackageApply(t, api, prev.StagingId, applyBody("new", "")), http.StatusConflict, codeNoDefaultWS)
		require.NotContains(t, er.Error, "nothing imported")
	})

	t.Run("D14-4 unknown workspace is 404 and staging stays ready", func(t *testing.T) {
		api := packageAPI(t)
		prev := stageUpload(t, api, validMainPackage(t))
		requirePackageStatus(t, callPackageApply(t, api, prev.StagingId, applyBody("new", "missing-workspace")), http.StatusNotFound, codeWorkspaceMiss)
		// A refusal that wrote nothing returns the staging to ready (staging lifecycle).
		retry := createWorkspaceViaAPI(t, api, "Research", "notes")
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", retry))
		require.Equal(t, http.StatusOK, got.Code, "D9 row 9: staging stays usable; body=%s", got.Body)
	})

	t.Run("D6 and D14 name clash is decided at apply", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		require.Equal(t, "Writer", prev.WillAdd[0].ProposedName, "D6 row 1 at preview: no clash yet")
		require.False(t, prev.WillAdd[0].Renamed)
		seedClashNames(t, api, []string{"Writer"})
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		res := decodeResult(t, got.Body)
		require.Equal(t, "Writer (imported)", res.Created[0].Name, "D6 row 2")
		require.Contains(t, resultWarningCodes(res), "changed_since_preview", "D14 row 1")
	})

	t.Run("D6 row 3 case and space", func(t *testing.T) {
		res := importNamed(t, []string{"writer "}, "Writer")
		require.Equal(t, "Writer (imported)", res.Created[0].Name)
	})

	t.Run("D6 row 4 second suffix", func(t *testing.T) {
		res := importNamed(t, writerClashNames(1), "Writer")
		require.Equal(t, "Writer (imported 2)", res.Created[0].Name)
	})

	t.Run("D6 row 7 100 character name is truncated to 100", func(t *testing.T) {
		name := strings.Repeat("A", 100)
		res := importNamed(t, []string{name}, name)
		want := strings.Repeat("A", 89) + " (imported)"
		require.Equal(t, want, res.Created[0].Name)
		require.Len(t, res.Created[0].Name, 100)
	})

	t.Run("D6 row 6 suffixes exhausted", func(t *testing.T) {
		api := packageAPI(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		seedClashNames(t, api, writerClashNames(99))
		prev := stageUpload(t, api, validMainPackage(t))
		requirePackageStatus(t, callPackageApply(t, api, prev.StagingId, applyBody("new", ws)), http.StatusConflict, omnpkg.CodeNameSuffixExhausted)
	})

	t.Run("D4 row 10 custom Mia is not the built-in", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		seedListedAgent(t, api, config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore})
		seedListedAgent(t, api, config.AgentConfig{ID: specSourceID, Name: "Mia", Type: "custom"})
		miaBefore := snapshotHome(t, api.homePath)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		exp := callPackageExport(t, api, `{"agent_ids":["`+specSourceID+`"]}`)
		require.Equal(t, http.StatusOK, exp.Code, "D4 row 10: a custom agent named Mia exports; body=%s", exp.Body)
		prev := stageUpload(t, api, exp.Body)
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		res := decodeResult(t, got.Body)
		require.Equal(t, "Mia (imported)", res.Created[0].Name)
		require.NotEqual(t, "mia", res.Created[0].NewId)
		miaAfter := snapshotHome(t, api.homePath)
		for _, rel := range []string{
			filepath.Join("entities", "agents", "mia.json"),
			filepath.Join("agents", "mia", "SOUL.md"),
		} {
			require.Equal(t, miaBefore[rel], miaAfter[rel], "built-in Mia must be byte-identical (%s)", rel)
		}
	})
}

// TestPackagesApply_ModesConcurrency is TDD row 28 (FR-030, FR-038).
func TestPackagesApply_ModesConcurrency(t *testing.T) {
	t.Run("replace and merge are 422 and staging stays ready", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		for _, mode := range []string{"replace", "merge"} {
			requirePackageStatus(t, callPackageApply(t, api, prev.StagingId, applyBody(mode, ws)), http.StatusUnprocessableEntity, codeModeUnavailable)
		}
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "staging must still be ready; body=%s", got.Body)
	})

	t.Run("missing mode is 400 field mode", func(t *testing.T) {
		api := packageAPI(t)
		prev := stageUpload(t, api, validMainPackage(t))
		er := requirePackageStatus(t, callPackageApply(t, api, prev.StagingId, `{}`), http.StatusBadRequest, "")
		require.Equal(t, "mode", packageErrField(er), "FR-030")
	})

	t.Run("one apply at a time", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		body := applyBody("new", ws)
		path := "/api/v1/packages/import/" + prev.StagingId + "/apply"
		reqA := packageAuthRequest(t, http.MethodPost, path, strings.NewReader(body))
		reqB := packageAuthRequest(t, http.MethodPost, path, strings.NewReader(body))
		reqA.Header.Set("Content-Type", "application/json")
		reqB.Header.Set("Content-Type", "application/json")
		var wg sync.WaitGroup
		codes := make([]int, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			codes[0] = doPackage(api.handlePackageImportApply, reqA).Code
		}()
		go func() {
			defer wg.Done()
			codes[1] = doPackage(api.handlePackageImportApply, reqB).Code
		}()
		wg.Wait()
		ok, blocked := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				ok++
			case http.StatusConflict, http.StatusNotFound:
				blocked++
			default:
				t.Errorf("FR-038 / D13 row 2: got %d, want 200 and 409 import_in_progress (or 404)", c)
			}
		}
		require.Equal(t, 1, ok, "codes %v", codes)
		require.Equal(t, 1, blocked, "codes %v", codes)
	})
}

// TestPackagesApply_PostCommitWarnings is TDD row 27 (D7 rows 7–9 and 17, FR-036).
func TestPackagesApply_PostCommitWarnings(t *testing.T) {
	t.Run("D7-7 reload error is activation pending", func(t *testing.T) {
		res, raw := applyWithSeams(t, func() {
			packageT5.Reload = func() (bool, error) { return false, errors.New("reload failed") }
			packageT5.EnsureMain = func(string, string) error {
				t.Error("main chat must not be attempted when publication is not confirmed")
				return nil
			}
		})
		require.Equal(t, gen.PackageImportResultActivationPending, res.Activation)
		require.NotNil(t, res.ActivationMessage)
		require.Contains(t, strings.ToLower(*res.ActivationMessage), "restart")
		require.NotContains(t, strings.ToLower(string(raw)), "nothing imported")
		require.NotEmpty(t, res.Created, "the stored agent is kept")
	})

	t.Run("D7-17 reload timeout is activation pending", func(t *testing.T) {
		res, _ := applyWithSeams(t, func() {
			packageT5.ReloadTimeout = true
			packageT5.Reload = func() (bool, error) { return false, errors.New("reload timed out") }
		})
		require.Equal(t, gen.PackageImportResultActivationPending, res.Activation)
	})

	t.Run("D7-8 main chat failure is a warning", func(t *testing.T) {
		res, raw := applyWithSeams(t, func() {
			packageT5.Reload = func() (bool, error) { return true, nil }
			packageT5.EnsureMain = func(string, string) error { return errors.New("chat failed") }
		})
		require.Equal(t, gen.PackageImportResultActivationActive, res.Activation)
		require.Contains(t, resultWarningCodes(res), "main_chat_pending")
		require.NotContains(t, strings.ToLower(string(raw)), "nothing imported")
	})

	t.Run("D7-9 audit failure after commit is a warning", func(t *testing.T) {
		res, raw := applyWithSeams(t, func() {
			packageT5.Reload = func() (bool, error) { return true, nil }
			packageT5.AuditWrite = func(string, map[string]string) error { return errAuditFailed }
		})
		require.Equal(t, gen.PackageImportResultActivationActive, res.Activation)
		require.Contains(t, resultWarningCodes(res), codeAuditFailed)
		require.NotContains(t, strings.ToLower(string(raw)), "nothing imported")
		require.NotEmpty(t, res.Created)
	})
}

// TestPackagesApply_MainChatScope is TDD row 21b (FR-036a).
func TestPackagesApply_MainChatScope(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	var mains []string
	packageT5.Reload = func() (bool, error) { return true, nil }
	packageT5.EnsureMain = func(agentID, workspaceID string) error {
		mains = append(mains, agentID+"@"+workspaceID)
		return nil
	}
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	y := createMainAgent(t, api, "Yarrow", "soul y")
	v := createMainAgent(t, api, "Vetch", "soul v")
	beforeY := fileBytes(t, filepath.Join(api.homePath, "entities", "agents", y+".json"))
	beforeV := fileBytes(t, filepath.Join(api.homePath, "entities", "agents", v+".json"))
	prev := stageUpload(t, api, validMainPackage(t))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.Equal(t, []string{res.Created[0].NewId + "@" + ws}, mains, "only the imported Main gets a main chat")
	require.Equal(t, beforeY, fileBytes(t, filepath.Join(api.homePath, "entities", "agents", y+".json")))
	require.Equal(t, beforeV, fileBytes(t, filepath.Join(api.homePath, "entities", "agents", v+".json")))
}

// TestPackagesApply_NativeWorkerReachable is TDD row 21c (FR-036b).
func TestPackagesApply_NativeWorkerReachable(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	packageT5.EnsureMain = func(string, string) error {
		t.Error("a native worker must not get a main chat")
		return nil
	}
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	rec := map[string]any{
		"id": specSourceID, "name": "Summariser", "type": "worker", "description": "summarises",
	}
	pkg := buildAgentPackage(t, specSourceID, "Summariser", rec, []byte("Be brief.\n"), nil, 0)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	require.Contains(t, strings.ToLower(string(got.Body)), "delegat", "FR-036b: the result says the worker is reached by delegation")
	res := decodeResult(t, got.Body)
	require.NotEqual(t, specSourceID, res.Created[0].NewId)
	store, err := os.ReadFile(filepath.Join(api.homePath, "entities", "delegation", ws+".json"))
	if err == nil {
		require.NotContains(t, string(store), `"to_agent":"`+res.Created[0].NewId+`"`, "import must not draw an edge to the worker")
	}
}

// TestPackagesApply_Rollback is TDD row 26 (D7 revision 7, FR-037, FR-049).
func TestPackagesApply_Rollback(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		step   string
		setup  func()
	}{
		{"D7-1 soul replace fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func() { packageT5.CreatePendingFault = "soul" }},
		{"D7-2 fault before the join", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func() { packageT5.CrashAt = "membership" }},
		{"D7-3 AGENT.md replace fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func() { packageT5.CreatePendingFault = "agentmd" }},
		{"D7-4 membership write fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func() {
			packageT5.WriteWorkspace = func(string, string, workspace.Workspace) error { return errors.New("membership") }
		}},
		{"D7-22 publish rename fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func() {
			packageT5.PublishRename = func(string, string) error { return errors.New("rename") }
		}},
		{"D7-11 rollback keeps its code when audit fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func() {
			packageT5.WriteWorkspace = func(string, string, workspace.Workspace) error { return errors.New("membership") }
			packageT5.AuditWrite = func(string, map[string]string) error { return errAuditFailed }
		}},
		{"D7-6 DiscardPending fails", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C2", func() {
			packageT5.CrashAt = "membership"
			packageT5.DiscardPending = func(string) error { return errors.New("pending remains") }
		}},
		{"D7-13 C1 membership removal fails and C2 still runs", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C1", func() {
			var n int
			packageT5.SaveDelegation = func(string, string, []workspace.DelegationEdge) error { return errors.New("self-row") }
			packageT5.WriteWorkspace = func(string, string, workspace.Workspace) error {
				n++
				if n > 1 {
					return errors.New("leave")
				}
				return nil
			}
		}},
		{"D7-14 partial C1", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C1", func() {
			var n int
			packageT5.PublishRename = func(string, string) error { return errors.New("rename") }
			packageT5.SaveDelegation = func(string, string, []workspace.DelegationEdge) error {
				n++
				if n >= 2 {
					return errors.New("leave save")
				}
				return nil
			}
		}},
		{"D7-18 partial C2 leaves the folder", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C2", func() {
			packageT5.CrashAt = "membership"
			packageT5.DiscardPending = func(id string) error { return errors.New("folder remains") }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			tc.setup()
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			before := entityNames(t, api.homePath)
			prev := stageUpload(t, api, validMainPackage(t))
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			er := requirePackageStatus(t, got, tc.status, tc.code)
			requireNoRollbackActivation(t, er)
			if tc.step != "" {
				require.Equal(t, tc.step, packageDetail(er, "failed_step"), "[r7] failed_step")
				require.NotEmpty(t, packageDetail(er, "agent_id"))
				require.NotEmpty(t, packageDetail(er, "staging_id"))
			}
			require.Equal(t, before, entityNames(t, api.homePath), "C2 discards the pending record; no .json appears")
			if tc.code == omnpkg.CodeImportRolledBack {
				_, err := os.Stat(filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
				require.True(t, os.IsNotExist(err), "complete rollback retires the staging folder")
			}
		})
	}

	t.Run("D7-5 competing writer keeps Z and drops X", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		var agentID string
		acquired := make(chan struct{}, 1)
		packageT5.AfterMembershipWrite = func(wsID, id string) {
			agentID = id
			go func() {
				unlock := workspace.LockID(wsID)
				acquired <- struct{}{}
				unlock()
			}()
		}
		packageT5.SaveDelegation = func(string, string, []workspace.DelegationEdge) error {
			return errors.New("self-row")
		}
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		er := requirePackageStatus(t, got, http.StatusInternalServerError, omnpkg.CodeImportRolledBack)
		requireNoRollbackActivation(t, er)
		require.NotEmpty(t, agentID, "the membership hook must run inside the lock hold")
		select {
		case <-acquired:
		case <-packageCtx(t).Done():
			t.Fatal("D7-5: competing LockID did not acquire the workspace lock after the helper returned")
		}
		team := workspaceTeam(t, api.homePath, ws)
		require.NotContains(t, team, agentID, "X is removed before the lock is released")
	})

	t.Run("D7-10 invisible until commit, then rolled back", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		var probed string
		packageT5.AfterMembershipWrite = func(_, id string) {
			probed = id
			requireAgentInvisible(t, api, id)
		}
		packageT5.PublishRename = func(string, string) error { return errors.New("rename") }
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		er := requirePackageStatus(t, got, http.StatusInternalServerError, omnpkg.CodeImportRolledBack)
		requireNoRollbackActivation(t, er)
		require.NotEmpty(t, probed, "FR-049: the invisibility probe must run before the rename")
		requireAgentInvisible(t, api, probed)
	})
}

// TestPackages_StagingHTTP is the handler projection of Dataset D9 and the
// slot limit (FR-022, FR-039, FR-044). The state machine itself is the unit
// test TestStaging_StateMachine.
func TestPackages_StagingHTTP(t *testing.T) {
	t.Run("D9-4 a staging that never existed is 404", func(t *testing.T) {
		api := packageAPI(t)
		requirePackageStatus(t, callPackageApply(t, api, "does-not-exist", applyBody("new", "ws")), http.StatusNotFound, omnpkg.CodeStagingNotFound)
	})

	t.Run("D9-7 a traversal id is 404 and reads no file", func(t *testing.T) {
		api := packageAPI(t)
		sentinel := filepath.Join(api.homePath, "entities", "secret.txt")
		require.NoError(t, os.MkdirAll(filepath.Dir(sentinel), 0o700))
		require.NoError(t, os.WriteFile(sentinel, []byte("do-not-read"), 0o600))
		requirePackageStatus(t, callPackageApply(t, api, "..%2F..%2Fentities", applyBody("new", "ws")), http.StatusNotFound, omnpkg.CodeStagingNotFound)
		got, err := os.ReadFile(sentinel)
		require.NoError(t, err)
		require.Equal(t, "do-not-read", string(got))
	})

	t.Run("D9 expiry at exactly 30 minutes", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
		packageT5.Now = func() time.Time { return start }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		packageT5.Now = func() time.Time { return start.Add(30 * time.Minute) }
		requirePackageStatus(t, callPackageApply(t, api, prev.StagingId, applyBody("new", ws)), http.StatusNotFound, omnpkg.CodeStagingNotFound)
	})

	t.Run("D9-1 29 min 59 s is still ready", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
		packageT5.Now = func() time.Time { return start }
		packageT5.Reload = func() (bool, error) { return true, nil }
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		packageT5.Now = func() time.Time { return start.Add(30*time.Minute - time.Second) }
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	})

	t.Run("sixth staged upload is 409", func(t *testing.T) {
		api := packageAPI(t)
		var last packageHTTP
		for i := 0; i < omnpkg.MaxStagedImports; i++ {
			last = callPackageImport(t, api, validMainPackage(t))
			require.Equal(t, http.StatusCreated, last.Code, "slot %d body=%s", i+1, last.Body)
		}
		requirePackageStatus(t, callPackageImport(t, api, validMainPackage(t)), http.StatusConflict, omnpkg.CodeTooManyStagedImports)
	})

	t.Run("FR-044 unfinished journal warns on the next upload", func(t *testing.T) {
		api := packageAPI(t)
		writeStagingJournal(t, api.homePath, "stg-unfinished", specSourceID, "ws-1", omnpkg.JournalRollbackIncomplete)
		got := callPackageImport(t, api, validMainPackage(t))
		require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
		require.Contains(t, previewWarningCodes(decodePreview(t, got.Body)), "unfinished_import_found")
	})
}

// TestImportCreateParity is TDD row 21 (FR-034, FR-047).
func TestImportCreateParity(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	const (
		soul = "Answer in rhyme."
		desc = "Drafts"
	)
	restBody := `{"name":"ParityWriter","type":"Main","soul":"` + soul + `","description":"` + desc + `","model":"m-1","provider":"openrouter","color":"#4F8A80","max_tool_iterations":7,"voice":"alloy","memory_enabled":false,"context_window_override":64000}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(restBody))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "REST create fixture; body=%s", w.Body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	restRec := readAgentRecord(t, api.homePath, created.ID)
	_, restHasVoice := restRec["voice"]
	require.False(t, restHasVoice, "REST create still does not store voice (known limit L17)")

	rec := mainRecord(specSourceID, "ParityWriter", "  "+desc+"  ")
	rec["color"] = "#4F8A80"
	rec["max_tool_iterations"] = 7
	rec["voice"] = "alloy"
	rec["memory_enabled"] = false
	rec["context_window_override"] = 64000
	pkg := buildAgentPackage(t, specSourceID, "ParityWriter", rec, []byte("\n  "+soul+"\n\n"), []byte("  rules\n"), 0)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	imp := readAgentRecord(t, api.homePath, res.Created[0].NewId)
	require.Equal(t, restRec["description"], imp["description"], "FR-034 description")
	require.Equal(t, restRec["color"], imp["color"])
	require.EqualValues(t, restRec["max_tool_iterations"], imp["max_tool_iterations"])
	require.Equal(t, soul, string(fileBytes(t, filepath.Join(api.homePath, "agents", res.Created[0].NewId, "SOUL.md"))), "prompt is trimmed")
	require.Equal(t, "alloy", imp["voice"], "FR-047 voice stored as create_agent stores it")
	require.Equal(t, false, imp["memory_enabled"])
	require.EqualValues(t, 64000, imp["context_window_override"])
	require.NotEqual(t, specSourceID, res.Created[0].NewId)
}

// TestPackages_ConfigUntouched is TDD row 30 (SC-008, FR-034).
func TestPackages_ConfigUntouched(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	path := filepath.Join(api.homePath, "config.json")
	before := fileBytes(t, path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	prev := stageUpload(t, api, validMainPackage(t))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	require.Equal(t, before, fileBytes(t, path))
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(after.ModTime()), "config.json modification time unchanged")
}

// TestPackages_TwoHomeRoundTrip is TDD row 31 (SC-001, FR-031).
func TestPackages_TwoHomeRoundTrip(t *testing.T) {
	src := packageAPI(t)
	id := createMainAgent(t, src, "Writer", "\n  Answer in rhyme.\n\n")
	require.NoError(t, os.WriteFile(filepath.Join(src.homePath, "agents", id, "AGENT.md"), []byte("  rules\n"), 0o600))
	exp := callPackageExport(t, src, `{"agent_ids":["`+id+`"]}`)
	require.Equal(t, http.StatusOK, exp.Code, "body=%s", exp.Body)

	dst := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, dst, "Research", "notes")
	prev := stageUpload(t, dst, exp.Body)
	got := callPackageApply(t, dst, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.NotEqual(t, id, res.Created[0].NewId, "FR-031")
	require.Equal(t, "Writer", res.Created[0].Name)
	require.Equal(t, "Answer in rhyme.", string(fileBytes(t, filepath.Join(dst.homePath, "agents", res.Created[0].NewId, "SOUL.md"))))
	require.Equal(t, "  rules\n", string(fileBytes(t, filepath.Join(dst.homePath, "agents", res.Created[0].NewId, "AGENT.md"))), "AGENT.md stays byte-identical")
}

func stageUpload(t *testing.T, api *restAPI, pkg []byte) gen.PackageImportPreview {
	t.Helper()
	got := callPackageImport(t, api, pkg)
	require.Equal(t, http.StatusCreated, got.Code, "upload before apply; body=%s", got.Body)
	return decodePreview(t, got.Body)
}

func applyBody(mode, workspaceID string) string {
	if workspaceID == "" {
		return `{"mode":` + packageJSONString(mode) + `}`
	}
	return `{"mode":` + packageJSONString(mode) + `,"workspace_id":` + packageJSONString(workspaceID) + `}`
}

func decodeResult(t *testing.T, raw []byte) gen.PackageImportResult {
	t.Helper()
	var res gen.PackageImportResult
	require.NoError(t, json.Unmarshal(raw, &res), "result body=%s", raw)
	return res
}

func resultWarningCodes(res gen.PackageImportResult) []string {
	out := make([]string, 0, len(res.Warnings))
	for _, w := range res.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func applyWithSeams(t *testing.T, setup func()) (gen.PackageImportResult, []byte) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	setup()
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, validMainPackage(t))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	return decodeResult(t, got.Body), got.Body
}

func importNamed(t *testing.T, existing []string, incoming string) gen.PackageImportResult {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	seedClashNames(t, api, existing)
	rec := mainRecord(specSourceID, incoming, "Drafts")
	pkg := buildAgentPackage(t, specSourceID, incoming, rec, []byte("Answer in rhyme.\n"), nil, 0)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	return decodeResult(t, got.Body)
}

func seedClashNames(t *testing.T, api *restAPI, names []string) {
	t.Helper()
	for i, name := range names {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		seedListedAgent(t, api, config.AgentConfig{ID: id, Name: name, Type: "custom"})
	}
}

func writerClashNames(through int) []string {
	names := []string{"Writer"}
	if through >= 1 {
		names = append(names, "Writer (imported)")
	}
	for i := 2; i <= through; i++ {
		names = append(names, fmt.Sprintf("Writer (imported %d)", i))
	}
	return names
}

func packageDetail(er gen.ErrorResponse, key string) string {
	if er.Details == nil {
		return ""
	}
	v, ok := (*er.Details)[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func requireNoRollbackActivation(t *testing.T, er gen.ErrorResponse) {
	t.Helper()
	require.NotContains(t, er.Error, "activation")
	if er.Details == nil {
		return
	}
	_, ok := (*er.Details)["activation"]
	require.False(t, ok, "[r7] no details.activation on a rollback answer")
}

func entityNames(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, "entities", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func readAgentRecord(t *testing.T, home, id string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(fileBytes(t, filepath.Join(home, "entities", "agents", id+".json")), &m))
	return m
}

func workspaceTeam(t *testing.T, home, id string) []string {
	t.Helper()
	var ws struct {
		CoreTeam []string `json:"core_team"`
	}
	raw, err := os.ReadFile(filepath.Join(home, "entities", "workspaces", id+".json"))
	if err != nil {
		raw, err = os.ReadFile(filepath.Join(home, "workspaces", id+".json"))
	}
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &ws))
	return ws.CoreTeam
}

func requireAgentInvisible(t *testing.T, api *restAPI, id string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(api.homePath, "entities", "agents", id+".json"))
	require.True(t, os.IsNotExist(err), "FR-049: %s.json must not exist before commit", id)
	list := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(list, req)
	require.NotContains(t, list.Body.String(), id, "agent list must not show a pending agent")
	one := httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	get.URL.Path = "/api/v1/agents/" + id
	api.HandleAgents(one, get)
	require.Equal(t, http.StatusNotFound, one.Code, "GET agent must 404 a pending id; body=%s", one.Body)
	sess := httptest.NewRecorder()
	body := strings.NewReader(`{"agent_id":"` + id + `"}`)
	post := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", body)
	post.Header.Set("Content-Type", "application/json")
	api.HandleSessions(sess, post)
	require.NotEqual(t, http.StatusCreated, sess.Code, "a pending agent must not get a session")
}

func writeStagingJournal(t *testing.T, home, staging, agent, ws string, state omnpkg.JournalState) {
	t.Helper()
	dir := filepath.Join(home, "backups", ".staging", staging)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, omnpkg.WriteJournal(dir, omnpkg.JournalFile{
		State: state, AgentID: agent, StagingID: staging, WorkspaceID: ws,
	}))
}
