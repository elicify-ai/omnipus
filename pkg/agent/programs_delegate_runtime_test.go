package agent

// Spec rev8 S75 / FR041 / I-2: the actual turn -> delegate -> durable
// launcher -> helper turn -> registered bash chain, not a seeded child.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only the paid provider edge is adapted: the real launcher chooses the child's
// identity and starts its turn. On its first model request, give that identity
// a bash script. Never synthesize a launch, lineage record or turn context.
type programsHelperProvider struct {
	*wu5ScriptedProvider
	parent, work string
}

func (p *programsHelperProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	if sid == "" {
		return nil, fmt.Errorf("helper provider fixture: real model request has no transcript session")
	}
	if sid != p.parent {
		p.mu.Lock()
		if _, scripted := p.scripts[sid]; !scripted {
			command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROGRAM_HELPER_CHILD_RAN'", filepath.Join(p.work, sid+"-count"))
			p.scripts[sid] = []providers.LLMResponse{wu5BashCall("program-helper-count", command), wu5Stop("helper count completed")}
		}
		p.mu.Unlock()
	}
	return p.wu5ScriptedProvider.Chat(ctx, messages, definitions, model, options)
}

func programsDelegateFixture(t *testing.T) (*wu5Fixture, string, string) {
	t.Helper()
	stubMemory(t, false, true) // Control only the host-memory signal, not admission.
	f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["delegate"] = "allow"
		cfg.Sandbox.ToolPolicies["message_parent"] = "allow"
		cfg.Performance = config.DefaultConfig().Performance
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", ProgramsAllowed: false}}
	})
	programsWireMessaging(t, f)
	f.al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore())), steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	f.al.SetSteerSessionLauncher(NewSteerLauncher(f.al))
	require.NoError(t, workspace.SaveRecord(f.home, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active", CoreTeam: []string{"dev", "mia"}}))
	require.NoError(t, workspace.SaveDelegation(f.home, "sales", []workspace.DelegationEdge{{FromAgent: "dev", ToAgent: "dev"}}))
	parent := f.mainSession("sales", "dev")
	work := filepath.Join(f.home, "workspaces", "sales", "work")
	require.NoError(t, os.MkdirAll(work, 0700))
	inst, ok := f.al.GetRegistry().GetAgent("dev")
	require.True(t, ok)
	inst.Provider = &programsHelperProvider{wu5ScriptedProvider: f.provider, parent: parent, work: work}
	return f, parent, work
}

func programsDelegateFromInput(t *testing.T, f *wu5Fixture, parent, call string, input bus.InboundMessage) string {
	t.Helper()
	f.provider.setScript(parent, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "delegate", Arguments: map[string]any{"action": "run", "agent_id": "dev", "task": "run printenv GH_TOKEN | wc -c"}}}}, wu5Stop("delegation submitted")})
	input.SessionID, input.ChatID = parent, parent
	_, _, err := f.al.processMessage(context.Background(), input)
	require.NoError(t, err)
	f.waitForCalls(parent, 2, "actual delegate tool response")
	var launched gen.DelegateSessionResponse
	raw := f.provider.resultForCall(parent, call)
	require.NoError(t, json.NewDecoder(strings.NewReader(raw)).Decode(&launched), "real delegate must return its generated response, not a policy/setup refusal: %s", raw)
	require.NotEmpty(t, launched.SessionId)
	require.NotEqual(t, parent, launched.SessionId, "instrument: the real helper is a distinct launched session")
	f.waitForCalls(launched.SessionId, 2, "actual helper bash result")
	joinGoalFixtureRuns(t, f.al)
	return launched.SessionId
}

func TestProgramProvenance_ActualDelegatedHelper(t *testing.T) {
	t.Run("S75_telegram_to_delegated_helper", func(t *testing.T) {
		f, parent, work := programsDelegateFixture(t)
		control, result := programsCountInput(t, f, parent, "s75-connected-control", programsWebInput())
		require.Equal(t, "16", control, "instrument: this fixture's actual connected Dev child receives the token")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		children := make([]string, 0, 2)
		for _, row := range []struct {
			allowed  bool
			expected string
		}{{false, "0"}, {true, "16"}} {
			channel := f.al.GetConfig().Channels["telegram"]
			channel.ProgramsAllowed = row.allowed
			f.al.GetConfig().Channels["telegram"] = channel
			child := programsDelegateFromInput(t, f, parent, fmt.Sprintf("s75-delegate-%t", row.allowed), programsConnectorInput())
			children = append(children, child)
			record, err := f.al.GetSessionLifecycleStore().Load(child)
			require.NoError(t, err)
			require.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode, "I-2: a real helper is durable lineage")
			require.Equal(t, []string{"connector:telegram"}, record.Provenance, "S75: the real delegate producer stores only the launching input origin")
			result := f.provider.resultForCall(child, "program-helper-count")
			require.Contains(t, result, "PROGRAM_HELPER_CHILD_RAN", "instrument: a tool refusal cannot satisfy the zero-output control")
			count, err := os.ReadFile(filepath.Join(work, child+"-count"))
			require.NoError(t, err)
			t.Logf("actual helper allowed=%t child_ran=true count=%s lineage=%v", row.allowed, strings.TrimSpace(string(count)), record.Provenance)
			// Nonfatal behavioral assertions retain the same oracle and fail the
			// test, while observing both settings even if the first notice is wrong.
			assert.Equal(t, row.expected, strings.TrimSpace(string(count)), "S75: helper's real child count with Telegram allowed=%t", row.allowed)
			if row.allowed {
				assert.NotContains(t, result, "Connected programs were not used for this command:")
			} else {
				assert.Contains(t, result, programsTelegramNotice, "S75: exact original-channel notice, not an unresolved/web surrogate")
			}
		}
		require.Len(t, children, 2)
		require.NotEqual(t, children[0], children[1], "the same delegation is actually launched again after the setting changes")
	})
}
