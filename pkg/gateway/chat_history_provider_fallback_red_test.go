package gateway

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

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

const pagingPFPrimary = "primary-y"
const pagingPFAnswered = "final-x2"
const pagingPFAnswer = "PF genuine fallback answer"

// Only the external model transport is controlled. The real engine's fallback
// chain classifies these actual provider errors and publishes/persists its note.
type pagingPFTransport struct {
	mu    sync.Mutex
	code  string
	calls []string
}

func (p *pagingPFTransport) GetDefaultModel() string { return pagingPFPrimary }
func (p *pagingPFTransport) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, model)
	p.mu.Unlock()
	if model == pagingPFPrimary {
		if p.code == "model_retired" {
			return nil, &common.ProviderError{Status: 404, Body: "This model has been decommissioned", RetryAfterSeconds: 3600}
		}
		// Provider spec C18: beyond the120s retry ceiling, so the real chain
		// falls back immediately. No timing/sleep oracle in this test.
		return nil, &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 3600}
	}
	return &providers.LLMResponse{Content: pagingPFAnswer, FinishReason: "stop"}, nil
}

func (p *pagingPFTransport) observed() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

type pagingPFFixture struct {
	api       *restAPI
	view      *attachFixture
	live      *wsConn
	transport *pagingPFTransport
}

func pagingPFRealFixture(t *testing.T, code string) *pagingPFFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Gateway: config.GatewayConfig{DevModeBypass: true}, Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: pagingPFPrimary}, MaxTokens: 4096}}}
	seedTestAgents(cfg)
	messages := bus.NewMessageBus()
	t.Cleanup(messages.Close)
	transport := &pagingPFTransport{code: code}
	loop := mustAgentLoop(t, cfg, messages, transport)
	instance, ok := loop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	// Configure a real two-candidate turn and bind its process-edge transport.
	// No fallback event/result, chain, gateway, writer or page is substituted.
	instance.Candidates = []providers.FallbackCandidate{{Provider: "pf-test", Model: pagingPFPrimary}, {Provider: "pf-test", Model: pagingPFAnswered}}
	instance.StoreProviderPool(map[string]providers.LLMProvider{"pf-test": transport})
	store := loop.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	pagingSeed(t, store, meta.ID, "pf-ordinary", 1)
	api := &restAPI{agentLoop: loop, homePath: home}
	// Mandatory SAME-RUN baseline: old-array/index/setup failures are not PF RED.
	ordinaryResponse := pagingRequest(api, meta.ID, "")
	require.Equal(t, http.StatusOK, ordinaryResponse.Code)
	ordinary := historyMigrationPage(t, meta.ID, ordinaryResponse.Body.Bytes(), "latest", false, false)
	require.Equal(t, []string{"pf-ordinary-001"}, pagingIDs(ordinary))
	t.Log("PF_ORDINARY_PAGE_CONTROL=PASS actual generated page and Message schema, exactly1 ordinary source")
	handler := newWSHandler(messages, loop, "")
	t.Cleanup(handler.Wait)
	view := &attachFixture{h: handler, store: store, sid: meta.ID}
	live := view.newConn("pf-live-view")
	handler.handleAttachSession(context.Background(), "pf-live-view", meta.ID, nil, live)
	baseline := pagingDrain(t, live)
	require.GreaterOrEqual(t, indexOfType(baseline, "catch_up_complete"), 0)
	return &pagingPFFixture{api: api, view: view, live: live, transport: transport}
}

func pagingPFRunActualTurn(t *testing.T, fixture *pagingPFFixture) (session.TranscriptEntry, generated.ProviderFallbackFrame) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var turnErr error
	go func() {
		defer close(done)
		_, turnErr = fixture.api.agentLoop.ProcessScheduled(ctx, "mia", fixture.view.sid, "Answer the PF parity instruction", "webchat", "pf-live-view")
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("owned PF turn failed to join")
		}
	})
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("real fallback turn did not complete; this setup failure is NOT PF-specific RED")
	}
	require.NoError(t, turnErr, "real engine/fallback chain must complete before testing historical parity")
	require.Equal(t, []string{pagingPFPrimary, pagingPFAnswered}, fixture.transport.observed(), "real primary failure and configured fallback, no hidden relaunch")
	entries, err := fixture.view.store.ReadTranscript(fixture.view.sid)
	require.NoError(t, err)
	answerIndex, noteIndex, noteCount := -1, -1, 0
	var note session.TranscriptEntry
	for i, entry := range entries {
		if entry.Role == "assistant" && entry.Content == pagingPFAnswer {
			answerIndex = i
		}
		if entry.Type == session.EntryTypeProviderFallback {
			note, noteIndex = entry, i
			noteCount++
		}
	}
	require.GreaterOrEqual(t, answerIndex, 0, "genuine fallback assistant answer was saved")
	require.Equal(t, 1, noteCount, "one actual durable fallback note, not a manually seeded positive fixture")
	require.Greater(t, noteIndex, answerIndex, "FB2: fallback note follows the saved answer")
	require.NotEmpty(t, note.ID)
	require.Equal(t, "system", note.Role)
	require.Equal(t, "mia", note.AgentID)
	require.Equal(t, pagingPFAnswered, note.Model)
	require.Equal(t, pagingPFPrimary, note.UnavailableModel)
	require.Equal(t, fixture.transport.code, note.UnavailableCode)
	wantText := "Answered by the Fallback model (final-x2) because primary-y was unavailable."
	if fixture.transport.code == "model_retired" {
		wantText += " Pick a new model in the agent's settings."
	}
	require.Equal(t, wantText, note.Content, "provider specification frozen copy, not observed output used as the oracle")
	frames := pagingDrain(t, fixture.live)
	var live generated.ProviderFallbackFrame
	found := 0
	for _, frame := range frames {
		if frame.Type == "provider_fallback" {
			found++
			require.NoError(t, json.Unmarshal(frame.raw, &live))
		}
	}
	require.Equal(t, 1, found, "genuine real-engine event/hub publication reached the live session")
	require.Equal(t, fixture.view.sid, live.SessionId)
	require.Equal(t, note.TurnID, live.TurnId)
	require.Equal(t, pagingPFAnswered, live.AnsweredModel)
	require.Equal(t, pagingPFPrimary, live.UnavailableModel)
	require.Equal(t, fixture.transport.code, live.UnavailableCode)
	t.Log("PF_LIVE_AND_DURABLE_CONTROL=PASS real fallback frame and valid saved note AFTER answer")
	return note, live
}

func pagingPFAssertRESTNote(t *testing.T, body []byte, field string, note session.TranscriptEntry) string {
	t.Helper()
	entries := historyMigrationEntryObjects(t, body, field)
	var saved map[string]any
	count := 0
	for _, entry := range entries {
		if entry["id"] == note.ID {
			saved = entry
			count++
		}
	}
	require.Equal(t, 1, count, "PF-A recorded note occurrence must survive the real page, never be dropped")
	// Expected pre-contract RED: the real typed page returns its generic
	// placeholder for this valid note. Do not relax this after regeneration.
	require.Equal(t, "provider_fallback", saved["type"], "PF-A fallback-specific RED: valid persisted provider_fallback must not become an ordinary corruption placeholder")
	require.Equal(t, note.Content, saved["content"])
	require.Equal(t, "system", saved["role"])
	require.Equal(t, note.AgentID, saved["agent_id"])
	require.Equal(t, note.TurnID, saved["turn_id"])
	require.Equal(t, note.Timestamp.Format(time.RFC3339Nano), saved["timestamp"])
	require.Equal(t, pagingPFAnswered, saved["model"])
	require.Equal(t, pagingPFPrimary, saved["unavailable_model"])
	require.Equal(t, note.UnavailableCode, saved["unavailable_code"])
	position, ok := saved["history_position"].(string)
	require.True(t, ok)
	require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, position)
	return position
}

func httptestPFDetail(api *restAPI, sid string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil)
	api.HandleSessions(response, request)
	return response
}

// rate_limit remains a NEGATIVE, never an alias added to rescue the old matrix
// fixture. The valid positive control prevents a universal enum rejection from
// masquerading as proof of unavailable_code validation.
func TestSessionHistory_ProviderFallbackInvalidCodeIsNotPositiveFixture(t *testing.T) {
	valid := []byte(`{"id":"pf-profile","timestamp":"2026-10-21T12:00:00Z","agent_id":"mia","type":"provider_fallback","role":"system","content":"Saved fallback sentence.","model":"final-x2","unavailable_model":"primary-y","unavailable_code":"rate_limited"}`)
	message, serverError := validateBodyAgainstSchema("Message", valid)
	require.False(t, serverError)
	require.Empty(t, message, "valid PF profile control must pass before a malformed-value negative can be trusted")
	var document map[string]any
	require.NoError(t, json.Unmarshal(valid, &document))
	document["unavailable_code"] = "rate_limit"
	schema, compileErr := compileInboundSchema("Message")
	require.NoError(t, compileErr)
	var validation *jsonschema.ValidationError
	require.ErrorAs(t, schema.Validate(document), &validation)
	leaves := 0
	var visit func(*jsonschema.ValidationError)
	visit = func(issue *jsonschema.ValidationError) {
		if len(issue.Causes) == 0 {
			leaves++
			require.Equal(t, []string{"unavailable_code"}, issue.InstanceLocation)
			require.Equal(t, []string{"enum"}, issue.ErrorKind.KeywordPath())
		}
		for _, cause := range issue.Causes {
			visit(cause)
		}
	}
	visit(validation)
	require.Equal(t, 1, leaves)
}

// Q5 prerequisite: SAME named test/assertions before and after the PF contract.
// Ordinary page + actual live/writer controls must pass before a PF assertion.
func TestSessionHistory_ProviderFallbackRESTSnapshotLiveParity(t *testing.T) {
	for _, code := range []string{"rate_limited", "model_retired"} {
		t.Run(code, func(t *testing.T) {
			fixture := pagingPFRealFixture(t, code)
			note, _ := pagingPFRunActualTurn(t, fixture)
			canonicalPath := filepath.Join(fixture.view.store.BaseDir(), fixture.view.sid, "transcript.jsonl")
			before, err := os.ReadFile(canonicalPath)
			require.NoError(t, err)
			response := pagingRequest(fixture.api, fixture.view.sid, "")
			require.Equal(t, http.StatusOK, response.Code, "valid fallback source cannot make the whole page unavailable")
			historyMigrationPage(t, fixture.view.sid, response.Body.Bytes(), "latest", false, false)
			position := pagingPFAssertRESTNote(t, response.Body.Bytes(), "entries", note)
			detailResponse := httptestPFDetail(fixture.api, fixture.view.sid)
			require.Equal(t, http.StatusOK, detailResponse.Code)
			historyMigrationDetail(t, fixture.view.sid, detailResponse.Body.Bytes(), false)
			require.Equal(t, position, pagingPFAssertRESTNote(t, detailResponse.Body.Bytes(), "messages", note))
			viewer := fixture.view.newConn("pf-snapshot-view")
			fixture.view.h.handleAttachSession(context.Background(), "pf-snapshot-view", fixture.view.sid, nil, viewer)
			frames := pagingDrain(t, viewer)
			replayed := 0
			for _, frame := range frames {
				if frame.Type != "replay_provider_fallback" {
					continue
				}
				var replay generated.ProviderFallbackNote
				require.NoError(t, json.Unmarshal(frame.raw, &replay))
				replayed++
				require.Equal(t, note.ID, replay.EntryId)
				require.Equal(t, note.Content, replay.Message)
				require.Equal(t, note.Timestamp.Format(time.RFC3339Nano), replay.Timestamp)
				require.NotNil(t, replay.AnsweredModel)
				require.Equal(t, pagingPFAnswered, *replay.AnsweredModel)
				require.NotNil(t, replay.UnavailableModel)
				require.Equal(t, pagingPFPrimary, *replay.UnavailableModel)
				require.NotNil(t, replay.UnavailableCode)
				require.Equal(t, code, *replay.UnavailableCode)
				require.NotNil(t, replay.HistoryOrigin)
				require.Equal(t, position, replay.HistoryOrigin.Position)
				require.NotNil(t, replay.HistoryOrigin.EntryId)
				require.Equal(t, note.ID, *replay.HistoryOrigin.EntryId)
			}
			require.Equal(t, 1, replayed)
			after, readErr := os.ReadFile(canonicalPath)
			require.NoError(t, readErr)
			require.Equal(t, before, after, "all carrier reads leave canonical note bytes unchanged")
		})
	}
}
