package agent

// Oracle: r17 §7.14 / scenarios 21.1–21.5, 21.16–21.19. The real
// delegate → Launch → Dispatch chain, seeded self-row, durable lifecycle,
// admission and completion delivery remain real. Only model HTTP-equivalent
// calls and the existing host-memory signal are controlled process edges.
import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t8HelperTurn struct {
	release  chan struct{}
	once     sync.Once
	messages []providers.Message
}

func (turn *t8HelperTurn) finish() { turn.once.Do(func() { close(turn.release) }) }

type t8HelperProvider struct {
	entered chan *t8HelperTurn
	done    chan struct{}
	once    sync.Once
}

func (p *t8HelperProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	turn := &t8HelperTurn{release: make(chan struct{}), messages: append([]providers.Message(nil), messages...)}
	select {
	case p.entered <- turn:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return &providers.LLMResponse{Content: "T8 helper answer"}, nil
	}
	select {
	case <-turn.release:
		return &providers.LLMResponse{Content: "T8 helper answer"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return &providers.LLMResponse{Content: "T8 helper answer"}, nil
	}
}
func (*t8HelperProvider) GetDefaultModel() string { return "t8-helper-model" }
func (p *t8HelperProvider) close()                { p.once.Do(func() { close(p.done) }) }
func (p *t8HelperProvider) next(t *testing.T) *t8HelperTurn {
	t.Helper()
	select {
	case turn := <-p.entered:
		return turn
	case <-time.After(10 * time.Second):
		t.Fatal("real helper never reached its provider edge")
		return nil
	}
}
func t8SelfDelegateFixture(t *testing.T) (*AgentLoop, *t8HelperProvider, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "t8-helper-model"}
	cfg.Agents.List = nil
	require.True(t, coreagent.SeedConfig(cfg))
	for i := range cfg.Agents.List {
		cfg.Agents.List[i].Model = nil
		cfg.Agents.List[i].FallbackModels = nil
	}
	provider := &t8HelperProvider{entered: make(chan *t8HelperTurn, 20), done: make(chan struct{})}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	mintGenuineBootEpochForLoop(t, al)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	// Match gateway boot: installing stores alone does not wire the real
	// launcher or upward-delivery audience boundary onto the agent's tools.
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())), steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	al.SetSteerSessionLauncher(NewSteerLauncher(al))
	t.Cleanup(func() { al.Close() })
	t.Cleanup(provider.close)
	const ws = "t8-default"
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: ws, Name: "T8 default", Status: "active", IsDefault: true, CoreTeam: []string{"omnipus"}}))
	edges := workspace.SelfEdgeSeedRows([]string{"omnipus"}, nil, cfg.SelfEdgeExcludeAgentIDSet(), delegationDepthBound(cfg.Performance, "T8 fixture seed"))
	require.Len(t, edges, 1, "fresh Omnipus workspace gets its ordinary self-row")
	require.NoError(t, workspace.SaveDelegation(home, ws, edges))
	parent := newTestSteeringSessionOwnedBy(t, al, ws, "omnipus")
	return al, provider, parent
}
func t8DelegateTool(t *testing.T, al *AgentLoop) tools.Tool {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	tool, ok := inst.Tools.Get("delegate")
	require.True(t, ok)
	return tool
}
func t8DelegateCall(t *testing.T, al *AgentLoop, parent, brief string) gen.DelegateSessionResponse {
	t.Helper()
	ctx := tools.WithWorkspaceID(tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), parent), "omnipus"), "t8-default")
	result := t8DelegateTool(t, al).Execute(ctx, map[string]any{"action": "run", "agent_id": "omnipus", "task": brief})
	require.False(t, result.IsError, "real delegate/launcher gate: %s", result.ForLLM)
	var out gen.DelegateSessionResponse
	require.NoError(t, json.NewDecoder(strings.NewReader(result.ForLLM)).Decode(&out), "generated delegate response")
	require.NotEmpty(t, out.SessionId)
	meta, err := al.GetSessionStore().GetMeta(out.SessionId)
	require.NoError(t, err)
	assert.Equal(t, "omnipus", meta.AgentID)
	record, err := al.GetSessionLifecycleStore().Load(out.SessionId)
	require.NoError(t, err)
	require.NotNil(t, record.SteeredBy)
	assert.Equal(t, parent, record.SteeredBy.SteeringSessionID)
	if string(out.State) == "queued" {
		require.NotNil(t, out.QueuePosition)
		assert.Contains(t, result.ForLLM, fmt.Sprintf("queue position %d", *out.QueuePosition), "queued prose agrees with the generated response; the caller asserts the specified exact position")
	}
	return out
}
func t8WaitHelperQueue(t *testing.T, al *AgentLoop, active, queued int) {
	t.Helper()
	waitForGate(t, "real helper admission settlement", func() bool {
		return al.steerAdmission().activeCount() == active && al.steerAdmission().queueLen() == queued
	})
}
func TestOmnipusSelfDelegate_SpawnsChildAndReportsBack(t *testing.T) {
	stubMemory(t, false, true)
	al, provider, parent := t8SelfDelegateFixture(t)
	first := t8DelegateCall(t, al, parent, "T8 child one")
	require.Equal(t, "running", string(first.State))
	one := provider.next(t)
	second := t8DelegateCall(t, al, parent, "T8 child two")
	require.Equal(t, "running", string(second.State))
	two := provider.next(t)
	assert.NotEqual(t, first.SessionId, second.SessionId, "21.2 distinct concurrent copies")
	assert.Equal(t, 2, al.steerAdmission().activeCount())
	one.finish()
	two.finish()
	al.steerAdmission().turns.Wait()
	entries, err := al.GetMessageInboxStore().Entries(parent)
	require.NoError(t, err)
	for _, child := range []string{first.SessionId, second.SessionId} {
		var matches []string
		for _, entry := range entries {
			if entry.Message == nil {
				continue
			}
			raw, err := json.Marshal(entry.Message)
			require.NoError(t, err)
			if strings.Contains(string(raw), child) && strings.Contains(string(raw), "T8 helper answer") {
				matches = append(matches, string(raw))
			}
		}
		assert.Len(t, matches, 1, "21.1/21.2 each child's actual completion arrives once in parent's durable chat inbox")
	}
}
func TestOmnipusSelfDelegate_Refusals(t *testing.T) {
	for _, missingEdge := range []bool{false, true} {
		name := "21.3 helper depth"
		if missingEdge {
			name = "21.5 workspace without self-row"
		}
		t.Run(name, func(t *testing.T) {
			stubMemory(t, false, true)
			al, provider, parent := t8SelfDelegateFixture(t)
			caller := parent
			if !missingEdge {
				child := t8DelegateCall(t, al, parent, "depth-one helper")
				require.Equal(t, "running", string(child.State))
				provider.next(t)
				caller = child.SessionId
			}
			ctx := tools.WithWorkspaceID(tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), caller), "omnipus"), "t8-default")
			if missingEdge {
				require.NoError(t, workspace.SaveDelegation(config.OmnipusHomeDir(), "t8-default", nil))
			} else {
				ctx = tools.WithDelegationDepth(ctx, 1)
			}
			before, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
			require.NoError(t, err)
			result := t8DelegateTool(t, al).Execute(ctx, map[string]any{"action": "run", "agent_id": "omnipus", "task": "must not spawn"})
			require.True(t, result.IsError, "refuse before Launch: %s", result.ForLLM)
			if missingEdge {
				assert.Contains(t, result.ForLLM, "trust_set")
			} else {
				assert.Contains(t, result.ForLLM, "policy: depth")
			}
			after, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
			require.NoError(t, err)
			assert.Equal(t, before, after, "nothing spawned on denial")
		})
	}
}
func TestOmnipusSelfDelegate_QueuedAtExplicitParallelCap(t *testing.T) {
	stubMemory(t, false, true)
	al, provider, parent := t8SelfDelegateFixture(t)
	al.GetConfig().Performance.MaxParallelAgents = 1
	first := t8DelegateCall(t, al, parent, "occupy slot")
	require.Equal(t, "running", string(first.State))
	turn := provider.next(t)
	second := t8DelegateCall(t, al, parent, "wait for slot")
	require.Equal(t, "queued", string(second.State))
	require.NotNil(t, second.QueuePosition)
	assert.Equal(t, 1, *second.QueuePosition, "21.4 first queued helper")
	t8WaitHelperQueue(t, al, 1, 1)
	turn.finish()
	next := provider.next(t)
	t8WaitHelperQueue(t, al, 1, 0)
	next.finish()
	al.steerAdmission().turns.Wait()
}
func TestSelfDelegate_MemoryPressureQueues_ProductionWiring(t *testing.T) {
	for _, tc := range []struct {
		name       string
		measurable bool
		explicit   int
	}{
		{"21.16 pressure", true, 0}, {"21.19 unmeasurable", false, 0}, {"21.19 explicit lower cap", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubMemory(t, true, tc.measurable)
			al, provider, parent := t8SelfDelegateFixture(t)
			al.GetConfig().Performance.MaxParallelAgents = tc.explicit
			cap := 2
			if tc.explicit > 0 {
				cap = tc.explicit
			}
			turns := make([]*t8HelperTurn, 0, cap)
			for range cap {
				r := t8DelegateCall(t, al, parent, "occupy pressure slot")
				require.Equal(t, "running", string(r.State))
				turns = append(turns, provider.next(t))
			}
			extra := t8DelegateCall(t, al, parent, "wait under pressure")
			require.Equal(t, "queued", string(extra.State), "FR-200 memory applies on real delegate admission")
			require.NotNil(t, extra.QueuePosition)
			assert.Equal(t, 1, *extra.QueuePosition)
			t8WaitHelperQueue(t, al, cap, 1)
			if tc.name == "21.16 pressure" {
				stubMemory(t, false, true) // specified healthy-relief schedule
			}
			turns[0].finish()
			queued := provider.next(t)
			t8WaitHelperQueue(t, al, cap, 0)
			queued.finish()
			for _, turn := range turns {
				turn.finish()
			}
			al.steerAdmission().turns.Wait()
		})
	}
}
func TestSelfDelegate_PromotionRespectsMemoryCap_ProductionWiring(t *testing.T) {
	stubMemory(t, false, true)
	al, provider, parent := t8SelfDelegateFixture(t)
	var turns []*t8HelperTurn
	for range 3 {
		r := t8DelegateCall(t, al, parent, "healthy running helper")
		require.Equal(t, "running", string(r.State))
		turns = append(turns, provider.next(t))
	}
	stubMemory(t, true, true)
	extra := t8DelegateCall(t, al, parent, "21.17 pressure queued helper")
	require.Equal(t, "queued", string(extra.State))
	require.NotNil(t, extra.QueuePosition)
	assert.Equal(t, 1, *extra.QueuePosition)
	turns[0].finish()
	t8WaitHelperQueue(t, al, 2, 1)
	turns[1].finish()
	promoted := provider.next(t)
	t8WaitHelperQueue(t, al, 2, 0)
	promoted.finish()
	turns[2].finish()
	al.steerAdmission().turns.Wait()
}
func TestSelfDelegate_PressureQueueDrainsInOrder_ProductionWiring(t *testing.T) {
	stubMemory(t, true, true)
	al, provider, parent := t8SelfDelegateFixture(t)
	var active []*t8HelperTurn
	for range 2 {
		r := t8DelegateCall(t, al, parent, "21.18 occupy pressure slot")
		require.Equal(t, "running", string(r.State))
		active = append(active, provider.next(t))
	}
	var queued []string
	for i := 0; i < 3; i++ {
		r := t8DelegateCall(t, al, parent, "21.18 queued work")
		require.Equal(t, "queued", string(r.State))
		require.NotNil(t, r.QueuePosition)
		assert.Equal(t, i+1, *r.QueuePosition)
		queued = append(queued, r.SessionId)
	}
	for i := 0; i < 3; i++ {
		active[i].finish()
		active = append(active, provider.next(t))
		t8WaitHelperQueue(t, al, 2, 2-i)
		record, err := al.GetSessionLifecycleStore().Load(queued[i])
		require.NoError(t, err)
		assert.Equal(t, session.LifecycleRunning, record.State, "FIFO queued child starts, one per end")
	}
	for _, turn := range active {
		turn.finish()
	}
	al.steerAdmission().turns.Wait()
}
