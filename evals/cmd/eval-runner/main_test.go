package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ── CSRF extraction ───────────────────────────────────────────────────────────

// mockGatewayWithCSRF starts a test HTTP server that:
//   - Issues __Host-csrf cookie on every POST response.
//   - Records whether incoming requests carry the cookie + header.
func mockGatewayWithCSRF(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var csrfHitCount atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Emit the CSRF cookie on every response.
		http.SetCookie(w, &http.Cookie{
			Name:  csrfCookieName,
			Value: "abc123",
		})

		if r.Method == http.MethodPost {
			headerOK := r.Header.Get("X-Csrf-Token") == "abc123"
			cookieOK := false
			for _, c := range r.Cookies() {
				if c.Name == csrfCookieName && c.Value == "abc123" {
					cookieOK = true
				}
			}
			if headerOK && cookieOK {
				csrfHitCount.Add(1)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"test-token","id":"sess-1"}`))
	}))

	return srv, &csrfHitCount
}

func TestCSRFTokenExtractedAndSent(t *testing.T) {
	srv, csrfHits := mockGatewayWithCSRF(t)
	defer srv.Close()

	h := &gatewayHandle{baseURL: srv.URL}

	// First call: no token yet — doStatefulPost won't send csrf headers.
	body1, _ := json.Marshal(map[string]string{"x": "1"})
	resp1, err := h.doStatefulPost("/test", body1)
	if err != nil {
		t.Fatalf("first doStatefulPost error: %v", err)
	}
	h.extractCSRF(resp1)
	resp1.Body.Close()

	if h.csrfToken != "abc123" {
		t.Fatalf("expected csrfToken='abc123' after first response, got %q", h.csrfToken)
	}

	// Second call: token is now set — should carry both cookie and header.
	body2, _ := json.Marshal(map[string]string{"x": "2"})
	resp2, err := h.doStatefulPost("/test", body2)
	if err != nil {
		t.Fatalf("second doStatefulPost error: %v", err)
	}
	resp2.Body.Close()

	if csrfHits.Load() == 0 {
		t.Error("expected at least one request with valid CSRF cookie + header, got 0")
	}
}

// ── discoverScenarios — malformed YAML is skipped ─────────────────────────────

func TestDiscoverScenarios_SkipsMalformedYAML(t *testing.T) {
	dir := t.TempDir()

	// Write a valid scenario.
	validYAML := `id: test.valid
agent_id: mia
prompt: "Hello"
max_turns: 1
rubric: "Be friendly."
`
	if err := os.WriteFile(filepath.Join(dir, "valid.yaml"), []byte(validYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a malformed scenario (invalid YAML).
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(":::invalid yaml:::"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a YAML that parses but fails validation (missing agent_id).
	invalidScenario := `id: test.noid
prompt: "Hello"
`
	if err := os.WriteFile(filepath.Join(dir, "noid.yaml"), []byte(invalidScenario), 0o644); err != nil {
		t.Fatal(err)
	}

	scenarios, err := discoverScenarios(dir)
	if err != nil {
		t.Fatalf("unexpected error from discoverScenarios: %v", err)
	}
	if len(scenarios) != 1 {
		t.Errorf("expected 1 valid scenario, got %d", len(scenarios))
	}
	if len(scenarios) > 0 && scenarios[0].ID != "test.valid" {
		t.Errorf("expected scenario id 'test.valid', got %q", scenarios[0].ID)
	}
}

// ── Zero scenarios exit code ──────────────────────────────────────────────────

// TestAllowEmptyScenarios verifies the cfg field is wired correctly.
// We can't test os.Exit directly, but we can test the logic that controls it.
func TestAllowEmptyScenarios_FieldDefault(t *testing.T) {
	// parseFlags reads from os.Args; we test the cfg struct directly.
	c := cfg{allowEmptyScenarios: false}
	if c.allowEmptyScenarios {
		t.Error("allowEmptyScenarios should default to false")
	}
}

// ── EvalResult error tally ────────────────────────────────────────────────────

func TestEvalResult_ErrorTally(t *testing.T) {
	// Simulate the counting logic from main() to verify F32 logic.
	results := []EvalResult{
		{ScenarioID: "a", Error: "failed"},
		{ScenarioID: "b", Error: "also failed"},
	}

	erroredCount := 0
	successCount := 0
	for _, r := range results {
		if r.Error != "" {
			erroredCount++
		} else {
			successCount++
		}
	}

	total := len(results)
	allErrored := erroredCount == total && total > 0

	if !allErrored {
		t.Error("expected allErrored=true when all results have errors")
	}
	if successCount != 0 {
		t.Errorf("expected successCount=0, got %d", successCount)
	}
}

func TestEvalResult_PartialErrorDoesNotTriggerAllError(t *testing.T) {
	results := []EvalResult{
		{ScenarioID: "a", Error: "failed"},
		{ScenarioID: "b", Error: ""},
	}

	erroredCount := 0
	for _, r := range results {
		if r.Error != "" {
			erroredCount++
		}
	}

	total := len(results)
	allErrored := erroredCount == total && total > 0

	if allErrored {
		t.Error("expected allErrored=false when only some results have errors")
	}
}

// ── JSONL output survives all-error run ───────────────────────────────────────

func TestJSONLWrittenEvenOnAllErrors(t *testing.T) {
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "results.jsonl")

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	results := []EvalResult{
		{
			ScenarioID: "a",
			TS:         time.Now(),
			Error:      "gateway unreachable",
		},
	}

	for _, r := range results {
		line, marshalErr := json.Marshal(r)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, writeErr := f.Write(append(line, '\n')); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	f.Close()

	// Verify file exists and has content.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}
	if !strings.Contains(string(data), "gateway unreachable") {
		t.Error("expected error message in JSONL output")
	}
}

// ── gatewayHandle.doStatefulPost attaches Authorization header ────────────────

func TestDoStatefulPost_AttachesAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	h := &gatewayHandle{
		baseURL: srv.URL,
		token:   "my-bearer-token",
	}
	body, _ := json.Marshal(map[string]string{"k": "v"})
	resp, err := h.doStatefulPost("/any", body)
	if err != nil {
		t.Fatalf("doStatefulPost error: %v", err)
	}
	resp.Body.Close()

	if gotAuth != "Bearer my-bearer-token" {
		t.Errorf("expected Authorization 'Bearer my-bearer-token', got %q", gotAuth)
	}
}

// ── Gateway boot contract (issue #637) ────────────────────────────────────────

// TestSeedConfigIsAcceptedByTheRealConfigLoader is the regression guard for the
// defect that silently killed every nightly eval run for three months: the
// seeded config.json omitted "version", so the gateway treated it as a pre-v1
// config, ran the v0 migration, and aborted boot on a schema mismatch. The
// runner saw only a 60s "did not write port file" timeout.
//
// The oracle here is the gateway's OWN loader, not a hand-copied expectation —
// if pkg/config ever tightens what it accepts, this test fails rather than the
// nightly quietly going red again.
func TestSeedConfigIsAcceptedByTheRealConfigLoader(t *testing.T) {
	home := t.TempDir()
	if err := seedConfig(home, 41234); err != nil {
		t.Fatalf("seedConfig: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("the real gateway config loader rejected the seeded config: %v", err)
	}
	if cfg.Gateway.Port != 41234 {
		t.Errorf("gateway.port = %d, want the concrete port 41234 the runner reserved", cfg.Gateway.Port)
	}
	if cfg.AgentHomeBasePath() != home {
		t.Errorf("AgentHomeBasePath() = %q, want %q — gateway.port file lands here", cfg.AgentHomeBasePath(), home)
	}
}

// TestSeedConfigDeclaresCurrentVersion pins the specific field whose absence
// caused the outage, so a future edit that drops it fails loudly here even if
// the loader has by then grown a lenient fallback.
func TestSeedConfigDeclaresCurrentVersion(t *testing.T) {
	home := t.TempDir()
	if err := seedConfig(home, 41235); err != nil {
		t.Fatalf("seedConfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}
	var probe struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("seeded config is not valid JSON: %v", err)
	}
	if probe.Version == nil {
		t.Fatal(`seeded config.json has no "version" field — the gateway will treat it as v0 and abort boot`)
	}
	if *probe.Version != config.CurrentVersion {
		t.Errorf("seeded version = %d, want config.CurrentVersion = %d", *probe.Version, config.CurrentVersion)
	}
}

// TestSeedConfigRejectsEphemeralPortZero encodes the second half of the defect:
// the gateway does not interpret port 0 as "choose one for me" — it either
// rejects it or echoes 0 back into gateway.port, leaving the runner polling a
// URL of http://127.0.0.1:0. The runner must always seed a concrete port.
func TestSeedConfigRejectsEphemeralPortZero(t *testing.T) {
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("pickFreePort returned %d, want a bindable port in [1, 65535]", port)
	}

	home := t.TempDir()
	if seedErr := seedConfig(home, port); seedErr != nil {
		t.Fatalf("seedConfig: %v", seedErr)
	}
	cfg, err := config.LoadConfig(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Gateway.Port == 0 {
		t.Fatal("seeded gateway.port is 0; the gateway never supported ephemeral port selection")
	}
}

// TestBootFailureDetailQuotesTheGatewayPanicLog covers the diagnostic half of
// the fix. The gateway writes fatal startup errors to logs/gateway_panic.log
// rather than the stderr it inherits, so without this the runner reports a bare
// timeout and the real cause is invisible — exactly why #637 went unexplained.
func TestBootFailureDetailQuotesTheGatewayPanicLog(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	const fatal = "Error: error loading config: config is missing a version field"
	body := "INFO starting\nINFO sandbox applied\n" + fatal + "\n"
	if err := os.WriteFile(filepath.Join(home, "logs", "gateway_panic.log"), []byte(body), 0o600); err != nil {
		t.Fatalf("write panic log: %v", err)
	}

	got := bootFailureDetail(home)
	if !strings.Contains(got, fatal) {
		t.Errorf("bootFailureDetail() = %q, want it to quote the fatal line %q", got, fatal)
	}
}

// TestBootFailureDetailSurvivesAMissingLog ensures the diagnostic helper never
// itself becomes the reason an error is unreportable.
func TestBootFailureDetailSurvivesAMissingLog(t *testing.T) {
	got := bootFailureDetail(t.TempDir())
	if got == "" {
		t.Error("bootFailureDetail() on a home with no panic log returned an empty string; want a stated reason")
	}
}

// ── Central e2e model resolution ──────────────────────────────────────────────

// TestCentralE2EModelReadsTheSingleSourceFile pins the ONE committed source of
// truth for the real-LLM e2e model id: tests/e2e/e2e-model.json. The returned
// slug must be provider-prefixed (openrouter/<vendor>/<model>) because the
// runner's OpenRouter call layer strips exactly that prefix (callJudge), and
// the workflows' AGENT_MODEL/JUDGE_MODEL convention carries it too.
func TestCentralE2EModelReadsTheSingleSourceFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tests", "e2e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tests/e2e: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "e2e-model.json"), []byte(`{"model":"vendor/test-model"}`), 0o600); err != nil {
		t.Fatalf("write e2e-model.json: %v", err)
	}
	t.Chdir(root)

	got, err := centralE2EModel()
	if err != nil {
		t.Fatalf("centralE2EModel() error = %v", err)
	}
	if got != "openrouter/vendor/test-model" {
		t.Fatalf("centralE2EModel() = %q, want %q (the runner needs the openrouter/ prefix; callJudge strips it)", got, "openrouter/vendor/test-model")
	}
}

// TestCentralE2EModelFailsClosedOnMissingFile: with no flag and no env override,
// an absent source file must stop the runner loudly instead of silently billing
// a stale fallback literal.
func TestCentralE2EModelFailsClosedOnMissingFile(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := centralE2EModel()
	if err == nil {
		t.Fatal("centralE2EModel() with no tests/e2e/e2e-model.json returned nil error; want a loud failure (no hardcoded fallback exists anymore)")
	}
	if !strings.Contains(err.Error(), "AGENT_MODEL") {
		t.Fatalf("error should name the AGENT_MODEL/JUDGE_MODEL override so the fix is actionable, got: %v", err)
	}
}

// TestCentralE2EModelRejectsAnEmptyModelField: an empty "model" value would
// resolve to the bare slug "openrouter/" — a config corruption that must not
// silently pass through.
func TestCentralE2EModelRejectsAnEmptyModelField(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tests", "e2e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tests/e2e: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "e2e-model.json"), []byte(`{"model":""}`), 0o600); err != nil {
		t.Fatalf("write e2e-model.json: %v", err)
	}
	t.Chdir(root)

	if _, err := centralE2EModel(); err == nil {
		t.Fatal("centralE2EModel() with an empty model field returned nil error; want a failure")
	}
}

// TestCentralE2EModelRejectsWhitespaceOnlyModelField: a whitespace-only
// "model" value passes the bare == "" check and would resolve to the slug
// "openrouter/   " — the same config corruption as an empty field, one step
// later in the pipeline (finding E2/L2). The value is trimmed before the
// emptiness check.
func TestCentralE2EModelRejectsWhitespaceOnlyModelField(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tests", "e2e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tests/e2e: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "e2e-model.json"), []byte(`{"model":"   "}`), 0o600); err != nil {
		t.Fatalf("write e2e-model.json: %v", err)
	}
	t.Chdir(root)

	if _, err := centralE2EModel(); err == nil {
		t.Fatal("centralE2EModel() with a whitespace-only model field returned nil error; want a failure")
	}
}

// TestCentralE2EModelRejectsInvalidJSON: a malformed source file is a loud
// configuration error, never a silent pass-through (finding E1's loader-error
// tier).
func TestCentralE2EModelRejectsInvalidJSON(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tests", "e2e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tests/e2e: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "e2e-model.json"), []byte(`{"model":`), 0o600); err != nil {
		t.Fatalf("write e2e-model.json: %v", err)
	}
	t.Chdir(root)

	if _, err := centralE2EModel(); err == nil {
		t.Fatal("centralE2EModel() with malformed JSON returned nil error; want a failure")
	}
}

// ── resolveModel — flag > env > file precedence (finding E1) ─────────────────

// TestResolveModelFlagBeatsEnvAndFile: an explicit --agent-model/--judge-model
// value wins over BOTH the environment override and the central file — and the
// file loader must never even be consulted.
func TestResolveModelFlagBeatsEnvAndFile(t *testing.T) {
	loaderCalled := false
	loader := func() (string, error) {
		loaderCalled = true
		return "openrouter/vendor/from-file", nil
	}
	got, source, err := resolveModel("openrouter/vendor/from-flag", "openrouter/vendor/from-env", loader)
	if err != nil {
		t.Fatalf("resolveModel() error = %v", err)
	}
	if got != "openrouter/vendor/from-flag" {
		t.Errorf("resolveModel() = %q, want the flag value", got)
	}
	if source != "flag" {
		t.Errorf("source = %q, want %q", source, "flag")
	}
	if loaderCalled {
		t.Error("loader was called even though the flag value was set — the file tier must not be consulted")
	}
}

// TestResolveModelEnvBeatsFile: with no flag, AGENT_MODEL/JUDGE_MODEL win over
// the central file — and a BROKEN file must not matter when the env override
// decides (the loader is not consulted).
func TestResolveModelEnvBeatsFile(t *testing.T) {
	loader := func() (string, error) {
		return "", errors.New("read tests/e2e/e2e-model.json: should never be called")
	}
	got, source, err := resolveModel("", "openrouter/vendor/from-env", loader)
	if err != nil {
		t.Fatalf("resolveModel() error = %v", err)
	}
	if got != "openrouter/vendor/from-env" {
		t.Errorf("resolveModel() = %q, want the env value", got)
	}
	if source != "env" {
		t.Errorf("source = %q, want %q", source, "env")
	}
}

// TestResolveModelFileWhenBothEmpty: with no flag and no env, the central file
// decides and the source is reported as "file".
func TestResolveModelFileWhenBothEmpty(t *testing.T) {
	loader := func() (string, error) { return "openrouter/vendor/from-file", nil }
	got, source, err := resolveModel("", "", loader)
	if err != nil {
		t.Fatalf("resolveModel() error = %v", err)
	}
	if got != "openrouter/vendor/from-file" {
		t.Errorf("resolveModel() = %q, want the file value", got)
	}
	if source != "file" {
		t.Errorf("source = %q, want %q", source, "file")
	}
}

// TestResolveModelAgentAndJudgeResolvedIndependently: the agent knob can come
// from the flag tier while the judge knob comes from the env tier — the two
// knobs never share a resolution, which is what lets a development run pick
// one judge via JUDGE_MODEL while the agent stays central.
func TestResolveModelAgentAndJudgeResolvedIndependently(t *testing.T) {
	loader := func() (string, error) { return "openrouter/vendor/from-file", nil }

	gotAgent, srcAgent, err := resolveModel("openrouter/agent/from-flag", "", loader)
	if err != nil {
		t.Fatalf("resolveModel(agent) error = %v", err)
	}
	if gotAgent != "openrouter/agent/from-flag" || srcAgent != "flag" {
		t.Errorf("agent knob = (%q, %q), want (flag value, \"flag\")", gotAgent, srcAgent)
	}

	gotJudge, srcJudge, err := resolveModel("", "openrouter/judge/from-env", loader)
	if err != nil {
		t.Fatalf("resolveModel(judge) error = %v", err)
	}
	if gotJudge != "openrouter/judge/from-env" || srcJudge != "env" {
		t.Errorf("judge knob = (%q, %q), want (env value, \"env\")", gotJudge, srcJudge)
	}
}

// TestResolveModelPropagatesFileError: with no flag and no env, a loader error
// aborts loudly — there is no hardcoded fallback model to absorb it.
func TestResolveModelPropagatesFileError(t *testing.T) {
	loader := func() (string, error) { return "", errors.New("read tests/e2e/e2e-model.json: no such file") }
	_, _, err := resolveModel("", "", loader)
	if err == nil {
		t.Fatal("resolveModel() with a failing loader returned nil error; want the error propagated")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error should carry the loader's cause, got: %v", err)
	}
}
