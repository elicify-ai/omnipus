package secretrequest_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWU4RegistryCreateStateIOFailureDiagnosesAllocatedRequestAndSession(t *testing.T) {
	c := newRegistryFaultCapture(t)
	_, before := registryDiagnosticWitness(t, c)
	sid := c.f.newSession("omnipus", "")
	require.NoError(t, c.f.sessions.WriteSecretRequestState(sid, []byte("[]")))
	repair := blockRegistryStatePublication(t, c, sid)
	out, err := c.f.reg.Create(context.Background(), sid, "omnipus", "web-card", "", providerInput())
	require.Error(t, err, "real atomic request-state publication must fail at the directory obstruction")
	assert.Empty(t, out.Card.RequestId, "failed Create cannot return a usable pending card")
	assert.Empty(t, c.f.reg.Snapshot(matrixOwner, sid))
	assertRegistryRepairGuidance(t, err.Error())
	entries := c.logs(t)
	assertRegistryIODiagnostic(t, entries, "", sid, "state")
	allocated := 0
	for _, entry := range entries {
		fields := registryStringFields(entry)
		if registryHasField(fields, sid, "session_id", "session") && strings.Contains(strings.ToLower(registryFirstField(fields, "stage", "operation_stage", "operation")), "state") {
			id := registryFirstField(fields, "request_id", "request")
			assert.NotEmpty(t, id, "the server allocated this request ID BEFORE the failed state publication; diagnosis must preserve it")
			assert.NotEqual(t, sid, id, "do not substitute session identity for request identity")
			allocated++
		}
	}
	assert.Positive(t, allocated, "no diagnostic for the failed new request despite allocated request/session identities")
	repair()
	assert.Empty(t, c.diskRecords(t, sid), "failed Create must leave the original empty state, not invent a pending request")
	after, readErr := os.ReadFile(c.f.store.Path())
	require.NoError(t, readErr)
	assert.Equal(t, before, after)
	valid := c.f.create(sid, "omnipus", providerInput())
	cancelled, cancelErr := c.f.reg.Submit(valid.Card.RequestId, matrixOwner, cancelInput())
	require.NoError(t, cancelErr)
	require.Equal(t, "cancelled", cancelled.Card.State)
	c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}

func TestWU4RegistryCancelStateIOFailureDiagnosesContextBeforePublication(t *testing.T) {
	c := newRegistryFaultCapture(t)
	witness, before := registryDiagnosticWitness(t, c)
	pending := c.f.pending()
	repair := blockRegistryStatePublication(t, c, pending.Card.SessionId)
	out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, cancelInput())
	requireMatrixFailure(t, err, http.StatusInternalServerError, "")
	assert.Equal(t, pending.Card, out.Card)
	assertRegistryRepairGuidance(t, err.Error())
	assertRegistryIODiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, "state")
	assert.Empty(t, c.auditTransitions(t, pending.Card.RequestId, "cancelled"))
	got, readErr := c.f.reg.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, readErr)
	assert.Equal(t, pending.Card, got.Card)
	repair()
	assertRegistryNoNewStorage(t, c, pending, witness, before)
	cancelled, cancelErr := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, cancelInput())
	require.NoError(t, cancelErr)
	require.Equal(t, "cancelled", cancelled.Card.State)
	c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}

// FR-063/079/087: the actual encrypted values+receipt commit is BEFORE the
// blocked session-state write. No failure after that fact may claim pending,
// nothing stored, or require the user to re-enter/replay plaintext.
func TestWU4RegistryPostcommitStateIOFailureDiagnosesButReturnsSubmittedReferences(t *testing.T) {
	c := newRegistryFaultCapture(t)
	witness, _ := registryDiagnosticWitness(t, c)
	pending := c.f.pending()
	repair := blockRegistryStatePublication(t, c, pending.Card.SessionId)
	out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
	requireMatrixFailure(t, err, http.StatusServiceUnavailable, "submitted")
	require.Equal(t, "submitted", out.Card.State, "atomic receipt publication is the storage fact")
	require.Len(t, out.Card.ReferenceNames, 1)
	assert.NotContains(t, strings.ToLower(err.Error()), "pending")
	assert.NotContains(t, strings.ToLower(err.Error()), "nothing stored")
	assertRegistryRepairGuidance(t, err.Error())
	assertRegistryIODiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, "state")
	receipt, exists, receiptErr := c.f.store.CardReceipt(pending.Card.RequestId)
	require.NoError(t, receiptErr)
	require.True(t, exists, "instrument: postcommit failure must have a REAL receipt")
	require.Equal(t, out.Card.ReferenceNames, receipt.References)
	require.Equal(t, pending.Card.SessionId, receipt.SessionID)
	var submittedFailure *secretrequest.RequestError
	require.ErrorAs(t, err, &submittedFailure)
	require.Equal(t, pending.Card.RequestId, submittedFailure.State.RequestId)
	require.NotNil(t, submittedFailure.State.ReferenceNames, "the error State is the real HTTP response body; references must not exist only in the ignored Record result")
	require.Equal(t, receipt.References, *submittedFailure.State.ReferenceNames)
	stored, readErr := c.f.store.Get(out.Card.ReferenceNames[0])
	require.NoError(t, readErr)
	require.Equal(t, registryDiagnosticValue, stored)
	require.NotEqual(t, witness.Card.ReferenceNames, out.Card.ReferenceNames, "new submit cannot overwrite the healthy witness version")
	require.Len(t, c.auditTransitions(t, pending.Card.RequestId, "submitted"), 1)
	got, readErr := c.f.reg.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, readErr)
	require.Equal(t, out, got, "live value-free read must agree with storage despite failed metadata publication")
	_, claimed, claimErr := c.f.reg.ClaimResume(pending.Card.RequestId, pending.Card.SessionId)
	require.NoError(t, claimErr)
	require.True(t, claimed)
	repair()
	disk, exists := registryRecordByID(c.diskRecords(t, pending.Card.SessionId), pending.Card.RequestId)
	require.True(t, exists)
	require.Equal(t, "pending", disk.Card.State, "instrument: original prepublication metadata survived; recovery must consult the receipt")
	boot := c.fresh(t)
	require.NoError(t, boot.Recover())
	recovered, readErr := boot.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, readErr)
	require.Equal(t, out, recovered)
	require.NoError(t, boot.RetryResumes())
	_, claimed, claimErr = boot.ClaimResume(pending.Card.RequestId, pending.Card.SessionId)
	require.NoError(t, claimErr)
	require.False(t, claimed, "actual request-id transcript claim suppresses replay after boot")
	_, replayErr := boot.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
	requireMatrixFailure(t, replayErr, http.StatusConflict, "submitted")
	transcript, readErr := c.f.sessions.ReadTranscript(pending.Card.SessionId)
	require.NoError(t, readErr)
	require.Len(t, transcript, 1)
	requireValueFree(t, "postcommit public response and error", map[string]any{"state": secretrequest.State(out), "error": err.Error()}, registryDiagnosticValue, registryDiagnosticRawText)
	requireValueFree(t, "postcommit real transcript", transcript, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
	c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}
