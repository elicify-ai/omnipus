// packages_t5_apply_red_test.go — RED tests for POST /packages/import/{id}/apply
// (epic #1094, T-5). Expected values are spec revision 7. The handler is the
// 501 stub, so each case fails on the spec status (or on recovery's error)
// rather than on an undefined symbol.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/gorilla/websocket"
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
		frames := agentCreatedRecorder(api)
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
		gotFrames := frames()
		require.Len(t, gotFrames, 1, "oracle 7 positive control: a confirmed import emits one agent_created")
		require.Equal(t, res.Created[0].NewId, gotFrames[0].AgentId)
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

	// Sign-off C-2: apply re-reads the staged package and refuses the same raw effort.
	t.Run("C-2 primary reasoning_effort of 33 Unicode characters is refused at apply", func(t *testing.T) {
		applyEffortRefusal(t, recordWithPrimaryEffort(effortRunes(33)), effortPrimaryField, effortLengthSentence)
	})
	t.Run("C-2 primary reasoning_effort of 32 Unicode characters is kept", func(t *testing.T) {
		applyEffortAccepted(t, recordWithPrimaryEffort(effortRunes(32)), func(t *testing.T, stored map[string]any) {
			require.Equal(t, effortRunes(32), stored["reasoning_effort"])
		})
	})
	t.Run("C-2 primary reasoning_effort JSON null is refused at apply", func(t *testing.T) {
		applyEffortRefusal(t, recordWithPrimaryEffort(nil), effortPrimaryField, effortNullSentence)
	})
	t.Run("C-2 fallback reasoning_effort of 33 Unicode characters is refused at apply", func(t *testing.T) {
		applyEffortRefusal(t, recordWithFallbackEffort(effortRunes(33)), effortFallbackField, effortLengthSentence)
	})
	t.Run("C-2 fallback reasoning_effort of 32 Unicode characters is kept", func(t *testing.T) {
		applyEffortAccepted(t, recordWithFallbackEffort(effortRunes(32)), func(t *testing.T, stored map[string]any) {
			chain, ok := stored["fallback_models"].([]any)
			require.True(t, ok, "stored fallback_models=%#v", stored["fallback_models"])
			require.Len(t, chain, 1)
			entry, ok := chain[0].(map[string]any)
			require.True(t, ok, "entry=%#v", chain[0])
			require.Equal(t, effortRunes(32), entry["reasoning_effort"])
		})
	})
	t.Run("C-2 fallback reasoning_effort JSON null is refused at apply", func(t *testing.T) {
		applyEffortRefusal(t, recordWithFallbackEffort(nil), effortFallbackField, effortNullSentence)
	})

	// F05 at apply. The global limit is changed after the preview where the row says so.
	t.Run("F05 explicit max_tool_iterations -1 is refused at apply", func(t *testing.T) {
		applyIterationsRefusal(t, true, -1, iterationsBoundSentence, 0)
	})
	t.Run("F05 omitted max_tool_iterations stores no own limit", func(t *testing.T) {
		applyIterationsAccepted(t, false, nil, nil)
	})
	t.Run("F05 max_tool_iterations 1 is kept", func(t *testing.T) {
		applyIterationsAccepted(t, true, 1, float64(1))
	})
	t.Run("F05 max_tool_iterations 1001 is refused at apply when the global limit is 1000", func(t *testing.T) {
		applyIterationsRefusal(t, true, 1001, "1000", 1000)
	})
	t.Run("F05 max_tool_iterations 900 is refused at apply when the global limit is 200", func(t *testing.T) {
		applyIterationsRefusal(t, true, 900, "200", 0)
	})

	// F07 / FR-042 / D14: skill and provider differences are listed, not only the name.
	t.Run("F07 preview freshness lists skill and provider differences", func(t *testing.T) {
		previewFreshnessCases(t)
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
		mux := packageRouteMux(t, api)
		cfg := api.agentLoop.GetConfig()
		serve := func(req *http.Request) int {
			req = req.WithContext(putConfigSnapshot(req.Context(), cfg))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			return w.Code
		}
		var wg sync.WaitGroup
		codes := make([]int, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			codes[0] = serve(reqA)
		}()
		go func() {
			defer wg.Done()
			codes[1] = serve(reqB)
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
		setup  func(t *testing.T, home string)
	}{
		{"D7-1 soul replace fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func(t *testing.T, home string) { packageT5.CreatePendingFault = "soul" }},
		{"D7-2 fault before the join", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func(t *testing.T, home string) { packageT5.CrashAt = "membership" }},
		{"D7-3 AGENT.md replace fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func(t *testing.T, home string) { packageT5.CreatePendingFault = "agentmd" }},
		{"D7-4 membership write fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func(t *testing.T, home string) {
			packageT5.WriteWorkspace = func(string, string, workspace.Workspace) error { return errors.New("membership") }
		}},
		{"D7-22 publish rename fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func(t *testing.T, home string) {
			packageT5.PublishRename = func(string, string) error { return errors.New("rename") }
		}},
		{"D7-11 rollback keeps its code when audit fails", http.StatusInternalServerError, omnpkg.CodeImportRolledBack, "", func(t *testing.T, home string) {
			packageT5.WriteWorkspace = func(string, string, workspace.Workspace) error { return errors.New("membership") }
			packageT5.AuditWrite = func(string, map[string]string) error { return errAuditFailed }
		}},
		{"D7-6 DiscardPending fails", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C2", func(t *testing.T, home string) {
			packageT5.CrashAt = "membership"
			packageT5.DiscardPending = func(string) error { return errors.New("pending remains") }
		}},
		{"D7-13 C1 membership removal fails and C2 still runs", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C1", func(t *testing.T, home string) {
			var n int
			packageT5.SaveDelegation = func(string, string, []workspace.DelegationEdge) error { return errors.New("self-row") }
			packageT5.WriteWorkspace = func(dir, wsID string, w workspace.Workspace) error {
				n++
				if n > 1 {
					return errors.New("leave")
				}
				require.Equal(t, filepath.Join(home, "workspaces"), dir)
				require.Equal(t, wsID, w.ID)
				// SaveRecord takes the file sidecar, not the already-held LockID.
				return workspace.SaveRecord(home, w)
			}
			t.Cleanup(func() { require.Equal(t, 2, n, "D7-13: real join, then failed C1 membership removal") })
		}},
		{"D7-14 partial C1", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C1", func(t *testing.T, home string) {
			var n int
			packageT5.PublishRename = func(string, string) error { return errors.New("rename") }
			packageT5.SaveDelegation = func(home, wsID string, edges []workspace.DelegationEdge) error {
				n++
				if n >= 2 {
					return errors.New("leave save")
				}
				return workspace.SaveDelegation(home, wsID, edges)
			}
			t.Cleanup(func() { require.Equal(t, 2, n, "D7-14: seed persisted, then C1 delegation save failed") })
		}},
		{"D7-18 partial C2 leaves the folder", http.StatusInternalServerError, omnpkg.CodeImportRollbackIncomplete, "C2", func(t *testing.T, home string) {
			packageT5.CrashAt = "membership"
			packageT5.DiscardPending = func(id string) error {
				// D7-18 fails only AFTER the pending record has been deleted.
				if err := os.Remove(filepath.Join(home, "entities", "agents", id+".pending")); err != nil {
					return err
				}
				return errors.New("folder remains")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			survivor := createMainAgent(t, api, "Survivor", "Keep the existing prompt.")
			require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "agents", survivor, "AGENT.md"), []byte("Keep these rules.\n"), 0o600))
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			createWorkspaceViaAPI(t, api, "Untouched", "second workspace")
			before := t5CaptureRollback(t, api.homePath)
			homeBefore := snapshotHome(t, api.homePath)
			listedBefore := listedAgentIDs(t, api)
			frames := agentCreatedRecorder(api)
			tc.setup(t, api.homePath)
			packageT5.Reload = func() (bool, error) {
				t.Error("rollback must not request a live-publication reload")
				return false, errors.New("unexpected rollback reload")
			}
			packageT5.EnsureMain = func(string, string) error {
				t.Error("rollback must not prepare a main chat")
				return errors.New("unexpected rollback main chat")
			}
			prev := stageUpload(t, api, validMainPackage(t))
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			er := requirePackageStatus(t, got, tc.status, tc.code)
			requireNoRollbackActivation(t, er)
			id := t5NewRollbackID(t, api.homePath, before.entries)
			if tc.step != "" {
				require.Equal(t, tc.step, packageDetail(er, "failed_step"), "[r7] failed_step")
				require.NotEmpty(t, packageDetail(er, "agent_id"))
				require.NotEmpty(t, packageDetail(er, "staging_id"))
				require.Equal(t, id, packageDetail(er, "agent_id"))
				require.Equal(t, prev.StagingId, packageDetail(er, "staging_id"))
			}
			if tc.code == omnpkg.CodeImportRollbackIncomplete {
				// Check the specified partial state before clearing the disk fault.
				t5RequireRollbackState(t, api.homePath, ws, id, tc.name, before)
				requireAgentInvisible(t, api, id)
				state, err := api.packageRegistry().State(prev.StagingId)
				require.NoError(t, err)
				require.Equal(t, omnpkg.StagingNeedsCleanup, state)
				journal, err := omnpkg.ReadJournal(filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
				require.NoError(t, err)
				require.Equal(t, omnpkg.JournalRollbackIncomplete, journal.State)
				// Revision 7 checks the stored-state cleanup AFTER fault-free recovery.
				packageT5 = packageT5Seams{}
				require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
			}
			t5RequireRollbackState(t, api.homePath, ws, id, "", before)
			// Required even after recovery; a retained terminal journal is NOT a pass.
			t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
			require.Empty(t, frames(), "oracle 7: a rollback sends no agent_created frame")
			require.Equal(t, listedBefore, listedAgentIDs(t, api), "oracle 1: GET /api/v1/agents equals the set before")
			requireNoSessionNames(t, api, id)
			t5RequireOtherFiles(t, homeBefore, snapshotHome(t, api.homePath), id, ws)
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
		z := createMainAgent(t, api, "Zed", "Keep Z.")
		var agentID string
		errCh := make(chan error, 1)
		packageT5.AfterMembershipWrite = func(wsID, id string) {
			agentID = id
			require.NotContains(t, workspaceTeam(t, api.homePath, ws), z, "D7-5: Z is not on the team while the import holds W's lock")
			go func() {
				unlock := workspace.LockID(wsID)
				defer unlock()
				errCh <- t5AppendTeam(api.homePath, wsID, z)
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
		case err := <-errCh:
			require.NoError(t, err, "D7-5: the competing writer adds Z after the helper releases W's lock")
		case <-packageCtx(t).Done():
			t.Fatal("D7-5: competing writer did not acquire the workspace lock after the helper returned")
		}
		team := workspaceTeam(t, api.homePath, ws)
		require.NotContains(t, team, agentID, "X is removed before the lock is released")
		require.Contains(t, team, z, "D7-5: the competing writer's Z is kept")
	})

	t.Run("D7-10 invisible until commit, then rolled back", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		var probed string
		known := createMainAgent(t, api, "Visible", "A real chat partner.")
		requireSessionCreated(t, api, known)
		packageT5.AfterMembershipWrite = func(_, id string) {
			probed = id
			requireAgentInvisible(t, api, id)
			requirePackageChatRefused(t, api, id)
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
	restBody := `{"name":"ParityWriter","type":"Main","soul":"` + soul + `","description":"` + desc + `","model":"m-1","provider":"openrouter","color":"#4F8A80","max_tool_iterations":7,"voice":"alloy"}`
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
	require.Equal(t, desc, restRec["description"], "spec-normalised REST description")
	require.Equal(t, "#4F8A80", restRec["color"])
	require.EqualValues(t, 7, restRec["max_tool_iterations"])
	_, restHasVoice := restRec["voice"]
	require.False(t, restHasVoice, "REST create still does not store voice (known limit L17)")
	_, restHasMemory := restRec["memory_enabled"]
	require.False(t, restHasMemory, "REST create does not store memory_enabled")
	_, restHasWindow := restRec["context_window_override"]
	require.False(t, restHasWindow, "REST create does not store context_window_override")
	restPrimary, restProvider := recordModel(t, restRec)
	require.Equal(t, "m-1", restPrimary, "spec-literal REST model")
	require.Equal(t, "openrouter", restProvider, "spec-literal REST provider")

	toolID := createParityAgentByTool(t, api, soul, desc)
	toolRec := readAgentRecord(t, api.homePath, toolID)

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
	impPrimary, impProvider := recordModel(t, imp)
	require.Equal(t, "m-1", impPrimary)
	require.Equal(t, "openrouter", impProvider)
	require.Equal(t, restRec["type"], imp["type"], "shared type")
	toolPrimary, toolProvider := recordModel(t, toolRec)
	require.Equal(t, desc, toolRec["description"], "create_agent stores the normalised description")
	require.Equal(t, "#4F8A80", toolRec["color"])
	require.EqualValues(t, 7, toolRec["max_tool_iterations"])
	require.Equal(t, "m-1", toolPrimary)
	require.Equal(t, "openrouter", toolProvider)
	require.Equal(t, "alloy", toolRec["voice"], "FR-047: import and create_agent store the same voice")
	require.Equal(t, toolRec["memory_enabled"], imp["memory_enabled"])
	require.EqualValues(t, toolRec["context_window_override"], imp["context_window_override"])
	require.Equal(t, false, toolRec["memory_enabled"])
	require.EqualValues(t, 64000, toolRec["context_window_override"])
	require.Equal(t, soul, string(fileBytes(t, filepath.Join(api.homePath, "agents", toolID, "SOUL.md"))), "create_agent stores the trimmed prompt")
	// ARCH-RULING-T5-A-D Decision A. Expected red on current production:
	// withToolPolicyCoverageCheck subtracts baseline gaps, so import accepts
	// a gap REST create refuses.
	t.Run("FR-034_coverage_gap_refused_like_REST_create", testImportCoverageGapRefusedLikeRESTCreate)
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
	require.Equal(t, http.StatusBadRequest, sess.Code, "FR-049: a pending agent is an unknown agent; body=%s", sess.Body)
	require.Contains(t, sess.Body.String(), id)
	require.Contains(t, sess.Body.String(), "not found")
	requireNoSessionNames(t, api, id)
	agents, skipped, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "a corrupt record must not hide a pending agent")
	for _, agent := range agents {
		require.NotEqual(t, id, agent.ID, "agentstore.List must not contain a pending agent")
	}
}

func writeStagingJournal(t *testing.T, home, staging, agent, ws string, state omnpkg.JournalState) {
	t.Helper()
	dir := filepath.Join(home, "backups", ".staging", staging)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, omnpkg.WriteJournal(dir, omnpkg.JournalFile{
		State: state, AgentID: agent, StagingID: staging, WorkspaceID: ws,
	}))
}

func listedAgentIDs(t *testing.T, api *restAPI) []string {
	t.Helper()
	list := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(list, req)
	require.Equal(t, http.StatusOK, list.Code, "GET /api/v1/agents; body=%s", list.Body)
	var agents []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &agents), "agent list body=%s", list.Body)
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		require.NotEmpty(t, agent.ID)
		ids = append(ids, agent.ID)
	}
	sort.Strings(ids)
	return ids
}

// requireNoSessionNames is rollback oracle 7: no session, main chat or transcript names X.
func requireNoSessionNames(t *testing.T, api *restAPI, id string) {
	t.Helper()
	roots := []string{filepath.Join(api.homePath, "sessions")}
	agentRoot := filepath.Join(api.homePath, "agents")
	entries, err := os.ReadDir(agentRoot)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				roots = append(roots, filepath.Join(agentRoot, entry.Name(), "sessions"))
			}
		}
	}
	for _, root := range roots {
		if _, statErr := os.Stat(root); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			require.NoError(t, statErr, "session store %s", root)
		}
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info == nil || info.IsDir() {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "read session file %s", path)
			require.NotContains(t, string(raw), id, "oracle 7: session store names %s in %s", id, path)
			return nil
		})
		require.NoError(t, walkErr)
	}
}

func requireSessionCreated(t *testing.T, api *restAPI, id string) {
	t.Helper()
	sess := httptest.NewRecorder()
	post := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"`+id+`"}`))
	post.Header.Set("Content-Type", "application/json")
	api.HandleSessions(sess, post)
	require.Equal(t, http.StatusCreated, sess.Code, "positive control: a registered agent gets a session; body=%s", sess.Body)
}

// requirePackageChatRefused dials the production websocket handler and requires
// the unknown-agent refusal (spec invisibility probe, D7-10). The token is the
// test bearer already configured on the home.
func requirePackageChatRefused(t *testing.T, api *restAPI, id string) {
	t.Helper()
	handler := newWSHandler(bus.NewMessageBus(), api.agentLoop, "")
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/chat/ws", nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	write := func(frame wsClientFrameTestHelper) {
		t.Helper()
		raw, marshalErr := json.Marshal(frame)
		require.NoError(t, marshalErr)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
	}
	write(wsClientFrameTestHelper{Type: "auth", Token: packageTestBearer})
	write(wsClientFrameTestHelper{Type: "message", Content: "hello", AgentID: id})
	var saw string
	for i := 0; i < 6; i++ {
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		saw += string(raw) + "\n"
		if strings.Contains(string(raw), id) && strings.Contains(string(raw), "not available") {
			return
		}
	}
	t.Fatalf("FR-049: websocket chat to %s was not refused as an unknown agent; frames=%s", id, saw)
}

// t5AppendTeam is D7-5's competing writer. The caller already holds no lock;
// LockID in the caller serialises it behind the import.
func t5AppendTeam(home, wsID, agentID string) error {
	path := filepath.Join(home, "workspaces", wsID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record workspace.Workspace
	if err = json.Unmarshal(raw, &record); err != nil {
		return err
	}
	if record.ID == "" {
		record.ID = wsID
	}
	for _, id := range record.CoreTeam {
		if id == agentID {
			return nil
		}
	}
	record.CoreTeam = append(record.CoreTeam, agentID)
	return workspace.SaveRecord(home, record)
}

func recordModel(t *testing.T, rec map[string]any) (string, string) {
	t.Helper()
	model, _ := rec["model"].(map[string]any)
	primary, _ := model["primary"].(string)
	provider, _ := model["provider"].(string)
	require.NotEmpty(t, primary, "stored model.primary missing in %#v", rec["model"])
	require.NotEmpty(t, provider, "stored model.provider missing in %#v", rec["model"])
	return primary, provider
}

// createParityAgentByTool runs the real create_agent tool (FR-047). It does not
// copy an import result into the expectation.
func createParityAgentByTool(t *testing.T, api *restAPI, soul, desc string) string {
	t.Helper()
	deps := &systools.Deps{
		Home:         api.homePath,
		ConfigPath:   filepath.Join(api.homePath, "config.json"),
		GetCfg:       api.agentLoop.GetConfig,
		MutateConfig: api.agentLoop.MutateConfig,
		SaveConfigLocked: func(cfg *config.Config) error {
			return config.SaveConfig(filepath.Join(api.homePath, "config.json"), cfg)
		},
	}
	reg := tools.NewToolRegistry()
	reg.Register(systools.NewAgentCreateTool(deps))
	res := reg.Execute(context.Background(), "create_agent", map[string]any{
		"name":                    "ParityWriter",
		"description":             desc,
		"soul":                    soul,
		"model":                   "m-1",
		"provider":                "openrouter",
		"agent_type":              "Main",
		"color":                   "#4F8A80",
		"max_tool_iterations":     7,
		"voice":                   "alloy",
		"memory_enabled":          false,
		"context_window_override": 64000,
	})
	require.NotNil(t, res)
	require.False(t, res.IsError, "create_agent failed: %s", res.ForLLM)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &payload), "create_agent result=%s", res.ForLLM)
	id, _ := payload["id"].(string)
	require.NotEmpty(t, id, "create_agent result has no id: %s", res.ForLLM)
	return id
}

// coverageGapAgentA is the existing agent with no per-agent entry for the
// ceiling tool that this test removes. coverageToolT is a shipped built-in
// (pkg/config/defaults.go::defaultToolPoliciesGeneral).
const (
	coverageGapAgentA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	coverageToolT     = "bash"
)

// testImportCoverageGapRefusedLikeRESTCreate is Decision A
// (ARCH-RULING-T5-A-D). The ceiling is the reconciled production ceiling.
// Agent A has no per-agent entry for T. The package agent carries an entry
// for T, so the new agent itself is covered and the only gap is A's.
func testImportCoverageGapRefusedLikeRESTCreate(t *testing.T) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	cfg := api.agentLoop.GetConfig()
	gaps := repairAndValidateToolPolicyCoverage(cfg)
	require.Empty(t, gaps, "repairAndValidateToolPolicyCoverage must leave a reconciled ceiling with no gaps before T is removed")
	shipped, ok := cfg.Sandbox.ToolPolicies[coverageToolT]
	require.True(t, ok, "bash must be on the reconciled ceiling (defaultToolPoliciesGeneral)")
	delete(cfg.Sandbox.ToolPolicies, coverageToolT)
	seedListedAgent(t, api, config.AgentConfig{ID: coverageGapAgentA, Name: "CeilingA", Type: "custom"})
	require.Nil(t, agentTools(t, api, coverageGapAgentA), "A must have no per-agent tool entry")

	rest := postRESTCreate(t, api, `{"name":"CoverageWriter","type":"Main","soul":"Answer in rhyme."}`)
	require.Equal(t, http.StatusBadRequest, rest.Code, "REST create must refuse A's gap for %s; body=%s", coverageToolT, rest.Body)
	require.Contains(t, string(rest.Body), coverageGapAgentA, "REST create body must name A")
	require.Contains(t, string(rest.Body), coverageToolT, "REST create body must name T")

	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	rec := mainRecord(specSourceID, "CoverageWriter", "Drafts")
	rec["tools"] = map[string]any{
		"builtin": map[string]any{
			"policies": map[string]any{coverageToolT: "ask"},
		},
	}
	pkg := buildAgentPackage(t, specSourceID, "CoverageWriter", rec, []byte("Answer in rhyme.\n"), nil, 0)
	before := agentEntityNames(t, api.homePath)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	if got.Code == http.StatusOK || got.Code == http.StatusCreated {
		t.Fatalf("FR-034: apply succeeded (HTTP %d) while REST create refused agent %s missing tool %s. "+
			"withToolPolicyCoverageCheck subtracts baseline gaps, so A's pre-existing gap is dropped and import succeeds. body=%s",
			got.Code, coverageGapAgentA, coverageToolT, got.Body)
	}
	er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
	require.Contains(t, er.Error, coverageGapAgentA, "the refusal must list the same gap REST create named (agent A)")
	require.Contains(t, er.Error, coverageToolT, "the refusal must list the same gap REST create named (tool T)")
	require.Equal(t, before, agentEntityNames(t, api.homePath), "no pending or final agent file for the new id")
	_, err := os.Stat(filepath.Join(api.packageStagingBase(), prev.StagingId, omnpkg.JournalName))
	require.ErrorIs(t, err, os.ErrNotExist, "a refusal before any write removes the journal")
	state, err := api.packageRegistry().State(prev.StagingId)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReady, state, "staging stays ready after a create-rule refusal")

	// Positive control. The spec route table says apply is 200
	// (export-import-s1-spec.md). The ruling wrote 201, which is the upload
	// status; asserting 201 would stay red after the subtraction is removed.
	cfg.Sandbox.ToolPolicies[coverageToolT] = shipped
	again := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, again.Code, "T restored to the global ceiling: the same apply succeeds (spec status 200). body=%s", again.Body)
}

func agentTools(t *testing.T, api *restAPI, id string) *config.AgentToolsCfg {
	t.Helper()
	for _, agent := range api.agentLoop.GetConfig().Agents.List {
		if agent.ID == id {
			return agent.Tools
		}
	}
	t.Fatalf("agent %s is not on the live roster", id)
	return nil
}

func postRESTCreate(t *testing.T, api *restAPI, body string) packageHTTP {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return packageHTTP{Code: w.Code, Body: w.Body.Bytes(), Hdr: w.Header()}
}

func agentEntityNames(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, "entities", "agents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}
