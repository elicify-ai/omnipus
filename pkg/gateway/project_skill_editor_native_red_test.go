//go:build goolm && stdjson

package gateway

// CHECK F25 / D14#10 / C-11 editor exception and C-25. Only the external model
// is scripted. Real read_file -> Skill -> bash calls run through the native loop
// on one actual persisted project mount. The permitted editor read includes the
// sentinel; only instruction injection, successful Skill load state/audit and
// helper authority must remain absent. No decision-returning adapter. Native
// kernel/platform certification remains UNVERIFIED here, mapped to ST-28.
// GREEN, mutation and proof-of-failability defer to independent CHECK.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const f25ProjectBody = "---\nname: notes\ndescription: Project notes for editor isolation.\n---\n\nF25-PROJECT-INSTRUCTIONS-8Q\n"
const f25Readme = "F25-ORDINARY-README-3K\n"

type f25NativeModel struct {
	mu       sync.Mutex
	steps    []providers.ToolCall
	requests [][]providers.Message
}

func (*f25NativeModel) GetDefaultModel() string { return "native-consent-script" }
func (p *f25NativeModel) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	var frozen []providers.Message
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	index := len(p.requests)
	p.requests = append(p.requests, frozen)
	if index < len(p.steps) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{p.steps[index]}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *f25NativeModel) snapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

func TestProjectSkillEditor_Native(t *testing.T) {
	t.Run("instrument real allowed load and exact read are visible to independent observers", func(t *testing.T) {
		runF25NativeEditor(t, "allow", false)
	})
	t.Run("D14#10 editor read returns all bytes but Deny still stops actual load and helper", func(t *testing.T) {
		runF25NativeEditor(t, "deny", true)
	})
}

func runF25NativeEditor(t *testing.T, permission string, editor bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("UNVERIFIED: Unix native-helper fixture does not certify Windows; independent ST-28 lane required")
	}
	h, closeLoop := newNativeD10Harness(t, "allow", "allow")
	require.NoError(t, os.RemoveAll(filepath.Join(h.api.homePath, "skills", "notes-helper")))
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(root, ".claude", "skills", "notes")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	file, readme := filepath.Join(dir, "SKILL.md"), filepath.Join(root, "README.md")
	require.NoError(t, os.WriteFile(file, []byte(f25ProjectBody), 0o600))
	require.NoError(t, os.WriteFile(readme, []byte(f25Readme), 0o600))
	require.NoError(t, workspace.SaveRecord(h.api.homePath, workspace.Workspace{ID: "f25", Name: "F25 native project", Status: "active", CoreTeam: []string{"test-agent"}}))
	work, err := workspace.EnsureWorkDir(h.api.homePath, "f25")
	require.NoError(t, err)
	_, warning, err := workspace.CreateMount(h.api.homePath, "f25", "project", root)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, []string{root}, workspace.AllowedMountRoots(h.api.homePath, "f25"))
	effect, helper := filepath.Join(work, "f25-helper-effect"), filepath.Join(dir, "helper.sh")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'F25-HELPER-RAN\\n' > "+quote(effect)+"\n"), 0o700))
	cfg := h.api.agentLoop.GetConfig()
	cfg.Agents.Defaults.MaxToolIterations = 8 // four real calls and their terminal model round, with headroom.
	for _, name := range []string{"Skill", "read_file", "bash", "edit_skill"} {
		cfg.Sandbox.ToolPolicies[name] = "allow"
	}
	_, err = agentstore.New(h.api.homePath).Update("test-agent", func(a *config.AgentConfig) error {
		// The shared custom-agent seed carries its own ordinary tool limits.
		// Open both stored layers for the three attempted tools so the test
		// isolates project skill authority, not an unrelated own-tool Deny.
		for _, name := range []string{"Skill", "read_file", "bash"} {
			a.Tools.Builtin.Policies[name] = config.ToolPolicyAllow
		}
		a.Tools.Builtin.Policies["edit_skill"] = config.ToolPolicyDeny
		if editor {
			a.Tools.Builtin.Policies["edit_skill"] = config.ToolPolicyAllow
		}
		return nil
	})
	require.NoError(t, err)
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "test-agent" {
			record, err := agentstore.New(h.api.homePath).Get("test-agent")
			require.NoError(t, err)
			cfg.Agents.List[i] = *record
		}
	}
	literal, err := json.Marshal(map[string]any{"skill_policies": map[string]string{"project:f25/notes": permission}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(literal, &cfg.Sandbox))
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	document["sandbox"].(map[string]any)["skill_policies"] = map[string]string{"project:f25/notes": permission}
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(h.api.homePath, "config.json"), raw, 0o600))
	call := func(id, name string, args map[string]any) providers.ToolCall {
		return providers.ToolCall{ID: id, Type: "function", Name: name, Arguments: args}
	}
	model := &f25NativeModel{steps: []providers.ToolCall{
		call("f25-readme", "read_file", map[string]any{"path": readme}),
		call("f25-editor-read", "read_file", map[string]any{"path": file}),
		call("f25-skill-load", "Skill", map[string]any{"name": "notes"}),
	}}
	if editor {
		model.steps = append(model.steps, call("f25-helper", "bash", map[string]any{"command": "/bin/sh " + quote(helper), "timeout_seconds": float64(5)}))
	}
	require.NoError(t, h.api.agentLoop.ReloadProviderAndConfig(context.Background(), model, cfg))
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	for _, name := range []string{"Skill", "read_file", "bash"} {
		tool, found := inst.Tools.GetIncludingHidden(name)
		require.True(t, found, "actual native tool %s is wired", name)
		require.Equal(t, "allow", tools.EffectiveToolPolicy(inst.LoadToolPolicy(), tool.Scope(), inst.AgentType, name), "isolate skill/path policy, not ordinary tool Ask")
	}
	meta, err := h.api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)
	wsID := "f25"
	require.NoError(t, h.api.agentLoop.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wsID}))
	require.NoError(t, h.api.agentLoop.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, AgentID: "test-agent", OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat}}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A real web-chat inbound carries SessionID; ProcessDirectWithChannelAs
	// is the CLI-style helper and drops that input, so it cannot select f25.
	runDone := make(chan error, 1)
	go func() { runDone <- h.api.agentLoop.Run(ctx) }()
	var stopOnce sync.Once
	stopRun := func() {
		stopOnce.Do(func() {
			cancel()
			require.NoError(t, <-runDone, "owned native dispatcher joins")
			h.api.agentLoop.WaitForActiveRequests()
		})
	}
	t.Cleanup(stopRun)
	for _, prompt := range []string{
		"Read for editing, then try loading and running the helper.",
		"Report completion without another tool call.",
	} {
		require.NoError(t, h.handler.msgBus.PublishInbound(ctx, bus.InboundMessage{
			Channel: "webchat", ChatID: "f25-chat", SessionID: meta.ID,
			SessionKey: "agent:test-agent:session:" + meta.ID,
			Sender:     bus.SenderInfo{CanonicalID: "f25-person"},
			Content:    prompt, UserInitiated: true,
			Metadata: map[string]string{"agent_id": "test-agent", "workspace_id": wsID},
		}))
		select {
		case reply := <-h.handler.msgBus.OutboundChan():
			require.Equal(t, meta.ID, reply.SessionID)
			require.Equal(t, "done", reply.Content, "real native turn completed")
		case <-ctx.Done():
			t.Fatal("fixture UNVERIFIED: native web-chat turn did not complete; deadline is not refusal")
		}
	}
	// File history may remain in the real second turn; it is not injection.
	stopRun()
	requests := model.snapshot()
	require.GreaterOrEqual(t, len(requests), len(model.steps)+2, "every real result plus the next-turn request was captured")
	results := map[string]string{}
	for _, request := range requests {
		for _, message := range request {
			if message.Role == "tool" {
				if old, found := results[message.ToolCallID]; found {
					assert.Equal(t, old, message.Content)
				}
				results[message.ToolCallID] = message.Content
				continue // permitted raw file bytes are not skill instruction injection.
			}
			if editor {
				assert.NotContains(t, message.Content, "F25-PROJECT-INSTRUCTIONS-8Q", "no instruction injection, distinct from editor-read tool output")
				for _, part := range message.SystemParts {
					assert.NotContains(t, part.Text, "F25-PROJECT-INSTRUCTIONS-8Q")
				}
			}
		}
	}
	assertF25FileBody(t, results["f25-readme"], f25Readme)
	assertF25FileBody(t, results["f25-editor-read"], f25ProjectBody)
	if editor {
		var refusal gen.PermissionDenied
		if assert.NoError(t, json.Unmarshal([]byte(results["f25-skill-load"]), &refusal), "actual attempted Skill call is refused, never inferred from the read") {
			assert.Equal(t, "permission_denied", refusal.Error)
			assert.Equal(t, "Skill", refusal.Tool)
			assert.Contains(t, refusal.Reason+"\n"+refusal.Message, "Deny")
		}
		assert.NotContains(t, results["f25-skill-load"], "F25-PROJECT-INSTRUCTIONS-8Q")
		require.Contains(t, results, "f25-helper", "actually attempted helper call")
		assert.Contains(t, results["f25-helper"], "Deny")
		_, err := os.Stat(effect)
		assert.ErrorIs(t, err, os.ErrNotExist, "editor read added no helper authority/effect")
	} else {
		assert.Contains(t, results["f25-skill-load"], "F25-PROJECT-INSTRUCTIONS-8Q", "instrument: actual Skill load is distinguishable from raw read")
	}
	assert.Empty(t, h.reg.pendingApprovals(), "Deny never asks")
	closeLoop() // joins writers and flushes the actual audit log before scanning.
	fileReads, loads := 0, 0
	for _, record := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
		if record["event"] == audit.EventFileOp {
			details, _ := record["details"].(map[string]any) // classic audit.Entry, not consent's audit.Record.
			if record["agent_id"] == "test-agent" && details["op"] == "read" && details["path"] == file {
				fileReads++
			}
		}
		if record["event"] == audit.EventSkillCall {
			details, _ := record["details"].(map[string]any)
			if record["agent_id"] == "test-agent" && details["slug"] == "notes" && details["mode"] == "load" && details["outcome"] == "loaded" {
				loads++
			}
		}
	}
	assert.Equal(t, 1, fileReads, "actual permitted read audit is a positive record-scanner control")
	wantLoads := 1
	if editor {
		wantLoads = 0
	}
	assert.Equal(t, wantLoads, loads, "actual native loaded-skill state marker; the same observer sees one in the allow instrument")
	assert.NoFileExists(t, trustApprovalsPath(h.api), "editor read is not consent")
}

func assertF25FileBody(t *testing.T, actual, literal string) {
	t.Helper()
	// PromptGuard::wrapUntrusted adds these delimiters to external file tool
	// results. Remove only that real envelope, then read_file's metadata header;
	// no trimming or filtering of the literal file bytes/sentinel is allowed.
	require.True(t, strings.HasPrefix(actual, "[UNTRUSTED_CONTENT]\n"))
	require.True(t, strings.HasSuffix(actual, "\n[/UNTRUSTED_CONTENT]"))
	actual = strings.TrimSuffix(strings.TrimPrefix(actual, "[UNTRUSTED_CONTENT]\n"), "\n[/UNTRUSTED_CONTENT]")
	_, body, ok := strings.Cut(actual, "\n\n")
	require.True(t, ok, "actual read_file carrier must have its header and complete body: %q", actual)
	assert.Equal(t, literal, body)
}
