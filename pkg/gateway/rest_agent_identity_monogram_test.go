package gateway

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Monogram is the 5th AgentFigure (founder decision 2026-10-09; ARCH-RULING-
// monogram D1/D4/D5). The Go identity path is enum-driven: Monogram is accepted
// once the generated enum carries it, and every wrong-case / null / unknown
// figure still rejects the whole write with the published 400 envelope.
//
// Oracles are HTTP round-trips and the exact error envelope — never a value
// read back from a helper the implementation also drives.
const monogramFigureError = "figure must be Robot, Man, Woman, Omnipus, or Monogram"

// AC-3: POST and PUT with figure "Monogram" (exact case) are accepted and
// persisted; GET returns "Monogram".
func TestAgentIdentity_MonogramRoundTripsOnCreateAndUpdate(t *testing.T) {
	api := buildExecutorTestAPI(t)

	created := postAgent(t, api, `{"name":"Monogram Created","type":"Main","soul":"monogram-soul","figure":"Monogram","role":"general","color":"#3B82F6"}`)
	require.Equal(t, http.StatusCreated, created.Code, "body: %s", created.Body.String())
	body := decodeObject(t, created.Body.Bytes())
	assert.Equal(t, "Monogram", body["figure"], "create response must echo the accepted figure")
	id, ok := body["id"].(string)
	require.True(t, ok, "create response id must be a string, got %#v", body["id"])
	assert.Equal(t, "Monogram", savedAgent(t, api, id)["figure"], "the accepted figure must be persisted")
	assert.Equal(t, "Monogram", getAgentObject(t, api, id)["figure"], "GET must emit the stored figure")

	// PUT "Monogram" onto an editable agent, then read it back through GET.
	updated := putAgent(t, api, "test-agent", `{"figure":"Monogram"}`)
	require.Equal(t, http.StatusOK, updated.Code, "body: %s", updated.Body.String())
	assert.Equal(t, "Monogram", savedAgent(t, api, "test-agent")["figure"], "PUT must persist the accepted figure")
	assert.Equal(t, "Monogram", getAgentObject(t, api, "test-agent")["figure"], "GET must emit the stored figure")
}

// AC-4: figure "monogram" (any other case) on PUT -> 400, zero write.
func TestAgentIdentity_MonogramWrongCaseRejectedOnUpdate(t *testing.T) {
	api := buildExecutorTestAPI(t)
	storedBefore := savedAgent(t, api, "test-agent")
	stateBefore, err := agentstore.New(api.homePath).ReadState("test-agent")
	require.NoError(t, err)

	rec := putAgent(t, api, "test-agent", `{"figure":"monogram","role":"writer","color":"#22D3EE","description":"must not save"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "wrong-case figure must reject; body: %s", rec.Body.String())
	assert.Equal(t, map[string]any{"error": monogramFigureError}, decodeObject(t, rec.Body.Bytes()), "published 400 text is exact-case")
	assert.Equal(t, storedBefore, savedAgent(t, api, "test-agent"), "a rejected figure must not persist valid siblings")

	stateAfter, err := agentstore.New(api.homePath).ReadState("test-agent")
	require.NoError(t, err)
	assert.Equal(t, stateBefore.Revision, stateAfter.Revision, "rejection must not bump the configuration revision")
	assert.Equal(t, stateBefore, stateAfter, "rejection is zero-write across the stored configuration state")
}

// AC-4b: figure "monogram" on POST (the separate create-validation site)
// -> 400, no agent persisted.
func TestAgentIdentity_MonogramWrongCaseRejectedOnCreate(t *testing.T) {
	api := buildExecutorTestAPI(t)
	before := agentNameSet(t, api)

	rec := postAgent(t, api, `{"name":"Bad Monogram Case","type":"Main","soul":"bad-soul","figure":"monogram","role":"general","color":"#3B82F6"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "wrong-case figure must reject on create; body: %s", rec.Body.String())
	assert.Equal(t, map[string]any{"error": monogramFigureError}, decodeObject(t, rec.Body.Bytes()))
	assert.Equal(t, before, agentNameSet(t, api), "an invalid figure must not create an agent")
}

// AC-4c: PUT figure null -> 400, zero write for the entire request including
// valid siblings -- the AgentFigure schema description's core promise.
func TestAgentIdentity_UpdateNullFigureIsZeroWrite(t *testing.T) {
	api := buildExecutorTestAPI(t)
	created := postAgent(t, api, `{"name":"Null Figure Editable","type":"Main","soul":"editable-soul","figure":"Man","role":"developer","color":"#3B82F6"}`)
	require.Equal(t, http.StatusCreated, created.Code, "body: %s", created.Body.String())
	id, ok := decodeObject(t, created.Body.Bytes())["id"].(string)
	require.True(t, ok, "created identity id must be a string")
	storedBefore := savedAgent(t, api, id)
	stateBefore, err := agentstore.New(api.homePath).ReadState(id)
	require.NoError(t, err)

	rec := putAgent(t, api, id, `{"figure":null,"role":"writer","color":"#22D3EE","description":"must not save"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "an explicit null figure rejects the whole update; body: %s", rec.Body.String())
	assert.Equal(t, map[string]any{"error": monogramFigureError}, decodeObject(t, rec.Body.Bytes()))
	assert.Equal(t, storedBefore, savedAgent(t, api, id), "null figure must not persist the valid role/colour siblings")

	stateAfter, err := agentstore.New(api.homePath).ReadState(id)
	require.NoError(t, err)
	assert.Equal(t, stateBefore.Revision, stateAfter.Revision, "null figure rejection must not bump the revision")
	assert.Equal(t, stateBefore, stateAfter, "null figure rejection is zero-write across the stored state")
}

// AC-6 (regression): an omitted figure on create still defaults Omnipus; the
// new value is not the default.
func TestAgentIdentity_OmittedFigureStillDefaultsOmnipusNotMonogram(t *testing.T) {
	for _, variant := range []string{"Main", "Subagent", "subagent_3p"} {
		t.Run(fmt.Sprintf("variant=%s", variant), func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			rec := postAgent(t, api, identityCreateInput(variant, ""))
			require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
			response := decodeObject(t, rec.Body.Bytes())
			assert.Equal(t, "Omnipus", response["figure"], "the create default is unchanged (not Monogram)")
			id, ok := response["id"].(string)
			require.True(t, ok)
			assert.Equal(t, "Omnipus", savedAgent(t, api, id)["figure"])
		})
	}
}
