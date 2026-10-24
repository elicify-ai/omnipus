package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// wu7ConnectAttempt is test bookkeeping, not a wire format. All reports below
// are decoded from real tool-result JSON; no mock supplies a connection result.
type wu7ConnectAttempt struct {
	instance, workspace, agentID string
	secretRef, accessRef         string
	report                       map[string]json.RawMessage
	before, after                wu7ConnectSnapshot
}

func wu7Search(id, name string) providers.LLMResponse {
	return wu7Call(id, "ToolSearch", map[string]any{"names": []any{name}})
}

func wu7Call(id, name string, args map[string]any) providers.LLMResponse {
	return providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: id, Type: "function", Name: name, Arguments: args,
	}}}
}

// wu7ConnectJourney keeps the real configure -> list -> two request_secret
// cards -> K5 submits -> approved connect flow. Only the provider and platform
// HTTP edge are scripted. The connect sees card-bound references and the exact
// current revision reported by list_channels, including on the negative path.
func wu7ConnectJourney(t *testing.T, f *wu7LiveFixture, agentID, accessValue string) wu7ConnectAttempt {
	t.Helper()
	const instance, workspace = "line.wu7-live", "sales"
	w := f.wu6Fixture
	seedWU5Membership(t, w.f1277.home) // Sales contains Mia, not Omnipus.
	mainID := w.mainSession(testHarnessWorkspaceMembershipID, "omnipus")
	events := w.al.SubscribeEvents(256)
	defer w.al.UnsubscribeEvents(events.ID)
	w.provider.setDefaultScript([]providers.LLMResponse{
		wu7Search("wj-load-configure", "configure_channel"),
		wu7Call("wj-configure", "configure_channel", map[string]any{
			"id": instance, "expected_revision": "absent",
		}),
		wu7Search("wj-load-list", "list_channels"),
		wu7Call("wj-list", "list_channels", map[string]any{}),
		wu7Search("wj-load-card", "request_secret"),
		wu7Call("wj-card-secret", "request_secret", map[string]any{
			"purpose": "LINE channel secret", "instance": instance, "field": "channel_secret",
		}),
		wu7Call("wj-card-access", "request_secret", map[string]any{
			"purpose": "LINE bot API credential", "instance": instance, "field": "channel_access_token",
		}),
	})
	require.NoError(t, w.bus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "wj-connect", SessionID: mainID,
		Content: "Connect LINE to Mia in Sales", UserInitiated: true,
		Metadata: map[string]string{"agent_id": "omnipus", "workspace_id": workspace},
	}))
	w.awaitApproval("configure_channel", 15*time.Second)
	w.approveNext()

	card1 := w.awaitPendingCard(nil)
	wu7AwaitPark(t, events, mainID)
	revision := wu7ListedRevision(t, w.provider, mainID, instance)
	secretRef := wu7SubmitCard(t, w, card1, "qa1277-connect-secret")
	card2 := w.awaitPendingCard([]string{card1.RequestId})
	wu7AwaitPark(t, events, mainID)

	// The isolated instance has never held a version. Derive the first
	// reference from the actual card's recipient before submitting, so the
	// resume script is installed BEFORE the asynchronous K5 continuation.
	record, err := w.al.SecretRequests().Get(card2.RequestId, wu6Owner)
	require.NoError(t, err)
	require.Len(t, record.Recipients, 1)
	accessRef := secretrequest.ReferenceBase(record.Recipients[0]) + ".v1"
	w.provider.appendDefaultScript([]providers.LLMResponse{
		wu7Search("wj-load-connect", "connect_channel"),
		wu7Call("wj-connect", "connect_channel", map[string]any{
			"id": instance, "workspace_id": workspace, "agent_id": agentID,
			"expected_revision": revision, "channel_secret_ref": secretRef,
			"channel_access_token_ref": accessRef,
		}),
		{Content: "Connection attempt complete", FinishReason: "stop"},
	})
	require.Equal(t, accessRef, wu7SubmitCard(t, w, card2, accessValue))
	w.awaitApproval("connect_channel", 15*time.Second)
	before := wu7Snapshot(t, f)
	w.approveNext()

	// Bind the report to the real tool-call id, then wait for THAT turn's
	// completed event and active-request drain. A report appearing mid-turn
	// is not completion and never acts as the synchronization instrument.
	raw := wu7AwaitConnectCompletion(t, w, events, mainID)
	var report map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &report), "connect result is JSON")
	return wu7ConnectAttempt{
		instance: instance, workspace: workspace, agentID: agentID,
		secretRef: secretRef, accessRef: accessRef, report: report,
		before: before, after: wu7Snapshot(t, f),
	}
}

func wu7SubmitCard(t *testing.T, w *wu6Fixture, card gen.SessionStateSecretRequest, value string) string {
	t.Helper()
	require.Len(t, card.Fields, 1, "each LINE card requests exactly its credential field")
	values := map[string]string{card.Fields[0].Key: value}
	body, err := json.Marshal(gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	response := w.do(http.MethodPost, "/api/v1/secret-requests/"+card.RequestId+"/submit", string(body))
	require.Equal(t, http.StatusOK, response.Code, "K5 submit: %s", response.Body.String())
	var state gen.SecretRequestState
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
	require.Equal(t, gen.SecretRequestStateStateSubmitted, state.State)
	require.Equal(t, card.RequestId, state.RequestId)
	require.NotNil(t, state.ReferenceNames)
	require.Len(t, *state.ReferenceNames, 1)
	return (*state.ReferenceNames)[0]
}

func wu7AwaitPark(t *testing.T, events agent.EventSubscription, mainID string) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, open := <-events.C:
			require.True(t, open, "event subscription closed before the card turn parked")
			if event.Kind != agent.EventKindTurnEnd {
				continue
			}
			end, ok := event.Payload.(agent.TurnEndPayload)
			require.True(t, ok)
			if end.SessionID == mainID {
				require.Equal(t, agent.TurnEndStatusParked, end.Status)
				return
			}
		case <-deadline.C:
			t.Fatal("real request_secret turn did not park before its K5 submit")
		}
	}
}

func wu7ListedRevision(t *testing.T, provider *wu6ScriptedProvider, mainID, instance string) string {
	t.Helper()
	provider.mu.Lock()
	var result string
	for _, messages := range provider.prompts[mainID] {
		for _, message := range messages {
			if message.Role == "tool" && message.ToolCallID == "wj-list" {
				result = message.Content
			}
		}
	}
	provider.mu.Unlock()
	require.NotEmpty(t, result, "the real list_channels result reached the model")
	var listing map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result), &listing))
	var channels []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(listing["channels"], &channels))
	for _, channel := range channels {
		if wu7String(t, channel, "id") == instance {
			revision := wu7String(t, channel, "revision")
			require.NotEmpty(t, revision)
			require.NotEqual(t, "absent", revision, "configure already saved this exact instance")
			return revision
		}
	}
	t.Fatalf("list_channels did not list the configured instance %s", instance)
	return ""
}

func wu7AwaitConnectCompletion(t *testing.T, w *wu6Fixture, events agent.EventSubscription, mainID string) string {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	var result, turnID string
	for {
		select {
		case event, open := <-events.C:
			require.True(t, open, "event subscription closed before connect completed")
			switch event.Kind {
			case agent.EventKindToolExecEnd:
				end, ok := event.Payload.(agent.ToolExecEndPayload)
				require.True(t, ok)
				if end.SessionID == mainID && end.Tool == "connect_channel" && string(end.ToolCallID) == "wj-connect" {
					result, turnID = end.Result, event.Meta.TurnID
				}
			case agent.EventKindTurnEnd:
				end, ok := event.Payload.(agent.TurnEndPayload)
				require.True(t, ok)
				if result != "" && end.SessionID == mainID && event.Meta.TurnID == turnID {
					require.Equal(t, agent.TurnEndStatusCompleted, end.Status)
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					require.True(t, w.al.WaitForActiveRequestsContext(ctx), "connect request did not finish draining")
					return result
				}
			}
		case <-deadline.C:
			t.Fatal("connect did not produce its own tool result and complete its owning turn")
		}
	}
}

func wu7String(t *testing.T, object map[string]json.RawMessage, key string) string {
	t.Helper()
	require.Contains(t, object, key)
	var value string
	require.NoError(t, json.Unmarshal(object[key], &value))
	return value
}
