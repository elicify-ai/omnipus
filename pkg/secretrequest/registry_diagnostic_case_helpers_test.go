package secretrequest_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	registryDiagnosticValue   = "REGISTRY-DIAGNOSTIC-SUBMITTED-VALUE"
	registryDiagnosticRawText = "REGISTRY-ARBITRARY-LOWER-ERROR-SECRET"
	registryDiagnosticOld     = "REGISTRY-DIAGNOSTIC-HEALTHY-WITNESS"
)

// Every category gets IDENTICAL arbitrary Error text. The only information
// distinguishing permission/locked/full/metadata is its typed Unwrap cause.
// This is a process-edge store-opener control, not a fake Registry/CommitCard.
type registryOpaqueCauseError struct{ cause error }

func (e registryOpaqueCauseError) Error() string { return registryDiagnosticRawText }
func (e registryOpaqueCauseError) Unwrap() error { return e.cause }

func registryDiagnosticWitness(t *testing.T, c *registryFaultCapture) (secretrequest.Record, []byte) {
	t.Helper()
	rec := c.f.pending()
	stored, err := c.f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticOld}))
	require.NoError(t, err, "public healthy Submit is the per-case audit/storage control")
	require.Equal(t, "submitted", stored.Card.State)
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "requested"), 1)
	require.Len(t, c.auditTransitions(t, rec.Card.RequestId, "submitted"), 1)
	raw, err := os.ReadFile(c.f.store.Path())
	require.NoError(t, err)
	require.NotContains(t, string(raw), registryDiagnosticOld)
	return stored, raw
}

func assertRegistryKnownPending(t *testing.T, c *registryFaultCapture, pending, out secretrequest.Record, publicErr error) {
	t.Helper()
	assert.Equal(t, pending.Card, out.Card, "a PRE-commit failure must keep the known pending request unchanged")
	got, err := c.f.reg.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	assert.Equal(t, pending.Card, got.Card)
	disk, exists := registryRecordByID(c.diskRecords(t, pending.Card.SessionId), pending.Card.RequestId)
	require.True(t, exists)
	assert.Equal(t, pending.Card, disk.Card)
	assert.Empty(t, c.auditTransitions(t, pending.Card.RequestId, "submitted", "recovered_submitted", "recovered-submitted"))
	require.Error(t, publicErr)
	assertRegistryRepairGuidance(t, publicErr.Error())
	requireValueFree(t, "public PRE-commit refusal", map[string]any{
		"error": publicErr.Error(), "record": out, "state": secretrequest.State(out),
	}, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}

func assertRegistryNoNewStorage(t *testing.T, c *registryFaultCapture, pending, witness secretrequest.Record, before []byte) {
	t.Helper()
	after, err := os.ReadFile(c.f.store.Path())
	require.NoError(t, err)
	assert.Equal(t, before, after, "failed transaction must not publish values, bindings or a receipt")
	_, exists, err := c.f.store.CardReceipt(pending.Card.RequestId)
	require.NoError(t, err)
	assert.False(t, exists)
	names, err := c.f.store.List()
	require.NoError(t, err)
	assert.Equal(t, witness.Card.ReferenceNames, names)
	assert.Len(t, c.f.attempts, 1, "only the healthy witness dispatched; failed transaction must not resume")
}

func registryDiagnosticRepairSubmit(t *testing.T, c *registryFaultCapture, pending secretrequest.Record) {
	t.Helper()
	out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
	require.NoError(t, err, "repair plus an EXPLICIT user submit, never automatic plaintext retry")
	require.Equal(t, "submitted", out.Card.State)
	require.Len(t, out.Card.ReferenceNames, 1)
	value, err := c.f.store.Get(out.Card.ReferenceNames[0])
	require.NoError(t, err)
	require.Equal(t, registryDiagnosticValue, value)
	_, claimed, err := c.f.reg.ClaimResume(pending.Card.RequestId, pending.Card.SessionId)
	require.NoError(t, err)
	require.True(t, claimed)
	transcript, err := c.f.sessions.ReadTranscript(pending.Card.SessionId)
	require.NoError(t, err)
	require.Len(t, transcript, 1)
	require.Contains(t, transcript[0].Content, out.Card.ReferenceNames[0])
	requireValueFree(t, "real repaired continuation", transcript, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
	c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}

// The specification requires a safe cause category but does not prescribe the
// incidental spelling for a directory/read/write obstruction. Accept this fixed
// semantic class only; an arbitrary raw error or generic 'failed' cannot pass.
func assertRegistryIODiagnostic(t *testing.T, entries []map[string]any, requestID, sessionID string, stageWords ...string) {
	t.Helper()
	allowed := []string{"io_error", "invalid_storage_path", "store_unreadable", "read_failed", "write_failed", "storage_read_failed", "storage_write_failed", "is_directory"}
	for _, entry := range entries {
		fields := registryStringFields(entry)
		if requestID != "" && !registryHasField(fields, requestID, "request_id", "request") {
			continue
		}
		if !registryHasField(fields, sessionID, "session_id", "session") {
			continue
		}
		category := registryFirstField(fields, "cause_category", "error_category", "reason_code", "category", "cause", "reason", "code")
		for _, safe := range allowed {
			if category == safe {
				assertRegistryDiagnostic(t, entries, requestID, sessionID, safe, stageWords...)
				return
			}
		}
	}
	assert.Fail(t, "actual I/O fault has no safely classified request/session diagnostic", "stage=%v; accepted I/O classes=%v", stageWords, allowed)
}

func registryUnlockAfterFault(t *testing.T, store *credentials.Store) {
	t.Helper()
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x44}, 32)))
}

func assertRegistryUnavailableNotInventedState(t *testing.T, reg *secretrequest.Registry, id string) {
	t.Helper()
	rec, err := reg.Get(id, matrixOwner)
	var refused *secretrequest.RequestError
	require.True(t, errors.As(err, &refused), "unknown request state must be an explicit unavailable refusal")
	assert.Equal(t, 503, refused.Status, "unknown is unavailable, never an invented missing request")
	assert.Nil(t, refused.State)
	assert.Empty(t, rec.Card.State, "unreadable state must not become pending/submitted/absent")
	assert.NotContains(t, strings.ToLower(refused.Message), "not found")
	assert.NotContains(t, strings.ToLower(refused.Message), "pending")
	assertRegistryRepairGuidance(t, refused.Message)
	requireValueFree(t, "public unavailable-state error and output", map[string]any{
		"error": refused.Message, "state": refused.State, "record": rec,
	}, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}
