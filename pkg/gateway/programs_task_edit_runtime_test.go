package gateway

// Spec rev8 S107 / I-2 / FR041: actual web create -> Telegram prompt write ->
// real task launch, with and without a gateway-domain runtime/store restart.
import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualTaskPromptEdit(t *testing.T) {
	for _, row := range []struct {
		name    string
		restart bool
	}{
		{"S107_telegram_edit_same_write_and_run", false},
		{"S107_telegram_edit_same_write_then_gateway_runtime_restart", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t)
			const initial = "prepare the Ops command after a later instruction"
			const edited = "run printenv GH_TOKEN | wc -c"
			id := x.agentCreate(t, "s107-web-create", bus.InboundMessage{Channel: "webchat", Content: "create the Ops task", Provenance: bus.NewProvenance(bus.OriginWeb)}, initial)
			creatorOrigins := x.provider.observedOrigins(x.dev)
			require.NotEmpty(t, creatorOrigins, "instrument: actual web creator reached its registered tool")
			assert.Equal(t, bus.NewProvenance(bus.OriginWeb), creatorOrigins[0], "S107's actual web-origin creator, not a fixture-seeded task")
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), initial, []string{"web"}, "actual web-created task")
			beforeOrigins := len(creatorOrigins)
			atWrite := programsCaptureTaskWrite(t, x, id, func() {
				programsAgentEditTask(t, x, id, "s107-telegram-update", edited)
			})
			writerOrigins := x.provider.observedOrigins(x.dev)
			require.GreaterOrEqual(t, len(writerOrigins), beforeOrigins+2, "instrument: actual writer made and consumed the registered update")
			assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), writerOrigins[beforeOrigins], "actual Telegram writer context, not a hand-joined task carrier")
			expected := []string{"web", "connector:telegram"}
			programsAssertTaskDocument(t, atWrite, edited, expected, "same in-lock persisted write before reconciliation")
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), edited, expected, "after actual update_task returns")
			if row.restart {
				programsRestartTaskRuntime(t, x)
				programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), edited, expected, "after full gateway-domain runtime/store reconstruction")
				controlPath := x.scriptCount(x.ops, "s107-post-restart-control")
				require.NoError(t, x.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: x.ops, ChatID: x.ops, Content: "connected Ops after restart", Provenance: bus.NewProvenance(bus.OriginWeb)}))
				x.waitEnds(t, x.ops)
				count, result := programsPeerCount(t, x.programsPeerFixture, controlPath, "s107-post-restart-control")
				require.Equal(t, "16", count, "instrument: real checked Programs connection remains usable after restart")
				require.NotContains(t, result, "Connected programs were not used for this command:")
			}
			count, result, sid := x.runBoard(t, id)
			record, err := x.al.GetSessionLifecycleStore().Load(sid)
			require.NoError(t, err)
			assert.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode)
			assert.ElementsMatch(t, expected, record.Provenance, "S107/I-2: real task launch preserves web and the instruction writer's Telegram origin")
			assert.Equal(t, "0", count, "S107: actual edited task child must not receive GH_TOKEN")
			assert.Contains(t, result, programsPeerTelegramNotice, "S107: exact Telegram-off notice, not an unresolved notice")
			received := x.provider.observedOrigins(sid)
			require.GreaterOrEqual(t, len(received), 2, "instrument: actual task command and result-consumption boundaries")
			for _, actual := range received[:2] {
				assert.ElementsMatch(t, []bus.Origin{bus.OriginWeb, bus.ConnectorOrigin("telegram")}, actual.Origins(), "S107/I-2: exact real receiving turn origin set")
			}
			t.Logf("actual edited task child_ran=true restart=%t count=%s launch_origins=%v receiving_origins=%v", row.restart, count, record.Provenance, received[:2])
		})
	}
}
