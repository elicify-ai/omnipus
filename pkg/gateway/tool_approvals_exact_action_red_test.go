//go:build goolm && stdjson

package gateway

// Order 64 / CHECK F15: C-26 point 9, S-129, D17#21–25.
// Drive real model calls through AgentLoop.Run -> resolveAskPolicy -> the
// production policyApproverAdapter -> approval registry -> HTTP answer.
// Never insert a pending approval from this test. Auto is ON and the global
// policy is Ask; a second variant plants a real standing grant to prove the
// exact-action exclusion wins over both early-release paths.
// Only the paid model and tool side effects (email, installer, browser,
// config mutation and mount) are replaced. Ask resolution, classification,
// offered-action construction, registry, handler and grant store are real.
// C-26's table permits Cancel where the card already has it (spec line 574).
// RED: the offered-action builder is absent; a planted standing grant also
// releases a real call without a card. GREEN/mutation proof is CHECK's job.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	browsertools "github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The new list is absent; a nil binding is loud RED, not an empty-list pass.
var exactActionToolsOf func(t testing.TB) []string

type exactActionRow struct {
	tool, duration, exactKey string
	exactValue               any
	args                     map[string]any
}

type exactActionScript struct {
	row   exactActionRow
	calls atomic.Int64
	armed atomic.Bool
}

func (p *exactActionScript) GetDefaultModel() string { return "exact-action-script" }
func (p *exactActionScript) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	// One scripted action per user call; do not infer turn boundaries from
	// the runtime's reminder/history layout (not the subject of this test).
	if !p.armed.CompareAndSwap(true, false) {
		return &providers.LLMResponse{Content: "done"}, nil
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: fmt.Sprintf("exact-call-%d", p.calls.Add(1)), Type: "function",
		Name: p.row.tool, Arguments: p.row.args,
	}}}, nil
}

// Side-effect sink, not a policy/approval double. Its execution proves the
// real decision path released a call; no test action inserts a card.
type exactActionEffectSink struct {
	tools.Tool // Preserve the real schema, scope, category and description.
	ran        chan map[string]any
}

func (s *exactActionEffectSink) AutoApproveVerdict(ctx context.Context, args map[string]any) tools.AutoVerdict {
	return tools.ClassifyAutoApprove(ctx, s.Name(), s.Tool, args)
}

func (s *exactActionEffectSink) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	s.ran <- args
	return &tools.ToolResult{ForLLM: "effect recorded"}
}

func exactActionTool(t *testing.T, name string) tools.Tool {
	t.Helper()
	// Zero-valued production objects supply only metadata/classification;
	// their external-effect Execute methods are never called.
	switch name {
	case "environment_setup":
		return &tools.EnvironmentSetupTool{}
	case "request_mount":
		return &tools.RequestMountTool{}
	case "send_email":
		return &tools.SendEmailTool{}
	case "browser_evaluate":
		return &browsertools.EvaluateTool{}
	case "set_config":
		return &systools.ConfigSetTool{}
	default:
		t.Fatalf("fixture: no production tool metadata for %q", name)
		return nil
	}
}

type exactActionHarness struct {
	api     *restAPI
	handler *WSHandler
	reg     *approvalRegistryV2
	sink    *exactActionEffectSink
	session string
	events  agent.EventSubscription
	script  *exactActionScript
}

func newExactActionHarness(t *testing.T, row exactActionRow) *exactActionHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "work")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "exact-action-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia"}}
	cfg.Sandbox.AutoApprove = true
	cfg.Sandbox.GodMode = false
	cfg.Sandbox.ToolPolicies[row.tool] = "ask"
	// Make the five scripted calls callable before exercising Ask; discovery
	// is not this test's subject. GREEN replaces this retired manifest
	// fixture with a stored Full level when C-6 lands.
	cfg.Tools.Manifest.Compressed = false
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	script := &exactActionScript{row: row}
	al := mustAgentLoop(t, cfg, msgBus, script)
	sink := &exactActionEffectSink{Tool: exactActionTool(t, row.tool), ran: make(chan map[string]any, 8)}
	al.RegisterTool(sink)
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	ceiling := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
	for name, policy := range cfg.Sandbox.ToolPolicies {
		ceiling[name] = config.ToolPolicy(policy)
	}
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: ceiling})
	require.Equal(t, "ask", tools.EffectiveToolPolicy(inst.LoadToolPolicy(), sink.Scope(), inst.AgentType, row.tool),
		"fixture: the scripted tool resolves to global Ask")
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	require.True(t, al.SessionAutoApprove(meta.ID), "fixture: Auto is ON")
	events := al.SubscribeEvents(128)
	t.Cleanup(func() { al.UnsubscribeEvents(events.ID) })
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("approval integration loop did not shut down")
		}
		handler.Wait()
	})
	return &exactActionHarness{
		api:     &restAPI{agentLoop: al, approvalReg: reg, homePath: home},
		handler: handler, reg: reg, sink: sink, session: meta.ID, events: events, script: script,
	}
}

func (h *exactActionHarness) call(t *testing.T, callID string) {
	t.Helper()
	h.script.armed.Store(true)
	h.handler.handleChatMessageWithClientID(context.Background(), "exact-chat", h.session,
		"perform the exact action", "mia", nil, "", "", callID, boolPtr(true), makeTestConn())
}

// Poll only for admission (not for an expected verdict). A bypass is
// observed immediately at the real side-effect boundary, not as a timeout.
func (h *exactActionHarness) pending(t *testing.T) *approvalEntry {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if entries := h.reg.pendingApprovals(); len(entries) > 0 {
			require.Len(t, entries, 1, "one pending card for the one actual call")
			return entries[0]
		}
		select {
		case args := <-h.sink.ran:
			t.Fatalf("S-129: exact action executed without a card under Auto/standing grant: %v", args)
		case <-timer.C:
			t.Fatal("S-129: the real call reached neither approval nor execution; fixture/entry path UNVERIFIED")
		case <-tick.C:
		}
	}
}

func (h *exactActionHarness) finish(t *testing.T, entry *approvalEntry) {
	t.Helper()
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_once"}`)
	require.Equal(t, http.StatusOK, w.Code, "Once releases the actual pending call: %s", w.Body.String())
	select {
	case got := <-h.sink.ran:
		require.Equal(t, entry.Args, got)
	case <-time.After(5 * time.Second):
		t.Fatal("approved call did not reach the side-effect boundary")
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e, open := <-h.events.C:
			require.True(t, open, "fixture: event subscription closed before turn completion")
			if end, ok := e.Payload.(agent.TurnEndPayload); ok && end.SessionID == h.session {
				require.Equal(t, agent.TurnEndStatusCompleted, end.Status, "the approved real turn completed")
				return
			}
		case <-timer.C:
			t.Fatal("fixture: no completion event after approved execution")
		}
	}
}

func assertExactActionCard(t *testing.T, h *exactActionHarness, entry *approvalEntry, row exactActionRow) {
	t.Helper()
	snap := h.handler.pendingApprovalSnapshot(entry)
	require.Equal(t, row.tool, snap.ToolName)
	require.Equal(t, row.args, snap.Args, "S-129: the card shows the complete exact action")
	require.Equal(t, row.exactValue, snap.Args[row.exactKey], "D17: exact command/path/recipient/script/key")
	assert.Equal(t, []string{"allow_once", "deny", "cancel"}, snap.OfferedActions, "C-26: exact one-time choices")
	assert.Empty(t, snap.AllowDurations, "C-26: exact-action cards offer no duration")
}

func TestToolApprovals_ExactActionNoTimeChoice(t *testing.T) {
	t.Run("C-26 exact-action list contains exactly five names", func(t *testing.T) {
		if exactActionToolsOf == nil {
			t.Fatal("BLOCKED: ExactActionTools is not implemented — required by C-26 point 9 and D17#21–25")
		}
		require.ElementsMatch(t, []string{"browser_evaluate", "environment_setup", "request_mount", "send_email", "set_config"}, exactActionToolsOf(t))
	})
	rows := []exactActionRow{
		{"environment_setup", "1d", "command", "apt-get install -y poppler-utils", map[string]any{"command": "apt-get install -y poppler-utils"}},
		{"request_mount", "1h", "path", "/home/p/Projects", map[string]any{"path": "/home/p/Projects", "reason": "Edit the project files."}},
		{"send_email", "3h", "to", []any{"person@example.com"}, map[string]any{"to": []any{"person@example.com"}, "subject": "Quarterly report", "body": "Hello."}},
		{"browser_evaluate", "1h", "js", "document.title", map[string]any{"js": "document.title"}},
		{"set_config", "1d", "key", "gateway.preview_enabled", map[string]any{"key": "gateway.preview_enabled", "value": true}},
	}
	for _, row := range rows {
		t.Run(row.tool+"/Auto on, rejected time choice writes no allowance, Once releases only one call", func(t *testing.T) {
			h := newExactActionHarness(t, row)
			h.call(t, "first")
			entry := h.pending(t)
			assertExactActionCard(t, h, entry, row)
			grants := h.api.agentLoop.ApprovalGrants()
			require.False(t, grants.IsAllowed(h.session, "mia", row.tool, row.args))
			body, err := json.Marshal(map[string]string{"action": "allow_for", "duration": row.duration})
			require.NoError(t, err)
			w := postToolApprovalRaw(t, h.api, entry.ApprovalID, string(body))
			assert.Equal(t, http.StatusConflict, w.Code, "D17 rejected time choice: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "This choice is not available for this request.")
			require.False(t, grants.IsAllowed(h.session, "mia", row.tool, row.args), "no actual store entry after rejected allow_for")
			require.False(t, grants.IsAllowed(h.session, "mia", row.tool, nil), "no whole-tool allowance either")
			h.finish(t, entry)
			require.False(t, grants.IsAllowed(h.session, "mia", row.tool, row.args), "Approve Once stores no allowance")
			h.call(t, "second")
			again := h.pending(t)
			require.NotEqual(t, entry.ApprovalID, again.ApprovalID, "same action raises a NEW card")
			require.NotEqual(t, entry.ToolCallID, again.ToolCallID)
			assertExactActionCard(t, h, again, row)
			h.finish(t, again)
		})
		t.Run(row.tool+"/a standing-grant attempt cannot release the call", func(t *testing.T) {
			h := newExactActionHarness(t, row)
			grants := h.api.agentLoop.ApprovalGrants()
			// Exercise the real legacy writer without requiring the new store
			// to accept a forbidden grant. Whether rejected at creation or
			// ignored by resolution, the real next call MUST show a card.
			recorded := grants.Record(h.session, "mia", row.tool, row.args)
			t.Logf("legacy grant attempt recorded=%t, lookup=%t", recorded, grants.IsAllowed(h.session, "mia", row.tool, row.args))
			h.call(t, "with-standing-grant")
			entry := h.pending(t)
			assertExactActionCard(t, h, entry, row)
			h.finish(t, entry)
		})
	}
}
