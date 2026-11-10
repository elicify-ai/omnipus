package secretrequest_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S4 / FR-084 / FR-087 / §6.7: the crash cut is after the real atomic
// credential transaction, before ANY submitted request-state or audit write.
// Direct CommitCard is the public durable seam, not fabricated session state.
func TestWU4RegistryRecoveredCommittedSubmitEmitsValueFreeAudit(t *testing.T) {
	c := newRegistryFaultCapture(t)
	f := c.f
	rec := f.pending()
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "requested"), 1, "public Create proves the audit sink is live")
	require.Equal(t, []secretrequest.Record{rec}, c.diskRecords(t, rec.Card.SessionId))
	const value = "REGISTRY-S4-COMMITTED-VALUE-WITNESS"
	at := time.Date(2026, 10, 17, 14, 15, 16, 0, time.UTC)
	wantRefs := []string{"registry_s4_cut.v1"}
	receipt, err := f.store.CommitCard(rec.Card.RequestId, rec.Card.SessionId, []credentials.CardValue{{
		BaseName: "registry_s4_cut", Recipient: rec.Recipients[0], Value: value,
	}}, at)
	require.NoError(t, err)
	require.Equal(t, credentials.CardReceipt{
		RequestID: rec.Card.RequestId, SessionID: rec.Card.SessionId, References: wantRefs, SubmittedAt: at,
	}, receipt, "atomic commit publishes the exact value-free receipt")
	storedReceipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "crash must be AFTER receipt publication")
	require.Equal(t, receipt, storedReceipt)
	stored, err := f.store.Get(wantRefs[0])
	require.NoError(t, err)
	require.Equal(t, value, stored, "the receipt must witness a real encrypted value")
	bindings, err := f.store.CardSecretMetadata()
	require.NoError(t, err)
	require.Equal(t, map[string]credentials.CardSecret{wantRefs[0]: {Recipient: rec.Recipients[0], Version: 1}}, bindings)
	rawStore, err := os.ReadFile(f.store.Path())
	require.NoError(t, err)
	require.NotContains(t, string(rawStore), value, "transaction must store ciphertext, not a plaintext test double")
	require.Equal(t, []secretrequest.Record{rec}, c.diskRecords(t, rec.Card.SessionId), "crash must be BEFORE submitted metadata")
	require.Empty(t, c.auditTransitions(t, rec.Card.RequestId, "submitted", "recovered_submitted", "recovered-submitted"), "crash must be BEFORE submitted audit")

	boot := c.fresh(t)
	require.NoError(t, boot.Recover())
	got, err := boot.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State)
	require.Equal(t, wantRefs, got.Card.ReferenceNames)
	require.Equal(t, at, secretrequest.State(got).UpdatedAt)
	require.Equal(t, []secretrequest.Record{got}, c.diskRecords(t, rec.Card.SessionId))
	transitions := c.auditTransitions(t, rec.Card.RequestId, "submitted", "recovered_submitted", "recovered-submitted")
	assert.Len(t, transitions, 1, "S4: recovering an atomically committed receipt MUST emit one truthful submitted transition; requested alone is incomplete")
	for _, entry := range transitions {
		fields, ok := entry["fields"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, rec.Card.SessionId, fields["session_id"])
		assert.Equal(t, "submitted", fields["state"])
	}
	require.NoError(t, boot.RetryResumes())
	require.NoError(t, boot.RetryResumes())
	require.Len(t, f.attempts, 1)
	_, claimed, err := boot.ClaimResume(rec.Card.RequestId, rec.Card.SessionId)
	require.NoError(t, err)
	require.True(t, claimed)
	nextBoot := c.fresh(t)
	require.NoError(t, nextBoot.Recover())
	require.NoError(t, nextBoot.RetryResumes())
	require.Len(t, f.attempts, 1, "no plaintext replay or second reference continuation")
	assert.Len(t, c.auditTransitions(t, rec.Card.RequestId, "submitted", "recovered_submitted", "recovered-submitted"), 1, "already-submitted metadata must not fabricate another recovered transition")
	_, err = nextBoot.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": value}))
	requireMatrixFailure(t, err, http.StatusConflict, "submitted")
	transcript, err := f.sessions.ReadTranscript(rec.Card.SessionId)
	require.NoError(t, err)
	require.Len(t, transcript, 1)
	require.Equal(t, secretrequest.ResumeEntryID(rec.Card.RequestId), transcript[0].ID)
	require.Contains(t, transcript[0].Content, wantRefs[0])
	requireValueFree(t, "real recovered transcript", transcript, value)
	requireValueFree(t, "recovered disk state", c.diskRecords(t, rec.Card.SessionId), value)
	c.assertValueFree(t, value)
	t.Log("S4 cut reached: atomic receipt/value/binding present; pending metadata; real recovery submitted; audit checked; later boot/claim/replay checked")
}

func TestWU4RegistryNormalSubmitAuditControlDoesNotInventRecoveryTransition(t *testing.T) {
	c := newRegistryFaultCapture(t)
	rec := c.f.pending()
	out := submitMatrix(t, c.f, rec)
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "requested"), 1)
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "submitted"), 1, "public Submit must reach the SAME real audit sink")
	boot := c.fresh(t)
	require.NoError(t, boot.Recover())
	require.NoError(t, boot.Recover())
	got, err := boot.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, out, got)
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "submitted", "recovered_submitted", "recovered-submitted"), 1, "normal submitted metadata is not a recovery transition")
	c.assertValueFree(t, matrixValue)
}

func TestWU4RegistryNoReceiptRecoveryAuditsInterruptedNotSubmitted(t *testing.T) {
	c := newRegistryFaultCapture(t)
	rec := c.f.pending()
	_, exists, err := c.f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.False(t, exists, "control must be BEFORE atomic commit")
	boot := c.fresh(t)
	require.NoError(t, boot.Recover())
	got, err := boot.Get(rec.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "interrupted", got.Card.State)
	require.Empty(t, got.Card.ReferenceNames)
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "interrupted"), 1)
	require.Empty(t, c.auditTransitions(t, rec.Card.RequestId, "submitted", "recovered_submitted", "recovered-submitted"))
	require.NoError(t, boot.RetryResumes())
	require.Empty(t, c.f.attempts)
	names, err := c.f.store.List()
	require.NoError(t, err)
	require.Empty(t, names)
	nextBoot := c.fresh(t)
	require.NoError(t, nextBoot.Recover())
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "interrupted"), 1)
	c.assertValueFree(t, matrixValue)
}
