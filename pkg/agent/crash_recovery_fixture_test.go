//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracle: tests/plans/crash-recovery.md and DESIGN.md Decision 1–5. Only
// the external model is scripted. The real loop, tools, admission and stores run.
type crashScript struct {
	mu       sync.Mutex
	requests [][]providers.Message
	next     func(int, []providers.Message) (*providers.LLMResponse, error)
	entered  chan int
	gate     <-chan struct{}
}

func (p *crashScript) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	i := len(p.requests)
	copyMessages := cloneProviderMessages(messages)
	p.requests = append(p.requests, copyMessages)
	p.mu.Unlock()
	if p.entered != nil {
		p.entered <- i
	}
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.next != nil {
		return p.next(i, copyMessages)
	}
	return &providers.LLMResponse{Content: "new work finished", FinishReason: "stop"}, nil
}
func (*crashScript) GetDefaultModel() string { return "crash-recovery-script" }
func (p *crashScript) calls() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]providers.Message, len(p.requests))
	for i := range out {
		out[i] = cloneProviderMessages(p.requests[i])
	}
	return out
}

func crashAwait[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("fixture barrier was not reached (not a behavioural RED oracle)")
		var zero T
		return zero
	}
}

func crashAwaitDone(t *testing.T, c <-chan struct{}) { t.Helper(); crashAwait(t, c) }

type crashHarness struct {
	home string
	al   *AgentLoop
	boot *session.BootEpochStore
	p    *crashScript
}

func crashNew(t *testing.T, p *crashScript) *crashHarness {
	t.Helper()
	h := &crashHarness{home: t.TempDir()}
	h.open(t, p)
	t.Cleanup(func() { h.al.Close() })
	return h
}
func (h *crashHarness) open(t *testing.T, p *crashScript) {
	t.Helper()
	t.Setenv("HOME", h.home)
	t.Setenv(config.EnvHome, h.home)
	work := filepath.Join(h.home, "workspace")
	require.NoError(t, os.MkdirAll(work, 0700))
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: work, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 2},
		List:     []config.AgentConfig{{ID: testDefaultAgentID, Home: work}},
	}, Sandbox: config.OmnipusSandboxConfig{AuditLog: true}}
	h.al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), p)
	h.al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(h.home, "session_messages")), session.NewLifecycleStore(filepath.Join(h.home, "session_lifecycle")))
	h.boot = mintGenuineBootEpochForLoop(t, h.al)
	pwWireRuntime(t, h.al)
	h.p = p
	inst := h.al.GetRegistry().primaryChatAgentForTest()
	inst.Tools.RegisterReplacing(tools.NewReadFileTool(work, true, tools.MaxReadFileSize))
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.Tools.RegisterReplacing(tools.NewWriteFileTool(work, true))
	inst.Tools.RegisterReplacing(tools.NewEditFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"read_file": config.ToolPolicyAllow, "append_file": config.ToolPolicyAllow, "write_file": config.ToolPolicyAllow, "edit_file": config.ToolPolicyAllow, "send_message": config.ToolPolicyAllow,
	}})
}
func (h *crashHarness) recover(t *testing.T) {
	t.Helper()
	r := &SteerBootRecovery{Lifecycle: h.al.GetSessionLifecycleStore(), Sessions: h.al.GetSessionStore(), Inbox: h.al.GetMessageInboxStore(), BootEpoch: h.boot,
		Classifier: NewSteerRecordClassifier(h.al.GetSessionLifecycleStore(), h.al.GetSessionStore()), Deliverer: h.al.getUpwardDeliverer()}
	require.NoError(t, r.Run(context.Background()), "run real boot recovery")
}
func (h *crashHarness) reboot(t *testing.T, p *crashScript) {
	t.Helper()
	h.al.Close()
	h.open(t, p)
	h.recover(t)
}
func (h *crashHarness) human(t *testing.T, id, content string) (string, error) {
	t.Helper()
	out, _, err := h.al.processMessage(context.Background(), bus.InboundMessage{Channel: "webchat", ChatID: id, SessionID: id,
		Sender: bus.SenderInfo{CanonicalID: "owner"}, GatewayUserID: "owner", UserInitiated: true, OperatorPrompt: true, Content: content,
		Metadata: map[string]string{"agent_id": testDefaultAgentID, "workspace_id": testHarnessWorkspaceMembershipID}})
	return out, err
}
func (h *crashHarness) record(t *testing.T, id string) *session.LifecycleRecord {
	t.Helper()
	r, err := session.NewLifecycleStore(h.al.GetSessionLifecycleStore().Dir()).Load(id)
	require.NoError(t, err)
	return r
}
func crashFields(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}
func crashCall(id, name string, args map[string]any) providers.ToolCall {
	return providers.ToolCall{ID: id, Name: name, Arguments: args}
}
func crashAppend(t *testing.T, h *crashHarness, id string, m providers.Message) {
	t.Helper()
	_, err := appendWindowMsg(h.al.GetSessionStore(), context.Background(), id, m)
	require.NoError(t, err, "persist real addressed model entry")
}
func crashChild(t *testing.T, h *crashHarness, p *crashScript) (*session.LifecycleRecord, *executionDisposition) {
	t.Helper()
	parent := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	launched, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID,
		Task: "perform the requested file work", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "crash-child"}})
	require.NoError(t, err)
	result, err := NewSteerLauncher(h.al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, result.State)
	require.Equal(t, 0, crashAwait(t, p.entered), "first real provider request")
	rec := h.record(t, launched.SessionID)
	owner := h.al.executionDispositionFor(h.al.executionClaimFor(rec))
	require.NotNil(t, owner, "real admitted execution owns a settlement barrier")
	return rec, owner
}

// Snapshot only while the writer is held at a named boundary. Let the original
// writer settle, close it, then restore the captured bytes at the SAME temp-home
// path. This models a kill without a second test process or surviving old writer,
// and preserves target paths/witness identities across the new boot.
func crashCopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, e := os.Readlink(path)
			if e != nil {
				return e
			}
			// Restore preserves directory identities and may retain an unchanged
			// mount symlink. Reuse that exact link rather than failing with EEXIST.
			if existing, err := os.Readlink(target); err == nil && existing == link {
				return nil
			}
			return os.Symlink(link, target)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("snapshot refuses nonregular fixture file %s", path)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0600)
	})
}

// Restore file bytes while keeping the existing directory identities. A real
// process restart does not replace the workspace directory: removing/recreating
// it would manufacture a changed-anchor Unknown in every valid-witness probe.
func crashRestoreTree(src, dst string) error {
	if err := filepath.WalkDir(dst, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dst, path)
		if err != nil || rel == "." {
			return err
		}
		original, statErr := os.Lstat(filepath.Join(src, rel))
		if os.IsNotExist(statErr) || (statErr == nil && original.IsDir() != d.IsDir()) {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return statErr
	}); err != nil {
		return err
	}
	return crashCopyTree(src, dst)
}

func (h *crashHarness) snapshot(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	require.NoError(t, crashCopyTree(h.home, dst))
	return dst
}
func (h *crashHarness) restore(t *testing.T, snapshot string, p *crashScript) {
	t.Helper()
	h.al.Close()
	require.NoError(t, crashRestoreTree(snapshot, h.home))
	h.open(t, p)
}
