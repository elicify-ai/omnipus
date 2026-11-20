// packages_t5_harness_test.go — fixtures for the T-5 RED tests (epic #1094).
//
// Packages are built with the standard library and klauspost/compress/zstd,
// never by reading a handler's output back into an expectation
// (elicify-test-writing: oracle independence). Expected statuses and codes
// come from docs/internal/specs/export-import-s1-spec.md revision 7.
package gateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// packageTestBearer is the legacy env-token these tests send. It is not a
// secret; OMNIPUS_BEARER_TOKEN is set to the same value for the request.
const packageTestBearer = "package-test-token"

// packageCallTimeout bounds every handler call (no sleeps anywhere in the T-5 tests).
const packageCallTimeout = 30 * time.Second

// packageUploadLimit is the upload route body cap (FR-008): 64 MiB.
const packageUploadLimit = 64 << 20

// packageJSONLimit is the export and apply JSON body cap (FR-045): 1 MiB.
const packageJSONLimit = 1 << 20

// specSourceID is the contract example agent id (PackageExportRequest.yaml).
const specSourceID = "5f0c1a2b-3d4e-4f5a-8b7c-9d0e1f2a3b4c"

// usePackageSeams saves the fault seams and restores them when the test ends.
// T-5 tests must not call t.Parallel: the seams are process-wide.
func usePackageSeams(t *testing.T) {
	t.Helper()
	prev := packageT5
	t.Cleanup(func() { packageT5 = prev })
	packageT5 = packageT5Seams{}
}

func packageCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), packageCallTimeout)
	t.Cleanup(cancel)
	return ctx
}

// packageAPI is a home-scoped gateway with the test bearer configured.
func packageAPI(t *testing.T) *restAPI {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", packageTestBearer)
	// ARCH-RULING-T5-A-D: the harness uses the reconciled ceiling. Seeded agents
	// ride it. There is no per-agent backfill and no production bypass.
	cfg := api.agentLoop.GetConfig()
	gaps := repairAndValidateToolPolicyCoverage(cfg)
	require.Empty(t, gaps, "a reconciled ceiling with no uncovered agents must have no gaps")
	wirePackageAuditLogger(t, api)
	return api
}

// wirePackageAuditLogger gives package operations the shared logger tests read
// under <home>/system/audit.jsonl. Audit-off tests clear it again.
func wirePackageAuditLogger(t *testing.T, api *restAPI) {
	t.Helper()
	lg, err := audit.NewLogger(audit.LoggerConfig{
		Dir:               filepath.Join(api.homePath, "system"),
		RedactEnabled:     true,
		AuditLogRequested: true,
		RetentionDays:     90,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lg.Close() })
	api.auditor = lg
	api.agentLoop.GetConfig().Sandbox.AuditLog = true
}

func packageAuthRequest(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req = req.WithContext(packageCtx(t))
	req.Header.Set("Authorization", "Bearer "+packageTestBearer)
	return req
}

type packageHTTP struct {
	Code int
	Body []byte
	Hdr  http.Header
}

// packageRouteMux is the production registrar (FR-045: tests go through the
// registered routes, including auth and the route body caps). A direct
// handler call is not a substitute.
func packageRouteMux(t *testing.T, api *restAPI) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return mux
}

// serveRegisteredPackage sends req through that registrar. The config snapshot
// is what withAuth reads; the bearer on req is the test token.
func serveRegisteredPackage(t *testing.T, api *restAPI, req *http.Request) packageHTTP {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	req = req.WithContext(putConfigSnapshot(req.Context(), cfg))
	w := httptest.NewRecorder()
	packageRouteMux(t, api).ServeHTTP(w, req)
	return packageHTTP{Code: w.Code, Body: w.Body.Bytes(), Hdr: w.Header()}
}

func callPackageExport(t *testing.T, api *restAPI, body string) packageHTTP {
	t.Helper()
	req := packageAuthRequest(t, http.MethodPost, "/api/v1/packages/export", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return serveRegisteredPackage(t, api, req)
}

func callPackageImport(t *testing.T, api *restAPI, body []byte) packageHTTP {
	t.Helper()
	req := packageAuthRequest(t, http.MethodPost, "/api/v1/packages/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	return serveRegisteredPackage(t, api, req)
}

func callPackageApply(t *testing.T, api *restAPI, stagingID, jsonBody string) packageHTTP {
	t.Helper()
	path := "/api/v1/packages/import/" + stagingID + "/apply"
	req := packageAuthRequest(t, http.MethodPost, path, strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.URL.Path = path
	return serveRegisteredPackage(t, api, req)
}

func decodePackageErr(t *testing.T, raw []byte) gen.ErrorResponse {
	t.Helper()
	var er gen.ErrorResponse
	require.NoError(t, json.Unmarshal(raw, &er), "error body must be ErrorResponse JSON, got %q", raw)
	return er
}

func packageErrCode(er gen.ErrorResponse) string {
	if er.Code == nil {
		return ""
	}
	return *er.Code
}

func packageErrField(er gen.ErrorResponse) string {
	if er.Field == nil {
		return ""
	}
	return *er.Field
}

// requirePackageStatus fails naming the spec status and the body, so a 501
// from the RED stub is an assertion failure and not an undefined symbol.
func requirePackageStatus(t *testing.T, got packageHTTP, status int, code string) gen.ErrorResponse {
	t.Helper()
	require.Equal(t, status, got.Code, "body=%s", got.Body)
	er := decodePackageErr(t, got.Body)
	if code != "" {
		require.Equal(t, code, packageErrCode(er), "error code; body=%s", got.Body)
	}
	require.NotEmpty(t, er.Error, "error message must be non-empty")
	return er
}

// ---------------------------------------------------------------------------
// Package builder (test-owned; not the engine).
// ---------------------------------------------------------------------------

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func agentRecordPath(id string) string {
	return omnpkg.AgentsPrefix + id + "/" + omnpkg.AgentRecordName
}

func agentSoulPath(id string) string {
	return omnpkg.AgentsPrefix + id + "/" + omnpkg.AgentSoulName
}

func agentMDPath(id string) string {
	return omnpkg.AgentsPrefix + id + "/" + omnpkg.AgentInstructionsNm
}

// mainRecord is a Main agent record the create rules accept: custom type,
// non-empty name, no visual/skill/tool fields (those take the create defaults).
func mainRecord(id, name, description string) map[string]any {
	rec := map[string]any{
		"id":   id,
		"name": name,
		"type": "custom",
		"model": map[string]any{
			"primary":  "m-1",
			"provider": "openrouter",
		},
	}
	if description != "" {
		rec["description"] = description
	}
	return rec
}

type tarPart struct {
	name     string
	body     []byte
	typeflag byte
	link     string
}

func zstdTar(t *testing.T, parts []tarPart) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, p := range parts {
		flag := p.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     p.name,
			Size:     int64(len(p.body)),
			Typeflag: flag,
			Linkname: p.link,
			Mode:     0o644,
			ModTime:  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		}
		if flag != tar.TypeReg {
			hdr.Size = 0
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if flag == tar.TypeReg && len(p.body) > 0 {
			_, err := tw.Write(p.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out, zstd.WithWindowSize(1<<20))
	require.NoError(t, err)
	_, err = zw.Write(raw.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return out.Bytes()
}

func gzipTar(t *testing.T, parts []tarPart) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, p := range parts {
		hdr := &tar.Header{Name: p.name, Size: int64(len(p.body)), Typeflag: tar.TypeReg, Mode: 0o644}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(p.body) > 0 {
			_, err := tw.Write(p.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	_, err := zw.Write(raw.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return out.Bytes()
}

// buildAgentPackage builds a one-agent package. formatVersion 0 means the
// spec's version 1. soul or agentMD nil omits that file. record replaces the
// default Main record when non-nil.
func buildAgentPackage(t *testing.T, id, name string, record map[string]any, soul, agentMD []byte, formatVersion int) []byte {
	t.Helper()
	if record == nil {
		record = mainRecord(id, name, "Drafts")
	}
	rawRec, err := json.Marshal(record)
	require.NoError(t, err)
	files := map[string][]byte{agentRecordPath(id): rawRec}
	owned := []string{agentRecordPath(id)}
	if soul != nil {
		files[agentSoulPath(id)] = soul
		owned = append(owned, agentSoulPath(id))
	}
	if agentMD != nil {
		files[agentMDPath(id)] = agentMD
		owned = append(owned, agentMDPath(id))
	}
	// formatVersion 0 means the spec version (1). Negative means the literal 0
	// (Dataset D1 row 8: format_version 0 is not_a_package).
	ver := formatVersion
	if ver < 0 {
		ver = 0
	} else if ver == 0 {
		ver = omnpkg.FormatVersion
	}
	m := omnpkg.Manifest{
		Format:        omnpkg.FormatName,
		FormatVersion: ver,
		Scope:         omnpkg.ScopeCustom,
		Elements: []omnpkg.ManifestElement{{
			Type: omnpkg.ElementTypeAgent, Schema: omnpkg.AgentSchemaVersion,
			SourceID: id, Name: name, Files: owned,
		}},
		Counts:           map[string]int{"agent": 1},
		Files:            map[string]omnpkg.FileInfo{},
		SourceBuild:      "v0.1.1-test",
		CreatedAt:        time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		IncludesSessions: false,
	}
	for p, b := range files {
		m.Files[p] = omnpkg.FileInfo{SHA256: sha256hex(b), Size: int64(len(b))}
	}
	man, err := json.Marshal(m)
	require.NoError(t, err)
	parts := make([]tarPart, 0, 1+len(owned))
	parts = append(parts, tarPart{name: omnpkg.ManifestEntry, body: man})
	// Stable payload order: record, soul, instructions.
	for _, p := range owned {
		parts = append(parts, tarPart{name: p, body: files[p]})
	}
	return zstdTar(t, parts)
}

func validMainPackage(t *testing.T) []byte {
	t.Helper()
	return buildAgentPackage(t, specSourceID, "Writer", nil,
		[]byte("Answer in rhyme.\n"), []byte("House rules.\n"), 0)
}

// snapshotHome records sha256|mtime for every file under home, so a preview
// can be proved to change nothing except staging and the audit log (FR-023).
func snapshotHome(t *testing.T, home string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	require.NoError(t, filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		require.NoError(t, rerr)
		rel, rerr := filepath.Rel(home, p)
		require.NoError(t, rerr)
		snap[rel] = sha256hex(data) + "|" + fi.ModTime().UTC().Format(time.RFC3339Nano)
		return nil
	}))
	return snap
}

// requireHomeUnchangedExcept allows only the staging tree and the audit log
// to differ (FR-023: upload changes no agent, workspace, skill, connector or
// configuration file).
func requireHomeUnchangedExcept(t *testing.T, before, after map[string]string) {
	t.Helper()
	allowed := func(rel string) bool {
		return rel == filepath.Join("system", "audit.jsonl") ||
			strings.HasPrefix(rel, filepath.Join("backups", ".staging")+string(os.PathSeparator)) ||
			rel == filepath.Join("backups", ".staging")
	}
	for k, v := range before {
		if allowed(k) {
			continue
		}
		require.Equal(t, v, after[k], "file changed outside staging and audit: %s", k)
	}
	for k := range after {
		if allowed(k) {
			continue
		}
		_, ok := before[k]
		require.True(t, ok, "new file outside staging and audit: %s", k)
	}
}

func configBytes(t *testing.T, home string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	return b
}

// createMainAgent creates a custom Main through the existing REST create
// handler and returns its id. The import parity test compares against this
// path (FR-034).
func createMainAgent(t *testing.T, api *restAPI, name, soul string) string {
	t.Helper()
	body := `{"name":` + packageJSONString(name) + `,"type":"Main","soul":` + packageJSONString(soul) + `}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "REST create fixture must succeed; body=%s", w.Body)
	var resp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.ID)
	return resp.ID
}

func packageJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// seedListedAgent puts an agent on the live roster and on disk so export can
// see it. Built-ins are seeded this way because the test home does not boot
// the shipped roster (Dataset D4 is by id).
func seedListedAgent(t *testing.T, api *restAPI, agent config.AgentConfig) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, agent)
	dir := filepath.Join(api.homePath, "entities", "agents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(agent)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, agent.ID+".json"), raw, 0o600))
	home := filepath.Join(api.homePath, "agents", agent.ID)
	require.NoError(t, os.MkdirAll(home, 0o700))
	if agent.Type != config.AgentTypeWorker || agent.Subagents == nil || agent.Subagents.Executor == nil ||
		agent.Subagents.Executor.Kind != config.ExecutorKindExternalCLI {
		require.NoError(t, os.WriteFile(filepath.Join(home, "SOUL.md"), []byte("Answer in rhyme.\n"), 0o600))
	}
}

// putConfigSnapshot stores cfg where withAuth reads it (configFromContext).
func putConfigSnapshot(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
}

// hashBearer hashes a raw bearer the way a legacy token hash is stored
// (bcrypt of the full raw string). MinCost keeps the test off the production
// cost without changing what VerifyToken compares.
func hashBearer(t *testing.T, raw string) config.BcryptHash {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.MinCost)
	require.NoError(t, err)
	return config.BcryptHash(h)
}

func decompressZstd(t *testing.T, raw []byte) []byte {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	return plain
}

func tarNew(t *testing.T, plain []byte) *tar.Reader {
	t.Helper()
	return tar.NewReader(bytes.NewReader(plain))
}

func readTar(t *testing.T, tr *tar.Reader, hdr *tar.Header) []byte {
	t.Helper()
	buf := make([]byte, hdr.Size)
	_, err := io.ReadFull(tr, buf)
	require.NoError(t, err)
	return buf
}

var errEOF = io.EOF

func jsonUnmarshal(raw []byte, dest any) error {
	return json.Unmarshal(raw, dest)
}

func readAudit(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "system", "audit.jsonl"))
	if err != nil {
		return ""
	}
	return string(b)
}

// externalPackage is an external-worker package (D1 rows 30–31). No SOUL.md:
// an external worker has no prompt, and a SOUL.md on one is invalid_element.
func externalPackage(t *testing.T, withRuntime bool) []byte {
	t.Helper()
	exec := map[string]any{"kind": "external-cli", "cli": "codex"}
	if withRuntime {
		exec["cli_args"] = "--key sk-TEST-123"
		exec["env_overrides"] = map[string]string{"OPENAI_API_KEY": "sk-TEST-456"}
	}
	rec := map[string]any{
		"id": specSourceID, "name": "Coder", "type": "worker", "description": "codes",
		"subagents": map[string]any{"executor": exec},
	}
	return buildAgentPackage(t, specSourceID, "Coder", rec, nil, nil, 0)
}

// unknownElementPackage has only a skill: Dataset D12-2 / FR-009 requires
// no_supported_element. The mixed fixture below separately covers FR-006.
func unknownElementPackage(t *testing.T) []byte {
	t.Helper()
	const path = "skills/writing/SKILL.md"
	body := []byte("# writing\n")
	m := omnpkg.Manifest{
		Format: omnpkg.FormatName, FormatVersion: omnpkg.FormatVersion, Scope: omnpkg.ScopeCustom,
		Elements: []omnpkg.ManifestElement{{
			Type: "skill", Schema: 1, SourceID: "writing", Name: "writing", Files: []string{path},
		}},
		Counts:           map[string]int{"skill": 1},
		Files:            map[string]omnpkg.FileInfo{path: {SHA256: sha256hex(body), Size: int64(len(body))}},
		SourceBuild:      "v0.1.1-test",
		CreatedAt:        time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		IncludesSessions: false,
	}
	man, err := json.Marshal(m)
	require.NoError(t, err)
	return zstdTar(t, []tarPart{{name: omnpkg.ManifestEntry, body: man}, {name: path, body: body}})
}

// mixedUnknownElementPackage is a test-owned, otherwise valid Main package
// with a second (unsupported in S1a) skill element: FR-006, not D12-2.
func mixedUnknownElementPackage(t *testing.T) []byte {
	t.Helper()
	manifest, files := decodePackageBytes(t, validMainPackage(t))
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	var m omnpkg.Manifest
	require.NoError(t, json.Unmarshal(raw, &m))
	const path = "skills/writing/SKILL.md"
	body := []byte("# writing\n")
	m.Elements = append(m.Elements, omnpkg.ManifestElement{
		Type: "skill", Schema: 1, SourceID: "writing", Name: "writing", Files: []string{path},
	})
	m.Counts["skill"] = 1
	m.Files[path] = omnpkg.FileInfo{SHA256: sha256hex(body), Size: int64(len(body))}
	raw, err = json.Marshal(m)
	require.NoError(t, err)
	parts := make([]tarPart, 0, 1+len(m.Elements[0].Files)+1)
	parts = append(parts, tarPart{name: omnpkg.ManifestEntry, body: raw})
	for _, path := range m.Elements[0].Files {
		parts = append(parts, tarPart{name: path, body: files[path]})
	}
	parts = append(parts, tarPart{name: path, body: body})
	return zstdTar(t, parts)
}
