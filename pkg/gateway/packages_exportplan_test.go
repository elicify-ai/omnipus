package gateway

// RED B1 — the export review ("plan") handler tests (spec TDD order 6–12 and
// 14, issue #1967). Spec: docs/internal/specs/export-import-b1-spec.md
// revision 5 — "API and Data" (PackageExportPlan field table, left-out rules,
// disclosures), Datasets D1/D5/D6, FR-001…FR-009, SC-002/SC-003/SC-005.
//
// Every expected status, code, size and message below is transcribed from
// that specification (oracle independence). The RED stub handlers answer 501,
// so each assertion fails naming the missing feature, never a compile error.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// B1 helpers (local to this file; the T-5 harness is reused, not extended).
// ---------------------------------------------------------------------------

func callPackagePlanCreate(t *testing.T, api *restAPI, body string) packageHTTP {
	t.Helper()
	req := packageAuthRequest(t, http.MethodPost, "/api/v1/packages/export/plan", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return serveRegisteredPackage(t, api, req)
}

func callPackagePlanRead(t *testing.T, api *restAPI, planID string) packageHTTP {
	t.Helper()
	path := "/api/v1/packages/export/plan/" + planID
	req := packageAuthRequest(t, http.MethodGet, path, nil)
	req.URL.Path = path
	return serveRegisteredPackage(t, api, req)
}

func callPackageCancel(t *testing.T, api *restAPI, stagingID string) packageHTTP {
	t.Helper()
	path := "/api/v1/packages/import/" + stagingID
	req := packageAuthRequest(t, http.MethodDelete, path, nil)
	req.URL.Path = path
	return serveRegisteredPackage(t, api, req)
}

// b1Clock is a settable fake clock installed as the package clock seam
// (packageT5.Now, the existing staging-clock surface; GREEN's plan store must
// read the same packageNow() clock so the TTL and the cap are testable).
type b1Clock struct {
	mu  sync.Mutex
	now time.Time
}

func newB1Clock() *b1Clock {
	return &b1Clock{now: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)}
}

func (c *b1Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *b1Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// b1UseClock resets the fault seams and installs clk as the package clock.
func b1UseClock(t *testing.T, clk *b1Clock) {
	t.Helper()
	usePackageSeams(t)
	packageT5.Now = clk.Now
}

// b1SeedMainWriter seeds a custom Main agent ("Writer") whose SOUL.md is
// exactly 2,000 bytes and AGENT.md exactly 500 bytes (Dataset D1-1).
func b1SeedMainWriter(t *testing.T, api *restAPI, id, name string) {
	t.Helper()
	seedListedAgent(t, api, config.AgentConfig{
		ID: id, Name: name, Type: "custom", Description: "Drafts and edits prose",
		Model: &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"},
	})
	home := filepath.Join(api.homePath, "agents", id)
	require.NoError(t, os.WriteFile(filepath.Join(home, "SOUL.md"), d1Body(2000), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "AGENT.md"), d1Body(500), 0o600))
}

// b1SeedExternalWorker seeds Dataset D1-4: an external worker whose executor
// carries the D6-7/8/9 runtime values.
func b1SeedExternalWorker(t *testing.T, api *restAPI, id string) {
	t.Helper()
	seedListedAgent(t, api, config.AgentConfig{
		ID: id, Name: "Coder", Type: config.AgentTypeWorker, Description: "Runs Codex tasks",
		Model: &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"},
		Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
			Kind: config.ExecutorKindExternalCLI, CLI: "codex",
			CLIPath:      "/opt/x/CANARY_CLI_PATH/claude",
			CLIArgs:      "--token=CANARY_ARG --verbose",
			EnvOverrides: map[string]string{"API_TOKEN": "CANARY_ENV", "REGION": "eu"},
		}},
	})
}

// d1Body builds a deterministic printable body of exactly n bytes (same rule
// as the unit fixtures).
func d1Body(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// planMap decodes a review body as generic JSON. The review's wire shape is
// the spec's field table, so the test never imports a generated type that
// does not exist yet (contract regeneration is GREEN's first step).
func planMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m), "review body must be JSON, got %q", raw)
	return m
}

// b1PlanFileSizes checks the reviewed file entries: exactly len(pinned)+1
// entries; every pinned suffix present with its exact spec size (D1-1:
// SOUL.md 2000, AGENT.md 500); the unpinned agent.json entry present with a
// positive size. Entry order is not pinned by the spec.
func b1PlanFileSizes(t *testing.T, files []any) {
	t.Helper()
	require.Len(t, files, 3, "the review lists agent.json, SOUL.md and AGENT.md")
	seen := map[string]float64{}
	var total int64
	for _, f := range files {
		entry, ok := f.(map[string]any)
		require.True(t, ok, "file entry is an object: %v", f)
		path, ok := entry["path"].(string)
		require.True(t, ok, "file entry carries a path: %v", entry)
		size, ok := entry["size"].(float64)
		require.True(t, ok, "file entry %s carries a numeric size", path)
		matched := false
		for _, suffix := range []string{"agent.json", "SOUL.md", "AGENT.md"} {
			if strings.HasSuffix(path, suffix) {
				require.NotContains(t, seen, suffix, "two entries end in %s", suffix)
				seen[suffix] = size
				matched = true
				break
			}
		}
		require.True(t, matched, "unexpected reviewed file %q", path)
		total += int64(size)
	}
	require.Greater(t, seen["agent.json"], float64(0), "agent.json size is positive")
	require.Equal(t, float64(2000), seen["SOUL.md"], "reviewed SOUL.md size (spec D1-1)")
	require.Equal(t, float64(500), seen["AGENT.md"], "reviewed AGENT.md size (spec D1-1)")
	_ = total
}

// wantPlanLeftOutRow asserts one left_out row by position with the spec's
// exact type, reason, message and items.
func wantPlanLeftOutRow(t *testing.T, rows []any, idx int, typ, reason, message string, items []any) {
	t.Helper()
	require.Less(t, idx, len(rows), "expected left_out row %d (%s), got %d rows: %v", idx, typ, len(rows), rows)
	row, ok := rows[idx].(map[string]any)
	require.True(t, ok, "left_out row %d is an object", idx)
	require.Equal(t, typ, row["type"], "left_out row %d type", idx)
	require.Equal(t, reason, row["reason"], "left_out row %d (%s) reason", idx, typ)
	require.Equal(t, message, row["message"], "left_out row %d (%s) message is the exact spec text", idx, typ)
	if items == nil {
		require.Empty(t, row["items"], "left_out row %d (%s) carries no items", idx, typ)
	} else {
		require.Equal(t, items, row["items"], "left_out row %d (%s) items", idx, typ)
	}
}

// b1WantFixedLeftOut asserts the four rows every agent's review carries, in
// the spec's order (left_out rules table, "always" rows).
func b1WantFixedLeftOut(t *testing.T, rows []any) {
	t.Helper()
	wantPlanLeftOutRow(t, rows, 0, "agent_memory", "type_not_exportable",
		"This agent's memory is not included in this version.", nil)
	wantPlanLeftOutRow(t, rows, 1, "session", "type_not_exportable",
		"This agent's chats are not included.", nil)
	wantPlanLeftOutRow(t, rows, 2, "work_files", "type_not_exportable",
		"Workspace files are not included.", nil)
	wantPlanLeftOutRow(t, rows, 3, "team", "not_carried",
		"Team membership and delegation lines are not included. On import the agent joins the workspace you choose, with no delegation lines.", nil)
}

// b1WantFreeTextDisclosure asserts the one disclosure every review carries,
// with the exact spec text.
func b1WantFreeTextDisclosure(t *testing.T, disclosures []any) {
	t.Helper()
	require.Len(t, disclosures, 1, "B1 carries exactly one disclosure")
	d, ok := disclosures[0].(map[string]any)
	require.True(t, ok, "disclosure is an object")
	require.Equal(t, "free_text_copied", d["code"])
	require.Equal(t, "Text you wrote (prompts and instructions) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle.",
		d["message"], "the free-text disclosure text is exact")
}

// b1D6Canaries are Dataset D6-1…9; no review answer may carry any of them.
var b1D6Canaries = []string{
	"CANARY_CRED_VAULT", "CANARY_MASTER_KEY", "CANARY_CLI_TOKEN", "CANARY_AUTH_JSON",
	"CANARY_PROVIDER_KEY", "CANARY_CHANNEL_TOKEN", "CANARY_CLI_PATH",
	"--token=CANARY_ARG --verbose", "CANARY_ENV",
}

// ---------------------------------------------------------------------------
// Order 6 — TestExportPlanHandler_CreateAndRead.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_CreateAndRead — spec order 6: the 201 body matches
// the spec's PackageExportPlan field table for Dataset D1-1, the GET read-back
// is equal while held, and the fake-clock TTL boundary is exact: T+29:59 →
// 200 (equal), T+30:00 → 404 plan_not_found. A malformed plan id on the read
// path answers the same 404.
func TestExportPlanHandler_CreateAndRead(t *testing.T) {
	const body = `{"scope":"agents","agent_ids":["` + specSourceID + `"]}`

	clk := newB1Clock()
	b1UseClock(t, clk)
	api := packageAPI(t)
	b1SeedMainWriter(t, api, specSourceID, "Writer")

	got := callPackagePlanCreate(t, api, body)
	require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
	m := planMap(t, got.Body)

	planID, ok := m["plan_id"].(string)
	require.True(t, ok, "plan_id is a string; body=%v", m)
	_, err := uuid.Parse(planID)
	require.NoError(t, err, "plan_id is a UUID handle")
	require.Equal(t, "agents", m["scope"], "scope echoes the request")

	createdAt, err := time.Parse(time.RFC3339, m["created_at"].(string))
	require.NoError(t, err)
	expiresAt, err := time.Parse(time.RFC3339, m["expires_at"].(string))
	require.NoError(t, err)
	require.Equal(t, 30*time.Minute, expiresAt.Sub(createdAt), "expires_at − created_at = 30 minutes")

	elements, ok := m["elements"].([]any)
	require.True(t, ok, "elements is a list; body=%v", m)
	require.Len(t, elements, 1, "B1 reviews exactly one element")
	el, ok := elements[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "agent", el["type"], "element type")
	require.Equal(t, specSourceID, el["source_id"])
	require.Equal(t, "Writer", el["name"])
	require.Equal(t, "main", el["kind"], "a custom Main reviews as kind main")
	files, ok := el["files"].([]any)
	require.True(t, ok, "element files is a list")
	b1PlanFileSizes(t, files)
	var fileSum int64
	for _, f := range files {
		fileSum += int64(f.(map[string]any)["size"].(float64))
	}
	elBytes, ok := el["bytes"].(float64)
	require.True(t, ok, "element bytes is numeric")
	require.Equal(t, fileSum, int64(elBytes), "element bytes equals the sum of its file sizes")

	rows, ok := m["left_out"].([]any)
	require.True(t, ok, "left_out is a list")
	require.Len(t, rows, 4, "D1-1 has no skills or connectors: only the four fixed rows")
	b1WantFixedLeftOut(t, rows)

	secrets, ok := m["needs_secrets"].([]any)
	require.True(t, ok, "needs_secrets is a list")
	require.Empty(t, secrets, "a Main carries no removed values")

	disclosures, ok := m["disclosures"].([]any)
	require.True(t, ok, "disclosures is a list")
	b1WantFreeTextDisclosure(t, disclosures)

	sizeBytes, ok := m["size_bytes"].(float64)
	require.True(t, ok, "size_bytes is numeric")
	require.Greater(t, sizeBytes, float64(0), "size_bytes is the bundle size estimate")
	fileName, ok := m["file_name"].(string)
	require.True(t, ok, "file_name is a string")
	require.True(t, strings.HasSuffix(fileName, ".omnpkg"), "file_name ends in .omnpkg; got %q", fileName)

	// GET read-back while held: 200 and an equal body.
	gotRead := callPackagePlanRead(t, api, planID)
	require.Equal(t, http.StatusOK, gotRead.Code, "body=%s", gotRead.Body)
	require.Equal(t, m, planMap(t, gotRead.Body), "the read-back equals the created review")

	// Fake-clock TTL boundary: still held at T+29:59, gone at T+30:00.
	clk.Set(clk.Now().Add(29*time.Minute + 59*time.Second))
	gotHeld := callPackagePlanRead(t, api, planID)
	require.Equal(t, http.StatusOK, gotHeld.Code, "held at T+29:59; body=%s", gotHeld.Body)
	clk.Set(clk.Now().Add(time.Second))
	gotExpired := callPackagePlanRead(t, api, planID)
	requirePackageStatus(t, gotExpired, http.StatusNotFound, "plan_not_found")

	// A malformed plan id is the same unknown-plan outcome.
	gotBad := callPackagePlanRead(t, api, "not-a-uuid")
	requirePackageStatus(t, gotBad, http.StatusNotFound, "plan_not_found")
}

// ---------------------------------------------------------------------------
// Order 7 — TestExportPlanHandler_RequestValidation.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_RequestValidation — spec order 7: the exact
// 400/404/405/413/422 validation table.
func TestExportPlanHandler_RequestValidation(t *testing.T) {
	t.Run("agent_ids [] and [A,B] are 400 export_selection_invalid", func(t *testing.T) {
		api := packageAPI(t)
		for _, body := range []string{
			`{"scope":"agents","agent_ids":[]}`,
			`{"scope":"agents","agent_ids":["a","b"]}`,
		} {
			got := callPackagePlanCreate(t, api, body)
			requirePackageStatus(t, got, http.StatusBadRequest, "export_selection_invalid")
		}
	})

	t.Run("unknown scope, unknown property and non-JSON are 400", func(t *testing.T) {
		api := packageAPI(t)
		for _, body := range []string{
			`{"scope":"everything","agent_ids":["` + specSourceID + `"]}`,
			`{"scope":"agents","agent_ids":["` + specSourceID + `"],"roots":[]}`,
			`not json at all`,
		} {
			got := callPackagePlanCreate(t, api, body)
			require.Equal(t, http.StatusBadRequest, got.Code, "body=%q must be 400", got.Body)
		}
	})

	t.Run("a 1 MiB + 1 byte body is 413", func(t *testing.T) {
		api := packageAPI(t)
		b1SeedMainWriter(t, api, specSourceID, "Writer")
		// The padding is inside a JSON string so the body is still one JSON
		// value larger than the route limit (same rule as the T-5 413 rows).
		body := `{"scope":"agents","agent_ids":["` + specSourceID + `"],"pad":"` +
			strings.Repeat("a", packageJSONLimit) + `"}`
		got := callPackagePlanCreate(t, api, body)
		require.Equal(t, http.StatusRequestEntityTooLarge, got.Code, "body=%s", got.Body)
	})

	t.Run("unknown and traversal agent IDs are 404 agent_not_found", func(t *testing.T) {
		api := packageAPI(t)
		for _, id := range []string{"../x", "6b9a12d3-0000-4000-8000-000000000001"} {
			body := `{"scope":"agents","agent_ids":["` + id + `"]}`
			got := callPackagePlanCreate(t, api, body)
			requirePackageStatus(t, got, http.StatusNotFound, "agent_not_found")
		}
	})

	t.Run("mia reviews as 422 core_agent_not_exportable", func(t *testing.T) {
		api := packageAPI(t)
		seedListedAgent(t, api, config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore})
		got := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["mia"]}`)
		requirePackageStatus(t, got, http.StatusUnprocessableEntity, "core_agent_not_exportable")
	})

	t.Run("wrong methods answer 405", func(t *testing.T) {
		api := packageAPI(t)
		req := packageAuthRequest(t, http.MethodGet, "/api/v1/packages/export/plan", nil)
		gotGet := serveRegisteredPackage(t, api, req)
		require.Equal(t, http.StatusMethodNotAllowed, gotGet.Code, "GET on the create path is 405; body=%s", gotGet.Body)

		req = packageAuthRequest(t, http.MethodPost, "/api/v1/packages/export/plan/some-plan", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		gotPost := serveRegisteredPackage(t, api, req)
		require.Equal(t, http.StatusMethodNotAllowed, gotPost.Code, "POST on the read path is 405; body=%s", gotPost.Body)
	})
}

// ---------------------------------------------------------------------------
// Order 8 — TestExportPlanHandler_Guards.
// ---------------------------------------------------------------------------

// The three B1 routes, for the guard probes.
const (
	b1RoutePlanCreate = iota
	b1RoutePlanRead
	b1RouteCancel
)

// b1PlanProbe sends one of the three B1 routes with a chosen auth shape,
// mirroring the T-5 hitAccess auth cases (none / bypass).
func b1PlanProbe(t *testing.T, route int, auth string) packageHTTP {
	t.Helper()
	api := packageAPI(t)
	mux := packageRouteMux(t, api)

	var req *http.Request
	switch route {
	case b1RoutePlanCreate:
		req = httptest.NewRequest(http.MethodPost, "/api/v1/packages/export/plan",
			strings.NewReader(`{"scope":"agents","agent_ids":["`+specSourceID+`"]}`))
		req.Header.Set("Content-Type", "application/json")
	case b1RoutePlanRead:
		req = httptest.NewRequest(http.MethodGet, "/api/v1/packages/export/plan/00000000-0000-4000-8000-000000000000", nil)
	case b1RouteCancel:
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/packages/import/00000000-0000-4000-8000-000000000000", nil)
	default:
		t.Fatalf("unknown route %d", route)
	}
	req = req.WithContext(packageCtx(t))
	switch auth {
	case "none":
	case "bypass":
		// No env token: withAuth reaches the bypass branch, then
		// RequireNotBypass answers 503 (same setup as the T-5 D8 rows).
		t.Setenv("OMNIPUS_BEARER_TOKEN", "")
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.DevModeBypass = true
		req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
		req = req.WithContext(putConfigSnapshot(req.Context(), cfg))
	default:
		t.Fatalf("unknown auth %q", auth)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return packageHTTP{Code: w.Code, Body: w.Body.Bytes(), Hdr: w.Header()}
}

// TestExportPlanHandler_Guards — spec order 8: 401 without sign-in and 503
// under developer bypass for create, read and cancel; 404 (routes not
// registered) in the platform edition for all three. The B1 routes are
// registered inside the same adminWrap chain as the existing package routes,
// so these ride real middleware.
func TestExportPlanHandler_Guards(t *testing.T) {
	for _, route := range []int{b1RoutePlanCreate, b1RoutePlanRead, b1RouteCancel} {
		name := map[int]string{b1RoutePlanCreate: "create", b1RoutePlanRead: "read", b1RouteCancel: "cancel"}[route]
		t.Run(name+" without sign-in is 401", func(t *testing.T) {
			got := b1PlanProbe(t, route, "none")
			require.Equal(t, http.StatusUnauthorized, got.Code, "%s must answer 401 without sign-in; body=%s", name, got.Body)
		})
		t.Run(name+" under developer bypass is 503", func(t *testing.T) {
			got := b1PlanProbe(t, route, "bypass")
			require.Equal(t, http.StatusServiceUnavailable, got.Code, "%s must refuse while developer bypass is on; body=%s", name, got.Body)
		})
	}

	t.Run("platform edition registers none of the three routes (404)", func(t *testing.T) {
		restore := config.SetEdition(config.EditionDesktop)
		t.Cleanup(restore)
		api := packageAPI(t)
		mux := packageRouteMux(t, api)
		specs := []struct {
			method, path string
		}{
			{http.MethodPost, "/api/v1/packages/export/plan"},
			{http.MethodGet, "/api/v1/packages/export/plan/00000000-0000-4000-8000-000000000000"},
			{http.MethodDelete, "/api/v1/packages/import/00000000-0000-4000-8000-000000000000"},
		}
		for _, s := range specs {
			req := packageAuthRequest(t, s.method, s.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			require.Equal(t, http.StatusNotFound, w.Code,
				"%s %s must not exist in the platform edition; body=%s", s.method, s.path, w.Body)
		}
	})
}

// ---------------------------------------------------------------------------
// Order 9 — TestExportPlanHandler_WritesNothing.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_WritesNothing — spec order 9 (FR-006): a review
// create and read change no file under the data folder, write no audit entry
// and use no staging slot.
func TestExportPlanHandler_WritesNothing(t *testing.T) {
	api := packageAPI(t)
	b1SeedMainWriter(t, api, specSourceID, "Writer")
	before := snapshotHome(t, api.homePath)
	auditBefore := readAudit(t, api.homePath)

	got := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["`+specSourceID+`"]}`)
	require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
	m := planMap(t, got.Body)
	planID, _ := m["plan_id"].(string)
	gotRead := callPackagePlanRead(t, api, planID)
	require.Equal(t, http.StatusOK, gotRead.Code, "body=%s", gotRead.Body)

	after := snapshotHome(t, api.homePath)
	require.Equal(t, before, after, "the review changed no file under the data folder (not even the audit log)")
	require.Equal(t, auditBefore, readAudit(t, api.homePath), "the review wrote no audit entry")

	// No staging slot was used: five uploads are still admitted afterwards.
	for i := 0; i < omnpkg.MaxStagedImports; i++ {
		prev := stageUpload(t, api, validMainPackage(t))
		require.NotEmpty(t, prev.StagingId, "upload %d is admitted (the review used no slot)", i+1)
	}
}

// ---------------------------------------------------------------------------
// Order 9a — TestExportPlanHandler_NoSecretFileOpens.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_NoSecretFileOpens — spec order 9a (FR-005a): the
// review create, read and refusal paths never open credentials.json,
// master.key, cli.token, auth.json or config.json. On Linux this is proved
// with the inotify open watcher, its positive control and a planted-open
// control; on other platforms the watcher is a stub, the output assertions
// still run, and the no-open half is not asserted there (the Linux CI leg
// owns the proof).
func TestExportPlanHandler_NoSecretFileOpens(t *testing.T) {
	api := packageAPI(t)
	b1SeedMainWriter(t, api, specSourceID, "Writer")
	seedListedAgent(t, api, config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore})

	// The watched files must exist for inotify; plant canary-shaped content.
	watched := []string{"credentials.json", "master.key", "cli.token", "auth.json"}
	for i, name := range watched {
		require.NoError(t, os.WriteFile(filepath.Join(api.homePath, name),
			[]byte(fmt.Sprintf(`{"canary":"CANARY_SECRET_FILE_%d"}`, i)), 0o600))
	}
	paths := make([]string, 0, len(watched)+1)
	for _, name := range watched {
		paths = append(paths, filepath.Join(api.homePath, name))
	}
	paths = append(paths, filepath.Join(api.homePath, "config.json"))
	w := newOpenWatch(t, paths)

	// Positive control (D6-12): a deliberate open of auth.json must be seen.
	f, err := os.Open(filepath.Join(api.homePath, "auth.json"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	w.requireProbeSeen(t, "the watcher must see the deliberate auth.json open (D6-12)")

	// The review create, its read-back and two refusal paths.
	got := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["`+specSourceID+`"]}`)
	require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
	m := planMap(t, got.Body)
	planID, _ := m["plan_id"].(string)
	gotRead := callPackagePlanRead(t, api, planID)
	require.Equal(t, http.StatusOK, gotRead.Code, "body=%s", gotRead.Body)

	// D1-7: a whitespace-only prompt is agent_prompt_missing.
	blankID := "6b9a12d3-0000-4000-8000-000000000002"
	seedListedAgent(t, api, config.AgentConfig{
		ID: blankID, Name: "Blank", Type: "custom", Description: "Blank prompt",
		Model: &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"},
	})
	require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "agents", blankID, "SOUL.md"),
		[]byte("   \n"), 0o600))
	gotBlank := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["`+blankID+`"]}`)
	requirePackageStatus(t, gotBlank, http.StatusUnprocessableEntity, "agent_prompt_missing")

	gotMia := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["mia"]}`)
	requirePackageStatus(t, gotMia, http.StatusUnprocessableEntity, "core_agent_not_exportable")

	w.requireNoOpens(t, "the review paths must not open any watched secret file")

	// Planted-open control: a seam that opens credentials.json inside the
	// create handler must be seen by the watcher — proving the instrument
	// could detect a leak. RED: the stub handler never calls the seam, so
	// this control fails here (the missing feature).
	usePackageSeams(t)
	packageT5.PlanOpenSecretFile = func() {
		f, err := os.Open(filepath.Join(api.homePath, "credentials.json"))
		require.NoError(t, err)
		_ = f.Close()
	}
	gotPlanted := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["`+specSourceID+`"]}`)
	_ = gotPlanted
	if runtime.GOOS == "linux" {
		require.NotZero(t, b1OpenWatchDrain(t, w),
			"the planted-open control must be seen by the watcher (a control that sees nothing proves nothing)")
	} else {
		t.Log("non-Linux platform: the watcher is a stub; the open proofs run on the Linux CI leg")
	}
}

// ---------------------------------------------------------------------------
// Order 10 — TestExportPlanHandler_StoreCap.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_StoreCap — spec order 10 (FR-008, Dataset D5 rows
// 6–8): at most 32 plans; adding the 33rd drops expired plans first, then the
// oldest — observable as "no unexpired review is lost while an expired one is
// held".
func TestExportPlanHandler_StoreCap(t *testing.T) {
	const body = `{"scope":"agents","agent_ids":["` + specSourceID + `"]}`
	create := func(t *testing.T, api *restAPI) string {
		t.Helper()
		got := callPackagePlanCreate(t, api, body)
		require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
		m := planMap(t, got.Body)
		id, _ := m["plan_id"].(string)
		return id
	}
	readCode := func(t *testing.T, api *restAPI, id string) int {
		t.Helper()
		return callPackagePlanRead(t, api, id).Code
	}
	seed := func(t *testing.T) (*b1Clock, *restAPI) {
		t.Helper()
		clk := newB1Clock()
		b1UseClock(t, clk)
		api := packageAPI(t)
		b1SeedMainWriter(t, api, specSourceID, "Writer")
		return clk, api
	}

	t.Run("D5-6 distinct creation times: the 33rd evicts the uniquely oldest P1", func(t *testing.T) {
		clk, api := seed(t)
		ids := make([]string, 0, 32)
		for n := 1; n <= 32; n++ {
			clk.Set(clk.Now().Add(time.Duration(n) * time.Second)) // P*n* at T + n s
			ids = append(ids, create(t, api))
		}
		clk.Set(clk.Now().Add(time.Minute - 32*time.Second)) // T + 1 min for P33
		p33 := create(t, api)
		require.Equal(t, http.StatusNotFound, readCode(t, api, ids[0]), "P1 (uniquely oldest, still unexpired) is evicted")
		for i := 1; i < len(ids); i++ {
			require.Equal(t, http.StatusOK, readCode(t, api, ids[i]), "P%d is kept", i+1)
		}
		require.Equal(t, http.StatusOK, readCode(t, api, p33), "P33 is kept")
	})

	t.Run("D5-7 one expired plan: the expired one is dropped, no unexpired plan lost", func(t *testing.T) {
		clk, api := seed(t)
		p1 := create(t, api) // created at T
		clk.Set(clk.Now().Add(10 * time.Minute))
		ids := make([]string, 0, 31)
		for n := 2; n <= 32; n++ {
			ids = append(ids, create(t, api)) // P2…P32 at T + 10 min
		}
		clk.Set(clk.Now().Add(20 * time.Minute)) // T + 30:00: only P1 expired
		p33 := create(t, api)
		require.Equal(t, http.StatusNotFound, readCode(t, api, p1), "the expired P1 is dropped")
		for i, id := range ids {
			require.Equal(t, http.StatusOK, readCode(t, api, id), "P%d (unexpired) is not lost", i+2)
		}
		require.Equal(t, http.StatusOK, readCode(t, api, p33))
	})

	t.Run("D5-8 several expired plans: all three expired are dropped", func(t *testing.T) {
		clk, api := seed(t)
		p1 := create(t, api)
		p2 := create(t, api)
		p3 := create(t, api)
		clk.Set(clk.Now().Add(10 * time.Minute))
		ids := make([]string, 0, 29)
		for n := 4; n <= 32; n++ {
			ids = append(ids, create(t, api))
		}
		clk.Set(clk.Now().Add(20 * time.Minute)) // T + 30:00: P1…P3 expired
		p33 := create(t, api)
		for i, id := range []string{p1, p2, p3} {
			require.Equal(t, http.StatusNotFound, readCode(t, api, id), "P%d (expired) is dropped", i+1)
		}
		for i, id := range ids {
			require.Equal(t, http.StatusOK, readCode(t, api, id), "P%d (unexpired) is not lost", i+4)
		}
		require.Equal(t, http.StatusOK, readCode(t, api, p33))
	})
}

// ---------------------------------------------------------------------------
// Order 11 — TestExportPlanHandler_Canaries.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_Canaries — spec order 11 (FR-005, SC-002): no D6-1…9
// canary appears in any review body, response header or captured log line;
// the allowed names do appear; and the planted-value control proves the
// scanner.
func TestExportPlanHandler_Canaries(t *testing.T) {
	api := packageAPI(t)
	b1SeedExternalWorker(t, api, "ext-canary")

	var logBuf bytes.Buffer
	prevLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLog) })

	got := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["ext-canary"]}`)
	require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
	m := planMap(t, got.Body)
	planID, _ := m["plan_id"].(string)
	gotRead := callPackagePlanRead(t, api, planID)
	require.Equal(t, http.StatusOK, gotRead.Code, "body=%s", gotRead.Body)

	// Refusal variants go through the same error path: trigger a 404 and a
	// 422 and scan those bodies too.
	gotMissing := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["6b9a12d3-0000-4000-8000-000000000001"]}`)
	require.Equal(t, http.StatusNotFound, gotMissing.Code)
	seedListedAgent(t, api, config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore})
	gotMia := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["mia"]}`)
	requirePackageStatus(t, gotMia, http.StatusUnprocessableEntity, "core_agent_not_exportable")

	bodies := [][]byte{got.Body, gotRead.Body, gotMissing.Body, gotMia.Body}
	hdrText := got.Hdr.Get("Content-Disposition") + gotRead.Hdr.Get("Content-Type")
	for i, body := range bodies {
		for _, canary := range b1D6Canaries {
			require.NotContains(t, string(body), canary, "body %d carries the D6 canary %q", i, canary)
		}
	}
	for _, canary := range b1D6Canaries {
		require.NotContains(t, hdrText, canary, "a response header carries the D6 canary %q", canary)
		require.NotContains(t, logBuf.String(), canary, "a captured log line carries the D6 canary %q", canary)
	}
	// Names are not values: the removed environment names must be present.
	require.Contains(t, string(got.Body), "API_TOKEN", "the environment-override names are present")
	require.Contains(t, string(got.Body), "REGION")

	// Planted-value control: a seam that plants D6-9's value into the built
	// plan must reach the response body — proving the scanner would see a
	// leak. RED: the stub ignores the seam, so this fails (missing feature).
	usePackageSeams(t)
	omnpkg.PlanCanarySeam = func(plan *omnpkg.ExportPlan) {
		if len(plan.Elements) > 0 {
			plan.Elements[0].Name += " CANARY_ENV"
		}
	}
	gotPlanted := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["ext-canary"]}`)
	require.Contains(t, string(gotPlanted.Body), "CANARY_ENV",
		"the planted-value control must reach the response body (the scanner's target)")
}

// ---------------------------------------------------------------------------
// Order 11a — TestExportPlanFlow_DecodedBundleCanaries.
// ---------------------------------------------------------------------------

// TestExportPlanFlow_DecodedBundleCanaries — spec order 11a: decode the
// payload and manifest of bundles exported through the B1 flow after a
// review; no D6-1…9 canary anywhere; the free-text controls ride in the
// payload and appear in neither review answer; the manifest lists
// needs_secrets names only.
func TestExportPlanFlow_DecodedBundleCanaries(t *testing.T) {
	api := packageAPI(t)
	b1SeedExternalWorker(t, api, "ext-canary")

	// The Main positive-control agent: D6-10/11 in its free text.
	seedListedAgent(t, api, config.AgentConfig{
		ID: "main-canary", Name: "Writer", Type: "custom", Description: "Drafts",
		Model: &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"},
	})
	mainHome := filepath.Join(api.homePath, "agents", "main-canary")
	require.NoError(t, os.WriteFile(filepath.Join(mainHome, "SOUL.md"),
		[]byte("Remember CANARY_PROMPT and answer in rhyme.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(mainHome, "AGENT.md"),
		[]byte("House rules mention CANARY_INSTRUCTIONS.\n"), 0o600))

	// Review both, then export both through the B1 flow (existing route).
	planExt := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["ext-canary"]}`)
	require.Equal(t, http.StatusCreated, planExt.Code, "body=%s", planExt.Body)
	planMain := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["main-canary"]}`)
	require.Equal(t, http.StatusCreated, planMain.Code, "body=%s", planMain.Body)

	gotExt := callPackageExport(t, api, `{"agent_ids":["ext-canary"]}`)
	require.Equal(t, http.StatusOK, gotExt.Code, "body=%s", gotExt.Body)
	gotMain := callPackageExport(t, api, `{"agent_ids":["main-canary"]}`)
	require.Equal(t, http.StatusOK, gotMain.Code, "body=%s", gotMain.Body)

	for i, got := range []packageHTTP{gotExt, gotMain} {
		manifest, files := decodePackageBytes(t, got.Body)
		manifestRaw, err := json.Marshal(manifest)
		require.NoError(t, err)
		for _, canary := range b1D6Canaries {
			require.NotContains(t, string(manifestRaw), canary, "bundle %d manifest carries the D6 canary %q", i, canary)
			for path, body := range files {
				require.NotContains(t, string(body), canary, "bundle %d payload entry %s carries the D6 canary %q", i, path, canary)
			}
		}
	}

	// Positive controls: the free-text canaries ride in the Main bundle's
	// payload and appear in neither review answer.
	_, mainFiles := decodePackageBytes(t, gotMain.Body)
	soulSeen, instrSeen := false, false
	for path, body := range mainFiles {
		if strings.HasSuffix(path, "SOUL.md") {
			require.Contains(t, string(body), "CANARY_PROMPT", "D6-10 rides in the payload")
			soulSeen = true
		}
		if strings.HasSuffix(path, "AGENT.md") {
			require.Contains(t, string(body), "CANARY_INSTRUCTIONS", "D6-11 rides in the payload")
			instrSeen = true
		}
	}
	require.True(t, soulSeen, "the decoded payload carries SOUL.md")
	require.True(t, instrSeen, "the decoded payload carries AGENT.md")
	for _, body := range [][]byte{planExt.Body, planMain.Body} {
		require.NotContains(t, string(body), "CANARY_PROMPT", "the review does not carry the free-text canary")
		require.NotContains(t, string(body), "CANARY_INSTRUCTIONS")
	}

	// The external worker's manifest lists the removed environment names —
	// names only, never their values (spec: needs_secrets carries names).
	extManifest, _ := decodePackageBytes(t, gotExt.Body)
	secrets, ok := extManifest["needs_secrets"].([]any)
	require.True(t, ok, "manifest needs_secrets present; manifest=%v", extManifest)
	require.Equal(t, []any{
		map[string]any{"name": "API_TOKEN", "used_by": "ext-canary", "kind": "executor_env"},
		map[string]any{"name": "REGION", "used_by": "ext-canary", "kind": "executor_env"},
	}, secrets, "the manifest carries the removed environment names, never their values")
}

// ---------------------------------------------------------------------------
// Order 12 — TestExportPlanHandler_BenchmarkFixtureWithin5s.
// ---------------------------------------------------------------------------

// TestExportPlanHandler_BenchmarkFixtureWithin5s — spec order 12 (SC-005): a
// review of the benchmark fixture (1 MiB prompt, 1 MiB instructions) answers
// within 5 seconds.
func TestExportPlanHandler_BenchmarkFixtureWithin5s(t *testing.T) {
	api := packageAPI(t)
	seedListedAgent(t, api, config.AgentConfig{
		ID: specSourceID, Name: "Writer", Type: "custom", Description: "Big fixture",
		Model: &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"},
	})
	home := filepath.Join(api.homePath, "agents", specSourceID)
	require.NoError(t, os.WriteFile(filepath.Join(home, "SOUL.md"), bytes.Repeat([]byte("a"), 1<<20), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "AGENT.md"), bytes.Repeat([]byte("b"), 1<<20), 0o600))

	start := time.Now()
	got := callPackagePlanCreate(t, api, `{"scope":"agents","agent_ids":["`+specSourceID+`"]}`)
	elapsed := time.Since(start)
	require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
	require.LessOrEqual(t, elapsed, 5*time.Second, "the review answers within 5 s (SC-005)")
}

// ---------------------------------------------------------------------------
// Order 14 — TestPackageRoutes_ExistingWireUnchanged.
// ---------------------------------------------------------------------------

// TestPackageRoutes_ExistingWireUnchanged — spec order 14 (FR-011, the
// Regression table): with the B1 routes registered, the existing export,
// upload and apply outcomes are exactly what they were. This is a regression
// pin: it exercises existing behaviour and is expected to hold in RED.
func TestPackageRoutes_ExistingWireUnchanged(t *testing.T) {
	api := packageAPI(t)
	b1SeedMainWriter(t, api, specSourceID, "Writer")

	// Export: 200, an .omnpkg Content-Disposition naming the agent, audited.
	gotExport := callPackageExport(t, api, `{"agent_ids":["`+specSourceID+`"]}`)
	require.Equal(t, http.StatusOK, gotExport.Code, "body=%s", gotExport.Body)
	require.Contains(t, gotExport.Hdr.Get("Content-Disposition"), "Writer.omnpkg")
	require.Contains(t, gotExport.Hdr.Get("Content-Disposition"), ".omnpkg")
	require.NotEmpty(t, readAudit(t, api.homePath), "the export is audited exactly as today")

	// Export refusal: the built-in refusal keeps its status and code.
	seedListedAgent(t, api, config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore})
	gotMia := callPackageExport(t, api, `{"agent_ids":["mia"]}`)
	requirePackageStatus(t, gotMia, http.StatusUnprocessableEntity, "core_agent_not_exportable")

	// Upload: 201 with the preview.
	prev := stageUpload(t, api, validMainPackage(t))
	require.Len(t, prev.WillAdd, 1)
	require.Equal(t, "Writer", prev.WillAdd[0].SourceName)

	// Apply: 200 with the created agent (mode new, all-or-nothing, unchanged).
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	gotApply := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, gotApply.Code, "body=%s", gotApply.Body)
	res := decodeResult(t, gotApply.Body)
	require.Len(t, res.Created, 1)
	// The export fixture seeded "Writer" on the roster, so mode-new applies
	// the package under the create rule's "(imported)" suffix (S1 create
	// rules, inherited) — that suffixed outcome is the golden wire here.
	require.Equal(t, "Writer (imported)", res.Created[0].Name)
	require.NotEqual(t, specSourceID, res.Created[0].NewId, "FR-031: the new agent has a fresh ID")
}
