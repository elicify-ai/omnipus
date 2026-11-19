// packages_t5_import_red_test.go — RED tests for POST /api/v1/packages/import
// (epic #1094, T-5). Expected statuses are Dataset D1 and FR-020…FR-023,
// FR-045. The handler is the 501 stub, so each case fails on the spec status.
package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// TestPackagesImport_Route is TDD row 24: provisional preview, damaged
// packages, the real-route size boundaries, and a package that declares a
// core agent (FR-008, FR-011, FR-020, FR-021, FR-023, FR-040, FR-045).
func TestPackagesImport_Route(t *testing.T) {
	t.Run("D1-1 upload gives a provisional preview and changes nothing", func(t *testing.T) {
		api := packageAPI(t)
		before := snapshotHome(t, api.homePath)
		cfgBefore := configBytes(t, api.homePath)
		got := callPackageImport(t, api, validMainPackage(t))
		require.Equal(t, http.StatusCreated, got.Code, "FR-021 body=%s", got.Body)
		prev := decodePreview(t, got.Body)
		require.NotEmpty(t, prev.StagingId)
		require.False(t, prev.ExpiresAt.IsZero(), "expires_at is ready time + 30 minutes")
		require.Len(t, prev.WillAdd, 1)
		require.Equal(t, "agent", prev.WillAdd[0].Type)
		require.Equal(t, specSourceID, prev.WillAdd[0].SourceId)
		require.Equal(t, "Writer", prev.WillAdd[0].SourceName)
		require.Equal(t, "Writer", prev.WillAdd[0].ProposedName, "no clash yet, so the proposed name is Writer")
		require.False(t, prev.WillAdd[0].Renamed)
		require.Empty(t, prev.NeedsSecrets, "S1a lists no secrets")
		// FR-023: only staging, staging maintenance and the audit log may change.
		require.Equal(t, cfgBefore, configBytes(t, api.homePath), "SC-008 config.json bytes unchanged")
		requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
	})

	t.Run("D1-25 a valid package of about 2 MiB is accepted", func(t *testing.T) {
		// Above the general 1 MiB request limit, within every content limit
		// (US-4 AS-6). crypto/rand bytes do not compress, so the body stays over 1 MiB.
		api := packageAPI(t)
		soul := make([]byte, 1<<20)
		agentMD := make([]byte, 1<<20)
		_, err := rand.Read(soul)
		require.NoError(t, err)
		_, err = rand.Read(agentMD)
		require.NoError(t, err)
		pkg := buildAgentPackage(t, specSourceID, "Writer", nil, soul, agentMD, 0)
		require.Greater(t, len(pkg), 1<<20, "the upload must be larger than the general 1 MiB limit")
		got := callPackageImport(t, api, pkg)
		require.Equal(t, http.StatusCreated, got.Code, "a ~2 MiB package must not be refused for size; body=%s", got.Body)
	})

	t.Run("D1-2 blank prompt is element_not_creatable field soul", func(t *testing.T) {
		api := packageAPI(t)
		pkg := buildAgentPackage(t, specSourceID, "Writer", nil, []byte(" \n\t\n"), nil, 0)
		er := requirePackageStatus(t, callPackageImport(t, api, pkg), http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
		require.Equal(t, "soul", packageErrField(er))
	})

	t.Run("D1-7 format_version 2 is package_too_new", func(t *testing.T) {
		api := packageAPI(t)
		pkg := buildAgentPackage(t, specSourceID, "Writer", nil, []byte("Answer in rhyme.\n"), nil, 2)
		requirePackageStatus(t, callPackageImport(t, api, pkg), http.StatusUnprocessableEntity, omnpkg.CodePackageTooNew)
	})

	t.Run("D1-8 format_version 0 is not_a_package", func(t *testing.T) {
		api := packageAPI(t)
		pkg := buildAgentPackage(t, specSourceID, "Writer", nil, []byte("Answer in rhyme.\n"), nil, -1)
		requirePackageStatus(t, callPackageImport(t, api, pkg), http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)
	})

	t.Run("D1-17 gzip is not_a_package", func(t *testing.T) {
		api := packageAPI(t)
		pkg := gzipTar(t, []tarPart{{name: "manifest.json", body: []byte("{}")}})
		requirePackageStatus(t, callPackageImport(t, api, pkg), http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)
	})

	t.Run("D1-18 empty body and random bytes are not_a_package", func(t *testing.T) {
		api := packageAPI(t)
		requirePackageStatus(t, callPackageImport(t, api, nil), http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)
		requirePackageStatus(t, callPackageImport(t, api, []byte("this is not a package")), http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)
	})

	t.Run("D1-22 type core or system is invalid_element", func(t *testing.T) {
		api := packageAPI(t)
		for _, typ := range []string{"core", "system"} {
			rec := mainRecord(specSourceID, "Writer", "Drafts")
			rec["type"] = typ
			pkg := buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme.\n"), nil, 0)
			requirePackageStatus(t, callPackageImport(t, api, pkg), http.StatusUnprocessableEntity, omnpkg.CodeInvalidElement)
		}
	})

	// FR-040: a safety refusal keeps its code when the audit write fails.
	t.Run("audit failure does not change an upload refusal", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.AuditWrite = func(string, map[string]string) error { return errAuditFailed }
		got := callPackageImport(t, api, []byte("not-a-package"))
		requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)
	})

	t.Run("D1-11 exactly 64 MiB is read in full and is not 413", func(t *testing.T) {
		api := packageAPI(t)
		body := bytes.Repeat([]byte{0xab}, packageUploadLimit)
		got, n := importViaRouteLimit(t, api, bytes.NewReader(body), int64(len(body)))
		require.Equal(t, http.StatusUnprocessableEntity, got.Code, "exactly 64 MiB is not too large; body=%s", got.Body)
		er := decodePackageErr(t, got.Body)
		require.Equal(t, omnpkg.CodeNotAPackage, packageErrCode(er))
		require.NotEqual(t, omnpkg.CodePackageTooLarge, packageErrCode(er))
		require.Equal(t, int64(packageUploadLimit), n, "the handler must read all 67108864 bytes before judging the body")
	})

	t.Run("D1-12 64 MiB plus 1 with a declared length is 413 package_too_large", func(t *testing.T) {
		api := packageAPI(t)
		body := bytes.Repeat([]byte{0xab}, packageUploadLimit+1)
		got, _ := importViaRouteLimit(t, api, bytes.NewReader(body), int64(len(body)))
		requirePackageStatus(t, got, http.StatusRequestEntityTooLarge, omnpkg.CodePackageTooLarge)
	})

	t.Run("D1-32 64 MiB plus 1 chunked with no declared length is 413", func(t *testing.T) {
		api := packageAPI(t)
		chunk := io.MultiReader(
			bytes.NewReader(bytes.Repeat([]byte{0xab}, packageUploadLimit)),
			bytes.NewReader([]byte{0x01}),
		)
		got, _ := importViaRouteLimit(t, api, chunk, -1)
		requirePackageStatus(t, got, http.StatusRequestEntityTooLarge, omnpkg.CodePackageTooLarge)
	})

	// Representative damaged packages the route must refuse (BDD "Damaged, unsafe
	// or non-creatable"). The full D1/D2/D12 grids are unit tests in pkg/omnpkg;
	// these rows prove the handler returns the engine's code instead of 201.
	t.Run("damaged packages are refused with the spec code", func(t *testing.T) {
		api := packageAPI(t)
		cases := []struct {
			name  string
			pkg   []byte
			code  string
			field string
		}{
			{"D1-19 manifest not first", manifestSecond(t), omnpkg.CodeNotAPackage, ""},
			{"D1-20 agent.json not JSON", badJSONPackage(t), omnpkg.CodeInvalidElement, ""},
			{"D1-21 blank name", blankNamePackage(t), omnpkg.CodeElementNotCreatable, "name"},
			{"D2-1 absolute path", absolutePathPackage(t), omnpkg.CodeUnsafePath, ""},
			{"D12-1 zero elements", zeroElementPackage(t), omnpkg.CodeNoSupportedElement, ""},
			{"D12-3 two agents", twoAgentPackage(t), omnpkg.CodeTooManyAgents, ""},
			{"D6-9 name of 101 characters", longNamePackage(t, 101), omnpkg.CodeElementNotCreatable, "name"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				er := requirePackageStatus(t, callPackageImport(t, api, tc.pkg), http.StatusUnprocessableEntity, tc.code)
				if tc.field != "" {
					require.Equal(t, tc.field, packageErrField(er))
				}
			})
		}
	})

	// Dataset D5 rows 2, 4, 6, 10 and D3 row 6: drops are warnings on a 201, not refusals.
	t.Run("D5 drops are warnings on the preview", func(t *testing.T) {
		api := packageAPI(t)
		rec := mainRecord(specSourceID, "Writer", "Drafts")
		rec["skills"] = []string{"my-custom"}
		rec["tools"] = map[string]any{
			"builtin": map[string]any{"policies": map[string]string{"retired_tool_x": "allow"}},
			"mcp":     map[string]any{"servers": []any{map[string]any{"id": "github"}}},
		}
		rec["model"] = map[string]any{"primary": "m-1", "provider": "openrouter", "fallbacks": []string{"x"}}
		pkg := buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme.\n"), nil, 0)
		got := callPackageImport(t, api, pkg)
		require.Equal(t, http.StatusCreated, got.Code, "body=%s", got.Body)
		prev := decodePreview(t, got.Body)
		codes := previewWarningCodes(prev)
		for _, want := range []string{"skill_dropped", "mcp_binding_dropped", "tool_policy_dropped", "provider_not_configured", "field_not_carried"} {
			require.Contains(t, codes, want, "FR-033 warning %s missing in %#v", want, codes)
		}
	})
}

func decodePreview(t *testing.T, raw []byte) gen.PackageImportPreview {
	t.Helper()
	var prev gen.PackageImportPreview
	require.NoError(t, json.Unmarshal(raw, &prev), "preview body=%s", raw)
	return prev
}

func previewWarningCodes(prev gen.PackageImportPreview) []string {
	out := make([]string, 0, len(prev.Warnings))
	for _, w := range prev.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func manifestSecond(t *testing.T) []byte {
	t.Helper()
	soul := []byte("Answer in rhyme.\n")
	pkg := buildAgentPackage(t, specSourceID, "Writer", nil, soul, nil, 0)
	plain := decompressZstd(t, pkg)
	tr := tarNew(t, plain)
	var parts []tarPart
	for {
		hdr, err := tr.Next()
		if errors.Is(err, errEOF) {
			break
		}
		require.NoError(t, err)
		body := readTar(t, tr, hdr)
		parts = append(parts, tarPart{name: hdr.Name, body: body})
	}
	require.GreaterOrEqual(t, len(parts), 2)
	// FR-001: the first entry must be the manifest. Swap it to second.
	parts[0], parts[1] = parts[1], parts[0]
	return zstdTar(t, parts)
}

func badJSONPackage(t *testing.T) []byte {
	t.Helper()
	path := agentRecordPath(specSourceID)
	body := []byte("not-json")
	return listedPackage(t, []tarPart{{name: path, body: body}}, omnpkg.ManifestElement{
		Type: omnpkg.ElementTypeAgent, Schema: omnpkg.AgentSchemaVersion,
		SourceID: specSourceID, Name: "Writer", Files: []string{path},
	})
}

func blankNamePackage(t *testing.T) []byte {
	t.Helper()
	rec := mainRecord(specSourceID, "   ", "Drafts")
	return buildAgentPackage(t, specSourceID, "   ", rec, []byte("Answer in rhyme.\n"), nil, 0)
}

func longNamePackage(t *testing.T, n int) []byte {
	t.Helper()
	name := string(bytes.Repeat([]byte("A"), n))
	rec := mainRecord(specSourceID, name, "Drafts")
	return buildAgentPackage(t, specSourceID, name, rec, []byte("Answer in rhyme.\n"), nil, 0)
}

func absolutePathPackage(t *testing.T) []byte {
	t.Helper()
	const path = "/etc/passwd"
	body := []byte("root:x:0:0:root:/root:/bin/sh\n")
	return listedPackage(t, []tarPart{{name: path, body: body}}, omnpkg.ManifestElement{
		Type: omnpkg.ElementTypeAgent, Schema: omnpkg.AgentSchemaVersion,
		SourceID: specSourceID, Name: "Writer", Files: []string{path},
	})
}

func zeroElementPackage(t *testing.T) []byte {
	t.Helper()
	m := omnpkg.Manifest{
		Format: omnpkg.FormatName, FormatVersion: omnpkg.FormatVersion, Scope: omnpkg.ScopeCustom,
		Elements: []omnpkg.ManifestElement{}, Counts: map[string]int{},
		Files: map[string]omnpkg.FileInfo{}, SourceBuild: "v0.1.1-test",
		CreatedAt: packageFixtureTime(), IncludesSessions: false,
	}
	man, err := json.Marshal(m)
	require.NoError(t, err)
	return zstdTar(t, []tarPart{{name: omnpkg.ManifestEntry, body: man}})
}

func twoAgentPackage(t *testing.T) []byte {
	t.Helper()
	id2 := "6a1d2b3c-4e5f-4a6b-9c8d-0e1f2a3b4c5d"
	soul := []byte("Answer in rhyme.\n")
	rec1, err := json.Marshal(mainRecord(specSourceID, "Writer", "Drafts"))
	require.NoError(t, err)
	rec2, err := json.Marshal(mainRecord(id2, "Editor", "Edits"))
	require.NoError(t, err)
	p1, p2 := agentRecordPath(specSourceID), agentRecordPath(id2)
	s1, s2 := agentSoulPath(specSourceID), agentSoulPath(id2)
	files := map[string][]byte{p1: rec1, s1: soul, p2: rec2, s2: soul}
	m := omnpkg.Manifest{
		Format: omnpkg.FormatName, FormatVersion: omnpkg.FormatVersion, Scope: omnpkg.ScopeCustom,
		Elements: []omnpkg.ManifestElement{
			{Type: omnpkg.ElementTypeAgent, Schema: omnpkg.AgentSchemaVersion, SourceID: specSourceID, Name: "Writer", Files: []string{p1, s1}},
			{Type: omnpkg.ElementTypeAgent, Schema: omnpkg.AgentSchemaVersion, SourceID: id2, Name: "Editor", Files: []string{p2, s2}},
		},
		Counts: map[string]int{"agent": 2}, Files: map[string]omnpkg.FileInfo{},
		SourceBuild: "v0.1.1-test", CreatedAt: packageFixtureTime(), IncludesSessions: false,
	}
	for p, b := range files {
		m.Files[p] = omnpkg.FileInfo{SHA256: sha256hex(b), Size: int64(len(b))}
	}
	man, err := json.Marshal(m)
	require.NoError(t, err)
	parts := make([]tarPart, 0, 5)
	parts = append(parts, tarPart{name: omnpkg.ManifestEntry, body: man})
	for _, p := range []string{p1, s1, p2, s2} {
		parts = append(parts, tarPart{name: p, body: files[p]})
	}
	return zstdTar(t, parts)
}

func listedPackage(t *testing.T, parts []tarPart, el omnpkg.ManifestElement) []byte {
	t.Helper()
	m := omnpkg.Manifest{
		Format: omnpkg.FormatName, FormatVersion: omnpkg.FormatVersion, Scope: omnpkg.ScopeCustom,
		Elements: []omnpkg.ManifestElement{el}, Counts: map[string]int{"agent": 1},
		Files: map[string]omnpkg.FileInfo{}, SourceBuild: "v0.1.1-test",
		CreatedAt: packageFixtureTime(), IncludesSessions: false,
	}
	for _, p := range parts {
		m.Files[p.name] = omnpkg.FileInfo{SHA256: sha256hex(p.body), Size: int64(len(p.body))}
	}
	man, err := json.Marshal(m)
	require.NoError(t, err)
	all := append([]tarPart{{name: omnpkg.ManifestEntry, body: man}}, parts...)
	return zstdTar(t, all)
}

func packageFixtureTime() time.Time {
	return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
}

// importViaRouteLimit posts through the production registrar (FR-045). The
// byte counter wraps the body outside that handler, so the 64 MiB cap is the
// route's own MaxBytesReader and not one the test installs.
func importViaRouteLimit(t *testing.T, api *restAPI, body io.Reader, contentLength int64) (packageHTTP, int64) {
	t.Helper()
	var n int64
	mux := packageRouteMux(t, api)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &countingBody{ReadCloser: r.Body, n: &n}
		cfg := api.agentLoop.GetConfig()
		r = r.WithContext(putConfigSnapshot(r.Context(), cfg))
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(packageCtx(t), http.MethodPost, srv.URL+"/api/v1/packages/import", body)
	require.NoError(t, err)
	req.ContentLength = contentLength
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+packageTestBearer)
	resp, err := (&http.Client{Timeout: packageCallTimeout}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return packageHTTP{Code: resp.StatusCode, Body: raw, Hdr: resp.Header}, n
}

type countingBody struct {
	io.ReadCloser
	n *int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	*c.n += int64(n)
	return n, err
}

// zstdMagic is the zstd frame magic an export body must start with when the
// handler returned package bytes rather than an error document.
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// TestPackagesAudit_OffIsHonoured is Decision D (ARCH-RULING-T5-A-D;
// FR-016, FR-040, FR-062). With audit_log false, package operations succeed
// and write no audit file. The harness logger is cleared first.
func TestPackagesAudit_OffIsHonoured(t *testing.T) {
	t.Run("audit_log false writes nothing and refuses nothing", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		setPackageAuditLog(t, api, false)
		api.auditor = nil
		removePackageAuditFiles(t, api.homePath)
		require.Nil(t, api.auditor, "audit off: the shared logger stays nil")
		require.False(t, api.agentLoop.GetConfig().Sandbox.AuditLog)
		require.Nil(t, api.agentLoop.AuditLogger(), "the agent loop's logger is nil when audit was off at loop start")

		id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
		exp := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, exp.Code, "export must not be 500 audit_failed when audit is off; body=%s", exp.Body)
		require.True(t, bytes.HasPrefix(exp.Body, zstdMagic), "export returns the package bytes")

		damaged := callPackageImport(t, api, []byte("this is not a package"))
		requirePackageStatus(t, damaged, http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)

		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		prev := stageUpload(t, api, validMainPackage(t))
		applied := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		// Spec route table: apply is 200. The ruling wrote 201 (the upload
		// status). Asserting 201 would fail after the fallback is removed.
		require.Equal(t, http.StatusOK, applied.Code, "apply succeeds when audit is off (spec status 200). body=%s", applied.Body)
		require.NotContains(t, resultWarningCodes(decodeResult(t, applied.Body)), "audit_failed")

		dir := filepath.Join(api.homePath, "backups", ".staging", "stg-audit-off")
		writeStagingJournal(t, api.homePath, "stg-audit-off", recoveryAgentID, "ws-missing", omnpkg.JournalStarted)
		bootRecoverImports(packageCtx(t), api.homePath, api.agentLoop.GetConfig(), nil)
		// Spec lines 323-324: the staging folder, journal included, is deleted.
		t5RequireAbsent(t, dir)
		requireNoRecoveryAgent(t, api.homePath)

		matches, gerr := filepath.Glob(filepath.Join(api.homePath, "system", "audit*.jsonl"))
		require.NoError(t, gerr)
		require.Empty(t, matches, "audit_log false: packageAuditLogger and packageRecoveryOps.writeAudit must not fall back to audit.NewLogger; found %v", matches)
	})

	t.Run("audit_log true writes exactly one entry per step", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		packageT5.Reload = func() (bool, error) { return true, nil }
		setPackageAuditLog(t, api, true)
		require.NotNil(t, api.auditor, "audit on: the harness wires the shared logger")
		// The agent and workspace are fixture, not one of the four audited steps.
		id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		n := len(auditJSONLLines(t, api.homePath))
		exp := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, exp.Code, "body=%s", exp.Body)
		require.True(t, bytes.HasPrefix(exp.Body, zstdMagic))
		n = requireOneNewAudit(t, api.homePath, n, "export")

		damaged := callPackageImport(t, api, []byte("this is not a package"))
		requirePackageStatus(t, damaged, http.StatusUnprocessableEntity, omnpkg.CodeNotAPackage)
		n = requireOneNewAudit(t, api.homePath, n, "upload")

		prev := stageUpload(t, api, validMainPackage(t))
		applied := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, applied.Code, "body=%s", applied.Body)
		require.NotContains(t, resultWarningCodes(decodeResult(t, applied.Body)), "audit_failed")
		n = requireOneNewAudit(t, api.homePath, n, "import")

		dir := filepath.Join(api.homePath, "backups", ".staging", "stg-audit-on")
		writeStagingJournal(t, api.homePath, "stg-audit-on", recoveryAgentID, "ws-missing", omnpkg.JournalStarted)
		bootRecoverImports(packageCtx(t), api.homePath, api.agentLoop.GetConfig(), nil)
		t5RequireAbsent(t, dir)
		requireNoRecoveryAgent(t, api.homePath)
		requireOneNewAudit(t, api.homePath, n, "recovery")
	})
}

func setPackageAuditLog(t *testing.T, api *restAPI, on bool) {
	t.Helper()
	api.agentLoop.GetConfig().Sandbox.AuditLog = on
	var doc map[string]any
	require.NoError(t, json.Unmarshal(configBytes(t, api.homePath), &doc))
	sandbox, _ := doc["sandbox"].(map[string]any)
	if sandbox == nil {
		sandbox = map[string]any{}
	}
	sandbox["audit_log"] = on
	doc["sandbox"] = sandbox
	raw, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"), append(raw, '\n'), 0o600))
}

func requireOneNewAudit(t *testing.T, home string, before int, operation string) int {
	t.Helper()
	lines := auditJSONLLines(t, home)
	require.Equal(t, before+1, len(lines), "exactly one new audit entry for %s", operation)
	var row struct {
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &row), "line=%s", lines[len(lines)-1])
	require.Equal(t, operation, row.Details["operation"], "line=%s", lines[len(lines)-1])
	return len(lines)
}

func auditJSONLLines(t *testing.T, home string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(home, "system", "audit*.jsonl"))
	require.NoError(t, err)
	var lines []string
	for _, path := range matches {
		raw, rerr := os.ReadFile(path)
		require.NoError(t, rerr)
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				lines = append(lines, string(line))
			}
		}
	}
	return lines
}

func removePackageAuditFiles(t *testing.T, home string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(home, "system", "audit*.jsonl"))
	require.NoError(t, err)
	for _, path := range matches {
		require.NoError(t, os.Remove(path))
	}
}
