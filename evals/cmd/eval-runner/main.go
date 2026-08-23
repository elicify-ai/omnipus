// eval-runner is the Omnipus LLM-as-judge eval harness.
// It discovers YAML scenario files, boots a fresh omnipus gateway per
// scenario, drives it through scripted user turns, calls the judge model
// to score the transcript, and writes JSONL results plus a Markdown report.
//
// Build: CGO_ENABLED=0 go build ./evals/cmd/eval-runner/
//
// Usage:
//
//	eval-runner [flags]
//
// See --help for all flags.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/evals/judge"
)

// ── Scenario schema ──────────────────────────────────────────────────────────

// Scenario is the parsed form of a YAML eval scenario file.
type Scenario struct {
	// ID is the primary key, e.g. "persona.mia-greets".
	ID string `yaml:"id"`
	// Category is one of: persona, capability, safety.
	Category string `yaml:"category"`
	// AgentID is the agent to converse with, e.g. "mia".
	AgentID string `yaml:"agent_id"`
	// Prompts is the ordered list of user turns.
	// Supports both prompt (singular) and prompts (list) fields.
	Prompts []string `yaml:"-"`
	// PromptSingular is populated when the YAML file uses `prompt: <string>`.
	PromptSingular string `yaml:"prompt"`
	// PromptList is populated when the YAML file uses `prompts: [...]`.
	PromptList []string `yaml:"prompts"`
	// ExpectedTools is the list of tool names the agent SHOULD call.
	ExpectedTools []string `yaml:"expected_tools"`
	// ForbiddenTools is the list of tool names the agent MUST NOT call.
	ForbiddenTools []string `yaml:"forbidden_tools"`
	// MaxTurns is the hard cap on conversational turns.
	MaxTurns int `yaml:"max_turns"`
	// Rubric is the free-text evaluation rubric passed to the judge.
	Rubric string `yaml:"rubric"`
	// AgentRole is an optional one-line description of the agent's role.
	// If absent, the runner uses AgentID as a fallback.
	AgentRole string `yaml:"agent_role"`
}

// validate checks the scenario for required fields and resolves Prompts.
func (s *Scenario) validate() error {
	if s.ID == "" {
		return fmt.Errorf("scenario missing required field 'id'")
	}
	if s.AgentID == "" {
		return fmt.Errorf("scenario %q missing required field 'agent_id'", s.ID)
	}
	// Resolve prompt vs prompts.
	hasSingular := s.PromptSingular != ""
	hasList := len(s.PromptList) > 0
	if hasSingular && hasList {
		return fmt.Errorf("scenario %q has both 'prompt' and 'prompts' — use one", s.ID)
	}
	if !hasSingular && !hasList {
		return fmt.Errorf("scenario %q has neither 'prompt' nor 'prompts'", s.ID)
	}
	if hasSingular {
		s.Prompts = []string{s.PromptSingular}
	} else {
		s.Prompts = s.PromptList
	}
	if s.MaxTurns <= 0 {
		s.MaxTurns = len(s.Prompts)
	}
	if s.AgentRole == "" {
		s.AgentRole = s.AgentID
	}
	return nil
}

// ── Transcript / result types ─────────────────────────────────────────────────

// TranscriptEntry is one turn in the conversation (user or assistant) or a
// tool call observation.
type TranscriptEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolName is non-empty for tool_call roles.
	ToolName string `json:"tool_name,omitempty"`
}

// TokenUsage records token counts for the run.
type TokenUsage struct {
	Agent int `json:"agent"`
	Judge int `json:"judge"`
}

// EvalResult is one JSONL line in the results file.
type EvalResult struct {
	ScenarioID        string       `json:"scenario_id"`
	TS                time.Time    `json:"ts"`
	AgentID           string       `json:"agent_id"`
	Category          string       `json:"category"`
	Scores            judge.Scores `json:"scores"`
	TranscriptExcerpt string       `json:"transcript_excerpt"`
	AgentModel        string       `json:"agent_model"`
	JudgeModel        string       `json:"judge_model"`
	TokenUsage        TokenUsage   `json:"token_usage"`
	CostUSD           float64      `json:"cost_usd"`
	// Error is non-empty when the scenario run failed without a score.
	Error string `json:"error,omitempty"`
}

// ── Config ────────────────────────────────────────────────────────────────────

type cfg struct {
	scenariosDir        string
	outPath             string
	reportPath          string
	agentModel          string
	judgeModel          string
	timeout             time.Duration
	dryRun              bool
	omnipusBin          string
	allowEmptyScenarios bool
}

func parseFlags() cfg {
	today := time.Now().UTC().Format("2006-01-02")
	c := cfg{}
	flag.StringVar(&c.scenariosDir, "scenarios", "evals/scenarios", "directory to walk for *.yaml scenario files")
	flag.StringVar(&c.outPath, "out", filepath.Join("evals", "results", today+".jsonl"), "JSONL output path")
	flag.StringVar(&c.reportPath, "report", filepath.Join("evals", "REPORT.md"), "Markdown report output path")
	flag.StringVar(
		&c.agentModel, "agent-model",
		envOrDefault("AGENT_MODEL", "openrouter/z-ai/glm-5-turbo"),
		"model for agent responses",
	)
	flag.StringVar(
		&c.judgeModel, "judge-model",
		envOrDefault("JUDGE_MODEL", "openrouter/anthropic/claude-sonnet-4.6"),
		"model for judge scoring",
	)
	flag.DurationVar(&c.timeout, "timeout", 5*time.Minute, "per-scenario hard cap")
	flag.BoolVar(&c.dryRun, "dry-run", false, "skip judge call, just collect transcripts")
	flag.StringVar(
		&c.omnipusBin, "omnipus-bin",
		envOrDefault("OMNIPUS_BIN", "./omnipus"),
		"path to compiled omnipus binary",
	)
	flag.BoolVar(
		&c.allowEmptyScenarios,
		"allow-empty-scenarios",
		false,
		"exit 0 (instead of 2) when no scenario files are found",
	)
	flag.Parse()
	return c
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── Scenario discovery ────────────────────────────────────────────────────────

func discoverScenarios(root string) ([]Scenario, error) {
	var scenarios []Scenario
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("eval: skipping unreadable scenario file", "path", path, "error", err)
			return nil
		}
		var s Scenario
		if err := yaml.Unmarshal(data, &s); err != nil {
			slog.Warn("eval: skipping malformed scenario file", "path", path, "error", err)
			return nil
		}
		if err := s.validate(); err != nil {
			slog.Warn("eval: skipping invalid scenario", "path", path, "error", err)
			return nil
		}
		scenarios = append(scenarios, s)
		return nil
	})
	return scenarios, err
}

// ── Gateway lifecycle ─────────────────────────────────────────────────────────

// gatewayHandle wraps a running omnipus process and its home directory.
type gatewayHandle struct {
	homeDir string
	baseURL string
	cmd     *exec.Cmd
	// waited receives the single cmd.Wait() result. startGateway reaps the
	// process in a goroutine so it can detect a boot abort while polling, so
	// nothing else may call cmd.Wait() — kill() drains this channel instead.
	waited    <-chan error
	token     string // bearer token obtained after onboarding
	csrfToken string // CSRF token extracted from Set-Cookie on state-changing responses
}

// gatewayBootTimeout bounds each phase of gateway startup (port file, then
// /health). It is a backstop for a genuinely wedged process — a healthy
// gateway binds in a couple of seconds and a misconfigured one now fails fast
// via the process-death check, so reaching this timeout is itself a finding.
const gatewayBootTimeout = 60 * time.Second

// csrfCookieName is the cookie name used by the Omnipus CSRF middleware.
const csrfCookieName = "__Host-csrf"

// extractCSRF scans resp.Cookies() for the CSRF cookie and stores it.
func (h *gatewayHandle) extractCSRF(resp *http.Response) {
	for _, c := range resp.Cookies() {
		if c.Name == csrfCookieName {
			h.csrfToken = c.Value
			return
		}
	}
}

// doStatefulPost performs a POST to path with the given body, attaching the
// CSRF cookie and X-Csrf-Token header so the CSRF middleware does not reject
// the request with 403. It also sets the Authorization header from h.token
// when a token is available.
func (h *gatewayHandle) doStatefulPost(path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.csrfToken != "" {
		req.Header.Set("X-Csrf-Token", h.csrfToken)
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: h.csrfToken})
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	return http.DefaultClient.Do(req)
}

// pickFreePort asks the kernel for an unused TCP port on the loopback
// interface and returns it, releasing the listener immediately.
//
// The gateway does NOT support `gateway.port: 0` as an "bind an ephemeral
// port" instruction — config validation rejects 0 outright ("gateway.port 0
// is out of range [1, 65535]"), and on builds without that validation the
// gateway echoes the *configured* value into gateway.port, so a 0 there means
// "the operator literally configured 0", not "here is the port I bound".
// The runner therefore has to choose a concrete port itself and hand it to
// the gateway. The window between closing this listener and the gateway
// binding is a benign TOCTOU race — a collision surfaces as a loud bind
// failure from the gateway, not as a silent hang.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve free port: %w", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		l.Close() // errcheck rationale (out of errcheck scope; kept as documentation): closing a listener we are abandoning on an impossible-type path; the returned error adds nothing to the one below
		return 0, fmt.Errorf("reserved listener has address type %T, want *net.TCPAddr", l.Addr())
	}
	port := addr.Port
	if err := l.Close(); err != nil {
		return 0, fmt.Errorf("release reserved port %d: %w", port, err)
	}
	return port, nil
}

// seedConfig writes a minimal config.json into homeDir, binding the gateway to
// the given concrete port. The provider entry is a placeholder — the real API
// key is passed during onboarding/complete.
//
// The `"version": 1` field is REQUIRED and must not be dropped. A config.json
// with no version field is treated as a pre-v1 (v0) config: the loader runs
// the v0→v1 migration, which unmarshals the file into the v0 schema and fails
// on any field whose shape differs (this is what silently killed every nightly
// eval run — the old seed's `"model_list": {}` is an object, while v0 declares
// model_list as an array). Newer builds reject a missing version outright.
func seedConfig(homeDir string, port int) error {
	// Write a bare config that lets the gateway start. The provider entry and
	// model are set via onboarding/complete, so no model keys are seeded here.
	cfgContent := `{
  "version": 1,
  "gateway": {
    "host": "127.0.0.1",
    "port": ` + strconv.Itoa(port) + `,
    "allow_empty": true,
    "dev_mode_bypass": true
  },
  "agents": {
    "defaults": {
      "workspace": "` + homeDir + `"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(homeDir, "config.json"), []byte(cfgContent), 0o600); err != nil {
		return fmt.Errorf("seed config.json: %w", err)
	}
	return nil
}

// bootFailureDetail returns a short diagnostic drawn from the gateway's own
// startup log, for use when the gateway dies before it is reachable.
//
// The gateway writes fatal startup errors (bad config, bind failure, credential
// unlock failure) to $OMNIPUS_HOME/logs/gateway_panic.log and NOT to the stderr
// it inherited from us. Without this, a hard boot abort is indistinguishable
// from a hang: the runner just times out with "gateway did not write port
// file", which is what left the nightly evals undiagnosed for three months.
func bootFailureDetail(homeDir string) string {
	data, err := os.ReadFile(filepath.Join(homeDir, "logs", "gateway_panic.log"))
	if err != nil {
		return fmt.Sprintf("(no gateway_panic.log: %v)", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// The fatal line is last; keep a little leading context.
	if len(lines) > bootLogTailLines {
		lines = lines[len(lines)-bootLogTailLines:]
	}
	return strings.Join(lines, " | ")
}

// bootLogTailLines is how many trailing gateway_panic.log lines are quoted
// into a boot-failure error.
const bootLogTailLines = 6

// startGateway starts the omnipus binary with the given home directory on the
// given port, waits for /health to respond, and returns a gatewayHandle.
//
// port must be the same concrete port that seedConfig wrote into config.json.
func startGateway(ctx context.Context, omnipusBin, homeDir string, port int) (*gatewayHandle, error) {
	cmd := exec.CommandContext(ctx, omnipusBin, "gateway", "--allow-empty")
	cmd.Env = append(os.Environ(),
		"OMNIPUS_HOME="+homeDir,
		"OMNIPUS_BEARER_TOKEN=",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gateway: %w", err)
	}

	// The gateway writes gateway.port once its listener is up, so the file is
	// still the readiness signal — but it echoes the CONFIGURED port, so it can
	// only ever confirm the port we asked for, never discover one.
	//
	// Poll for the file while simultaneously watching for process death. A
	// gateway that aborts at boot exits within a second or two; without the
	// liveness check we would sit here for the full timeout and then report a
	// misleading "never wrote its port file" instead of the actual cause.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	portFile := filepath.Join(homeDir, "gateway.port")
	wantPort := strconv.Itoa(port)
	var got string
	deadline := time.Now().Add(gatewayBootTimeout)
	for got == "" && time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
			got = string(bytes.TrimSpace(data))
			break
		}
		select {
		case werr := <-waited:
			return nil, fmt.Errorf(
				"gateway exited during startup before binding port %d: %s: %w",
				port, bootFailureDetail(homeDir), werr)
		case <-time.After(200 * time.Millisecond):
		}
	}
	if got == "" {
		cmd.Process.Kill() // errcheck rationale (out of errcheck scope; kept as documentation): killing a test subprocess; ErrProcessDone if it already exited is expected and harmless
		return nil, fmt.Errorf(
			"gateway still running but did not write %s within %s: %s",
			portFile, gatewayBootTimeout, bootFailureDetail(homeDir))
	}
	if got != wantPort {
		cmd.Process.Kill() // errcheck rationale (out of errcheck scope; kept as documentation): killing a test subprocess; ErrProcessDone if it already exited is expected and harmless
		return nil, fmt.Errorf("gateway bound port %s but config requested %s", got, wantPort)
	}

	baseURL := "http://127.0.0.1:" + wantPort

	// Wait for /health.
	healthURL := baseURL + "/health"
	deadline = time.Now().Add(gatewayBootTimeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL) //nolint:noctx
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			break
		}
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Final check.
	resp, err := http.Get(healthURL) //nolint:noctx
	if err != nil {
		cmd.Process.Kill() // errcheck rationale (out of errcheck scope; kept as documentation): killing a test subprocess; ErrProcessDone if it already exited is expected and harmless
		return nil, fmt.Errorf("gateway /health never responded: %w: %s", err, bootFailureDetail(homeDir))
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cmd.Process.Kill() // errcheck rationale (out of errcheck scope; kept as documentation): killing a test subprocess; ErrProcessDone if it already exited is expected and harmless
		return nil, fmt.Errorf("gateway /health returned %d", resp.StatusCode)
	}

	return &gatewayHandle{
		homeDir: homeDir,
		baseURL: baseURL,
		cmd:     cmd,
		waited:  waited,
	}, nil
}

// onboard completes onboarding against the running gateway, setting up the
// provider entry with the real API key and creating the eval admin account.
// It stores the returned bearer token in h.token.
func (h *gatewayHandle) onboard(agentModel string) error {
	apiKey := os.Getenv("OPENROUTER_API_KEY_EVAL")
	if apiKey == "" {
		return fmt.Errorf("OPENROUTER_API_KEY_EVAL is not set")
	}

	// The provider ID is always "openrouter"; model is the full path after the prefix.
	providerID := "openrouter"
	modelPath := agentModel
	if parts := strings.SplitN(agentModel, "/", 2); len(parts) == 2 {
		providerID = parts[0]
		modelPath = parts[1]
	}

	body := map[string]any{
		"provider": map[string]any{
			"id":      providerID,
			"api_key": apiKey,
			"model":   modelPath,
		},
		"admin": map[string]any{
			"username": "eval-admin",
			"password": "eval-pass-" + time.Now().Format("20060102"),
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal onboard body: %w", err)
	}

	resp, err := h.doStatefulPost("/api/v1/onboarding/complete", bodyBytes)
	if err != nil {
		return fmt.Errorf("onboard POST: %w", err)
	}
	defer resp.Body.Close()
	h.extractCSRF(resp)
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("onboard returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Extract the token from the response.
	var onboardResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &onboardResp); err == nil && onboardResp.Token != "" {
		h.token = onboardResp.Token
	}
	// If no token in onboard response, login separately.
	if h.token == "" {
		tok, err := h.login("eval-admin", "eval-pass-"+time.Now().Format("20060102"))
		if err != nil {
			return fmt.Errorf("login after onboard: %w", err)
		}
		h.token = tok
	}
	return nil
}

// login authenticates and returns a bearer token.
func (h *gatewayHandle) login(username, password string) (string, error) {
	body := map[string]string{"username": username, "password": password}
	bodyBytes, _ := json.Marshal(body)
	resp, err := h.doStatefulPost("/api/v1/auth/login", bodyBytes)
	if err != nil {
		return "", fmt.Errorf("login POST: %w", err)
	}
	defer resp.Body.Close()
	h.extractCSRF(resp)
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &lr); err != nil {
		return "", fmt.Errorf("parse login response: %w", err)
	}
	if lr.Token == "" {
		return "", fmt.Errorf("login response contained no token")
	}
	return lr.Token, nil
}

// kill terminates the gateway process and removes the home directory.
func (h *gatewayHandle) kill() {
	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Kill() // errcheck rationale (out of errcheck scope; kept as documentation): killing a test subprocess; ErrProcessDone if it already exited is expected and harmless
		// Reap via the channel startGateway's goroutine owns — calling
		// cmd.Wait() here as well would race it and return "Wait was already
		// called". The exit error is expected (we just killed it) and not asserted.
		if h.waited != nil {
			select {
			case <-h.waited:
			case <-time.After(5 * time.Second):
			}
		}
	}
	os.RemoveAll(h.homeDir) // errcheck rationale (out of errcheck scope; kept as documentation): test cleanup of a temp home dir; failure here does not affect the test's assertions
}

// ── Turn-by-turn conversation ─────────────────────────────────────────────────

// postMessage sends one user message to the gateway and collects the assistant
// reply by polling the session messages endpoint until a new assistant message
// appears after the given afterIdx.
func (h *gatewayHandle) postMessage(
	ctx context.Context,
	sessionID string,
	userText string,
	afterIdx int,
	turnTimeout time.Duration,
) ([]TranscriptEntry, error) {
	// POST the user message to the session.
	msgBody := map[string]any{
		"content": userText,
	}
	bodyBytes, _ := json.Marshal(msgBody)
	resp, err := h.doStatefulPost("/api/v1/sessions/"+sessionID+"/messages", bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("post message: %w", err)
	}
	defer resp.Body.Close()
	h.extractCSRF(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("post message returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Poll for the assistant response.
	deadline := time.Now().Add(turnTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		time.Sleep(500 * time.Millisecond)

		entries, err := h.fetchMessages(ctx, sessionID)
		if err != nil {
			slog.Warn("eval: poll messages error", "session", sessionID, "error", err)
			continue
		}
		// Check if we have a new assistant message beyond afterIdx.
		if len(entries) > afterIdx {
			for i := afterIdx; i < len(entries); i++ {
				if entries[i].Role == "assistant" {
					// Return all entries from afterIdx onward.
					return entries[afterIdx:], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("turn timeout after %s waiting for assistant response", turnTimeout)
}

// fetchMessages retrieves all messages for a session.
func (h *gatewayHandle) fetchMessages(ctx context.Context, sessionID string) ([]TranscriptEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.baseURL+"/api/v1/sessions/"+sessionID+"/messages", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch messages %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// The API returns an array of message objects.
	var msgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("decode messages: %w", err)
	}

	entries := make([]TranscriptEntry, 0, len(msgs))
	for _, m := range msgs {
		entries = append(entries, TranscriptEntry{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return entries, nil
}

// createSession creates a new chat session for the given agent and returns
// the session ID.
func (h *gatewayHandle) createSession(ctx context.Context, agentID string) (string, error) {
	body := map[string]string{"agent_id": agentID}
	bodyBytes, _ := json.Marshal(body)
	// doStatefulPost does not accept a context; use a plain request so we can
	// attach the context for the session-creation call.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.baseURL+"/api/v1/sessions",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.token)
	if h.csrfToken != "" {
		req.Header.Set("X-Csrf-Token", h.csrfToken)
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: h.csrfToken})
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()
	h.extractCSRF(resp)
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create session %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var sr struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &sr); err != nil || sr.ID == "" {
		return "", fmt.Errorf("parse create session response: %s", string(respBody))
	}
	return sr.ID, nil
}

// ── Judge call ────────────────────────────────────────────────────────────────

// openRouterRequest is the OpenRouter-compatible chat completion request.
type openRouterRequest struct {
	Model    string              `json:"model"`
	Messages []openRouterMessage `json:"messages"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openRouterResponse captures just the fields we need.
type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// callJudge sends the rendered prompt to the judge model via OpenRouter and
// returns the raw text response along with token counts.
func callJudge(ctx context.Context, model, prompt string) (string, int, error) {
	apiKey := os.Getenv("OPENROUTER_API_KEY_EVAL")
	if apiKey == "" {
		return "", 0, fmt.Errorf("OPENROUTER_API_KEY_EVAL is not set")
	}

	// Strip "openrouter/" prefix if present — the OpenRouter API uses the
	// bare model path (e.g. "anthropic/claude-sonnet-4.6").
	orModel := model
	if parts := strings.SplitN(model, "/", 2); len(parts) == 2 && parts[0] == "openrouter" {
		orModel = parts[1]
	}

	reqBody := openRouterRequest{
		Model: orModel,
		Messages: []openRouterMessage{
			{Role: "user", Content: prompt},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("marshal judge request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://openrouter.ai/api/v1/chat/completions",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", 0, fmt.Errorf("build judge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Http-Referer", "https://omnipus.ai")
	req.Header.Set("X-Title", "omnipus-evals")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("judge HTTP call: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("read judge response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("judge returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(respBytes, &orResp); err != nil {
		return "", 0, fmt.Errorf("parse judge response: %w", err)
	}
	if len(orResp.Choices) == 0 || orResp.Choices[0].Message.Content == "" {
		return "", 0, fmt.Errorf("judge returned empty choices")
	}
	totalTokens := orResp.Usage.PromptTokens + orResp.Usage.CompletionTokens
	return orResp.Choices[0].Message.Content, totalTokens, nil
}

// estimateCostUSD returns a rough cost estimate. OpenRouter pricing varies;
// we use a conservative $2.00/M tokens for the judge and $0.20/M for the agent.
func estimateCostUSD(agentTokens, judgeTokens int) float64 {
	return float64(agentTokens)/1_000_000*0.20 + float64(judgeTokens)/1_000_000*2.00
}

// transcriptExcerpt returns the first 500 chars of the transcript as a string.
func transcriptExcerpt(entries []TranscriptEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		line := fmt.Sprintf("[%s] %s\n", e.Role, e.Content)
		if sb.Len()+len(line) > 500 {
			sb.WriteString("...")
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}

// ── Scenario runner ───────────────────────────────────────────────────────────

// runScenario drives one scenario to completion and returns an EvalResult.
// Errors within the scenario are recorded in the result (not returned) so the
// outer loop can continue to the next scenario.
func runScenario(
	ctx context.Context,
	s Scenario,
	c cfg,
	outFile *os.File,
) EvalResult {
	result := EvalResult{
		ScenarioID: s.ID,
		TS:         time.Now().UTC(),
		AgentID:    s.AgentID,
		Category:   s.Category,
		AgentModel: c.agentModel,
		JudgeModel: c.judgeModel,
	}

	// Create per-scenario temp home.
	homeDir, err := os.MkdirTemp("", "omnipus-eval-*")
	if err != nil {
		result.Error = fmt.Sprintf("mkdirtemp: %v", err)
		return result
	}

	// The gateway cannot pick its own port (see pickFreePort), so reserve one
	// here and seed it into the config the gateway is about to read.
	var port int
	port, err = pickFreePort()
	if err != nil {
		os.RemoveAll(homeDir)
		result.Error = fmt.Sprintf("pick port: %v", err)
		return result
	}

	err = seedConfig(homeDir, port)
	if err != nil {
		os.RemoveAll(homeDir)
		result.Error = fmt.Sprintf("seed config: %v", err)
		return result
	}

	var gw *gatewayHandle
	gw, err = startGateway(ctx, c.omnipusBin, homeDir, port)
	if err != nil {
		os.RemoveAll(homeDir)
		result.Error = fmt.Sprintf("start gateway: %v", err)
		return result
	}
	defer gw.kill()

	err = gw.onboard(c.agentModel)
	if err != nil {
		result.Error = fmt.Sprintf("onboard: %v", err)
		return result
	}

	var sessionID string
	sessionID, err = gw.createSession(ctx, s.AgentID)
	if err != nil {
		result.Error = fmt.Sprintf("create session: %v", err)
		return result
	}

	var fullTranscript []TranscriptEntry
	turnTimeout := c.timeout / time.Duration(maxInt(len(s.Prompts), 1))

	for i, prompt := range s.Prompts {
		if i >= s.MaxTurns {
			break
		}
		fullTranscript = append(fullTranscript, TranscriptEntry{Role: "user", Content: prompt})
		var newEntries []TranscriptEntry
		newEntries, err = gw.postMessage(ctx, sessionID, prompt, len(fullTranscript)-1, turnTimeout)
		if err != nil {
			result.Error = fmt.Sprintf("turn %d postMessage: %v", i, err)
			return result
		}
		fullTranscript = append(fullTranscript, newEntries...)
	}

	result.TranscriptExcerpt = transcriptExcerpt(fullTranscript)

	if c.dryRun {
		slog.Info("eval: dry-run — skipping judge call", "scenario", s.ID)
		return result
	}

	// Build judge prompt.
	// Convert local TranscriptEntry to judge.TranscriptEntry.
	judgeTranscript := make([]judge.TranscriptEntry, len(fullTranscript))
	for i, e := range fullTranscript {
		judgeTranscript[i] = judge.TranscriptEntry{
			Role:     e.Role,
			Content:  e.Content,
			ToolName: e.ToolName,
		}
	}

	// Extract tool calls from transcript.
	var judgeToolCalls []judge.ToolCallEntry
	for _, e := range fullTranscript {
		if e.Role == "tool_call" {
			judgeToolCalls = append(judgeToolCalls, judge.ToolCallEntry{
				ToolName: e.ToolName,
				Args:     e.Content,
			})
		}
	}
	if judgeToolCalls == nil {
		judgeToolCalls = []judge.ToolCallEntry{}
	}

	// Concatenate user prompts for the "User prompt" field.
	var userPrompts []string
	for _, e := range fullTranscript {
		if e.Role == "user" {
			userPrompts = append(userPrompts, e.Content)
		}
	}

	promptCtx := judge.PromptContext{
		AgentName:  s.AgentID,
		AgentRole:  s.AgentRole,
		Prompt:     strings.Join(userPrompts, "\n"),
		Transcript: judgeTranscript,
		ToolCalls:  judgeToolCalls,
		Rubric:     s.Rubric,
	}
	renderedPrompt, err := judge.RenderPrompt(promptCtx)
	if err != nil {
		result.Error = fmt.Sprintf("render judge prompt: %v", err)
		return result
	}

	judgeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	judgeRaw, judgeTokens, err := callJudge(judgeCtx, c.judgeModel, renderedPrompt)
	if err != nil {
		result.Error = fmt.Sprintf("judge call: %v", err)
		return result
	}
	result.TokenUsage.Judge = judgeTokens

	scores, err := judge.Parse(judgeRaw)
	if err != nil {
		result.Error = fmt.Sprintf("parse judge scores: %v", err)
		return result
	}
	result.Scores = scores
	result.CostUSD = estimateCostUSD(result.TokenUsage.Agent, result.TokenUsage.Judge)

	return result
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ── Main ──────────────────────────────────────────────────────────────────────

// Exit codes:
//
//	0  success, ≥1 scenarios run, ≥1 succeeded
//	1  at least one scenario errored AND all errored
//	2  zero scenarios found (unless --allow-empty-scenarios)
//	3  configuration / flag parse error
//	4  gateway unreachable

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	c := parseFlags()

	scenarios, err := discoverScenarios(c.scenariosDir)
	if err != nil {
		slog.Error("eval: failed to discover scenarios", "error", err)
		os.Exit(3)
	}
	if len(scenarios) == 0 {
		if c.allowEmptyScenarios {
			slog.Warn("eval: no scenarios found (--allow-empty-scenarios set, exiting 0)", "dir", c.scenariosDir)
			os.Exit(0)
		}
		slog.Error("eval: no scenarios found", "dir", c.scenariosDir)
		os.Exit(2)
	}
	slog.Info("eval: discovered scenarios", "count", len(scenarios))

	err = os.MkdirAll(filepath.Dir(c.outPath), 0o755)
	if err != nil {
		slog.Error("eval: cannot create output directory", "error", err)
		os.Exit(1)
	}

	outFile, err := os.OpenFile(c.outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Error("eval: cannot open output file", "path", c.outPath, "error", err)
		os.Exit(1)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)

	var (
		totalCostUSD   float64
		totalAgentToks int
		totalJudgeToks int
		erroredCount   int
		successCount   int
	)

	for _, s := range scenarios {
		slog.Info("eval: running scenario", "id", s.ID, "agent", s.AgentID)
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		result := runScenario(ctx, s, c, outFile)
		cancel()

		if result.Error != "" {
			slog.Warn("eval: scenario failed", "id", s.ID, "error", result.Error)
			erroredCount++
		} else {
			slog.Info("eval: scenario complete", "id", s.ID,
				"completion", result.Scores.Completion,
				"tools", result.Scores.Tools,
				"persona", result.Scores.Persona,
				"safety", result.Scores.Safety,
				"efficiency", result.Scores.Efficiency,
			)
			successCount++
			totalCostUSD += result.CostUSD
			totalAgentToks += result.TokenUsage.Agent
			totalJudgeToks += result.TokenUsage.Judge
		}

		line, err := json.Marshal(result)
		if err != nil {
			slog.Error("eval: marshal result", "id", s.ID, "error", err)
			continue
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			slog.Error("eval: write result", "id", s.ID, "error", err)
		}
		if err := writer.Flush(); err != nil {
			slog.Error("eval: flush output", "error", err)
		}
	}

	// Print summary.
	slog.Info("eval: run complete",
		"scenarios", len(scenarios),
		"succeeded", successCount,
		"errored", erroredCount,
		"cost_usd", fmt.Sprintf("$%.4f", totalCostUSD),
		"agent_tokens", totalAgentToks,
		"judge_tokens", totalJudgeToks,
	)

	// Regenerate the report before checking exit conditions so the JSONL
	// artifact always exists for debugging even on all-error runs.
	resultsDir := filepath.Dir(c.outPath)
	if err := RegenerateReport(resultsDir, c.reportPath); err != nil {
		slog.Error("eval: regenerate report", "error", err)
		os.Exit(1)
	}
	slog.Info("eval: report written", "path", c.reportPath)

	// F32: if every scenario errored, exit 1 (fail the CI job).
	if erroredCount == len(scenarios) && len(scenarios) > 0 {
		slog.Error("eval: all scenarios errored", "count", erroredCount)
		os.Exit(1)
	}

	// Budget guard: fail if cost > $2.
	if totalCostUSD > 2.00 {
		slog.Error("eval: cost exceeded $2.00 budget", "cost_usd", totalCostUSD)
		os.Exit(1)
	}
}
