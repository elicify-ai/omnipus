package security_test

// File purpose: credential-leakage scanner for logs and HTTP responses (PR-D Axis-7).
//
// Threat model: credentials stored in credentials.json must never surface in:
//   - $OMNIPUS_HOME/system/audit.jsonl or rotated audit files
//   - $OMNIPUS_HOME/logs/* (gateway.log, panic log, etc.)
//   - any /api/v1/* JSON response body
//
// Strategy: exercise the surface (register providers, submit tool calls, hit
// every list endpoint), then scan every persisted log file + every response
// body we collected during the test. Any byte sequence matching a known
// token prefix (sk-or-v1, sk-ant, ghp_, AIza, AKIA, eyJ, "Bearer <token>",
// or the seeded credential values) is a leak.
//
// Plan reference: temporal-puzzling-melody.md §6 PR-D.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// knownCredentialTokens contains real-shaped credential prefixes + the exact
// values we inject during the test. A leak is ANY occurrence in logs or
// response bodies of one of these (except in the credentials.json source file
// itself, which we never scan).
type leakScanToken struct {
	label string
	value string
	// If literalOnly is true, only an exact substring match counts as a leak.
	// Otherwise, a prefix-style regex-lite match is applied.
	literalOnly bool
}

func buildScanTokens(injectedAPIKey, injectedBearerSentinel string) []leakScanToken {
	return []leakScanToken{
		// Provider-API-key prefixes — any match in a log is a leak.
		{label: "openrouter", value: "sk-or-v1-"},
		{label: "anthropic", value: "sk-ant-"},
		{label: "github_pat", value: "ghp_"},
		{label: "aws_access_key", value: "AKIA"},
		{label: "google_api_key", value: "AIza"},
		// JWT header prefix — often leaked whole
		{label: "jwt_prefix", value: "eyJ"},
		// The exact injected API key we passed to /onboarding.
		{label: "injected_api_key_literal", value: injectedAPIKey, literalOnly: true},
		// The exact sentinel we baked into bearer auth.
		{label: "bearer_sentinel", value: injectedBearerSentinel, literalOnly: true},
	}
}

func TestCredentialLeakage(t *testing.T) {
	// Onboard with a recognizable API key so a literal match inside a log file
	// is unambiguously a leak. We also use a distinctive bearer sentinel so
	// we can match it through request/response churn.
	injectedAPIKey := "sk-ant-api03-" + randSuffix() + "-testleakscan"
	bearerSentinel := "Bearer omnipus_" + randSuffix() + "leakscan"
	// The provider this test onboards and updates; requireKeyFlowAuditEntries
	// matches audit entries by it.
	providerID := "anthropic"

	// Audit logging must be ON for this gateway: the audit subtests below read
	// the real audit log, and the harness default leaves sandbox.audit_log off
	// (the harness config is a bare literal whose Sandbox carries only
	// Mode "off" — pkg/agent/testutil/gateway_harness.go::buildConfig).
	gw := testutil.StartTestGateway(t, auditOnForLeakScan())

	// --- Stage 1: onboard (creates admin, stores API key) ---
	onboardBody := map[string]any{
		"provider": map[string]any{
			"auth_method": "api_key",
			"id":          providerID,
			"api_key":     injectedAPIKey,
			"model":       "claude-sonnet-4-6",
			"endpoint":    startNoCreditProviderUpstream(t),
		},
		"admin": map[string]any{
			"username": "leakadmin",
			"password": "securepass123",
		},
	}
	b, _ := json.Marshal(onboardBody)
	req, err := gw.NewRequest(http.MethodPost, "/api/v1/onboarding/complete",
		bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var onboardResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&onboardResp))
	require.NotEmpty(t, onboardResp.Token)

	token := onboardResp.Token

	// Keep collecting every response body we read so we can scan them later.
	var collectedResponses [][]byte

	// Helper: issue a request using the admin token + Origin, capture the body
	// for later scanning.
	exercise := func(method, path string, body []byte) {
		var rdr *bytes.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		var reqBody *bytes.Reader
		if rdr != nil {
			reqBody = rdr
		}
		var r *http.Request
		if reqBody == nil {
			r, err = http.NewRequest(method, gw.URL+path, nil)
		} else {
			r, err = http.NewRequest(method, gw.URL+path, reqBody)
		}
		require.NoError(t, err)
		r.Header.Set("Origin", gw.URL)
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		rp, err := gw.HTTPClient.Do(r)
		if err != nil {
			return
		}
		defer rp.Body.Close()
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(rp.Body)
		collectedResponses = append(collectedResponses, buf.Bytes())
	}

	// --- Stage 2: exercise every list-style endpoint so audit.jsonl + logs
	// have churn. Include a PUT /config that contains the API key string to
	// see whether it leaks into the audit trail. ---
	exercise(http.MethodGet, "/api/v1/agents", nil)
	exercise(http.MethodGet, "/api/v1/config", nil)
	exercise(http.MethodGet, "/api/v1/credentials", nil)
	exercise(http.MethodGet, "/api/v1/tools", nil)
	exercise(http.MethodGet, "/api/v1/channels", nil)
	exercise(http.MethodGet, "/api/v1/providers", nil)
	exercise(http.MethodGet, "/api/v1/sessions", nil)
	exercise(http.MethodGet, "/api/v1/tasks", nil)
	exercise(http.MethodGet, "/api/v1/audit-log", nil)
	// C-22 row 6: the permissions read model (C-2) replaces the retired global tool-policy read.
	exercise(http.MethodGet, "/api/v1/security/permissions", nil)
	exercise(http.MethodGet, "/api/v1/security/rate-limits", nil)
	exercise(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	// Make a provider update that would route the API key through the
	// handler — any leakage along this path is particularly dangerous.
	updateBody, _ := json.Marshal(map[string]any{
		"api_key": injectedAPIKey,
		"model":   "claude-sonnet-4-6",
	})
	// PUT /providers/{id} is re-auth-gated (Spec-6 FR-12.2): without a single-use
	// consent token the handler 403s BEFORE decoding the body, so the injected
	// key would never traverse the handler and the leakage check on this path
	// would be vacuous. Mint a token (the onboarded leakadmin has a password) and
	// replay it so the key actually flows through the handler under test.
	{
		rt := mintReAuthToken(t, gw.HTTPClient, gw.URL, gw.URL, token, "securepass123")
		r, rerr := http.NewRequest(http.MethodPut, gw.URL+"/api/v1/providers/"+providerID,
			bytes.NewReader(updateBody))
		require.NoError(t, rerr)
		r.Header.Set("Origin", gw.URL)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(reAuthHeader, rt)
		rp, derr := gw.HTTPClient.Do(r)
		require.NoError(t, derr)
		pbuf := new(bytes.Buffer)
		_, _ = pbuf.ReadFrom(rp.Body)
		_ = rp.Body.Close()
		collectedResponses = append(collectedResponses, pbuf.Bytes())
		require.Equal(t, http.StatusOK, rp.StatusCode,
			"the provider update must succeed, or the audit checks below cover no update; body=%s", pbuf.String())
	}
	// Churn via a deliberately-rejected auth request carrying the bearer
	// sentinel — if anything echoes the Authorization header verbatim, this
	// catches it.
	r, _ := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/agents", nil)
	r.Header.Set("Authorization", bearerSentinel)
	r.Header.Set("Origin", gw.URL)
	if resp2, err := gw.HTTPClient.Do(r); err == nil {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp2.Body)
		collectedResponses = append(collectedResponses, buf.Bytes())
		_ = resp2.Body.Close()
	}

	// --- Stage 3: scan logs + response bodies for leaks ---
	scanTokens := buildScanTokens(injectedAPIKey, bearerSentinel)

	t.Run("audit_log_no_credential_leaks", func(t *testing.T) {
		auditPath := auditLogPathForTest(gw)
		requireKeyFlowAuditEntries(t, auditPath, providerID)
		assertNoLeaksInFile(t, auditPath, scanTokens, gw.Token(), token)
	})

	t.Run("gateway_logs_no_credential_leaks", func(t *testing.T) {
		logDir := filepath.Join(gw.HomeDir(), "logs")
		// A missing logs directory fails: the datamodel boot creates it
		// (pkg/datamodel/init.go omnipusDirs), so absence means something is
		// broken, not that there is nothing to scan.
		entries, err := os.ReadDir(logDir)
		require.NoError(t, err,
			"$OMNIPUS_HOME/logs must exist — the datamodel boot creates it; a leak scan of no directory proves nothing")
		scanned := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := filepath.Join(logDir, e.Name())
			if _, statErr := os.Stat(p); errors.Is(statErr, os.ErrNotExist) {
				continue // rotated away after ReadDir
			}
			assertNoLeaksInFile(t, p, scanTokens, gw.Token(), token)
			scanned++
		}
		// Empty set = silent pass on nothing. At least one log file must have
		// been scanned for this subtest to mean anything.
		require.NotZero(t, scanned, "no log files found in %s — the leak scan covered nothing", logDir)
		t.Logf("scanned %d log files", scanned)
	})

	t.Run("http_response_bodies_no_credential_leaks", func(t *testing.T) {
		for i, body := range collectedResponses {
			for _, tok := range scanTokens {
				if tok.label == "injected_api_key_literal" {
					assert.False(t, bytes.Contains(body, []byte(tok.value)),
						"HTTP response #%d leaked injected API key literal", i)
				}
				if tok.label == "bearer_sentinel" {
					assert.False(t, bytes.Contains(body, []byte(tok.value)),
						"HTTP response #%d leaked bearer sentinel", i)
				}
			}
		}
	})

	t.Run("audit_entry_for_provider_update_has_no_api_key", func(t *testing.T) {
		// The provider update above must have written its audit entry, and
		// that entry must not hold the literal injected api_key. A missing log
		// or a missing entry fails (requireKeyFlowAuditEntries): skipping would
		// prove nothing. Despite the historical name, this asserts the key is
		// ABSENT from the entry — not that a redactor ran: the audited flows
		// carry no api_key field at all (the write sites stamp only provider,
		// outcome, action — plus source on onboarding). The redactor itself is
		// unit-tested in pkg/audit. The update entries are matched by their
		// own outcome field, never by position or by the first matching entry.
		flows := requireKeyFlowAuditEntries(t, auditLogPathForTest(gw), providerID)
		for _, entry := range flows.update {
			assert.NotContains(t, entry.line, injectedAPIKey,
				"the provider-update audit entry carries the literal API key: %s",
				truncate(entry.line, 200))
			assert.NotContains(t, entry.details, "api_key",
				"the provider-update audit entry carries an api_key field: %s",
				truncate(entry.line, 200))
		}
		// Whole-log sweep at unchanged strength: no line of the audit log may
		// carry the literal key, whatever flow wrote it.
		f, err := os.Open(auditLogPathForTest(gw))
		require.NoError(t, err, "audit log must exist")
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 256*1024), 4*1024*1024)
		leakedIn := 0
		var leakedLine string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, injectedAPIKey) {
				leakedIn++
				if leakedLine == "" {
					leakedLine = line
				}
			}
		}
		require.NoError(t, scanner.Err())
		assert.Equal(t, 0, leakedIn,
			"audit.jsonl must not contain the literal provider API key; "+
				"first offending line (truncated): %s",
			truncate(leakedLine, 200))
	})
}

// auditOnForLeakScan returns the sandbox config option for THIS test's
// gateway: the harness default restated (sandbox off) plus audit logging on,
// so the audit subtests below scan a real log. The harness config is a bare
// literal, so its Sandbox carries only Mode "off" and AuditLog stays the zero
// value (pkg/agent/testutil/gateway_harness.go::buildConfig); with a zero
// AuditLog the loop never builds the audit logger
// (pkg/agent/loop_construct.go::initializeAudit) and no log is written.
// WithSandboxConfig replaces the whole sandbox block, so the option restates
// what the default carried. AllowInternal admits the loopback stand-in
// provider: PUT /providers/{id} SSRF-checks the persisted api_base before
// probing and refuses loopback otherwise
// (pkg/gateway/rest_providers.go::providerPutValidateKey), and an unallowed
// onboarding probe is skipped rather than run
// (pkg/gateway/rest_onboarding.go::validateAndStoreOnboardingKey).
func auditOnForLeakScan() testutil.Option {
	return testutil.WithSandboxConfig(config.OmnipusSandboxConfig{
		Mode:     "off",
		AuditLog: true,
		SSRF:     config.OmnipusSSRFConfig{AllowInternal: []string{"127.0.0.1"}},
	})
}

// startNoCreditProviderUpstream starts a stand-in provider whose key probe
// answers 402: the key authenticates but the account has no credit.
// pkg/providers/validate.go::probeCompletion probes POST {base}/chat/completions
// (validate.go:897-898) for every non-Bedrock provider — that is the one route
// BOTH audited flows hit: onboarding
// (pkg/gateway/rest_onboarding.go::validateAndStoreOnboardingKey → ValidateKey)
// and PUT /providers/{id}
// (pkg/gateway/rest_providers.go::providerPutValidateKey → ValidateKey). A
// catalog provider runs NO /models pre-fetch first (FR-022,
// pkg/providers/validate.go:712-723), so anything else arriving here is not
// the probe and gets a 404 — which validates to unreachable and fails the
// outcome assertion in requireKeyFlowAuditEntries. pkg/providers/validate.go::
// classify maps status 402 to OutcomeNoCredit; both flows proceed on that
// warning outcome (ValidationResult.Blocks is true only for invalid_key) and
// each write a provider_key_validated audit entry
// (pkg/gateway/rest_onboarding.go::validateAndStoreOnboardingKey,
// pkg/gateway/rest_providers.go::providerPutRespond). A probe that answers
// 200 classifies the key valid and writes NO entry, which would leave the
// audit checks here nothing to require — hence 402, not 200.
func startNoCreditProviderUpstream(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"insufficient credit"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// auditLogPathForTest is where the gateway under test writes its audit log.
// The loop derives its home from the config's agent home base —
// homePath = filepath.Dir(cfg.AgentHomeBasePath())
// (pkg/agent/loop_construct.go::newAgentLoop) — and builds the audit logger
// on filepath.Join(homePath, "system") when Sandbox.AuditLog is on
// (pkg/agent/loop_construct.go::initializeAudit); the file itself is the
// audit package's "audit.jsonl" literal. The harness sets
// Agents.Defaults.Home to the gateway home itself
// (pkg/agent/testutil/gateway_harness.go::buildConfig), so here the log
// lands one directory ABOVE gw.HomeDir() — not under it, where this test
// used to read and found nothing.
func auditLogPathForTest(gw *testutil.TestGateway) string {
	return filepath.Join(filepath.Dir(gw.HomeDir()), "system", "audit.jsonl")
}

// providerKeyValidatedEntry is one provider_key_validated line of the audit
// log: the raw line (for literal leak checks) and its details map.
type providerKeyValidatedEntry struct {
	line    string
	details map[string]any
}

// keyFlowAuditEntries holds the provider_key_validated entries of the two
// flows that handled the injected key, separated by flow.
type keyFlowAuditEntries struct {
	onboarding []providerKeyValidatedEntry
	update     []providerKeyValidatedEntry
}

// requireKeyFlowAuditEntries fails unless the audit log at path holds a
// provider_key_validated entry for providerID from each flow that handled
// the injected key: onboarding (details.source "onboarding") and the
// PUT /providers/{id} update. The update entries are matched by their own
// outcome field — details.outcome set, no details.source — never by position
// or by the first matching entry: onboarding stamps source "onboarding"
// (pkg/gateway/rest_onboarding.go::validateAndStoreOnboardingKey), the
// provider update writes outcome without a source
// (pkg/gateway/rest_providers.go::providerPutRespond). Every entry of either
// flow must also record details.outcome "no_credit" — the 402 stand-in's
// classification (pkg/providers/validate.go::classify: 402 → OutcomeNoCredit,
// 404 → OutcomeUnreachable) — so a stand-in answering the wrong route fails
// here even though entries still get written. A missing log, a
// malformed line, or a missing entry FAILS here — an audit-leak scan of
// nothing proves nothing, so it is never skipped.
func requireKeyFlowAuditEntries(t *testing.T, path, providerID string) keyFlowAuditEntries {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err,
		"audit log must exist at %s — audit logging is on for this gateway and both key flows must have audited", path)
	var flows keyFlowAuditEntries
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			Event   string         `json:"event"`
			Details map[string]any `json:"details"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry),
			"audit log %s holds a line that is not a JSON audit entry — the log is machine-written JSONL", path)
		if entry.Event != "provider_key_validated" || entry.Details["provider"] != providerID {
			continue
		}
		e := providerKeyValidatedEntry{line: line, details: entry.Details}
		source, hasSource := entry.Details["source"].(string)
		outcome, _ := entry.Details["outcome"].(string)
		// Both key-flow entries must record no_credit: the stand-in answers
		// 402 on the one probe route (startNoCreditProviderUpstream), and
		// pkg/providers/validate.go::classify maps status 402 to
		// OutcomeNoCredit (404 would classify as unreachable). Asserting the
		// outcome is what pins the stand-in to the real probe route — entries
		// would still be written if the probe hit a 404, only with the wrong
		// outcome, and this require fails on that.
		if (hasSource && source == "onboarding") || (!hasSource && outcome != "") {
			require.Equal(t, "no_credit", outcome,
				"provider_key_validated entry for %s must record the no_credit outcome the 402 stand-in produces (pkg/providers/validate.go::classify); got %q in line: %s",
				providerID, outcome, truncate(line, 200))
		}
		switch {
		case hasSource && source == "onboarding":
			flows.onboarding = append(flows.onboarding, e)
		case !hasSource:
			// The provider-update entry, matched by its outcome field.
			if outcome != "" {
				flows.update = append(flows.update, e)
			}
		}
	}
	require.NotEmpty(t, flows.onboarding,
		"no provider_key_validated entry from onboarding (details.source=onboarding) in %s", path)
	require.NotEmpty(t, flows.update,
		"no provider_key_validated entry from the provider update (details.outcome set, no source) in %s", path)
	return flows
}

// assertNoLeaksInFile scans the named file (line-by-line using bufio.Scanner
// so we never load the whole thing into memory) for any occurrence of the
// scanTokens that should not appear. excludeTokens is the set of legitimate
// bearer tokens we injected ourselves — these will necessarily appear in
// logs, and the test should not flag them as leaks. The function produces
// test-level assertion failures via t.Errorf.
func assertNoLeaksInFile(t *testing.T, path string, tokens []leakScanToken,
	excludeTokens ...string,
) {
	t.Helper()
	// A missing file FAILS: a scan of nothing proves nothing.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	// Use Scanner with a large buffer to handle long JSONL lines.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		for _, tok := range tokens {
			if !strings.Contains(line, tok.value) {
				continue
			}
			// Ignore expected self-inclusion of legitimate test bearer tokens.
			skipThisHit := false
			for _, e := range excludeTokens {
				if e != "" && tok.literalOnly && tok.value == e {
					skipThisHit = true
					break
				}
			}
			if skipThisHit {
				continue
			}
			// Prefix-style tokens (sk-or-v1-, etc.) are only a leak if the
			// line actually looks like a real token — i.e., the prefix is
			// followed by one or more alphanumeric/dash characters. This
			// avoids false positives from prose like "token prefix sk-or-".
			if !tok.literalOnly {
				idx := strings.Index(line, tok.value)
				after := ""
				if idx >= 0 && idx+len(tok.value) < len(line) {
					after = line[idx+len(tok.value):]
				}
				// If the next char isn't alphanumeric or dash, treat as prose.
				if after == "" {
					continue
				}
				c := after[0]
				isTokenChar := (c >= 'a' && c <= 'z') ||
					(c >= 'A' && c <= 'Z') ||
					(c >= '0' && c <= '9') || c == '-' || c == '_'
				if !isTokenChar {
					continue
				}
			}
			t.Errorf("credential leak in %s:%d — token=%s (label=%s); line: %s",
				filepath.Base(path), lineNo, tok.value, tok.label,
				truncate(line, 300))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
}
