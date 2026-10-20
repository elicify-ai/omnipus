package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestPrivateEnv_ValueIsMaskedBeforeAnyChildSeesIt is Train 2 gate r1 M3
// (security T2-S1; founder: fix now): a committed private value can reach a
// child of the same pair before its card's continuation registers it for
// masking (for example when the requesting chat's transcript is unreadable,
// so the dispatch callback never runs). The value is committed straight into
// the store here — no Submit, no continuation — and the moment the private
// environment is produced for a child, the value must already be masked.
func TestPrivateEnv_ValueIsMaskedBeforeAnyChildSeesIt(t *testing.T) {
	f := newWU5Fixture(t)
	sid := f.mainSession("sales", "mia")
	const value = "gate-r1-m3-private-weather-token-value"
	store := f.al.sysagentDeps.CredStore
	require.NotNil(t, store)
	_, err := store.CommitCard("req-m3", sid, []credentials.CardValue{{
		BaseName:  "private_weather_token_m3",
		Recipient: credentials.CardRecipient{Kind: credentials.KindAgentPrivate, WorkspaceID: "sales", AgentID: "mia", Field: "WEATHER_TOKEN"},
		Value:     value,
	}}, time.Unix(1, 0))
	require.NoError(t, err)
	require.False(t, strings.Contains(logger.ScrubSensitiveValues("x "+value), "[FILTERED]"),
		"instrument: with no continuation the value is not masked yet")

	ctx := tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), sid), "mia")
	vars, notice := f.al.privateEnvForTurn(ctx)
	require.Empty(t, notice)
	require.Len(t, vars, 1, "instrument: the child receives the selected private value")
	require.Equal(t, value, vars[0].Value)

	require.Contains(t, logger.ScrubSensitiveValues("output: "+value), "[FILTERED]",
		"a private value handed to a child must already be in the masking set")
	require.NotContains(t, f.al.GetConfig().FilterSensitiveData("output: "+value), value,
		"the model-output filter must mask it too")
}
