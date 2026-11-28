package gateway

// Real task tools, REST writes, stores, Board start and TaskExecutor.
// Only an unknown actual task session's paid-model response is scripted.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

type programsTaskProvider struct {
	*programsPeerProvider
	al       *agent.AgentLoop
	dev, ops string
	work     string
}

func (p *programsTaskProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	if sid != p.dev && sid != p.ops {
		meta, err := p.al.GetSessionStore().GetMeta(sid)
		if err != nil || meta == nil || meta.TaskID == "" {
			return nil, fmt.Errorf("task provider fixture: real receiving session has no task identity: %w", err)
		}
		p.mu.Lock()
		if _, scripted := p.scripts[sid]; !scripted {
			command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROGRAM_TASK_CHILD_RAN'", filepath.Join(p.work, sid+"-task-count"))
			p.scripts[sid] = []providers.LLMResponse{
				{ToolCalls: []providers.ToolCall{{ID: "task-count", Type: "function", Name: "bash", Arguments: map[string]any{"command": command}}}},
				{ToolCalls: []providers.ToolCall{{ID: "task-probe-finish", Type: "function", Name: "goal_claim", Arguments: map[string]any{"status": "blocked", "evidence": "S1 counted-child fixture finished; no paid Judge needed"}}}},
				wu5StopGW("counted task probe finished"),
			}
		}
		p.mu.Unlock()
	}
	return p.programsPeerProvider.Chat(ctx, messages, definitions, model, options)
}

type programsTaskFixture struct {
	*programsPeerFixture
	api *restAPI
}

func newProgramsTaskFixture(t *testing.T) *programsTaskFixture {
	t.Helper()
	f := newProgramsPeerFixture(t)
	require.NoError(t, workspace.SaveDelegation(f.home, "sales", []workspace.DelegationEdge{{FromAgent: "dev", ToAgent: "ops"}}))
	p := &programsTaskProvider{programsPeerProvider: f.provider, al: f.al, dev: f.dev, ops: f.ops, work: f.work}
	for _, id := range []string{"dev", "ops"} {
		inst, ok := f.al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		inst.Provider = p
	}
	te := agent.GetTaskExecutor(f.al)
	require.NotNil(t, te)
	t.Cleanup(func() { te.Drain(10 * time.Second) })
	api := &restAPI{agentLoop: f.al, homePath: f.home, taskStore: agent.GetTaskStore(f.al), taskLock: task.TaskFileLock, taskExecutor: te}
	x := &programsTaskFixture{programsPeerFixture: f, api: api}
	controlPath := f.scriptCount(f.ops, "task-connected-control")
	require.NoError(t, f.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: f.ops, ChatID: f.ops, Content: "connected Ops control", Provenance: bus.NewProvenance(bus.OriginWeb)}))
	f.waitEnds(t, f.ops)
	control, result := programsPeerCount(t, f, controlPath, "task-connected-control")
	require.Equal(t, "16", control, "instrument: this actual Ops child receives the real checked program before any task runs")
	require.NotContains(t, result, "Connected programs were not used for this command:")
	return x
}

func (x *programsTaskFixture) agentCreate(t *testing.T, call string, input bus.InboundMessage, prompt string) string {
	t.Helper()
	args := map[string]any{"title": "S1 task origin probe", "prompt": prompt, "agent_id": "ops", "workspace_id": "sales", "criteria": []any{map[string]any{"text": "the requested command has actually executed"}}, "dod": []any{map[string]any{"text": "no secret value appears in the output"}}}
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "create_task", Arguments: args}}},
		wu5StopGW("task created"),
	})
	input.SessionID, input.ChatID = x.dev, x.dev
	input.Metadata = map[string]string{"workspace_id": "sales", "agent_id": "dev"}
	require.NoError(t, x.bus.PublishInbound(context.Background(), input))
	x.waitEnds(t, x.dev)
	var receipt map[string]json.RawMessage
	raw := x.provider.result(x.dev, call)
	require.NoError(t, json.Unmarshal([]byte(raw), &receipt), "actual create_task result: %s", raw)
	var id string
	require.NoError(t, json.Unmarshal(receipt["task_id"], &id), "registered create_task must return a real task identity, not a setup/policy refusal: %s", raw)
	require.NotEmpty(t, id)
	created, err := x.api.taskStore.Get(id)
	require.NoError(t, err)
	require.True(t, created.CreatedByAgent("dev"), "instrument: actual agent creator authority, not a UI-seeded task")
	require.Equal(t, prompt, created.Prompt)
	require.Equal(t, "ops", created.AgentID)
	return id
}

// This is the UI's real handler with its already-authenticated person context.
// Authentication itself is outside this writer/dispatch test's boundary.
func (x *programsTaskFixture) personWrite(t *testing.T, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: "s1-person"}))
	w := httptest.NewRecorder()
	x.api.HandleTasks(w, r)
	return w
}

func (x *programsTaskFixture) personCreate(t *testing.T, prompt string) string {
	t.Helper()
	body := fmt.Sprintf(`{"title":"S1 UI task origin probe","prompt":%q,"action":"llm","agent_id":"ops","workspace_id":"sales","criteria":%s,"dod":%s}`, prompt, validCriteriaJSON, validDoDJSON)
	var request gen.TaskCreateRequest
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	w := x.personWrite(t, http.MethodPost, "/api/v1/tasks", request)
	require.Equal(t, http.StatusCreated, w.Code, "actual person's create: %s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	stored, err := x.api.taskStore.Get(created.Id)
	require.NoError(t, err)
	require.False(t, stored.CreatedByAgent("dev"), "instrument: actual UI creation is not attributed to Dev")
	require.Equal(t, "s1-person", stored.CreatedBy)
	require.Equal(t, prompt, stored.Prompt)
	return created.Id
}

func (x *programsTaskFixture) runBoard(t *testing.T, id string) (string, string, string) {
	t.Helper()
	before, err := x.api.taskStore.Get(id)
	require.NoError(t, err)
	if before.Status == task.StatusInbox {
		w := x.personWrite(t, http.MethodPatch, "/api/v1/tasks/"+id, map[string]any{"status": "next", "description": "ready for the counted command"})
		require.Equal(t, http.StatusOK, w.Code, "actual UI triage: %s", w.Body.String())
	}
	w := x.personWrite(t, http.MethodPatch, "/api/v1/tasks/"+id, map[string]any{"status": "in_progress"})
	require.Equal(t, http.StatusOK, w.Code, "actual Board start: %s", w.Body.String())
	var started gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &started))
	current, err := x.api.taskStore.Get(id)
	require.NoError(t, err)
	require.NotEmpty(t, current.SessionID, "instrument: the real executor bound a task session")
	sid := current.SessionID
	x.waitEnds(t, sid)
	require.Eventually(t, func() bool {
		latest, readErr := x.api.taskStore.Get(id)
		return readErr == nil && task.IsTerminal(latest.Status)
	}, 15*time.Second, 10*time.Millisecond, "the controlled blocked claim settles this real task; no paid Judge is required")
	result := x.provider.result(sid, "task-count")
	require.Contains(t, result, "PROGRAM_TASK_CHILD_RAN", "instrument: the actual task child executed; denial or missed dispatch cannot satisfy count0")
	count, err := os.ReadFile(filepath.Join(x.work, sid+"-task-count"))
	require.NoError(t, err)
	return strings.TrimSpace(string(count)), result, sid
}
