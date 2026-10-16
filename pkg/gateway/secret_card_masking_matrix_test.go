package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// FR-069/089, BDD-6.13: the REAL dispatch path must install masking BEFORE
// it queues a continuation. Rebuilding/cloning cannot forget unused versions.
func TestSecretK5_MasksEveryStoredVersionBeforeResumeAndAfterRebuild(t *testing.T) {
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
	f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
	values := []string{cardMatrixHTTPValue, "FAKE-WU4-UNUSED-REPLACEMENT"}
	detect := func(text, value string) bool { return strings.Contains(text, value) }
	require.True(t, detect("connector refused "+values[0], values[0]), "leak detector positive control")
	require.Equal(t, "connector refused "+values[0], logger.ScrubSensitiveValues("connector refused "+values[0]), "unregistered control must actually be visible")
	references := make([]string, 0, len(values))
	for i, value := range values {
		if i != 0 {
			rec, err := f.al.SecretRequests().Create(context.Background(), f.rec.Card.SessionId, "omnipus", "http-card", "", secretrequest.Input{Purpose: "Replacement OpenAI key", Endpoint: secretCardEndpoint})
			require.NoError(t, err)
			f.rec = rec
		}
		response := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": value}))
		state := requireCardMatrixState(t, response, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
		require.NotNil(t, state.ReferenceNames)
		require.Len(t, *state.ReferenceNames, 1)
		references = append(references, (*state.ReferenceNames)[0])
		// Dispatch has only enqueued, not executed: this tests masking before
		// any probe/model turn gets a chance to consume the continuation.
		for _, stored := range values[:i+1] {
			echo := "connector error: " + stored
			require.False(t, detect(logger.ScrubSensitiveValues(echo), stored), "stored value must be masked before use")
		}
		resume := f.takeResume(t)
		require.Contains(t, resume.Content, (*state.ReferenceNames)[0])
		require.False(t, detect(resume.Content, value))
	}
	require.NotEqual(t, references[0], references[1], "replacement is a NEW reference, not an overwrite")
	require.Regexp(t, `^.+\.v1$`, references[0])
	require.Regexp(t, `^.+\.v2$`, references[1])
	for i, ref := range references {
		stored, err := f.store.Get(ref)
		require.NoError(t, err)
		require.Equal(t, values[i], stored)
	}
	// Existing full-set rebuild API, then clone and another rebuild. Neither
	// call receives the old sentinels explicitly: the complete store source
	// must supply both versions, even though no integration has selected them.
	cfg := f.al.GetConfig()
	cfg.RegisterSensitiveValues(nil)
	clone, err := cfg.Clone()
	require.NoError(t, err)
	clone.RegisterSensitiveValues([]string{"FAKE-UNRELATED-CURRENT-KEY"})
	for _, value := range values {
		require.False(t, detect(logger.ScrubSensitiveValues("echo "+value), value), "unused old and replacement versions stay masked after rebuild")
	}
	// Every wire/read/metadata surface is nonempty and value-free after submit.
	stateRead := f.call(t, http.MethodGet, "")
	require.Equal(t, http.StatusOK, stateRead.Code)
	snapshot := f.al.SecretRequests().Snapshot("_env_token", f.rec.Card.SessionId)
	require.Len(t, snapshot, 2)
	rawSnapshot, err := json.Marshal(snapshot)
	require.NoError(t, err)
	metadata, err := f.al.GetSessionStore().ReadSecretRequestState(f.rec.Card.SessionId)
	require.NoError(t, err)
	require.NotEmpty(t, metadata)
	for _, text := range []string{stateRead.Body.String(), string(rawSnapshot), string(metadata)} {
		require.NotEmpty(t, text)
		for _, value := range values {
			require.False(t, detect(text, value), "state/snapshot/resume metadata must never re-serve a value")
		}
	}
}
