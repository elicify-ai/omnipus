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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
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

	gw := testutil.StartTestGateway(t)

	// --- Stage 1: onboard (creates admin, stores API key) ---
	onboardBody := map[string]any{
		"provider": map[string]any{
			"auth_method": "api_key",
			"id":          "anthropic",
			"api_key":     injectedAPIKey,
			"model":       "claude-sonnet-4-6",
			"endpoint":    startFakeProviderUpstream(t),
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
	exercise(http.MethodGet, "/api/v1/security/tool-policies", nil)
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
		r, rerr := http.NewRequest(http.MethodPut, gw.URL+"/api/v1/providers/anthropic",
			bytes.NewReader(updateBody))
		require.NoError(t, rerr)
		r.Header.Set("Origin", gw.URL)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(reAuthHeader, rt)
		if rp, derr := gw.HTTPClient.Do(r); derr == nil {
			pbuf := new(bytes.Buffer)
			_, _ = pbuf.ReadFrom(rp.Body)
			collectedResponses = append(collectedResponses, pbuf.Bytes())
			_ = rp.Body.Close()
		}
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
		auditPath := filepath.Join(gw.HomeDir(), "system", "audit.jsonl")
		assertNoLeaksInFile(t, auditPath, scanTokens, gw.Token(), token)
	})

	t.Run("gateway_logs_no_credential_leaks", func(t *testing.T) {
		logDir := filepath.Join(gw.HomeDir(), "logs")
		entries, err := os.ReadDir(logDir)
		if err != nil {
			if os.IsNotExist(err) {
				t.Skip("no $OMNIPUS_HOME/logs directory — nothing to scan")
			}
			require.NoError(t, err)
		}
		scanned := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			assertNoLeaksInFile(t, filepath.Join(logDir, e.Name()),
				scanTokens, gw.Token(), token)
			scanned++
		}
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

	t.Run("audit_entry_for_provider_update_redacts_api_key", func(t *testing.T) {
		// After the provider update above, the audit log (if it recorded the
		// PUT /providers/anthropic call) must redact the api_key. Read the
		// audit log and look for the literal injected key.
		auditPath := filepath.Join(gw.HomeDir(), "system", "audit.jsonl")
		f, err := os.Open(auditPath)
		if err != nil {
			if os.IsNotExist(err) {
				t.Skip("no audit.jsonl written — skipping provider-update redaction check")
			}
			require.NoError(t, err)
		}
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
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Logf("skipping %s (does not exist)", path)
			return
		}
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
