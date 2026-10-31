package agent

// effort_red_containment_test.go — RED tests for reasoning containment
// (US-1, Landing 1).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-01, S-02, S-03, S-04, S-05, S-06, S-85 (test order items
// T-C1..T-C4), FR-016, FR-017, FR-018, FR-041, FR-042, FR-043.
//
// RED reasons:
//   S-01/S-05 — today's "LLM response" debug line carries the full reasoning
//   text and target_channel, and the recap parse-failure WARN carries a
//   response_preview; both must become length-only (FR-016, FR-041).
//   S-03/S-04 — today's empty-answer path substitutes ReasoningContent as the
//   answer, so the sentinel never appears and the marker leaks (FR-018).
//   S-02 — zero-trace sweep; the reasoning channel still exists (FR-017).
//   S-06/S-85 — regression pins of today's truncation/classification
//   behaviour; they pass at RED and must KEEP passing at GREEN.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const redMarker = "SENTINEL-REASONING-7731"

// effortScriptedProvider returns the same scripted response for every call
// and counts calls, so retry rules are observable (S-03: 2 requests; S-85: 1).
type effortScriptedProvider struct {
	mu        sync.Mutex
	calls     int
	content   string
	reasoning string
	finish    string
}

func (p *effortScriptedProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return &providers.LLMResponse{
		Content: p.content,
		// Both carriers are set: the substitution path (FR-018) reads
		// ReasoningContent while the debug line and the recap fallback read
		// Reasoning — containment must hold for either carrier.
		ReasoningContent: p.reasoning,
		Reasoning:        p.reasoning,
		FinishReason:     p.finish,
	}, nil
}

func (p *effortScriptedProvider) GetDefaultModel() string { return "scripted-model" }

func (p *effortScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// captureFileLog enables the real zerolog file sink at DEBUG level AND
// redirects the standard slog default logger to a memory buffer, because the
// recap path logs through slog.Warn while the turn path logs through
// pkg/logger. Returns the file path; the slog buffer is read via slogBuffer.
func captureFileLog(t *testing.T) string {
	t.Helper()
	prev := logger.GetLevel()
	logger.SetLevelFromString("debug")
	path := filepath.Join(t.TempDir(), "effort-red.log")
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prev)
	})
	var mu sync.Mutex
	buf := &logBuf{mu: &mu}
	prevSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevSlog) })
	slogBufForTest = buf
	return path
}

// logBuf is a tiny mutex-guarded io.Writer over a strings.Builder.
type logBuf struct {
	mu  *sync.Mutex
	bld strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bld.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bld.String()
}

// slogBufForTest holds the buffer installed by captureFileLog for the
// current test (sequential tests only).
var slogBufForTest *logBuf

// logEvents reads the captured JSONL log file into raw maps.
func logEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "log file must exist")
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &ev), "each log line is JSON")
		events = append(events, ev)
	}
	return events
}

// newContainmentLoop builds an agent loop with the given provider, a real
// session store, and a pre-created webchat session, so containment turns are
// dispatched instead of dropped.
func newContainmentLoop(t *testing.T, cfg *config.Config, prov providers.LLMProvider) (*AgentLoop, *session.UnifiedStore, string) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	if cfg.Agents.Defaults.Home == "" {
		cfg.Agents.Defaults.Home = t.TempDir()
	}
	cfg.Agents.Defaults.MaxToolIterations = 3
	if cfg.Agents.Defaults.DefaultModel.Model == "" {
		cfg.Agents.Defaults.DefaultModel.Model = "scripted-model"
	}
	home := cfg.Agents.Defaults.Home
	// The default agent must exist for the session-owner binding.
	if len(cfg.Agents.List) == 0 {
		cfg.Agents.List = []config.AgentConfig{{ID: testDefaultAgentID, Home: home}}
	}

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })
	al.sharedSessionStore = store
	containmentSessionID = mustContainmentSession(t, store)
	return al, store, home
}

// containmentSessionID is the pre-created session for the current
// containment test (sequential tests only).
var containmentSessionID string

// containmentTurn builds a bound webchat message for the pre-created session.
func containmentTurn(content string) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:    "webchat",
		ChatID:     containmentSessionID,
		Content:    content,
		SessionID:  containmentSessionID,
		SessionKey: "agent:" + testDefaultAgentID + ":session:" + containmentSessionID,
	}
}

func mustContainmentSession(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "web", testDefaultAgentID)
	require.NoError(t, err)
	return meta.ID
}

// TestT_C1_S01_DebugLogCarriesReasoningLength pins FR-016: the "LLM response"
// debug line carries reasoning_chars (integer) and neither reasoning text nor
// target_channel; the marker appears nowhere in the log sink.
// Traces to: S-01, US-1 AS1, FR-016, FR-043.
func TestT_C1_S01_DebugLogCarriesReasoningLength(t *testing.T) {
	logPath := captureFileLog(t)
	reasoning := redMarker + " private reasoning steps " + redMarker
	prov := &effortScriptedProvider{content: "the answer", reasoning: reasoning, finish: "stop"}
	al, _, _ := newContainmentLoop(t, nil, prov)

	resp, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("hello"))
	require.NoError(t, err)
	assert.Equal(t, "the answer", resp)

	events := logEvents(t, logPath)
	var llmResponse map[string]any
	for _, ev := range events {
		if ev["message"] == "LLM response" {
			llmResponse = ev
			break
		}
	}
	require.NotNil(t, llmResponse, "the LLM response debug line must exist")
	rc, ok := llmResponse["reasoning_chars"]
	require.True(t, ok, "the line must carry reasoning_chars (FR-016)")
	assert.Equal(t, float64(len([]rune(reasoning))), rc, "reasoning_chars equals the reasoning text length")
	assert.NotContains(t, llmResponse, "reasoning", "the line must not carry reasoning text (S-01)")
	assert.NotContains(t, llmResponse, "target_channel", "the line must not carry target_channel (FR-016)")

	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), redMarker, "the marker appears nowhere in the log sink (S-01)")
}

// TestT_C1_S05_RecapWarnCarriesLengthOnly pins FR-041: the recap keeps its
// JSON-from-reasoning fallback; the parse-failure WARN carries response_chars
// and no response_preview, and the marker is in no log line.
// Traces to: S-05, US-1 AS5, FR-041, FD-22.
func TestT_C1_S05_RecapWarnCarriesLengthOnly(t *testing.T) {
	logPath := captureFileLog(t)

	t.Run("valid recap JSON in reasoning is still parsed", func(t *testing.T) {
		recapJSON := `{"recap":"recap from reasoning JSON sentinel","went_well":["a"],"needs_improvement":[],"worth_remembering":[]}`
		prov := &effortScriptedProvider{content: "", reasoning: "thinking... " + recapJSON, finish: "stop"}
		al, sessionID, ag := buildRecapTestLoop(t, prov)
		al.CloseSession(sessionID, "idle")
		pairPath := wu21ReaimPairPath(wu21ReaimHome(ag), ag.ID)
		waitForCond(t, 10*time.Second, "recap JSON carried into the carry-forward summary", func() bool {
			data, err := os.ReadFile(pairPath)
			return err == nil && strings.Contains(string(data), "recap from reasoning JSON sentinel")
		})
	})

	t.Run("unparseable reasoning logs length only and writes the fallback", func(t *testing.T) {
		prov := &effortScriptedProvider{content: "", reasoning: " " + redMarker + " not json at all", finish: "stop"}
		al, sessionID, ag := buildRecapTestLoop(t, prov)
		al.CloseSession(sessionID, "idle")
		pairPath := wu21ReaimPairPath(wu21ReaimHome(ag), ag.ID)
		waitForCond(t, 10*time.Second, "heuristic fallback recap write", func() bool {
			data, err := os.ReadFile(pairPath)
			return err == nil && len(strings.TrimSpace(string(data))) > 0
		})

		// The recap path logs through slog (default handler), so the warning
		// lives in the captured slog buffer; the turn path logs through
		// pkg/logger's file sink. Assert both sinks.
		require.NotNil(t, slogBufForTest)
		slogText := slogBufForTest.String()
		warnIdx := strings.Index(slogText, "session_end: recap JSON parse failed")
		require.GreaterOrEqual(t, warnIdx, 0, "the recap JSON parse failed warning must exist")
		warnLine := slogText[warnIdx:]
		if nl := strings.Index(warnLine, "\n"); nl >= 0 {
			warnLine = warnLine[:nl]
		}
		charsIdx := strings.Index(warnLine, "response_chars=")
		require.GreaterOrEqual(t, charsIdx, 0, "the warning carries response_chars (FR-041)")
		assert.NotContains(t, warnLine, "response_preview=", "the warning must not carry response_preview (S-05, FD-22)")
		raw, err := os.ReadFile(logPath)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), redMarker, "the marker appears in no log line (S-05)")
		assert.NotContains(t, slogText, redMarker, "the marker appears in no slog line (S-05)")
	})
}

// waitForCond polls cond until it holds or the timeout elapses.
func waitForCond(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within %s", what, timeout)
}

// onlySessionID returns the single session of a test-scoped store.
func onlySessionID(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1, "test store holds exactly one session")
	return sessions[0].ID
}

// TestT_C3_S03_NonTruncatedReasoningOnly pins FR-018: a non-truncated empty
// answer with reasoning retries once, then the unchanged sentinel; the marker
// never appears as the answer or in the saved transcript.
// Traces to: S-03, US-1 AS3, FR-018, machine-verifiable defaultResponse text.
func TestT_C3_S03_NonTruncatedReasoningOnly(t *testing.T) {
	prov := &effortScriptedProvider{content: "", reasoning: redMarker + " long reasoning", finish: "stop"}
	al, store, _ := newContainmentLoop(t, nil, prov)

	resp, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("hello"))
	require.NoError(t, err)
	assert.Equal(t, 2, prov.callCount(), "exactly two provider requests: the original and one retry (S-03)")
	assert.Equal(t, defaultResponse, resp, "the person sees exactly the unchanged sentinel text (S-03)")
	assert.NotContains(t, resp, redMarker, "the reasoning never appears as the answer")

	entries, err := store.ReadTranscript(onlySessionID(t, store))
	require.NoError(t, err)
	var answerEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].Role == "assistant" && entries[i].Type != session.EntryTypeToolCall {
			answerEntry = &entries[i]
		}
	}
	require.NotNil(t, answerEntry, "an assistant answer entry exists")
	assert.Equal(t, defaultResponse, answerEntry.Content, "the saved transcript content is the sentinel")
	assert.NotContains(t, answerEntry.Content, redMarker, "no reasoning marker in saved content (S-03)")
}

// TestT_C3_S04_SameTextOnMessengerAndParent pins FR-018: the messenger path
// and the parent-visible reply both receive exactly the S-03 text.
// Traces to: S-04, US-1 AS4.
func TestT_C3_S04_SameTextOnMessengerAndParent(t *testing.T) {
	msgBus := bus.NewMessageBus()
	prov := &effortScriptedProvider{content: "", reasoning: redMarker + " reasoning", finish: "stop"}
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = t.TempDir()
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.Defaults.DefaultModel.Model = "scripted-model"
	cfg.Agents.List = []config.AgentConfig{{ID: testDefaultAgentID, Home: cfg.Agents.Defaults.Home}}
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	chManager, err := channels.NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	require.NoError(t, err)
	chManager.RegisterChannel("telegram", &fakeChannel{id: "s04-chat"})
	al.SetChannelManager(chManager)

	resp, _, err := al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
		Channel: "telegram",
		Sender:  bus.SenderInfo{CanonicalID: "user-s04"},
		ChatID:  "s04-chat",
		Content: "hello",
	})
	require.NoError(t, err)
	// The parent-visible reply (what a delegating agent receives) is the same
	// ordinary reply text.
	assert.Equal(t, defaultResponse, resp, "the parent receives exactly the S-03 text (S-04)")
	assert.NotContains(t, resp, redMarker, "no reasoning text reaches the parent")

	// The messenger receives exactly the same text on its outbound frame.
	deadline := time.After(10 * time.Second)
	for {
		var outbound bus.OutboundMessage
		select {
		case outbound = <-msgBus.OutboundChan():
		case <-deadline:
			t.Fatal("expected the empty-response sentinel on the messenger outbound")
		}
		if outbound.ChatID != "s04-chat" || outbound.Channel != "telegram" {
			continue
		}
		assert.Equal(t, defaultResponse, outbound.Content, "the messenger receives exactly the S-03 text (S-04)")
		assert.NotContains(t, outbound.Content, redMarker, "no reasoning text reaches the messenger (S-04)")
		break
	}
}

// TestT_C4_S85_OutputLimitedReasoningOnly keeps today's truncation outcome
// (regression pin): no retry, empty answer, truncation_reason
// max_output_tokens, not marked failed.
// Traces to: S-85, US-1 AS8, FR-018, SC-016.
func TestT_C4_S85_OutputLimitedReasoningOnly(t *testing.T) {
	prov := &effortScriptedProvider{content: "", reasoning: redMarker + " truncated reasoning", finish: "length"}
	al, store, _ := newContainmentLoop(t, nil, prov)

	resp, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("hello"))
	require.NoError(t, err)
	assert.Equal(t, 1, prov.callCount(), "exactly one provider request: no empty-response retry (S-85)")
	assert.Equal(t, "", resp, "the final answer is empty (S-85)")
	assert.NotContains(t, resp, redMarker, "the reasoning never appears as the answer")

	entries, err := store.ReadTranscript(onlySessionID(t, store))
	require.NoError(t, err)
	var answerEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].Role == "assistant" && entries[i].Type != session.EntryTypeToolCall {
			answerEntry = &entries[i]
		}
	}
	require.NotNil(t, answerEntry)
	assert.True(t, answerEntry.Truncated, "the entry carries truncated: true (S-85)")
	assert.Equal(t, "max_output_tokens", answerEntry.TruncationReason, "truncation_reason max_output_tokens (S-85)")
}

// TestT_C4_S06_ReasoningOnlyTaskClassification keeps today's classification
// (regression pin): an output-limited reasoning-only try is still classified
// as such. Traces to: S-06, US-1 AS6, FR-042.
func TestT_C4_S06_ReasoningOnlyTaskClassification(t *testing.T) {
	home := t.TempDir()
	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeTask, "web", "mia")
	require.NoError(t, err)
	// The S-85 outcome shape, written through the production writer.
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:             "assistant",
		Content:          "",
		Truncated:        true,
		TruncationReason: "max_output_tokens",
		Timestamp:        time.Now().UTC(),
	}))
	assert.True(t, turnWasReasoningOnly(meta.ID, store, ""),
		"an output-limited reasoning-only try is still classified as reasoning-only (S-06)")
	assert.False(t, turnWasReasoningOnly(meta.ID, store, "an actual answer"),
		"a try with an answer is not reasoning-only")
}

// effortRepoRoot walks up from the package dir to the directory holding go.mod.
func effortRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "go.mod not found above %s", dir)
		dir = parent
	}
}

func TestT_C2_S02_ReasoningChannelZeroTrace(t *testing.T) {
	// Any remaining reasoning-channel symbol is a GREEN blocker. The sweep
	// greps the production trees the spec inventories (24 files in pkg/, 13
	// connector pages, env bindings).
	banned := []string{
		"ReasoningChannelID",
		"reasoning_channel_id",
		"REASONING_CHANNEL_ID",
		"targetReasoningChannelID",
	}
	roots := []string{"pkg", "cmd", "internal", "docs/connectors"}
	repo := effortRepoRoot(t)
	for _, root := range roots {
		_ = filepath.Walk(filepath.Join(repo, root), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".md") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") && strings.Contains(path, "effort_red") {
				return nil // this sweep itself
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			for _, b := range banned {
				if strings.Contains(string(data), b) {
					t.Errorf("%s contains %q — the reasoning channel must be deleted outright (S-02, FR-017)", path, b)
				}
			}
			return nil
		})
	}
}
