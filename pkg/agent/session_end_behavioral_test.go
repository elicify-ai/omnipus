// session_end_behavioral_test.go — end-to-end behavioral coverage for
// Fix C's session-end pipeline. Complements session_end_smoke_test.go with
// spec-mandated #35 (happy path), #43 (bootstrap pass), and boot-gate coverage
// for FR-029a (cheap-model allow-list).

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// scriptedProvider is a mockProvider with a predetermined Chat response and
// call-count observation. Exercises the session-end code path without a real
// LLM. Records the last request so tests can assert recap-option hygiene
// (max_tokens=512, extended_thinking=false, extra_body.reasoning.enabled=false).
type scriptedProvider struct {
	mu           sync.Mutex
	responseBody string
	responseErr  error
	callCount    int
	lastOpts     map[string]any
	lastModel    string
	lastMessages []providers.Message
}

func (s *scriptedProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	s.mu.Lock()
	s.callCount++
	s.lastModel = model
	s.lastOpts = opts
	s.lastMessages = messages
	resp, err := s.responseBody, s.responseErr
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: resp}, nil
}

func (s *scriptedProvider) GetDefaultModel() string { return "scripted-model" }

// TestRunRecap_HappyPath_PersistsLastSessionAndRetro exercises #35 end-to-end:
// a transcript is written, CloseSession is invoked, and after the recap
// goroutine completes the MemoryStore must contain LAST_SESSION.md + a retro.
// Also pins FR-029a cost-guard opts (max_tokens=512, extended_thinking=false,
// extra_body.reasoning.enabled=false) onto the Chat request.
func TestRunRecap_HappyPath_PersistsLastSessionAndRetro(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	// Configure a specific recap model so the resolution is deterministic.
	cfg.Agents.Defaults.RecapModel = "claude-haiku-3"

	script := &scriptedProvider{
		responseBody: `{"recap":"we shipped","went_well":["tests"],"needs_improvement":["log noise"],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	// Build a real shared session store with a single user-origin session.
	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	// Register a minimal agent owning the session.
	agentCfg := &config.AgentConfig{ID: "recap-agent", Name: "Recap"}
	defaults := &cfg.Agents.Defaults
	ag := NewAgentInstance(agentCfg, defaults, cfg, script)
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	// Point the agent's workspace at a dedicated dir so its MemoryStore
	// paths are deterministic and scoped to this test.
	ag.Home = filepath.Join(home, "agents", "recap-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("recap-agent", "Recap")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	// Create a real session owned by recap-agent and append a transcript entry.
	meta, err := store.NewSession(session.SessionTypeChat, "web", "recap-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID
	if appendErr := store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role:      "user",
		Content:   "please summarize what we did today",
		Timestamp: time.Now().UTC(),
		AgentID:   "recap-agent",
	}); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}

	// Kick the recap and wait for it.
	al.CloseSession(sessionID, "explicit")

	deadline := time.Now().Add(5 * time.Second)
	var lastSessionBytes []byte
	for time.Now().Before(deadline) {
		// Spec-5: last-session.md is now in <workspace>/.omnipus/last-session.md
		data, readErr := os.ReadFile(filepath.Join(ag.Home, ".omnipus", "last-session.md"))
		if readErr == nil {
			lastSessionBytes = data
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastSessionBytes == nil {
		t.Fatalf("last-session.md not produced within deadline; Chat calls=%d", script.callCount)
	}
	if !strings.Contains(string(lastSessionBytes), "we shipped") {
		t.Errorf("last-session.md missing recap content; got:\n%s", lastSessionBytes)
	}

	// FR-029a: recap Chat call must set max_tokens=2048, extended_thinking=false,
	// and extra_body.reasoning.enabled=false. max_tokens=2048 gives reasoning
	// models (e.g. glm-5.2 on OpenRouter/Fireworks) enough budget to generate
	// both the reasoning trace and the JSON content — the old 512-token cap was
	// consumed entirely by reasoning (~500 tokens), leaving content=null.
	script.mu.Lock()
	defer script.mu.Unlock()
	if script.callCount != 1 {
		t.Errorf("scripted Chat calls = %d, want 1", script.callCount)
	}
	if mt, _ := script.lastOpts["max_tokens"].(int); mt != 2048 {
		t.Errorf("opts.max_tokens = %v, want 2048", script.lastOpts["max_tokens"])
	}
	if et, _ := script.lastOpts["extended_thinking"].(bool); et {
		t.Error("opts.extended_thinking must be false")
	}
	eb, _ := script.lastOpts["extra_body"].(map[string]any)
	if eb == nil {
		t.Fatal("opts.extra_body missing")
	}
	reasoning, _ := eb["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["enabled"] != false {
		t.Errorf("opts.extra_body.reasoning.enabled must be false; got %v", eb)
	}
	if script.lastModel != "claude-haiku-3" {
		t.Errorf("recap model = %q, want claude-haiku-3 (recap_model config)", script.lastModel)
	}

	// A retro file must exist too — date-directory under .omnipus/retros/.
	// WriteLastSession and AppendRetro run sequentially in the recap
	// goroutine (pkg/agent/session_end.go), but the last-session.md
	// poll above can see the first write before the goroutine completes
	// the second one under heavy CI load. Poll the retro the same way.
	// Spec-5: retros are now in <workspace>/.omnipus/retros/<date>/.
	//
	// os.IsNotExist is treated as a transient condition here: AppendRetro
	// creates the retros/<date>/ directory atomically, so the directory may
	// not yet exist when the goroutine is between WriteLastSession and
	// AppendRetro. Calling t.Fatalf on ENOENT causes a spurious failure under
	// parallel-sibling load (the goroutine has less CPU time between the two
	// sequential writes). Only non-"not found" errors are fatal.
	// AppendRetro (memory.go) os.OpenFile(O_CREATE)s the retro path, then
	// writes its content in a separate syscall — os.ReadDir can observe the
	// just-created (still-empty) filename before the content lands. Treating
	// "the filename showed up" as proof the write is complete and reading it
	// exactly once (the previous shape of this loop) is a genuine TOCTOU
	// race, reproduced directly in the sibling test this pattern was copied
	// from (idle_timeout_seam_test.go — see its fix for the reproduction).
	// Keep polling — re-reading the file — until its CONTENT actually
	// contains what this test asserts on, not just its directory entry.
	sessionsDir := filepath.Join(ag.Home, ".omnipus", "retros")
	var retroBytes []byte
	var sawRetroFile bool
	retroDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(retroDeadline) {
		dateDirs, err := os.ReadDir(sessionsDir)
		if err != nil {
			if !os.IsNotExist(err) {
				t.Fatalf("read sessions dir (unexpected error): %v", err)
			}
			// Directory not yet created by AppendRetro — retry.
			time.Sleep(20 * time.Millisecond)
			continue
		}
		var content []byte
		var found bool
		for _, d := range dateDirs {
			if !d.IsDir() {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(sessionsDir, d.Name()))
			for _, f := range files {
				if strings.HasSuffix(f.Name(), "_retro.md") {
					found = true
					data, readErr := os.ReadFile(filepath.Join(sessionsDir, d.Name(), f.Name()))
					if readErr == nil {
						content = data
					}
				}
			}
		}
		if found {
			sawRetroFile = true
			retroBytes = content
		}
		if found && strings.Contains(string(content), "trigger=explicit") && strings.Contains(string(content), "fallback=false") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawRetroFile {
		t.Fatal("no _retro.md file was produced in the happy path within 5s")
	}
	if !strings.Contains(string(retroBytes), "trigger=explicit") {
		t.Errorf("retro missing trigger=explicit; got:\n%s", retroBytes)
	}
	if !strings.Contains(string(retroBytes), "fallback=false") {
		t.Errorf("happy-path retro should be fallback=false; got:\n%s", retroBytes)
	}
}

// TestRunRecap_JSONParseError_WritesFallback covers the failure mode where the
// model returns non-JSON: the fallback retro is written, the raw response is
// logged, and no recap is persisted to LAST_SESSION.md.
func TestRunRecap_JSONParseError_WritesFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "claude-haiku-3"

	script := &scriptedProvider{responseBody: "I'm not JSON, sorry"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, _ := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "parse-agent", Name: "Parse"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	ag.Home = filepath.Join(home, "agents", "parse-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("parse-agent", "Parse")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	pmeta, err := store.NewSession(session.SessionTypeChat, "web", "parse-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := pmeta.ID
	_ = store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role: "user", Content: "hi", Timestamp: time.Now().UTC(), AgentID: "parse-agent",
	})

	al.CloseSession(sessionID, "explicit")

	// Wait for a retro to land (it will, via the fallback path).
	// Spec-5: retros are now in <workspace>/.omnipus/retros/<date>/.
	//
	// AppendRetro (memory.go) os.OpenFile(O_CREATE)s the retro path, then
	// writes its content in a separate syscall — os.ReadDir can observe the
	// just-created (still-empty) filename before the content lands. The
	// previous shape of this loop treated "the filename showed up" as proof
	// the write was complete, capturing retroPath and reading it exactly
	// ONCE, outside the loop, with no retry — a genuine TOCTOU race,
	// reproduced directly on this exact assertion (parse-error retro must be
	// fallback=true; got: <empty>) under package-suite load. Keep polling —
	// re-reading the file — until its CONTENT actually contains what this
	// test asserts on, not just its directory entry.
	deadline := time.Now().Add(5 * time.Second)
	var retro string
	var sawRetroFile bool
	for time.Now().Before(deadline) {
		sessionsDir := filepath.Join(ag.Home, ".omnipus", "retros")
		dateDirs, _ := os.ReadDir(sessionsDir)
		var content string
		var found bool
		for _, d := range dateDirs {
			if !d.IsDir() {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(sessionsDir, d.Name()))
			for _, f := range files {
				if strings.HasSuffix(f.Name(), "_retro.md") {
					found = true
					data, readErr := os.ReadFile(filepath.Join(sessionsDir, d.Name(), f.Name()))
					if readErr == nil {
						content = string(data)
					}
				}
			}
		}
		if found {
			sawRetroFile = true
			retro = content
		}
		if found && strings.Contains(content, "fallback=true") && strings.Contains(content, "json_parse_error") && strings.Contains(content, "Tool calls:") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawRetroFile {
		t.Fatal("fallback retro not produced within deadline")
	}
	if !strings.Contains(retro, "fallback=true") {
		t.Errorf("parse-error retro must be fallback=true; got:\n%s", retro)
	}
	if !strings.Contains(retro, "json_parse_error") {
		t.Errorf("retro must mention json_parse_error reason; got:\n%s", retro)
	}
	// The fallback recap body must include tool-call count per Ambiguity #2.
	if !strings.Contains(retro, "Tool calls:") {
		t.Errorf("fallback recap body missing 'Tool calls:' count; got:\n%s", retro)
	}
}

// TestExtractJSONFromText covers the reasoning-extraction helper added to fix the
// glm-5.2 / OpenRouter json_parse_error bug: when content=null but the reasoning
// trace contains the drafted JSON, extractJSONFromText should recover it.
func TestExtractJSONFromText(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // expected extracted JSON, or "" if nothing found
	}{
		{
			name:  "simple_object",
			input: `{"recap":"hi","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
			want:  `{"recap":"hi","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
		},
		{
			name: "json_after_reasoning_text",
			input: `First I'll think about this...
Then I'll produce the JSON:
{"recap":"we shipped","went_well":["tests"],"needs_improvement":[],"worth_remembering":[]}`,
			want: `{"recap":"we shipped","went_well":["tests"],"needs_improvement":[],"worth_remembering":[]}`,
		},
		{
			name: "json_in_code_fence_prefix",
			// Model drafts JSON inside a reasoning trace with a ``` fence
			input: "reasoning step: produce JSON\n```json\n{\"recap\":\"done\",\"went_well\":[],\"needs_improvement\":[],\"worth_remembering\":[]}\n```",
			want:  `{"recap":"done","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
		},
		{
			name:  "no_json",
			input: "no json here at all",
			want:  "",
		},
		{
			name:  "empty_string",
			input: "",
			want:  "",
		},
		{
			name:  "nested_objects",
			input: `some text {"outer":{"inner":"val"},"arr":[1,2]} trailing`,
			want:  `{"outer":{"inner":"val"},"arr":[1,2]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSONFromText(tc.input)
			if tc.want == "" {
				if got != "" {
					t.Errorf("extractJSONFromText(%q) = %q, want empty string", tc.input, got)
				}
				return
			}
			// Normalise both via JSON round-trip so whitespace differences don't matter.
			var wantMap, gotMap map[string]any
			if err := json.Unmarshal([]byte(tc.want), &wantMap); err != nil {
				t.Fatalf("test case want is invalid JSON: %v", err)
			}
			if err := json.Unmarshal([]byte(got), &gotMap); err != nil {
				t.Errorf("extractJSONFromText(%q) = %q which is not valid JSON: %v", tc.input, got, err)
				return
			}
			wantBytes, _ := json.Marshal(wantMap)
			gotBytes, _ := json.Marshal(gotMap)
			if string(wantBytes) != string(gotBytes) {
				t.Errorf("extractJSONFromText(%q)\n  got  %s\n  want %s", tc.input, gotBytes, wantBytes)
			}
		})
	}
}

// TestRunRecap_ReasoningFallback_ExtractsJSONFromReasoning exercises the path
// where the LLM returns content="" but reasoning contains the drafted JSON.
// This is the glm-5.2 on OpenRouter/Fireworks behavior when reasoning tokens
// exhaust the budget — the model stops mid-reasoning with content=null.
// The fix: extractJSONFromText scans resp.Reasoning and recovers the JSON.
func TestRunRecap_ReasoningFallback_ExtractsJSONFromReasoning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "glm-reasoning-model"

	// Simulate the glm-5.2 response: content is empty but reasoning has the JSON.
	const recapText = "reasoning-extracted recap text sentinel"
	reasoningWithJSON := `I will now produce the JSON as requested:
{"recap":"` + recapText + `","went_well":["reasoning worked"],"needs_improvement":[],"worth_remembering":[]}`

	reasoningScript := &reasoningOnlyProvider{reasoning: reasoningWithJSON}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, reasoningScript)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "reasoning-agent", Name: "Reasoning"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, reasoningScript)
	ag.Home = filepath.Join(home, "agents", "reasoning-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("reasoning-agent", "Reasoning")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "reasoning-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID
	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role: "user", Content: "what did we do?", Timestamp: time.Now().UTC(), AgentID: "reasoning-agent",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	al.CloseSession(sessionID, "explicit")

	// Wait for last-session.md — the reasoning-extraction path must succeed.
	deadline := time.Now().Add(5 * time.Second)
	var lastSessionBytes []byte
	lastSessionPath := filepath.Join(ag.Home, ".omnipus", "last-session.md")
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(lastSessionPath)
		if readErr == nil {
			lastSessionBytes = data
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastSessionBytes == nil {
		t.Fatal("last-session.md not produced within deadline — reasoning-extraction fallback did not fire")
	}
	content := string(lastSessionBytes)
	// Must be the real LLM recap (from reasoning extraction), not the heuristic fallback.
	if !strings.Contains(content, recapText) {
		t.Errorf("last-session.md does not contain expected recap text %q; got:\n%s", recapText, content)
	}
	if strings.Contains(content, "Fallback reason:") {
		t.Errorf("last-session.md should contain LLM recap, not heuristic fallback; got:\n%s", content)
	}
}

// fencedContentProvider returns the JSON recap wrapped in a ```json markdown
// fence — the exact glm-5.2-on-OpenRouter pattern (content begins "```json\n{...")
// that a raw json.Unmarshal rejects at char 0.
type fencedContentProvider struct{ content string }

func (f *fencedContentProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: f.content}, nil
}

func (f *fencedContentProvider) GetDefaultModel() string { return "glm-fenced-model" }

// TestRunRecap_FencedContent verifies the recap parses a JSON envelope returned
// inside a ```json markdown fence (glm-5.2 / OpenRouter routinely does this).
// Before the fix, extractJSONFromText was only applied to resp.Reasoning, so a
// raw json.Unmarshal(resp.Content) failed and the recap fell back to a heuristic
// stub ("Turns: N ... Fallback reason: json_parse_error") that dropped the real
// summary — the failure observed in the recap-continuity e2e.
func TestRunRecap_FencedContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "glm-fenced-model"

	const recapText = "fenced-content recap sentinel"
	fenced := "```json\n{\"recap\":\"" + recapText +
		"\",\"went_well\":[\"shipped\"],\"needs_improvement\":[],\"worth_remembering\":[]}\n```"
	prov := &fencedContentProvider{content: fenced}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "fenced-agent", Name: "Fenced"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, prov)
	ag.Home = filepath.Join(home, "agents", "fenced-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("fenced-agent", "Fenced")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "fenced-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID
	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role: "user", Content: "remember FALCON-XYZ", Timestamp: time.Now().UTC(), AgentID: "fenced-agent",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	al.CloseSession(sessionID, "explicit")

	deadline := time.Now().Add(5 * time.Second)
	var lastSessionBytes []byte
	lastSessionPath := filepath.Join(ag.Home, ".omnipus", "last-session.md")
	for time.Now().Before(deadline) {
		if data, readErr := os.ReadFile(lastSessionPath); readErr == nil {
			lastSessionBytes = data
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastSessionBytes == nil {
		t.Fatal("last-session.md not produced within deadline — fenced-content parse did not fire")
	}
	content := string(lastSessionBytes)
	if !strings.Contains(content, recapText) {
		t.Errorf("last-session.md missing recap text %q; got:\n%s", recapText, content)
	}
	if strings.Contains(content, "Fallback reason:") {
		t.Errorf("last-session.md should contain the LLM recap, not a heuristic fallback; got:\n%s", content)
	}
}

// reasoningOnlyProvider simulates a reasoning model that returns empty content
// but populates the Reasoning field — the glm-5.2 / OpenRouter pattern when
// the token budget is exhausted during the reasoning phase.
type reasoningOnlyProvider struct {
	reasoning string
}

func (r *reasoningOnlyProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   "",          // empty — simulates content=null
		Reasoning: r.reasoning, // JSON is here instead
	}, nil
}

func (r *reasoningOnlyProvider) GetDefaultModel() string { return "glm-reasoning-model" }

// TestNewAgentLoop_AnyModelBoots verifies that the recap-model boot gate is
// gone: NewAgentLoop must succeed regardless of what model is configured for
// recap, including models that were previously blocked by the allow-list.
func TestNewAgentLoop_AnyModelBoots(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	// Previously "expensive/opus-7" would have triggered RecapModelBootError.
	// After the gate removal, NewAgentLoop must succeed.
	cfg.Agents.Defaults.RecapModel = "expensive/opus-7"
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop must succeed with any recap model; got error: %v", err)
	}
	if al == nil {
		t.Fatal("NewAgentLoop returned nil loop without error")
	}
	al.Close()
}

// TestBootstrapRecapPass_SkippedByDefault verifies FR-032a: without the
// BootstrapRecapEnabled opt-in the pass is a no-op, even when there are
// eligible sessions in the shared store. If this ever reverses, a fresh boot
// could burst LLM calls against every orphaned session — exactly MAJ-007.
func TestBootstrapRecapPass_SkippedByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.BootstrapRecapEnabled = false // default — must stay false

	script := &scriptedProvider{}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, _ := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	al.sharedSessionStore = store

	// Seed two orphaned sessions (owner won't resolve).
	for i := 0; i < 2; i++ {
		m, err := store.NewSession(session.SessionTypeChat, "web", "nonexistent")
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		_ = store.AppendTranscript(m.ID, session.TranscriptEntry{
			Role: "user", Content: "seed", Timestamp: time.Now().Add(-2 * time.Hour).UTC(),
			AgentID: "nonexistent",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	al.BootstrapRecapPass(ctx)

	script.mu.Lock()
	defer script.mu.Unlock()
	if script.callCount != 0 {
		t.Errorf("Bootstrap pass made %d Chat calls despite BootstrapRecapEnabled=false", script.callCount)
	}
}

// TestBootstrapRecapPass_OneIterationPerSession is the regression for the
// code-reviewer's High-1: BootstrapRecapPass previously iterated the shared
// sessions directory once per registered agent, burning the rate-limit slot N
// times per session. We verify a session is enqueued at most once.
func TestBootstrapRecapPass_OneIterationPerSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.BootstrapRecapEnabled = true
	cfg.Agents.Defaults.BootstrapRecapMaxPerMinute = 60 // will be floored to 1s by guard

	// Scripted provider returns a valid recap so runRecap succeeds and the
	// retro file appears — which is our "processed once" signal.
	script := &scriptedProvider{
		responseBody: `{"recap":"ok","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, _ := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	al.sharedSessionStore = store

	// Register three agents so the old bug would iterate the sessions dir
	// three times. The target session's owner is the first agent.
	for _, id := range []string{"alpha", "beta", "gamma"} {
		ac := &config.AgentConfig{ID: id, Name: id}
		ag := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg, script)
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	bootMeta, err := store.NewSession(session.SessionTypeChat, "web", "alpha")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := bootMeta.ID

	// Age the transcript timestamp so the 30-min gate lets it through.
	_ = store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role:      "user",
		Content:   "pre-boot work",
		Timestamp: time.Now().Add(-2 * time.Hour).UTC(),
		AgentID:   "alpha",
	})

	// Backdate the transcript on disk too — BootstrapRecapPass reads the
	// newest entry's timestamp, not the file mtime, but we backdate just in
	// case anything stat-based is introduced later.
	_ = os.Chtimes(filepath.Join(home, "sessions", sessionID, "transcript.jsonl"),
		time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	al.BootstrapRecapPass(ctx)

	// Give the CloseSession goroutine time to produce the retro.
	deadline := time.Now().Add(3 * time.Second)
	retroCount := -1
	for time.Now().Before(deadline) {
		retroCount = countRetrosFor(t, filepath.Join(home, "agents", "alpha"), sessionID)
		if retroCount > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if retroCount != 1 {
		t.Errorf("expected exactly 1 retro for session %s after bootstrap; got %d",
			sessionID, retroCount)
	}

	// After the recap goroutine completes, the claim must still be present —
	// claims are intentionally never deleted (F22: idempotency for lifetime of process).
	// File-level idempotency via agentSessionHasRetro prevents duplicate writes.
	if _, ok := al.claimedCloseSessions.Load(sessionID); !ok {
		t.Error("claim map entry must persist after recap completes (process-lifetime idempotency gate)")
	}

	// And Chat should have been invoked exactly once — not 3 times (once per agent).
	script.mu.Lock()
	gotCalls := script.callCount
	script.mu.Unlock()
	if gotCalls != 1 {
		t.Errorf("scripted Chat calls = %d, want 1 (once per unique session, regardless of agent count)", gotCalls)
	}
}

func countRetrosFor(t *testing.T, workspace, sessionID string) int {
	t.Helper()
	// Spec-5: retros now live in <workspace>/.omnipus/retros/<date>/<sessionID>_retro.md
	retrosDir := filepath.Join(workspace, ".omnipus", "retros")
	dates, err := os.ReadDir(retrosDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range dates {
		if !d.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(retrosDir, d.Name(), sessionID+"_retro.md")); err == nil {
			n++
		}
	}
	return n
}

// ensureJSONMarshalsSessionEndEntry catches a drift in session.TranscriptEntry
// JSON shape that would silently desync the bootstrap timestamp parser — the
// parser looks for a "timestamp" field and TranscriptEntry's tag must keep it.
// Keeps this cross-package invariant visible to lane S maintainers.
func ensureJSONMarshalsSessionEndEntry(t *testing.T) {
	t.Helper()
	e := session.TranscriptEntry{Timestamp: time.Unix(1000, 0).UTC()}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal TranscriptEntry: %v", err)
	}
	if !strings.Contains(string(raw), `"timestamp":`) {
		t.Errorf("TranscriptEntry JSON tag drift: expected \"timestamp\" field, got %s", raw)
	}
}

func TestTranscriptEntry_TimestampJSONField(t *testing.T) {
	ensureJSONMarshalsSessionEndEntry(t)
}

// modelSwitchProvider is a scriptedProvider variant that returns an error when
// Chat is called for a specific "fail model" and succeeds for any other model.
// Used to exercise the recap fallback chain: primary fails → fallback used.
type modelSwitchProvider struct {
	mu          sync.Mutex
	failModel   string
	callCount   int
	lastModel   string
	successBody string
}

func (m *modelSwitchProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	m.mu.Lock()
	m.callCount++
	m.lastModel = model
	m.mu.Unlock()
	if model == m.failModel {
		return nil, fmt.Errorf("provider: model %q unavailable (simulated)", model)
	}
	return &providers.LLMResponse{Content: m.successBody}, nil
}

func (m *modelSwitchProvider) GetDefaultModel() string { return "switch-model" }

// TestRunRecap_FallbackChain_PrimaryFails verifies that when the primary recap
// model returns an error, runRecap falls through to the first fallback and
// produces a retro using the fallback model's response.
func TestRunRecap_FallbackChain_PrimaryFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const primaryModel = "primary-model"
	const fallbackModel = "fallback-model"

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = primaryModel
	cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{
		{Model: fallbackModel, Provider: ""},
	}

	prov := &modelSwitchProvider{
		failModel:   primaryModel,
		successBody: `{"recap":"fallback recap","went_well":["fallback"],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "fallback-agent", Name: "Fallback"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, prov)
	ag.Home = filepath.Join(home, "agents", "fallback-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("fallback-agent", "Fallback")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "fallback-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_ = store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "trigger fallback recap",
		AgentID:   "fallback-agent",
		Timestamp: time.Now().UTC(),
	})

	al.CloseSession(meta.ID, "explicit")

	// Wait for the recap goroutine to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRetrosFor(t, ag.Home, meta.ID) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if countRetrosFor(t, ag.Home, meta.ID) == 0 {
		t.Fatal("expected a retro to be written via fallback model; none found")
	}

	// The provider must have been called at least twice: once for the failing
	// primary model and once (successfully) for the fallback model.
	prov.mu.Lock()
	calls := prov.callCount
	last := prov.lastModel
	prov.mu.Unlock()

	if calls < 2 {
		t.Errorf("expected ≥2 Chat calls (primary fail + fallback success); got %d", calls)
	}
	if last != fallbackModel {
		t.Errorf("last Chat call model = %q, want %q (fallback)", last, fallbackModel)
	}
}

// TestRunRecap_ModelResolution_UsesRecapModelField verifies the new priority
// order for recap model resolution: RecapModel config field takes precedence
// over GetModelName() and the session agent's own model.
func TestRunRecap_ModelResolution_UsesRecapModelField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const recapModel = "configured-recap-model"

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = recapModel
	cfg.Agents.Defaults.ModelName = "default-model" // must NOT be used

	script := &scriptedProvider{
		responseBody: `{"recap":"ok","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "model-res-agent", Name: "ModelRes"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	ag.Model = "agent-own-model" // must NOT be used when RecapModel is set
	ag.Home = filepath.Join(home, "agents", "model-res-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("model-res-agent", "ModelRes")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "model-res-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_ = store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "test model resolution",
		AgentID:   "model-res-agent",
		Timestamp: time.Now().UTC(),
	})

	al.CloseSession(meta.ID, "explicit")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRetrosFor(t, ag.Home, meta.ID) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	script.mu.Lock()
	usedModel := script.lastModel
	script.mu.Unlock()

	if usedModel != recapModel {
		t.Errorf("recap used model %q, want configured RecapModel %q", usedModel, recapModel)
	}
}
