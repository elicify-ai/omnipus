//go:build goolm && stdjson

package gateway

// Order 52 / CHECK F17: C-26, S-109 and S-113. Moved from pkg/agent so
// the test can drive the real policy approver/registry/HTTP handler without
// a circular production import. No test inserts a pending card or grant.
// Actual ProcessScheduled, actual Skill, protected generation grants, real
// later model requests/transcript and raw persisted policies are observed.
// Only a scripted no-paid-call provider and a clock are injectable.
//
// The local weather fixture is genuinely granted under trust Allow. It is
// NOT a fabricated hash-verified install. Exact S-109 verification metadata
// and S-113's DIFFERENT unverified/trust-Ask/restart scenario remain explicit
// BLOCKED obligations. ProcessScheduled is the fired-schedule turn entry,
// not proof that the scheduler fired or that the full gateway restarted.
// Mutation probes and complete GREEN acceptance belong to independent CHECK.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scheduledWeatherInstructions = "S109-WEATHER-INSTRUCTIONS-9Z"
const scheduledWeatherRefusal = "Not run: needs approval, and task and plan runs never ask."

// Input-only clock boundary. Never returns a card decision or allowance.
var injectScheduledAllowanceClock func(*agent.AgentLoop, func() time.Time, func() time.Duration)

type scheduledAllowanceClock struct {
	mu      sync.Mutex
	wall    time.Time
	elapsed time.Duration
}

func (c *scheduledAllowanceClock) wallNow() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *scheduledAllowanceClock) elapsedNow() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.elapsed
}

func (c *scheduledAllowanceClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.elapsed += d
}

type scheduledAllowanceScript struct {
	armed    atomic.Bool
	calls    atomic.Int64
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *scheduledAllowanceScript) GetDefaultModel() string { return "scheduled-allowance-script" }
func (p *scheduledAllowanceScript) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	// Freeze the actual request at the external model boundary, not an
	// internally computed permission verdict or a test-owned transcript.
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	var captured []providers.Message
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, captured)
	p.mu.Unlock()
	if p.armed.CompareAndSwap(true, false) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: fmt.Sprintf("weather-call-%d", p.calls.Add(1)), Type: "function", Name: "Skill", Arguments: map[string]any{"name": "weather"}}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *scheduledAllowanceScript) arm() {
	p.mu.Lock()
	p.requests = nil
	p.mu.Unlock()
	p.armed.Store(true)
}

func (p *scheduledAllowanceScript) captured() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

type scheduledAllowanceHarness struct {
	al      *agent.AgentLoop
	api     *restAPI
	reg     *approvalRegistryV2
	handler *WSHandler
	script  *scheduledAllowanceScript
	home    string
}

func newScheduledAllowanceHarness(t *testing.T, global, own string) *scheduledAllowanceHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	dir := filepath.Join(home, "skills", "weather")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: weather\ndescription: Scheduled weather fixture.\n---\n\n"+scheduledWeatherInstructions+"\n"), 0o600))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scheduled-allowance-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.SkillTrust = config.SkillTrustLevel("allow")
	cfg.Sandbox.ToolPolicies["Skill"] = "allow"
	cfg.Sandbox.AuditLog = true
	cfg.Tools.Manifest.Compressed = false // Discovery is not the permission boundary.
	require.NoError(t, json.Unmarshal([]byte(`{"skill_policies":{"weather":"`+global+`"}}`), &cfg.Sandbox))
	store := agentstore.New(home)
	for _, id := range []string{"mia", "ray"} {
		record := config.AgentConfig{ID: id, Name: id, Skills: []string{"weather"}}
		require.NoError(t, store.Create(id, &record), "real persistence binds the actual installed generation")
		if id == "mia" && own != "" {
			path := filepath.Join(home, "entities", "agents", id+".json")
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var literal map[string]any
			require.NoError(t, json.Unmarshal(raw, &literal))
			// The store record is flat: the agent tool settings are the top-level tools member
			// (tests/helpers/agentrecord); tools_cfg is only the REST request name, so a value
			// planted there is dead data no reader ever sees.
			tools, _ := literal["tools"].(map[string]any)
			if tools == nil {
				tools = map[string]any{}
				literal["tools"] = tools
			}
			tools["skills"] = map[string]any{"policies": map[string]string{"weather": own}}
			raw, err = json.Marshal(literal)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			require.Equal(t, map[string]any{"weather": own}, agentrecord.Policies(t, home, id, agentrecord.Skills),
				"the Given is planted at the carrier the store keeps, not in a place nothing reads")
		}
		persisted, err := store.Get(id)
		require.NoError(t, err)
		require.NotEmpty(t, persisted.SkillGrants["weather"], "actual generation grant, not a name-only allowlist")
		cfg.Agents.List = append(cfg.Agents.List, *persisted)
	}
	// Store the independent raw Given even while today's config type ignores
	// the new map; runtime retention is separately checked below, never assumed.
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var literal map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &literal))
	var sandbox map[string]any
	require.NoError(t, json.Unmarshal(literal["sandbox"], &sandbox))
	sandbox["skill_policies"] = map[string]string{"weather": global}
	literal["sandbox"], err = json.Marshal(sandbox)
	require.NoError(t, err)
	raw, err = json.Marshal(literal)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	script := &scheduledAllowanceScript{}
	al := mustAgentLoop(t, cfg, msgBus, script)
	for _, id := range []string{"mia", "ray"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		persisted, err := store.Get(id)
		require.NoError(t, err)
		inst.ContextBuilder.WithSkillAllowlist(persisted.Skills).WithSkillBindings(persisted.SkillGrants)
		_, found := inst.ContextBuilder.ResolveSkillFullForWorkspace("", "weather")
		require.True(t, found, "fixture: real weather instructions resolve for %s", id)
	}
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	return &scheduledAllowanceHarness{al: al, api: &restAPI{agentLoop: al, approvalReg: reg, homePath: home, auditor: al.AuditLogger()}, reg: reg, handler: handler, script: script, home: home}
}

func (h *scheduledAllowanceHarness) assertModelResult(t *testing.T, loads bool) {
	t.Helper()
	requests := h.script.captured()
	require.GreaterOrEqual(t, len(requests), 2, "actual tool result must reach a later model request")
	var results []string
	for _, message := range requests[1] {
		if message.Role == "tool" {
			results = append(results, message.Content)
		}
	}
	require.Len(t, results, 1)
	if loads {
		assert.Contains(t, results[0], scheduledWeatherInstructions, "the actual Skill tool returned instructions")
	} else {
		for _, request := range requests {
			raw, err := json.Marshal(request)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), scheduledWeatherInstructions, "refused skill bytes never reach ANY real model request")
		}
	}
}

func (h *scheduledAllowanceHarness) scheduled(t *testing.T, owner string, loads bool) {
	t.Helper()
	h.script.arm()
	meta, err := h.al.GetSessionStore().NewScheduledSession(owner)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = h.al.ProcessScheduled(ctx, owner, meta.ID, "Load weather.", "scheduled", meta.ID)
	require.NoError(t, err)
	require.NoError(t, ctx.Err(), "a fixture deadline is not unattended refusal")
	assert.Empty(t, h.reg.pendingApprovals(), "unattended turns never raise a card")
	h.assertModelResult(t, loads)
	entries, err := h.al.GetSessionStore().ReadTranscript(meta.ID)
	require.NoError(t, err)
	var texts []string
	for _, entry := range entries {
		texts = append(texts, entry.Content)
		for _, call := range entry.ToolCalls {
			if text, ok := call.Result["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	if loads {
		assert.NotContains(t, strings.Join(texts, "\n"), scheduledWeatherRefusal)
	} else {
		assert.Contains(t, texts, scheduledWeatherRefusal, "the exact S-109 refusal is actually persisted")
	}
}

func (h *scheduledAllowanceHarness) pendingFromRealChat(t *testing.T) (*approvalEntry, <-chan error) {
	t.Helper()
	h.script.arm()
	meta, err := h.al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		_, runErr := h.al.ProcessDirectWithChannelAs(ctx, "mia", "Load weather.", "agent:mia:session:"+meta.ID, "webchat", "weather-chat")
		done <- runErr
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("fixture: original interactive turn did not stop")
		}
	})
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if pending := h.reg.pendingApprovals(); len(pending) != 0 {
			require.Len(t, pending, 1)
			require.Equal(t, "mia", pending[0].AgentID)
			require.Equal(t, "Skill", pending[0].ToolName)
			require.Equal(t, map[string]any{"name": "weather"}, pending[0].Args)
			return pending[0], done
		}
		select {
		case runErr := <-done:
			require.NoError(t, runErr, "a real turn error is not the expected permission card")
			h.assertModelResult(t, false)
			t.Fatal("C-26/S-109: the real interactive Skill call completed without the required permission card")
		case <-ctx.Done():
			t.Fatal("UNVERIFIED: the real chat reached neither a card nor completion; deadline is a fixture failure")
		case <-tick.C:
		}
	}
}

func TestTimedAllowance_ReachesScheduledRun(t *testing.T) {
	for _, owner := range []string{"mia", "ray"} {
		t.Run("S-109 before answer "+owner+" really refuses", func(t *testing.T) {
			h := newScheduledAllowanceHarness(t, "ask", "")
			h.scheduled(t, owner, false)
		})
		t.Run("instrument granted Allow really loads for "+owner, func(t *testing.T) {
			h := newScheduledAllowanceHarness(t, "allow", "")
			h.scheduled(t, owner, true)
		})
	}
	for _, own := range []string{"", "ask"} {
		t.Run("S-109 actual chat HTTP answer reaches scheduled calls own="+own, func(t *testing.T) {
			h := newScheduledAllowanceHarness(t, "ask", own)
			entry, done := h.pendingFromRealChat(t)
			snap := h.handler.pendingApprovalSnapshot(entry)
			// C-24 permission-only skill card; unlike an ordinary tool
			// card, this new card's specified actions do not include Cancel.
			assert.Equal(t, []string{"allow_once", "allow_for", "deny"}, snap.OfferedActions)
			assert.Equal(t, []string{"1h", "3h", "1d"}, snap.AllowDurations)
			nativebinding.Require(t, "pkg/agent", injectScheduledAllowanceClock,
				"C-26/S-109: named loop-to-ApprovalGrantStore wall/monotonic input; actual card/handler/store are not replaceable")
			clock := &scheduledAllowanceClock{wall: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
			injectScheduledAllowanceClock(h.al, clock.wallNow, clock.elapsedNow)
			before := h.storedPolicyBytes(t)
			w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"1d"}`)
			require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
			var response gen.ToolApprovalResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.NotNil(t, response.ExpiresAt)
			require.Equal(t, time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC), response.ExpiresAt.UTC())
			require.Equal(t, boolPtr(true), response.GrantRecorded)
			require.NoError(t, <-done, "the original waiting call is released")
			h.assertModelResult(t, true)
			h.scheduled(t, "mia", true)
			h.scheduled(t, "ray", false)
			assert.Equal(t, before, h.storedPolicyBytes(t), "no global/agent policy is persisted by a card answer")
			t.Run("actual audit subject and decision", func(t *testing.T) {
				h.assertAllowanceAudit(t, entry.ApprovalID, response.ExpiresAt.UTC())
			})
			// An absent audit carrier must fail loudly without hiding the
			// independent scheduled-expiry assertions that follow.
			clock.advance(24*time.Hour - time.Nanosecond)
			h.scheduled(t, "mia", true)
			clock.advance(time.Nanosecond)
			h.scheduled(t, "mia", false)
			clock.advance(time.Nanosecond)
			h.scheduled(t, "mia", false)
		})
	}
	t.Run("C-26 runtime retains the literal global and own skill permissions", func(t *testing.T) {
		h := newScheduledAllowanceHarness(t, "ask", "ask")
		raw, err := json.Marshal(h.al.GetConfig().Sandbox)
		require.NoError(t, err)
		var runtime map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &runtime))
		if len(runtime["skill_policies"]) == 0 {
			t.Fatal("BLOCKED: sandbox.skill_policies is not implemented — required by C-2/C-26/S-109; the independent raw disk Given above must not certify an ignored runtime field")
		}
		var policies map[string]string
		require.NoError(t, json.Unmarshal(runtime["skill_policies"], &policies))
		assert.Equal(t, "ask", policies["weather"])
		for _, record := range h.al.GetConfig().Agents.List {
			if record.ID != "mia" {
				continue
			}
			raw, err = json.Marshal(record)
			require.NoError(t, err)
			var entity map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &entity))
			// A marshalled AgentConfig is flat: tools is the agent tool settings member.
			tools := map[string]json.RawMessage{}
			if len(entity["tools"]) != 0 {
				require.NoError(t, json.Unmarshal(entity["tools"], &tools))
			}
			if len(tools["skills"]) == 0 {
				t.Fatal("BLOCKED: agent tools.skills.policies is not implemented — required by C-4/C-26 point 5; a global Ask alone does not prove own-Ask retention")
			}
			var own map[string]map[string]string
			require.NoError(t, json.Unmarshal(tools["skills"], &own))
			assert.Equal(t, "ask", own["policies"]["weather"])
			return
		}
		t.Fatal("fixture: Mia is missing from the actual runtime roster")
	})
	t.Run("S-109 exact verified weather prerequisite", func(t *testing.T) {
		t.Fatal("BLOCKED: C-5 persisted hash-verified install metadata for weather is not implemented — required by the exact S-109 Given; a local trust-Allow runtime probe above is not a fabricated verified install or full S-109 acceptance")
	})
	t.Run("S-113 actual gateway restart retains R1 trust but ends allowance", func(t *testing.T) {
		t.Fatal("BLOCKED: C-24 durable version-approval/combined-card gateway startup wiring is not implemented — required by S-113; must drive trust Ask, notes-helper R1, Mia/Ray own-Deny, real restart, permission-only then R2 combined card. A new store/loop alone is not gateway restart proof")
	})
}

func (h *scheduledAllowanceHarness) storedPolicyBytes(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, name := range []string{"config.json", "entities/agents/mia.json", "entities/agents/ray.json"} {
		raw, err := os.ReadFile(filepath.Join(h.home, name))
		require.NoError(t, err)
		out[name] = raw
	}
	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out["config.json"], &cfg))
	var sandbox map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cfg["sandbox"], &sandbox))
	var policies map[string]string
	require.NoError(t, json.Unmarshal(sandbox["skill_policies"], &policies))
	require.Equal(t, "ask", policies["weather"], "independent raw global policy remains Ask")
	return out
}

func (h *scheduledAllowanceHarness) assertAllowanceAudit(t *testing.T, approvalID string, end time.Time) {
	t.Helper()
	rows := readAllAuditEntries(t, filepath.Join(h.home, "system"))
	var matching []map[string]any
	for _, row := range rows {
		details, _ := row["details"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskGranted && details["approval_id"] == approvalID {
			matching = append(matching, details)
		}
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "sandbox.skill_policies", "S-109: no stored permission-change audit")
	}
	require.Len(t, matching, 1, "one real approval-decision entry for the actual card")
	assert.Equal(t, "mia", matching[0]["agent_id"])
	assert.Equal(t, "admin", matching[0]["approver_user_id"])
	assert.Equal(t, "allow_for", matching[0]["action"])
	assert.Equal(t, "1d", matching[0]["duration"])
	assert.Equal(t, end.Format(time.RFC3339), matching[0]["expires_at"])
	t.Fatal("BLOCKED: C-26 skill-subject audit encoding is not implemented — required by S-109; reconcile this observer to the actual recorded skill key before claiming exact subject verification, never guess a JSON member")
}
