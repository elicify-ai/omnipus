package gateway

// F1 oracle: QA4 dispatch / pr-test-analyzer F1. Refused Stop must make actual
// REST DELETE fail visibly without losing the original transcript or uploads.
// The root is genuinely admitted through ProcessScheduled; its helper is really
// launched/dispatched. Only the external provider and filesystem fault are
// controlled. Records, execution identities and Stop outcomes are never seeded.
// Additional cancellation entries are allowed; original entries are not lost.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type qa4DeleteProvider struct {
	entered chan context.Context
	release chan struct{}
	once    sync.Once
}

func (p *qa4DeleteProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.entered <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return &providers.LLMResponse{Content: "The held external request was released."}, nil
	}
}

func (*qa4DeleteProvider) GetDefaultModel() string { return "qa4-delete-provider" }
func (p *qa4DeleteProvider) open()                 { p.once.Do(func() { close(p.release) }) }

// not-wire-format: in-memory test fixture, never serialized.
type qa4DeleteFixture struct {
	api       *restAPI
	rootID    string
	childID   string
	rootCtx   context.Context
	childCtx  context.Context
	originals map[string][]session.TranscriptEntry
	uploads   map[string][]byte
}

func qa4SeedOriginalEntries(t *testing.T, store *session.UnifiedStore, id string) []session.TranscriptEntry {
	t.Helper()
	entries := []session.TranscriptEntry{
		{ID: "qa4-original-question-" + id, Type: session.EntryTypeMessage, Role: "user", AgentID: "mia", Content: "Keep the original customer request.", Timestamp: time.Now().UTC()},
		{ID: "qa4-original-answer-" + id, Type: session.EntryTypeMessage, Role: "assistant", AgentID: "mia", Content: "Keep the original saved answer.", Timestamp: time.Now().UTC()},
	}
	for _, entry := range entries {
		require.NoError(t, store.AppendTranscript(id, entry))
	}
	return entries
}

func qa4NewRefusedDeleteFixture(t *testing.T) *qa4DeleteFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), DefaultModel: config.DefaultModel{Model: "qa4-delete-provider"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCustom}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 2, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	p := &qa4DeleteProvider{entered: make(chan context.Context, 2), release: make(chan struct{})}
	al := mustAgentLoop(t, cfg, msgBus, p)
	ls := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), ls)
	boot := session.NewBootEpochStore(home)
	_, err := boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(ls, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(ls, al.GetSessionStore())), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(testHarnessWorkspaceMembershipID)}))
	originals := map[string][]session.TranscriptEntry{meta.ID: qa4SeedOriginalEntries(t, store, meta.ID)}
	rootDone := make(chan struct{})
	go func() {
		defer close(rootDone)
		_, _ = al.ProcessScheduled(context.Background(), "mia", meta.ID, "Wait for the external request", "webchat", meta.ID)
	}()
	t.Cleanup(func() {
		p.open()
		select {
		case <-rootDone:
		case <-time.After(5 * time.Second):
			t.Error("released real root and its owning disposition did not join")
		}
		// mustAgentLoop's later Close cleanup drains the real helper producer,
		// including its disposition, before its stores and temp directory close.
	})
	awaitProvider := func() context.Context {
		select {
		case ctx := <-p.entered:
			require.NoError(t, ctx.Err(), "SETUP: the external provider must be live")
			return ctx
		case <-time.After(5 * time.Second):
			t.Fatal("SETUP: real execution never entered the held external provider")
			return nil
		}
	}
	rootCtx := awaitProvider()
	launcher := agent.NewSteerLauncher(al)
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID, TargetAgentID: "mia", Task: "Hold the helper's external request", Origin: steer.Origin{Kind: steer.OriginKindDelegate},
	})
	require.NoError(t, err)
	dispatched, err := launcher.Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State)
	childCtx := awaitProvider()
	originals[launched.SessionID] = qa4SeedOriginalEntries(t, store, launched.SessionID)
	uploads := make(map[string][]byte)
	for _, id := range []string{meta.ID, launched.SessionID} {
		dir := u18SeedUploads(t, id)
		path := filepath.Join(dir, "upload.bin")
		payload := []byte("original upload belonging to " + id + "\x00\xff")
		require.NoError(t, os.WriteFile(path, payload, 0o600))
		uploads[path] = payload
		before, readErr := store.ReadTranscript(id)
		require.NoError(t, readErr)
		for _, original := range originals[id] {
			require.Contains(t, before, original, "SETUP: original transcript must actually be on disk before DELETE")
		}
		rec, loadErr := ls.Load(id)
		require.NoError(t, loadErr)
		require.Equal(t, session.LifecycleRunning, rec.State)
		require.NotNil(t, rec.ExecutionID, "SETUP: both held executions must be production-owned")
	}
	return &qa4DeleteFixture{api: &restAPI{agentLoop: al, homePath: home}, rootID: meta.ID, childID: launched.SessionID, rootCtx: rootCtx, childCtx: childCtx, originals: originals, uploads: uploads}
}

func TestQA4Delete_RequiredStopRefusalPreservesOriginalTranscriptsAndUploads(t *testing.T) {
	for _, target := range []string{"root", "live_helper"} {
		t.Run(target, func(t *testing.T) {
			f := qa4NewRefusedDeleteFixture(t)
			failedID, failedCtx := f.rootID, f.rootCtx
			if target == "live_helper" {
				failedID, failedCtx = f.childID, f.childCtx
			}
			ls := f.api.agentLoop.GetSessionLifecycleStore()
			// A directory at a required file is a portable, real filesystem
			// refusal even on privileged runners. No Stop method is replaced.
			faultPath := filepath.Join(ls.Dir(), "controls", failedID+".jsonl")
			require.NoError(t, os.MkdirAll(faultPath, 0o700))
			t.Cleanup(func() {
				assert.NoError(t, os.Remove(faultPath), "restore the required Stop ledger before joining producers")
			})
			probe, openErr := os.OpenFile(faultPath, os.O_RDWR|os.O_APPEND, 0o600)
			if openErr == nil {
				require.NoError(t, probe.Close())
				t.Fatal("BLOCKED: filesystem fault did not refuse the required real Stop file")
			}
			var pathErr *os.PathError
			require.ErrorAs(t, openErr, &pathErr, "instrument must fail at actual filesystem access")
			require.Equal(t, faultPath, pathErr.Path)
			require.NoError(t, failedCtx.Err())

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+f.rootID, nil)
			f.api.HandleSessions(w, r)
			require.Equal(t, http.StatusInternalServerError, w.Code, "required Stop failure must visibly refuse DELETE; body=%s", w.Body.String())
			var envelope generated.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			assert.NotEmpty(t, envelope.Error, "refused DELETE must send a visible error to the caller")
			assert.Contains(t, envelope.Error, failedID, "visible failure must identify the still-live session")
			assert.Contains(t, envelope.Error, "still running", "refused DELETE must explain that Stop did not complete")
			assert.NoError(t, failedCtx.Err(), "DELETE must have encountered an actual still-running refused execution")
			failedRec, err := ls.Load(failedID)
			require.NoError(t, err)
			assert.Equal(t, session.LifecycleRunning, failedRec.State)
			assert.Nil(t, failedRec.Stop, "a refused acceptance cannot claim that it stopped this execution")

			// Reopen storage: cached metadata/transcripts cannot manufacture a
			// false preservation claim after a destructive DELETE regression.
			store := f.api.agentLoop.GetSessionStore()
			reopened, err := session.NewUnifiedStoreWithHome(store.BaseDir(), f.api.homePath)
			require.NoError(t, err)
			for id, originals := range f.originals {
				meta, metaErr := reopened.GetMeta(id)
				require.NoError(t, metaErr, "refused DELETE must preserve the original session identity")
				assert.Equal(t, id, meta.ID)
				entries, readErr := reopened.ReadTranscript(id)
				require.NoError(t, readErr)
				for _, original := range originals {
					assert.Contains(t, entries, original, "refused DELETE lost or rewrote original transcript entry %s", original.ID)
				}
			}
			for path, want := range f.uploads {
				got, readErr := os.ReadFile(path)
				require.NoError(t, readErr, "refused DELETE must preserve root AND descendant uploads")
				assert.Equal(t, want, got, "refused DELETE changed original upload bytes")
			}
		})
	}
}
