// packages_t5_export_red_test.go — RED tests for the package export route and
// the access rules on all three package routes (epic #1094, T-5).
//
// Spec: docs/internal/specs/export-import-s1-spec.md revision 7. Every case
// names its spec id. The handlers are the compile-only stub in
// stub_t5_for_red.go (DELETE when GREEN lands): a case that expects a spec
// status fails because the stub answers 501 not_implemented.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// TestPackages_RedStubAnswersNotImplemented is the explicit control that the
// RED stub is reachable. It is the one test allowed to pass: it proves a
// missing handler is visible as 501 not_implemented rather than a compile
// failure. Every other test in this file expects a spec status and must fail.
func TestPackages_RedStubAnswersNotImplemented(t *testing.T) {
	api := packageAPI(t)
	got := callPackageExport(t, api, `{"agent_ids":["`+specSourceID+`"]}`)
	requirePackageStatus(t, got, http.StatusNotImplemented, "not_implemented")
	// D7 closing note: crash, leave and publish seams are nil in production.
	require.False(t, packageSeamsBlocked(), "production package seams must be nil")
	// The stub registrar is what GREEN replaces. It must answer 501, not 404.
	mux := http.NewServeMux()
	api.registerPackageRoutes(&testMuxRegistrar{mux: mux})
	req := packageAuthRequest(t, http.MethodPost, "/api/v1/packages/export", strings.NewReader(`{"agent_ids":[]}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code, "stub route body=%s", rec.Body)
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
}

type accessOpts struct {
	auth    string
	bigBody bool
	want    int
}

func hitAccess(t *testing.T, opt accessOpts) packageHTTP {
	t.Helper()
	api := packageAPI(t)
	id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	var body string
	if opt.bigBody {
		// 1 MiB + 1 byte (FR-045, D8 row 7). The padding is inside a JSON string
		// so the body is still one JSON value larger than the route limit.
		body = `{"agent_ids":["` + id + `"],"pad":"` + strings.Repeat("a", packageJSONLimit) + `"}`
	} else {
		body = `{"agent_ids":["` + id + `"]}`
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/packages/export", strings.NewReader(body))
	req = req.WithContext(packageCtx(t))
	req.Header.Set("Content-Type", "application/json")
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
	require.Equal(t, opt.want, got.Code, "D8 auth=%s body=%s", opt.auth, got.Body)
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
		api := packageAPI(t)
		id := createMainAgent(t, api, "Writer", "Answer in rhyme.\n")
		// Values that are not in the prompt. Export must not open these files (FR-013).
		secrets := []string{"cred-SECRET-value", "master-KEY-value", "cli-TOKEN-value", "mcp-HEADER-value"}
		require.NoError(t, os.WriteFile(api.homePath+"/credentials.json", []byte(secrets[0]), 0o600))
		require.NoError(t, os.WriteFile(api.homePath+"/master.key", []byte(secrets[1]), 0o600))
		require.NoError(t, os.WriteFile(api.homePath+"/cli.token", []byte(secrets[2]), 0o600))
		// config.json is the file export must not open; leave it intact and put the
		// header value in a sibling the scan still checks the package against.
		require.NoError(t, os.WriteFile(api.homePath+"/mcp-header.txt", []byte(secrets[3]), 0o600))
		got := callPackageExport(t, api, `{"agent_ids":["`+id+`"]}`)
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		plain := decompressZstd(t, got.Body)
		for _, s := range secrets {
			require.NotContains(t, string(got.Body), s)
			require.NotContains(t, string(plain), s)
		}
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
	t.Run("upload unknown element type is element_type_not_supported", func(t *testing.T) {
		api := packageAPI(t)
		got := callPackageImport(t, api, unknownElementPackage(t))
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

// The helpers below are filled in the import file? No, keep decode here but
// I referenced helpers I haven't written: decompressZstd, tarNew, readTar,
// errEOF, jsonUnmarshal, require.NoDirExists, putConfigSnapshot, config.HashBearer,
// config.WithSnapshot (removed).
//
// I need to fix this file before compile. Let me add the missing helpers in the
// harness via a second edit, and fix HashBearer / snapshot / NoDirExists.
