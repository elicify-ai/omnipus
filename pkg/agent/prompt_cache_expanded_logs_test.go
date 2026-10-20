package agent

// T5 — count the REAL emitted accounting records, not formatted strings.
// Traces to: prompt-caching-spec.md FR-001 (one info line per completed
// turn-loop model call, content-free), FR-023 (no per-area log levels), FR-025
// (no prompt/tool/provider-payload text in the line), and
// pr-test-analyzer.md::T5.
//
// What the existing pack lacked: TestLogLine_UsageOnly calls
// FormatCallAccounting directly; the real-loop tests persist accounting but
// never assert the emitted record count, level or call-id uniqueness. Here the
// real logger runs with its real file sink (logger.EnableFileLogging — the
// second production sink in pkg/logger/logger.go::logMessage), around a real
// two-call turn (tool-call round, then final text) driven through the real
// loop, and the file is parsed back as bytes.
//
// The accounting scope is the turn loop: one INFO llm_usage per successfully
// completed turn-loop model call. Auxiliary model calls (recap, goal compile,
// audio) are excluded by production and by this harness (none run here).
//
// Nothing is mocked at the unit boundary: the loop, accounting, logger, store
// and transcript are real; the scripted provider sits at the provider
// transport edge (the same place testutil.ScenarioProvider sits) and the stub
// tool is registered through the real al.RegisterTool.
//
// Baseline expectations and the mutation plan are in handoff.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Privacy sentinels. Each is planted where FR-025 forbids it to surface and
// asserted absent from the real log bytes.
const (
	expandedLogsPromptSentinel  = "EXPANDED-LOGS-SECRET-PROMPT-SENTINEL"
	expandedLogsArgSentinel     = "EXPANDED-LOGS-SECRET-ARG-SENTINEL"
	expandedLogsResultSentinel  = "EXPANDED-LOGS-SECRET-RESULT-SENTINEL"
	expandedLogsFinalTextMarker = "EXPANDED-LOGS-FINAL-TEXT"
)

// expandedLogsScripted is the provider-transport-edge double. Its two steps
// carry full, hand-derived cache-split usage so the emitted line can be
// reconciled with the persisted per-call accounting.
type expandedLogsScripted struct {
	mu    sync.Mutex
	steps []*providers.LLMResponse
	idx   int
	calls int
}

func (s *expandedLogsScripted) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.idx >= len(s.steps) {
		return nil, fmt.Errorf("expandedLogsScripted: no step left for call %d", s.calls)
	}
	step := s.steps[s.idx]
	s.idx++
	return step, nil
}

func (s *expandedLogsScripted) GetDefaultModel() string { return "expanded-logs-model" }

func (s *expandedLogsScripted) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// expandedLogsProbeTool is the real registered tool the first call invokes.
// Its result text carries the result sentinel, which must never reach the
// accounting line. The execution counter lets the test prove the tool really
// ran — the sentinel was genuinely produced into the conversation — so the
// privacy assertion is never vacuous.
type expandedLogsProbeTool struct {
	tools.BaseTool
	executions atomic.Int64
}

func (p *expandedLogsProbeTool) Name() string        { return "expanded_logs_probe" }
func (p *expandedLogsProbeTool) Description() string { return "Accounting-emission probe stub" }
func (p *expandedLogsProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}
}
func (p *expandedLogsProbeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *expandedLogsProbeTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	p.executions.Add(1)
	return &tools.ToolResult{ForLLM: expandedLogsResultSentinel + " probe-ok", IsError: false}
}

func (p *expandedLogsProbeTool) executionCount() int64 { return p.executions.Load() }

// expandedLogsRunTurn drives ONE real turn that makes TWO provider calls:
// step 1 returns a tool call (executed by the registered probe tool), step 2
// returns final text. Usage numbers are hand-derived fixture values, chosen
// here — not read off any run.
func expandedLogsRunTurn(t *testing.T) (*AgentLoop, string, int, *expandedLogsProbeTool) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}},
		},
		// Fixture pins the uncompressed manifest path explicitly: compressed
		// mode withholds lazy/unknown names until a ToolSearch loads them.
		// The probe must be advertised by fixture fact, not by production
		// mode.
		Tools: config.ToolsConfig{Manifest: config.ManifestConfig{Compressed: false}},
	}
	probe := &expandedLogsProbeTool{}
	provider := &expandedLogsScripted{steps: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID: "expanded-logs-call-1",
				Function: &providers.FunctionCall{
					Name:      "expanded_logs_probe",
					Arguments: `{"query":"` + expandedLogsArgSentinel + `"}`,
				},
			}},
			Usage: &providers.UsageInfo{
				PromptTokens: 4100, CompletionTokens: 12,
				CacheReadTokens: 800, CacheWriteTokens: 200, CacheWrite5mTokens: 200,
				TotalTokens: 5112, // 4100 + 12 + 800 + 200
			},
		},
		{
			Content: expandedLogsFinalTextMarker + " done",
			Usage: &providers.UsageInfo{
				PromptTokens: 5200, CompletionTokens: 30,
				CacheReadTokens: 5100, CacheWriteTokens: 0,
				TotalTokens: 10330, // 5200 + 30 + 5100
			},
		},
	}}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	al.RegisterTool(probe)
	// Explicit fixture authorization: the probe is allowed BY NAME for every
	// agent, so advertisement (FilterToolsByPolicy) and execution resolve the
	// same explicit verdict — never a production policy fallback. Mirrors the
	// in-tree deny pattern (async_notifier_test.go), inverted.
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"expanded_logs_probe": config.ToolPolicyAllow},
		})
	}

	meta, err := al.sharedSessionStore.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
		Channel:   "webchat",
		SessionID: meta.ID,
		Sender:    bus.SenderInfo{CanonicalID: "user-logs-1"},
		ChatID:    "chat-expanded-logs",
		Content:   expandedLogsPromptSentinel + " please run the probe",
	})
	if err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	return al, meta.ID, provider.callCount(), probe
}

// expandedLogsCaptureFile turns on the logger's real file sink. The console
// sink stays enabled (pkg/logger hygiene fails a run that silences it).
func expandedLogsCaptureFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "expanded-logs.jsonl")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	return path
}

type expandedLogsRecord struct {
	Raw        string // the full structured line as written by the real file sink
	Line       string // the accounting message text itself
	Session    string
	CallID     string
	Uncached   int
	CacheRead  int
	CacheWrite int
	Output     int
	Total      int
}

var expandedLogsLineRe = regexp.MustCompile(
	`session="([^"]*)" call="([^"]*)" uncached=(\d+) cache_read=(\d+) cache_write=(\d+) output=(\d+) total=(\d+)`)

// expandedLogsUsageRecords parses the real log bytes back into llm_usage
// records. The file sink is zerolog JSON (pkg/logger/logger.go::logMessage →
// writeEvent: level/component fields, then the Msg text under "message";
// levels are lowercase strings). A line carrying llm_usage text that does not
// parse as an INFO record is a failure, not a skip.
func expandedLogsUsageRecords(t *testing.T, path string) []expandedLogsRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	var out []expandedLogsRecord
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "llm_usage ") {
			continue
		}
		var structured struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &structured); err != nil {
			t.Fatalf("parse log line: %v (%s)", err, line)
		}
		if structured.Level != "info" || !strings.HasPrefix(structured.Message, "llm_usage ") {
			continue
		}
		m := expandedLogsLineRe.FindStringSubmatch(structured.Message)
		if m == nil {
			t.Fatalf("llm_usage line does not match the FR-001 grammar: %q", structured.Message)
		}
		fields := make([]int, 5)
		for i := 0; i < 5; i++ {
			v, convErr := strconv.Atoi(m[3+i])
			if convErr != nil {
				t.Fatalf("llm_usage counter parse: %v (%q)", convErr, m[3+i])
			}
			fields[i] = v
		}
		out = append(out, expandedLogsRecord{
			Raw: line, Line: structured.Message, Session: m[1], CallID: m[2],
			Uncached: fields[0], CacheRead: fields[1], CacheWrite: fields[2], Output: fields[3], Total: fields[4],
		})
	}
	// Instrument self-check: a log-format surprise (renamed field) or the
	// line being emitted at a non-info level must look like itself — never
	// masquerade as a missing-emission finding.
	if rawCount := strings.Count(string(data), "llm_usage "); rawCount != len(out) {
		t.Fatalf("llm_usage parse mismatch: %d raw occurrences in the file but %d parsed INFO records — "+
			"either the real log line shape is not understood by this parser, or the line is being "+
			"emitted at a non-info level; inspect before trusting either number", rawCount, len(out))
	}
	return out
}

// TestExpandedLogs_TwoCallTurnEmitsOneInfoUsagePerCall asserts the emission
// boundary: exactly one INFO llm_usage per successfully completed turn-loop
// call, unique call ids, numbers matching the scripted usage AND the persisted
// transcript accounting, and — scope note — no private sentinel inside any
// emitted ACCOUNTING LINE. FR-025 governs the accounting line specifically;
// other log records may legitimately carry conversation or tool text by
// design, so no global redaction guarantee is claimed or tested here.
func TestExpandedLogs_TwoCallTurnEmitsOneInfoUsagePerCall(t *testing.T) {
	// The positive fixture pins INFO explicitly: EnableFileLogging alone must
	// never act as a level override, and the shipped default may be WARN.
	prev := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() { logger.SetLevel(prev) })

	path := expandedLogsCaptureFile(t)
	al, sessionID, calls, probe := expandedLogsRunTurn(t)
	if calls != 2 {
		t.Fatalf("provider saw %d calls, want a two-call turn (tool round + final text)", calls)
	}
	// The probe really executed once — the result sentinel was genuinely
	// produced into the conversation — so the privacy oracle below is not
	// vacuously green.
	if got := probe.executionCount(); got != 1 {
		t.Fatalf("probe tool executed %d times, want exactly 1; the result sentinel was never produced", got)
	}

	recs := expandedLogsUsageRecords(t, path)
	if len(recs) != 2 {
		t.Fatalf("got %d INFO llm_usage records, want exactly one per completed call (2); records=%v", len(recs), recs)
	}

	// Hand-derived from the scripted fixture above — never from a run.
	want := []expandedLogsRecord{
		{Uncached: 4100, CacheRead: 800, CacheWrite: 200, Output: 12, Total: 5112},
		{Uncached: 5200, CacheRead: 5100, CacheWrite: 0, Output: 30, Total: 10330},
	}
	for i, rec := range recs {
		if rec.CallID == "" {
			t.Errorf("llm_usage record %d has an empty call id (FR-001 requires a call correlation id): %s", i, rec.Line)
		}
		if i > 0 && rec.CallID == recs[i-1].CallID {
			t.Errorf("llm_usage records 0 and %d share call id %q; ids must be unique per call", i, rec.CallID)
		}
		if rec.Session != sessionID {
			t.Errorf("llm_usage record %d session = %q, want the request's session id %q", i, rec.Session, sessionID)
		}
		w := want[i]
		if rec.Uncached != w.Uncached || rec.CacheRead != w.CacheRead || rec.CacheWrite != w.CacheWrite ||
			rec.Output != w.Output || rec.Total != w.Total {
			t.Errorf("llm_usage record %d = uncached=%d cache_read=%d cache_write=%d output=%d total=%d, want %d/%d/%d/%d/%d (FR-001): %s",
				i, rec.Uncached, rec.CacheRead, rec.CacheWrite, rec.Output, rec.Total,
				w.Uncached, w.CacheRead, w.CacheWrite, w.Output, w.Total, rec.Line)
		}
		// FR-025 line-scope privacy: the accounting LINE carries no prompt
		// text, tool argument, or tool result. Checked on every record.
		for _, sentinel := range []string{expandedLogsPromptSentinel, expandedLogsArgSentinel, expandedLogsResultSentinel} {
			if strings.Contains(rec.Raw, sentinel) {
				t.Errorf("llm_usage record %q contains private sentinel %q (FR-025): %s", rec.CallID, sentinel, rec.Raw)
			}
		}
	}

	// Reconcile the lines with the persisted per-call accounting: the first
	// call id names a persisted transcript entry whose numbers match it.
	entries, err := al.sharedSessionStore.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%q): %v", sessionID, err)
	}
	firstMatched := false
	secondMatched := false
	for _, entry := range entries {
		if entry.ID == recs[0].CallID {
			firstMatched = true
			if entry.CacheReadTokens != want[0].CacheRead || entry.CacheWriteTokens != want[0].CacheWrite ||
				entry.PromptTokens != want[0].Uncached || entry.CompletionTokens != want[0].Output ||
				entry.Tokens != want[0].Total {
				t.Errorf("persisted entry %q numbers = read %d write %d prompt %d completion %d total %d, want %d/%d/%d/%d/%d — line and store disagree (FR-001)",
					entry.ID, entry.CacheReadTokens, entry.CacheWriteTokens, entry.PromptTokens, entry.CompletionTokens, entry.Tokens,
					want[0].CacheRead, want[0].CacheWrite, want[0].Uncached, want[0].Output, want[0].Total)
			}
		}
		if entry.Role == "assistant" && entry.CacheReadTokens == want[1].CacheRead && entry.Tokens == want[1].Total {
			secondMatched = true
			if entry.PromptTokens != want[1].Uncached || entry.CompletionTokens != want[1].Output ||
				entry.CacheWriteTokens != want[1].CacheWrite {
				t.Errorf("persisted narration entry %q numbers disagree with the second llm_usage line (FR-001)", entry.ID)
			}
		}
	}
	if !firstMatched {
		t.Errorf("no persisted transcript entry carries the first call id %q — the emitted line and the store must agree", recs[0].CallID)
	}
	if !secondMatched {
		t.Errorf("no persisted assistant entry matches the second llm_usage line (read %d total %d)", want[1].CacheRead, want[1].Total)
	}
}

// TestExpandedLogs_WarnLevelHidesAccounting is the level control: the calls
// still happen (and the probe still executes), but at WARN no accounting line
// is visible anywhere in the file. FR-001 keeps the line at info; FR-023
// forbids a per-area override that would resurrect it.
func TestExpandedLogs_WarnLevelHidesAccounting(t *testing.T) {
	prev := logger.GetLevel()
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.SetLevel(prev) })

	path := expandedLogsCaptureFile(t)
	_, _, calls, probe := expandedLogsRunTurn(t)
	if calls != 2 {
		t.Fatalf("provider saw %d calls at WARN, want the same two-call turn", calls)
	}
	if got := probe.executionCount(); got != 1 {
		t.Fatalf("probe executed %d times at WARN, want exactly 1 — the turn itself must be unchanged", got)
	}
	if recs := expandedLogsUsageRecords(t, path); len(recs) != 0 {
		t.Errorf("at WARN %d llm_usage records are visible, want zero (accounting stays at info; FR-001/FR-023): %v", len(recs), recs)
	}
}
