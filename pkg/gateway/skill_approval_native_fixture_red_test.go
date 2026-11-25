//go:build goolm && stdjson

package gateway

// F18 native fixtures. Only the model is scripted. Skill resolution, generation
// grants, registry, policy adapter, handler, file bytes and model requests are real.
// Fresh-process GET /skills reads durable trust without assuming its file encoding.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nativeSkillR1 = "---\nname: notes-helper\ndescription: Native consent fixture.\n---\n\nF18-NOTES-R1-INSTRUCTIONS-9Z\n"
const nativeSkillR2 = "---\nname: notes-helper\ndescription: A changed native consent fixture.\n---\n\nF18-NOTES-R2-INSTRUCTIONS-7Q\n"

// One call per literal prompt, so concurrent primary and unattended probes do
// not decide each other's scripted turn. Captured data is the real model input.
type nativeConsentScript struct {
	mu       sync.Mutex
	tool     string
	args     map[string]any
	requests map[string][][]providers.Message
}

func (p *nativeConsentScript) GetDefaultModel() string { return "native-consent-script" }
func (p *nativeConsentScript) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	prompt := ""
	for _, msg := range messages {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "F18-CALL-") {
			prompt = msg.Content
		}
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	var frozen []providers.Message
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.requests == nil {
		p.requests = map[string][][]providers.Message{}
	}
	p.requests[prompt] = append(p.requests[prompt], frozen)
	first := len(p.requests[prompt]) == 1
	p.mu.Unlock()
	if prompt != "" && first {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "native-" + prompt, Type: "function", Name: p.tool, Arguments: p.args}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}
func (p *nativeConsentScript) captured(prompt string) [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests[prompt]...)
}

type nativeSkillHarness struct {
	*timedCardHarness
	script *nativeConsentScript
	seq    int
}
type nativeSkillRun struct {
	prompt string
	done   <-chan error
}

func newNativeSkillHarness(t *testing.T, trust, permission string) *nativeSkillHarness {
	t.Helper()
	return newNativeSkillHarnessWithBody(t, trust, permission, nativeSkillR1)
}

// newNativeSkillHarnessWithBody is newNativeSkillHarness with the notes-helper SKILL.md bytes
// chosen by the caller (fix round 4, part D item 7: the card-frame test needs a skill whose
// metadata carries a display name, which no other native test has). The bytes are written before
// the agents' generation grants are bound, so the grants are the grants of exactly these bytes.
// Nothing else differs: newNativeSkillHarness is this function with nativeSkillR1.
func newNativeSkillHarnessWithBody(t *testing.T, trust, permission, skillMarkdown string) *nativeSkillHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	root := filepath.Join(home, "skills", "notes-helper")
	require.NoError(t, os.MkdirAll(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(skillMarkdown), 0o600))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "native-consent-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.SkillTrust = config.SkillTrustLevel(trust)
	cfg.Sandbox.ToolPolicies["Skill"] = "allow"
	cfg.Sandbox.ToolPolicies["install_skill"] = "ask"
	cfg.Tools.Manifest.Compressed = false
	require.NoError(t, json.Unmarshal([]byte(`{"skill_policies":{"notes-helper":"`+permission+`"}}`), &cfg.Sandbox))
	for _, id := range []string{"test-agent", "peer-agent"} {
		record := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Skills: []string{"notes-helper"}, Tools: coreagent.NewCustomAgentToolsCfg()}
		store := agentstore.New(home)
		require.NoError(t, store.Create(id, &record))
		persisted, err := store.Get(id)
		require.NoError(t, err)
		require.NotEmpty(t, persisted.SkillGrants["notes-helper"], "fixture: actual generation binding")
		cfg.Agents.List = append(cfg.Agents.List, *persisted)
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var literal map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &literal))
	var sandbox map[string]any
	require.NoError(t, json.Unmarshal(literal["sandbox"], &sandbox))
	sandbox["skill_policies"] = map[string]string{"notes-helper": permission}
	literal["sandbox"], err = json.Marshal(sandbox)
	require.NoError(t, err)
	raw, err = json.Marshal(literal)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	script := &nativeConsentScript{tool: "Skill", args: map[string]any{"name": "notes-helper"}}
	al := mustAgentLoop(t, cfg, msgBus, script)
	for _, record := range cfg.Agents.List {
		inst, ok := al.GetRegistry().GetAgent(record.ID)
		require.True(t, ok)
		inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
	}
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	return &nativeSkillHarness{timedCardHarness: &timedCardHarness{api: &restAPI{agentLoop: al, homePath: home, approvalReg: reg, auditor: al.AuditLogger()}, reg: reg, handler: handler}, script: script}
}

func (h *nativeSkillHarness) begin(t *testing.T) nativeSkillRun {
	t.Helper()
	h.seq++
	prompt := fmt.Sprintf("F18-CALL-%d", h.seq)
	meta, err := h.api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)
	// A real ordinary-root chat is an input, not a favorable attendance verdict.
	// Without its native record the real ancestry classifier correctly refuses
	// Ask as damaged/unattended, which cannot prove the C-24 interactive path.
	require.NoError(t, h.api.agentLoop.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: "test-agent", OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindChat},
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := h.api.agentLoop.ProcessDirectWithChannelAs(ctx, "test-agent", prompt, "agent:test-agent:session:"+meta.ID, "webchat", meta.ID)
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("fixture: native skill turn did not join")
		}
	})
	return nativeSkillRun{prompt: prompt, done: done}
}
func (h *nativeSkillHarness) pending(t *testing.T, run nativeSkillRun) *approvalEntry {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if rows := h.reg.pendingApprovals(); len(rows) > 0 {
			require.Len(t, rows, 1)
			return rows[0]
		}
		select {
		case err := <-run.done:
			require.NoError(t, err, "fixture: real turn error is not a consent decision")
			requests := h.script.captured(run.prompt)
			var results []string
			for _, request := range requests {
				for _, message := range request {
					if message.Role == "tool" {
						results = append(results, message.Content)
					}
				}
			}
			t.Fatalf("C-12/C-24: actual %s call completed without its required card; model requests=%d, tool results=%q", h.script.tool, len(requests), results)
		case <-timer.C:
			t.Fatal("fixture UNVERIFIED: native skill turn reached neither card nor completion")
		case <-tick.C:
		}
	}
}
func (h *nativeSkillHarness) finish(t *testing.T, run nativeSkillRun, loads bool) {
	t.Helper()
	select {
	case err := <-run.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("native waiting call was not released")
	}
	h.assertResult(t, run.prompt, loads)
}
func (h *nativeSkillHarness) assertResult(t *testing.T, prompt string, loads bool) {
	t.Helper()
	requests := h.script.captured(prompt)
	require.GreaterOrEqual(t, len(requests), 2, "real tool result reaches the next model request")
	var results []string
	for _, msg := range requests[1] {
		if msg.Role == "tool" {
			results = append(results, msg.Content)
		}
	}
	require.Len(t, results, 1)
	if loads {
		assert.Contains(t, results[0], "F18-NOTES-R1-INSTRUCTIONS-9Z", "actual Skill tool instructions")
	} else {
		for _, request := range requests {
			raw, err := json.Marshal(request)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "F18-NOTES-R1-INSTRUCTIONS-9Z")
			assert.NotContains(t, string(raw), "F18-NOTES-R2-INSTRUCTIONS-7Q")
		}
	}
}
func (h *nativeSkillHarness) unattended(t *testing.T, agentID string, loads bool) {
	t.Helper()
	h.seq++
	prompt := fmt.Sprintf("F18-CALL-%d", h.seq)
	meta, err := h.api.agentLoop.GetSessionStore().NewScheduledSession(agentID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = h.api.agentLoop.ProcessScheduled(ctx, agentID, meta.ID, prompt, "scheduled", meta.ID)
	require.NoError(t, err)
	require.NoError(t, ctx.Err(), "deadline is not unattended refusal")
	h.assertResult(t, prompt, loads)
}
func (h *nativeSkillHarness) combined(t *testing.T, entry *approvalEntry) {
	t.Helper()
	snap := h.handler.pendingApprovalSnapshot(entry)
	assert.Equal(t, []string{"approve_version", "approve_version_allow_for", "deny"}, snap.OfferedActions)
	assert.Equal(t, []string{"1h", "3h", "1d"}, snap.AllowDurations)
	require.NotNil(t, snap.Skill, "C-9 real skill facts, not a seeded entry")
	revision, err := skills.NewSkillWriter(filepath.Join(h.api.homePath, "skills")).SkillRevision("notes-helper")
	require.NoError(t, err)
	assert.Equal(t, gen.ToolApprovalSkill{Key: "notes-helper", Name: "notes-helper", DisplayName: "notes-helper", Revision: revision, Unverified: true, OpenChecks: []string{"permission", "trust"}}, *snap.Skill)
}

// The child reads the native public inventory in a NEW process. No trust parser
// or synthetic store is supplied by this test. The durable container is opaque.
func nativeTrustReadChild(t *testing.T) {
	home := os.Getenv("OMNIPUS_F18_TRUST_HOME")
	require.True(t, filepath.IsAbs(home))
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var cfg config.Config
	if unmarshalErr := json.Unmarshal(raw, &cfg); unmarshalErr != nil {
		t.Fatalf("BLOCKED: native config cannot load the C-3 trust vocabulary before C-24 durable readback — required by C-3/C-24; never remap ask to a legacy value in the reader: %v", unmarshalErr)
	}
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	al := mustAgentLoop(t, &cfg, msgBus, &restMockProvider{})
	require.Equal(t, home, config.OmnipusHomeDir(), "reader must stay on the original actual app home")
	api := &restAPI{agentLoop: al, homePath: home}
	w := httptest.NewRecorder()
	api.HandleSkills(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	var rows []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	body, err := json.Marshal(rows)
	require.NoError(t, err)
	fmt.Printf("F18_NATIVE_TRUST_JSON:%s\n", body)
}
func assertFreshSkillTrust(t *testing.T, home, key string, approved bool) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSkillApprovalActions_Handler$", "-test.v", "--", "--f18-native-trust-reader")
	// Let the child explicitly opt into this home after its TestMain has
	// isolated HOME. Inheriting OMNIPUS_HOME would mark it as ambient, so
	// mustAgentLoop would correctly replace it with another empty test home.
	for _, variable := range os.Environ() {
		variableName, _, _ := strings.Cut(variable, "=")
		if variableName != config.EnvHome && variableName != config.EnvConfig && variableName != "OMNIPUS_F18_TRUST_HOME" {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "OMNIPUS_F18_TRUST_HOME="+home)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "native child read/exit is an instrument obligation: %s", output)
	var rows []gen.Skill
	found := false
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "F18_NATIVE_TRUST_JSON:") {
			require.False(t, found)
			found = true
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "F18_NATIVE_TRUST_JSON:")), &rows))
		}
	}
	require.True(t, found, "instrument: actual child emitted generated inventory: %s", output)
	matches := 0
	for _, row := range rows {
		if row.Id == key {
			matches++
			assert.Equal(t, approved, row.TrustApproved, "fresh-process native current-revision trust for %s", key)
		}
	}
	require.Equal(t, 1, matches, "actual native reader must see the named installed skill")
}
