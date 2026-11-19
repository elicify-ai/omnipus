// packages_t5_export_red_test.go — RED tests for the package export route and
// the access rules on all three package routes (epic #1094, T-5).
//
// Spec: docs/internal/specs/export-import-s1-spec.md revision 7. Every case
// names its spec id. The handlers are the compile-only stub in
// stub_t5_for_red.go (DELETE when GREEN lands): a case that expects a spec
// status fails because the stub answers 501 not_implemented.
package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// TestPackages_ProductionSeamsAreNil preserves D7's production-seam guard.
// The RED-only 501 control is retired: stub_t5_for_red.go explicitly says
// "DELETE when GREEN lands". Production routing is checked separately below.
func TestPackages_ProductionSeamsAreNil(t *testing.T) {
	require.False(t, packageSeamsBlocked(), "production package seams must be nil")
}

// TestPackages_RoutesRegisteredLocalOnly is FR-045 [local mode only] and
// Dataset D8 row 6 (platform mode → 404).
//
// The local half sends the BDD selection `agent_ids: []` and expects 400
// export_selection_invalid through the production registrar
// (registerAdditionalEndpoints). A generic mux 404 means the route is not
// registered. The platform half expects 404 for the same request. Both halves
// live in one test so a missing route cannot pass the platform 404 by accident.
func TestPackages_RoutesRegisteredLocalOnly(t *testing.T) {
	api := packageAPI(t)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	// FR-045, BDD "Export via REST with wrong selection size": local mode.
	local := httptest.NewRecorder()
	req := packageAuthRequest(t, http.MethodPost, "/api/v1/packages/export", strings.NewReader(`{"agent_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	// withAuth reads the config snapshot when the route is wrapped. Inject the
	// live config so a registered route is not rejected for a missing snapshot.
	cfg := api.agentLoop.GetConfig()
	req = req.WithContext(putConfigSnapshot(req.Context(), cfg))
	mux.ServeHTTP(local, req)
	require.Equal(t, http.StatusBadRequest, local.Code, "local mode must register POST /api/v1/packages/export; body=%s", local.Body)
	er := decodePackageErr(t, local.Body.Bytes())
	require.Equal(t, omnpkg.CodeExportSelectionInvalid, packageErrCode(er))

	// D8 row 6, BDD "Access rules": platform mode hides the route (404).
	restore := config.SetEdition(config.EditionDesktop)
	t.Cleanup(restore)
	muxP := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: muxP})
	plat := httptest.NewRecorder()
	reqP := packageAuthRequest(t, http.MethodPost, "/api/v1/packages/export", strings.NewReader(`{"agent_ids":[]}`))
	reqP.Header.Set("Content-Type", "application/json")
	reqP = reqP.WithContext(putConfigSnapshot(reqP.Context(), cfg))
	muxP.ServeHTTP(plat, reqP)
	require.Equal(t, http.StatusNotFound, plat.Code, "platform mode must not register the package routes; body=%s", plat.Body)
}

// TestPackages_AccessRules is Dataset D8 (rows 1–5 and 7) and FR-045, FR-046.
// Requests go through the production registrar, as FR-045 requires
// ("Tests MUST go through the registered routes").
func TestPackages_AccessRules(t *testing.T) {
	// D8 row 1 — no header → 401. BDD "Access rules on registered routes".
	t.Run("D8-1 no header is 401", func(t *testing.T) {
		hitAccess(t, accessOpts{auth: "none", want: http.StatusUnauthorized})
	})
	// D8 row 2 — wrong bearer → 401.
	t.Run("D8-2 wrong bearer is 401", func(t *testing.T) {
		hitAccess(t, accessOpts{auth: "wrong", want: http.StatusUnauthorized})
	})
	// D8 row 3 — human admin bearer → 200 or 201 (export of a real custom agent).
	t.Run("D8-3 human admin bearer exports", func(t *testing.T) {
		hitAccess(t, accessOpts{auth: "admin", want: http.StatusOK})
	})
	// D8 row 4, FR-046 — cli.token works, no password re-entry.
	t.Run("D8-4 cli token exports without a password prompt", func(t *testing.T) {
		got := hitAccess(t, accessOpts{auth: "cli", want: http.StatusOK})
		require.NotContains(t, strings.ToLower(string(got.Body)), "password",
			"FR-046: New mode must not ask for the password again")
	})
	// D8 row 5 — developer bypass on → 503.
	t.Run("D8-5 developer bypass is 503", func(t *testing.T) {
		hitAccess(t, accessOpts{auth: "bypass", want: http.StatusServiceUnavailable})
	})
	// D8 row 7 — export JSON body over 1 MiB → 413 (the existing limit, not package_too_large).
	t.Run("D8-7 export JSON over 1 MiB is 413", func(t *testing.T) {
		hitAccess(t, accessOpts{auth: "admin", bigBody: true, want: http.StatusRequestEntityTooLarge})
	})
	// The same D8 rows on import and apply. Export-only coverage would stay green
	// if those registered routes dropped auth or the apply JSON cap (FR-045).
	t.Run("D8 import no header is 401", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "import", auth: "none", want: http.StatusUnauthorized})
	})
	t.Run("D8 import wrong bearer is 401", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "import", auth: "wrong", want: http.StatusUnauthorized})
	})
	t.Run("D8 import admin is 201", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "import", auth: "admin", want: http.StatusCreated})
	})
	t.Run("D8 import cli token is 201", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "import", auth: "cli", want: http.StatusCreated})
	})
	t.Run("D8 import bypass is 503", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "import", auth: "bypass", want: http.StatusServiceUnavailable})
	})
	t.Run("D8 apply no header is 401", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "apply", auth: "none", want: http.StatusUnauthorized})
	})
	t.Run("D8 apply wrong bearer is 401", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "apply", auth: "wrong", want: http.StatusUnauthorized})
	})
	t.Run("D8 apply admin is 200", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "apply", auth: "admin", want: http.StatusOK})
	})
	t.Run("D8 apply cli token is 200", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "apply", auth: "cli", want: http.StatusOK})
	})
	t.Run("D8 apply bypass is 503", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "apply", auth: "bypass", want: http.StatusServiceUnavailable})
	})
	t.Run("D8 apply JSON over 1 MiB is 413", func(t *testing.T) {
		hitAccess(t, accessOpts{route: "apply", auth: "admin", bigBody: true, want: http.StatusRequestEntityTooLarge})
	})
	// FR-045: a complete JSON value followed by more than 1 MiB must be refused
	// as too large. Decoding the first value and ignoring the rest is not the size layer.
	t.Run("FR-045 export valid JSON prefix then more than 1 MiB is 413", func(t *testing.T) {
		requireJSONPrefixStopped(t, "export")
	})
	t.Run("FR-045 apply valid JSON prefix then more than 1 MiB is 413", func(t *testing.T) {
		requireJSONPrefixStopped(t, "apply")
	})
}

// jsonBodyLimit is the export and apply JSON cap from FR-045: 1 MiB.
const jsonBodyLimit = 1 << 20

func requireJSONPrefixStopped(t *testing.T, route string) {
	t.Helper()
	api := packageAPI(t)
	var path, prefix, contentType string
	switch route {
	case "export":
		id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
		path = "/api/v1/packages/export"
		prefix = `{"agent_ids":["` + id + `"]}`
		contentType = "application/json"
	case "apply":
		path = "/api/v1/packages/import/00000000-0000-4000-8000-000000000099/apply"
		prefix = `{"mode":"new"}`
		contentType = "application/json"
	default:
		t.Fatalf("unknown route %s", route)
	}
	body := append([]byte(prefix), bytes.Repeat([]byte("x"), jsonBodyLimit+1)...)
	var n int64
	mux := packageRouteMux(t, api)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &countingBody{ReadCloser: r.Body, n: &n}
		cfg := api.agentLoop.GetConfig()
		r = r.WithContext(putConfigSnapshot(r.Context(), cfg))
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(packageCtx(t), http.MethodPost, srv.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+packageTestBearer)
	resp, err := (&http.Client{Timeout: packageCallTimeout}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode,
		"FR-045: %s must refuse a body over 1 MiB even when a valid JSON value is a prefix; read %d of %d; body=%s",
		route, n, len(body), raw)
	require.LessOrEqual(t, n, int64(jsonBodyLimit)+1,
		"FR-045: %s consumed %d bytes; the size layer stops at 1 MiB plus the one overflow byte, not the whole trailing body (%d)",
		route, n, len(body))
}

type accessOpts struct {
	auth    string
	route   string
	bigBody bool
	want    int
}

func hitAccess(t *testing.T, opt accessOpts) packageHTTP {
	t.Helper()
	if opt.route == "" {
		opt.route = "export"
	}
	api := packageAPI(t)
	var agentID string
	if opt.route == "export" {
		agentID = createMainAgent(t, api, "Writer", "Answer in rhyme.")
	}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	var (
		path        string
		body        string
		contentType string
	)
	switch opt.route {
	case "export":
		path = "/api/v1/packages/export"
		contentType = "application/json"
		if opt.bigBody {
			// 1 MiB + 1 byte (FR-045, D8 row 7). The padding is inside a JSON string
			// so the body is still one JSON value larger than the route limit.
			body = `{"agent_ids":["` + agentID + `"],"pad":"` + strings.Repeat("a", packageJSONLimit) + `"}`
		} else {
			body = `{"agent_ids":["` + agentID + `"]}`
		}
	case "import":
		path = "/api/v1/packages/import"
		contentType = "application/octet-stream"
		body = string(validMainPackage(t))
	case "apply":
		contentType = "application/json"
		// A non-UUID id is refused before the body is read, so the size case
		// uses a real id shape. The cap must answer 413 before any lookup.
		stagingID := "00000000-0000-4000-8000-000000000099"
		if opt.bigBody {
			body = `{"mode":"new","pad":"` + strings.Repeat("a", packageJSONLimit) + `"}`
		} else if opt.want == http.StatusOK {
			usePackageSeams(t)
			packageT5.Reload = func() (bool, error) { return true, nil }
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			prev := stageUpload(t, api, validMainPackage(t))
			stagingID = prev.StagingId
			body = applyBody("new", ws)
		} else {
			body = `{"mode":"new"}`
		}
		path = "/api/v1/packages/import/" + stagingID + "/apply"
	default:
		t.Fatalf("unknown route %q", opt.route)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req = req.WithContext(packageCtx(t))
	req.Header.Set("Content-Type", contentType)
	cfg := api.agentLoop.GetConfig()
	switch opt.auth {
	case "none":
	case "wrong":
		req.Header.Set("Authorization", "Bearer not-the-token")
	case "admin":
		{
			// Human account bearer (D8 row 3). A configured account is tried
			// before the env token, so this is not the legacy env-token path.
			const adminRaw = "admin-token-for-package-routes"
			cfg.Gateway.Users = []config.UserConfig{{
				Username: "admin", TokenHash: hashBearer(t, adminRaw),
			}}
			req.Header.Set("Authorization", "Bearer "+adminRaw)
		}
	case "cli":
		{
			// FR-046: the CLI signs in as the synthetic user cli. The raw token
			// is not a password, and the route must not ask for one.
			const cliRaw = "cli-token-for-package-routes"
			cfg.Gateway.CLIToken = &config.TokenEntry{ID: "cli01", Hash: hashBearer(t, cliRaw)}
			req.Header.Set("Authorization", "Bearer "+cliRaw)
		}
	case "bypass":
		// No env token: withAuth reaches the bypass branch, then RequireNotBypass
		// answers 503 (D8 row 5). See routes_admin_test.go's bypass setup.
		t.Setenv("OMNIPUS_BEARER_TOKEN", "")
		cfg.Gateway.DevModeBypass = true
		req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
	default:
		t.Fatalf("unknown auth opt %q", opt.auth)
	}
	req = req.WithContext(putConfigSnapshot(req.Context(), cfg))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	got := packageHTTP{Code: w.Code, Body: w.Body.Bytes(), Hdr: w.Header()}
	require.Equal(t, opt.want, got.Code, "D8 route=%s auth=%s body=%s", opt.route, opt.auth, got.Body)
	return got
}

// TestPackagesExport_Route is TDD row 23: export a Main, secrets scan, pasted
// secret, REST selection size, and Dataset D4 through the route (FR-002,
// FR-010, FR-011, FR-013, FR-015, FR-016, FR-017).
func TestPackagesExport_Route(t *testing.T) {
	t.Run("FR-002 FR-010 FR-015 export a Main with prompt and instructions", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		id := createMainAgent(t, api, "Writer", "Answer in rhyme.\n")
		// REST create trims its input; FR-015 exports the bytes STORED on disk.
		// Persist deliberate trailing whitespace so illegal export trimming still dies.
		require.NoError(t, os.WriteFile(api.homePath+"/agents/"+id+"/SOUL.md", []byte("Answer in rhyme.\n"), 0o600))
		// AGENT.md is import-only at create; write it the way a stored agent has it.
		mdPath := api.homePath + "/agents/" + id + "/AGENT.md"
		const rules = "  rules\n"
		require.NoError(t, os.WriteFile(mdPath, []byte(rules), 0o600))
		before := configBytes(t, api.homePath)
		got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		require.Equal(t, "application/octet-stream", got.Hdr.Get("Content-Type"))
		disp := got.Hdr.Get("Content-Disposition")
		require.True(t, strings.HasPrefix(disp, `attachment; filename="`), "FR-010 safe filename, got %q", disp)
		require.True(t, strings.HasSuffix(disp, `.omnpkg"`), "filename must end in .omnpkg, got %q", disp)
		require.NotContains(t, disp, "/")
		require.NotContains(t, disp, `\`)
		// "Writer" is already a safe name, so the filename is Writer.omnpkg.
		require.Contains(t, disp, `filename="Writer.omnpkg"`)
		man, files := decodePackageBytes(t, got.Body)
		require.Equal(t, "omnpkg", man["format"])
		require.EqualValues(t, 1, man["format_version"])
		require.Equal(t, "custom", man["scope"])
		require.Equal(t, false, man["includes_sessions"])
		counts, _ := man["counts"].(map[string]any)
		require.EqualValues(t, 1, counts["agent"])
		require.Equal(t, "Answer in rhyme.\n", string(files[agentSoulPath(id)]))
		require.Equal(t, rules, string(files[agentMDPath(id)]), "FR-015 AGENT.md byte-identical")
		// FR-016: exactly one audit entry with the actor, the agent id and the package sha256.
		sum := sha256hex(got.Body)
		auditText := readAudit(t, api.homePath)
		require.Equal(t, 1, strings.Count(auditText, `"export"`), "exactly one export audit entry, got %s", auditText)
		require.Contains(t, auditText, id)
		require.Contains(t, auditText, sum)
		require.Contains(t, auditText, "_env_token")
		// FR-017: export leaves no file in the data folder. config.json is unchanged.
		require.Equal(t, before, configBytes(t, api.homePath))
	})

	t.Run("FR-015 export an agent with no AGENT.md", func(t *testing.T) {
		api := packageAPI(t)
		id := createMainAgent(t, api, "Writer", "Answer in rhyme.\n")
		got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		_, files := decodePackageBytes(t, got.Body)
		_, hasMD := files[agentMDPath(id)]
		require.False(t, hasMD, "a missing AGENT.md must not be invented")
		require.Contains(t, files, agentSoulPath(id))
	})

	t.Run("FR-013 structured secrets do not appear", func(t *testing.T) {
		exportOmitsStructuredSecrets(t)
	})

	t.Run("pasted secret in the prompt travels", func(t *testing.T) {
		api := packageAPI(t)
		id := createMainAgent(t, api, "Writer", "keep sk-PASTED-789 safe")
		got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		_, files := decodePackageBytes(t, got.Body)
		require.Contains(t, string(files[agentSoulPath(id)]), "sk-PASTED-789")
	})

	// BDD "Export via REST with wrong selection size".
	t.Run("agent_ids empty or two ids is 400 export_selection_invalid", func(t *testing.T) {
		api := packageAPI(t)
		for _, body := range []string{`{"agent_ids":[]}`, `{"agent_ids":["a","b"]}`} {
			got := callPackageExport(t, api, body)
			requirePackageStatus(t, got, http.StatusBadRequest, omnpkg.CodeExportSelectionInvalid)
		}
	})

	// Dataset D4 rows 1–8, FR-011 [r7]: the message says built-in agents cannot
	// be exported, and does not say "yet".
	t.Run("D4 built-ins are refused core_agent_not_exportable", func(t *testing.T) {
		api := packageAPI(t)
		ids := []string{"mia", "jim", "omnipus", "worker", "planner", "researcher", "judge", "plansupervisor"}
		for _, id := range ids {
			seedListedAgent(t, api, config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCore})
			got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
			er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeCoreAgentNotExportable)
			msg := strings.ToLower(er.Error)
			require.Contains(t, msg, "cannot be exported", "FR-011 [r7] message for %s: %s", id, er.Error)
			require.NotContains(t, msg, "yet", "FR-011 [r7] must not say yet; message for %s: %s", id, er.Error)
		}
	})

	// D4 row 9 — a user-created subagent exports.
	t.Run("D4-9 a custom agent exports", func(t *testing.T) {
		api := packageAPI(t)
		id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
		got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	})

	// FR-011 unknown id.
	t.Run("unknown agent is 404 agent_not_found", func(t *testing.T) {
		api := packageAPI(t)
		got := callPackageExport(t, api, `{"agent_ids":["00000000-0000-4000-8000-000000000000"]}`)
		requirePackageStatus(t, got, http.StatusNotFound, "agent_not_found")
	})
}

// TestPackagesExport_AuditFailure is TDD row 21e, FR-016: when the audit write
// fails, export is 500 audit_failed and no package bytes are sent.
func TestPackagesExport_AuditFailure(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
	packageT5.AuditWrite = func(kind string, fields map[string]string) error {
		return errAuditFailed
	}
	got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
	requirePackageStatus(t, got, http.StatusInternalServerError, "audit_failed")
	require.NotContains(t, got.Hdr.Get("Content-Type"), "octet-stream", "no package bytes when audit fails")
	require.NotContains(t, string(got.Body), "omnpkg")
}

// TestPackages_S1aRefusals is TDD row 33 [S1a only]: external workers and unknown
// element types are refused on export and on upload (FR-006, FR-010, FR-020).
func TestPackages_S1aRefusals(t *testing.T) {
	t.Run("export external worker with runtime values", func(t *testing.T) {
		api := packageAPI(t)
		agent := config.AgentConfig{
			ID: "ext-1", Name: "Coder", Type: config.AgentTypeWorker, Description: "codes",
			Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
				Kind: config.ExecutorKindExternalCLI, CLI: "codex",
				CLIArgs: "--key sk-TEST-123", EnvOverrides: map[string]string{"OPENAI_API_KEY": "sk-TEST-456"},
			}},
		}
		seedListedAgent(t, api, agent)
		got := callPackageExport(t, api, `{"agent_ids":["ext-1"]}`)
		requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeExternalWorkerNotSupported)
	})
	t.Run("export external worker without runtime values", func(t *testing.T) {
		api := packageAPI(t)
		agent := config.AgentConfig{
			ID: "ext-2", Name: "Coder", Type: config.AgentTypeWorker, Description: "codes",
			Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
				Kind: config.ExecutorKindExternalCLI, CLI: "codex",
			}},
		}
		seedListedAgent(t, api, agent)
		got := callPackageExport(t, api, `{"agent_ids":["ext-2"]}`)
		requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeExternalWorkerNotSupported)
	})
	t.Run("upload external worker with and without runtime values", func(t *testing.T) {
		api := packageAPI(t)
		with := externalPackage(t, true)
		without := externalPackage(t, false)
		for _, pkg := range [][]byte{with, without} {
			got := callPackageImport(t, api, pkg)
			requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeExternalWorkerNotSupported)
			_, statErr := os.Stat(api.homePath + "/backups/.staging")
			require.True(t, os.IsNotExist(statErr), "a refused upload must not keep a staging folder")
		}
	})
	t.Run("D12-2 only unknown elements is no_supported_element", func(t *testing.T) {
		api := packageAPI(t)
		got := callPackageImport(t, api, unknownElementPackage(t))
		requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeNoSupportedElement)
	})
	t.Run("FR-006 agent plus unknown element is element_type_not_supported", func(t *testing.T) {
		api := packageAPI(t)
		got := callPackageImport(t, api, mixedUnknownElementPackage(t))
		requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementTypeNotSupported)
	})
}

// errAuditFailed is the test hook's audit failure. It is not a spec message.
var errAuditFailed = os.ErrPermission

// decodePackageBytes reads a package with zstd and tar (not the engine) and
// returns the manifest JSON object plus payload files. FR-001: manifest.json
// is the first entry.
func decodePackageBytes(t *testing.T, raw []byte) (map[string]any, map[string][]byte) {
	t.Helper()
	plain := decompressZstd(t, raw)
	tr := tarNew(t, plain)
	files := map[string][]byte{}
	first := true
	for {
		hdr, err := tr.Next()
		if err != nil {
			require.ErrorIs(t, err, errEOF)
			break
		}
		body := readTar(t, tr, hdr)
		if first {
			require.Equal(t, "manifest.json", hdr.Name, "FR-001 first entry is manifest.json")
			first = false
			continue
		}
		files[hdr.Name] = body
	}
	require.False(t, first, "package has no entries")
	// Re-read manifest from the start.
	plain = decompressZstd(t, raw)
	tr = tarNew(t, plain)
	hdr, err := tr.Next()
	require.NoError(t, err)
	require.Equal(t, "manifest.json", hdr.Name)
	var man map[string]any
	require.NoError(t, jsonUnmarshal(readTar(t, tr, hdr), &man))
	return man, files
}

// exportOmitsStructuredSecrets is FR-013. The sentinels live in the real
// config.json (provider api_key and an MCP header) and in auth.json
// (access_token / refresh_token), plus the other files export must not open.
// Absence in the package is not enough: an inotify watch fails if export opens
// one of those files even when its bytes never reach the package.
func exportOmitsStructuredSecrets(t *testing.T) {
	api := packageAPI(t)
	id := createMainAgent(t, api, "Writer", "Answer in rhyme.\n")
	const (
		providerKey = "sk-CONFIG-PROVIDER-9f3a"
		authAccess  = "oauth-ACCESS-authjson-9f3a"
		authRefresh = "oauth-REFRESH-authjson-9f3a"
	)
	plantConfigProviderKey(t, api.homePath, providerKey)
	require.NoError(t, os.WriteFile(api.homePath+"/auth.json", []byte(
		`{"access_token":"`+authAccess+`","refresh_token":"`+authRefresh+`","provider":"openai","auth_method":"sign_in"}`), 0o600))
	secrets := map[string]string{
		"credentials.json": "cred-SECRET-value",
		"master.key":       "master-KEY-value",
		"cli.token":        "cli-TOKEN-value",
	}
	for name, value := range secrets {
		require.NoError(t, os.WriteFile(api.homePath+"/"+name, []byte(value), 0o600))
	}
	watched := []string{
		api.homePath + "/config.json",
		api.homePath + "/auth.json",
		api.homePath + "/credentials.json",
		api.homePath + "/master.key",
		api.homePath + "/cli.token",
	}
	cfg, err := os.ReadFile(watched[0])
	require.NoError(t, err)
	require.Contains(t, string(cfg), providerKey, "the provider key must be in config.json before export")
	auth, err := os.ReadFile(watched[1])
	require.NoError(t, err)
	require.Contains(t, string(auth), authRefresh, "the credential must be in auth.json before export")

	watch := newOpenWatch(t, watched)

	got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	watch.requireNoOpens(t, "FR-013: export must not open config.json, auth.json, credentials, master.key or cli.token")
	probe, err := os.Open(api.homePath + "/auth.json")
	require.NoError(t, err)
	require.NoError(t, probe.Close())
	watch.requireProbeSeen(t, "the watch must observe an open of auth.json; otherwise the no-open check is blind")
	plain := decompressZstd(t, got.Body)
	for _, secret := range []string{providerKey, authAccess, authRefresh, "cred-SECRET-value", "master-KEY-value", "cli-TOKEN-value"} {
		require.NotContains(t, string(got.Body), secret)
		require.NotContains(t, string(plain), secret)
	}
}

func plantConfigProviderKey(t *testing.T, home, key string) {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(configBytes(t, home), &doc))
	doc["providers"] = []any{map[string]any{
		"name":        "OpenRouter",
		"provider":    "openrouter",
		"api_key":     key,
		"api_key_ref": "OPENROUTER_API_KEY",
	}}
	mcp, _ := doc["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	mcp["servers"] = map[string]any{
		"docs": map[string]any{
			"enabled": true,
			"type":    "http",
			"url":     "https://example.invalid/mcp",
			"headers": map[string]any{"Authorization": "Bearer " + key},
		},
	}
	doc["mcp"] = mcp
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(home+"/config.json", raw, 0o600))
}
