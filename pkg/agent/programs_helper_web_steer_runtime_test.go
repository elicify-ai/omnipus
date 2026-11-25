package agent

// Spec rev8 S138 / FR-041 / FR-050 / I-2: a Telegram turn delegates to a helper
// through the real delegate tool and durable launcher; the person then steers
// that helper from the web, and a helper record without stored provenance is
// woken. The first two rows read only records the production launcher and its
// turns wrote. The last row needs a record "written before this feature", which
// no current producer can write (the launcher always stamps provenance, and a
// finished helper's record is terminal and immutable), so it is a twin of the
// real helper's record (same parent edge, owner scope, workspace, agent, origin
// and lineage mode) with no stored origin set, written through the real
// lifecycle store.
// The connected identity is the delegating agent itself (self-delegation, ADR-091),
// as in S75/S110: the helper holds the connection, where the spec text has Ops
// delegated to by Dev.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// programsOriginProvider records the provenance each model request of a session
// ran under (the exact origin set of that turn), then defers to the fixture's
// scripted helper provider. It scripts and decides nothing.
type programsOriginProvider struct {
	*programsHelperProvider
	omu     sync.Mutex
	origins map[string][]bus.Provenance
}

func (p *programsOriginProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.omu.Lock()
	p.origins[sid] = append(p.origins[sid], tools.ToolTurnProvenance(ctx))
	p.omu.Unlock()
	return p.programsHelperProvider.Chat(ctx, messages, definitions, model, options)
}

func (p *programsOriginProvider) observed(sid string) []bus.Provenance {
	p.omu.Lock()
	defer p.omu.Unlock()
	return append([]bus.Provenance(nil), p.origins[sid]...)
}

// programsRecordOrigins wraps the delegate fixture's provider for agent id.
func programsRecordOrigins(t *testing.T, f *wu5Fixture, id string) *programsOriginProvider {
	t.Helper()
	inst, ok := f.al.GetRegistry().GetAgent(id)
	require.True(t, ok)
	helper, ok := inst.Provider.(*programsHelperProvider)
	require.True(t, ok, "instrument: the delegate fixture's helper provider is the one being wrapped")
	recorder := &programsOriginProvider{programsHelperProvider: helper, origins: map[string][]bus.Provenance{}}
	inst.Provider = recorder
	return recorder
}

// programsTelegramHelper launches a helper from a Telegram turn of the parent,
// after a web control proves the connected identity can receive the token, and
// returns the helper id with the count its own launch run printed.
func programsTelegramHelper(t *testing.T, f *wu5Fixture, parent, work, label string) (string, string) {
	t.Helper()
	control, result := programsCountInput(t, f, parent, label+"-connected-control", programsWebInput())
	require.Equal(t, "16", control, "instrument: this fixture's actual connected identity receives the token on trusted web input")
	require.NotContains(t, result, "Connected programs were not used for this command:")
	child := programsDelegateFromInput(t, f, parent, label+"-telegram-launch", programsConnectorInput())
	record, err := f.al.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err)
	require.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode, "Given: the real helper record is lineage")
	require.Equal(t, []string{"connector:telegram"}, record.Provenance, "Given: the real delegate producer stored {connector:telegram}")
	launched, err := os.ReadFile(filepath.Join(work, child+"-count"))
	require.NoError(t, err)
	return child, strings.TrimSpace(string(launched))
}

// programsLegacyTwinOf writes the pre-feature twin of a helper the real launcher
// made: a new live delegate session with the same parent edge, owner scope,
// workspace, agent and origin, in the same lineage mode, but with no stored
// provenance, which no current producer writes. A finished helper's record is
// terminal and immutable, so the twin is a new record, not an edited one.
func programsLegacyTwinOf(t *testing.T, f *wu5Fixture, parent string, producer *session.LifecycleRecord) string {
	t.Helper()
	store := f.al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeDelegate, "", producer.AgentID)
	require.NoError(t, err)
	workspaceID := producer.WorkspaceID
	require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID, ParentSessionID: &parent}))
	twin := &session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: producer.OwnerScopeKind, OwnerScopeID: producer.OwnerScopeID,
		WorkspaceID: producer.WorkspaceID, AgentID: producer.AgentID, ParentAgentID: producer.ParentAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		ProvenanceMode: session.ProvenanceModeLineage,
	}
	if producer.SteeredBy != nil {
		edge := *producer.SteeredBy
		twin.SteeredBy = &edge
	}
	require.NoError(t, f.al.GetSessionLifecycleStore().Persist(twin))
	return meta.ID
}

func TestProgramProvenance_ActualHelperWebSteer(t *testing.T) {
	both := []bus.Origin{bus.OriginWeb, bus.ConnectorOrigin("telegram")}

	t.Run("S138_telegram_helper_steered_from_the_web_through_its_parent_stays_withheld", func(t *testing.T) {
		f, parent, work := programsDelegateFixture(t)
		recorder := programsRecordOrigins(t, f, "dev")
		child, launched := programsTelegramHelper(t, f, parent, work, "s138-parent-steer")
		assert.Equal(t, "0", launched, "S138: the Telegram-launched helper's own run prints 0")

		const childCall, steerCall = "s138-steered-count", "s138-parent-steer"
		path := filepath.Join(work, "s138-steered-count")
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S138_STEERED_CHILD_RAN'", path)
		f.provider.setScript(child, []providers.LLMResponse{wu5BashCall(childCall, command), wu5Stop("steered helper completed")})
		f.provider.setScript(parent, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: steerCall, Type: "function", Name: "delegate", Arguments: map[string]any{"action": "steer", "session_id": child, "text": "run printenv GH_TOKEN | wc -c"}}}}, wu5Stop("steer submitted")})
		before := len(recorder.observed(child))
		input := programsWebInput()
		input.SessionID, input.ChatID = parent, parent
		_, _, err := f.al.processMessage(context.Background(), input)
		require.NoError(t, err)
		f.waitForCalls(parent, 2, "real web parent's steer result")
		f.waitForCalls(child, 2, "actual same-helper steer consumption and bash result")
		joinGoalFixtureRuns(t, f.al)

		result := f.provider.resultForCall(child, childCall)
		require.Contains(t, result, "S138_STEERED_CHILD_RAN", "instrument: the steered helper's real command ran, not just its launch run")
		count, err := os.ReadFile(path)
		require.NoError(t, err)
		after, err := f.al.GetSessionLifecycleStore().Load(child)
		require.NoError(t, err)
		assert.Equal(t, session.ProvenanceModeLineage, after.ProvenanceMode)
		assert.ElementsMatch(t, []string{"web", "connector:telegram"}, after.Provenance, "S138: the web steer joins its origin; the steered helper is {connector:telegram, web}")
		assert.Equal(t, "0", strings.TrimSpace(string(count)), "S138: the steered helper prints 0")
		assert.Contains(t, result, programsTelegramNotice, "S138: the Telegram-off notice, not unresolved")
		steered := recorder.observed(child)[before:]
		require.GreaterOrEqual(t, len(steered), 2, "instrument: the steered turn requested the command and consumed its real result")
		for _, actual := range steered[:2] {
			assert.ElementsMatch(t, both, actual.Origins(), "S138: the steered helper's turn is {connector:telegram, web}")
		}
	})

	t.Run("S138_web_message_into_telegram_helper_session_stays_withheld", func(t *testing.T) {
		f, parent, work := programsDelegateFixture(t)
		recorder := programsRecordOrigins(t, f, "dev")
		child, launched := programsTelegramHelper(t, f, parent, work, "s138-direct")
		assert.Equal(t, "0", launched, "S138: the Telegram-launched helper's own run prints 0")
		before := len(recorder.observed(child))
		count, result := programsCountInput(t, f, child, "s138-web-into-child", programsWebInput())
		assert.Equal(t, "0", count, "S138: a person's web message into the helper's own session prints 0")
		assert.Contains(t, result, programsTelegramNotice, "S138: the web turn inherits the Telegram origin and says so")
		turn := recorder.observed(child)[before:]
		require.GreaterOrEqual(t, len(turn), 2, "instrument: the re-entry requested the command and consumed its real result")
		for _, actual := range turn[:2] {
			assert.ElementsMatch(t, both, actual.Origins(), "S138: the web re-entry turn is {connector:telegram, web}")
		}
	})

	t.Run("S138_helper_record_without_stored_provenance_is_unresolved_when_woken", func(t *testing.T) {
		f, parent, work := programsDelegateFixture(t)
		child, _ := programsTelegramHelper(t, f, parent, work, "s138-legacy")
		producer, err := f.al.GetSessionLifecycleStore().Load(child)
		require.NoError(t, err)
		twin := programsLegacyTwinOf(t, f, parent, producer)
		stored, err := f.al.GetSessionLifecycleStore().Load(twin)
		require.NoError(t, err)
		require.Empty(t, stored.Provenance, "instrument: the twin stores no origin set")
		require.Equal(t, session.ProvenanceModeLineage, stored.ProvenanceMode, "instrument: same shape, still a lineage helper")
		count, result := programsCountInput(t, f, twin, "s138-no-provenance", programsWebInput())
		assert.Equal(t, "0", count, "S138: a helper record without stored provenance prints 0 when woken")
		assert.Contains(t, result, programsUnresolvedNotice, "S138: unresolved, never a permissive fallback")
	})
}
