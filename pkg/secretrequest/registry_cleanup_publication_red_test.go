package secretrequest_test

import (
	"net/http"
	"os"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SF-03 / Q-PUBLISH A: cleanup is maintenance, not a rollback of an already
// published cancellation. The unmodified REST adapter maps nil Submit error
// to HTTP 200; this package test exercises its real Registry boundary.
func TestWU4RegistryPublishedCancelSurvivesReceiptIOFailure(t *testing.T) {
	cut := newRegistryReceiptCut(t)
	c, f := cut.c, cut.c.f
	out, cancelErr := cut.cancelAcrossPublicationCut(t)
	assert.NoError(t, cancelErr, "Q-PUBLISH A: successful cancelled-state publication stays a success even when receipt maintenance fails")
	assert.Equal(t, "cancelled", out.Card.State)
	assert.Equal(t, gen.SecretRequestStateState("cancelled"), secretrequest.State(out).State)
	assert.Empty(t, out.Card.ReferenceNames)
	got, err := f.reg.Get(cut.pending.Card.RequestId, matrixOwner)
	require.NoError(t, err, "receipt maintenance cannot make a known cancelled state unavailable")
	assert.Equal(t, "cancelled", got.Card.State, "live GET must agree with already-published terminal state")
	disk := c.diskRecords(t, cut.pending.Card.SessionId)
	require.Len(t, disk, 32)
	durable, exists := registryRecordByID(disk, cut.pending.Card.RequestId)
	require.True(t, exists)
	require.Equal(t, "cancelled", durable.Card.State, "instrument must prove terminal publication really committed")
	assert.Equal(t, durable.Card, got.Card)
	assert.Equal(t, durable.Card, out.Card)
	_, oldStillVisible := registryRecordByID(disk, cut.old.Card.RequestId)
	require.False(t, oldStillVisible, "retention removed the old terminal record before receipt I/O was faulted")
	cancelNotices := 0
	for _, notification := range c.notifications {
		if notification.Card.RequestId == cut.pending.Card.RequestId && notification.Card.State == "cancelled" {
			cancelNotices++
		}
	}
	assert.Equal(t, 1, cancelNotices, "committed cancellation MUST notify its terminal state, not disappear with cleanup failure")
	assert.Len(t, c.auditTransitions(t, cut.pending.Card.RequestId, "cancelled"), 1, "committed cancellation MUST reach the positive real audit sink")
	assertRegistryReceiptMaintenanceDiagnostic(t, cut)
	const replay = "REGISTRY-CANCELLED-REPLAY-MUST-NOT-COMMIT"
	replayed, replayErr := f.reg.Submit(cut.pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": replay}))
	assertRegistryCancelledConflict(t, replayed, replayErr)
	cut.repair(t)
	cut.assertEncryptedValuesAndBindingsUnchanged(t)
	// No further operation on the old registry before this new reader recovers:
	// the retry fact must survive in persisted receipts, not just an in-memory map.
	boot := c.fresh(t)
	require.NoError(t, boot.Recover())
	fresh, err := boot.Get(cut.pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, durable, fresh)
	_, exists, err = f.store.CardReceipt(cut.old.Card.RequestId)
	require.NoError(t, err)
	assert.False(t, exists, "full-inventory fresh boot must retry the orphan receipt cleanup")
	_, replayErr = boot.Submit(cut.pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": replay}))
	requireMatrixFailure(t, replayErr, http.StatusConflict, "cancelled")
	cut.assertEncryptedValuesAndBindingsUnchanged(t)
	transcript, err := f.sessions.ReadTranscript(cut.pending.Card.SessionId)
	require.NoError(t, err)
	requireValueFree(t, "post-maintenance transcript", transcript, registryRetainedValue, replay)
	c.assertValueFree(t, registryRetainedValue, replay)
	t.Log("SF-03 real cut reached after cancelled bytes and retention were published; live/API state, audit/notification, resubmit, preserved ciphertext/bindings and fresh-boot sweep checked")
}

func TestWU4RegistryReceiptCleanupRetriesOnLaterStateOperation(t *testing.T) {
	cut := newRegistryReceiptCut(t)
	_, cancelErr := cut.cancelAcrossPublicationCut(t)
	assert.NoError(t, cancelErr, "published cancellation cannot report failure")
	cut.repair(t)
	_, exists, err := cut.c.f.store.CardReceipt(cut.old.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "pre-retry control: cleanup has genuinely been deferred")
	got, err := cut.c.f.reg.Get(cut.pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", got.Card.State)
	_, exists, err = cut.c.f.store.CardReceipt(cut.old.Card.RequestId)
	require.NoError(t, err)
	assert.False(t, exists, "SF-03: a later public state operation MUST retry reference-only receipt maintenance after repair")
	cut.assertEncryptedValuesAndBindingsUnchanged(t)
	afterRetry, err := os.ReadFile(cut.c.f.store.Path())
	require.NoError(t, err)
	_, err = cut.c.f.reg.Get(cut.pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.NoError(t, cut.c.f.store.PruneCardReceipts([]string{cut.old.Card.RequestId}))
	require.NoError(t, cut.c.f.store.PruneCardReceipts([]string{cut.old.Card.RequestId}))
	afterRepeatedPrune, err := os.ReadFile(cut.c.f.store.Path())
	require.NoError(t, err)
	assert.Equal(t, afterRetry, afterRepeatedPrune, "repeated pruning of an absent receipt is idempotent, with no ciphertext/binding rewrite")
	assert.Len(t, cut.c.auditTransitions(t, cut.pending.Card.RequestId, "cancelled"), 1)
	cut.c.assertValueFree(t, registryRetainedValue)
}

func TestWU4RegistryCancelPublicationFailureKeepsPendingAndDoesNotPruneReceipt(t *testing.T) {
	cut := newRegistryReceiptCut(t)
	c, f := cut.c, cut.c.f
	before := c.diskRecords(t, cut.pending.Card.SessionId)
	repair := blockRegistryStatePublication(t, c, cut.pending.Card.SessionId)
	out, err := f.reg.Submit(cut.pending.Card.RequestId, matrixOwner, cancelInput())
	requireMatrixFailure(t, err, http.StatusInternalServerError, "")
	require.Equal(t, cut.pending, out, "only a failed publication may restore the prior pending state")
	got, err := f.reg.Get(cut.pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, cut.pending, got)
	require.Empty(t, c.auditTransitions(t, cut.pending.Card.RequestId, "cancelled"))
	for _, notice := range c.notifications {
		if notice.Card.RequestId == cut.pending.Card.RequestId {
			require.Equal(t, "pending", notice.Card.State, "no false cancelled notification before publication")
		}
	}
	_, exists, err := f.store.CardReceipt(cut.old.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "failed state publication must not prune the old receipt")
	repair()
	require.Equal(t, before, c.diskRecords(t, cut.pending.Card.SessionId))
	cancelled, err := f.reg.Submit(cut.pending.Card.RequestId, matrixOwner, cancelInput())
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Card.State)
	require.Len(t, c.auditTransitions(t, cut.pending.Card.RequestId, "cancelled"), 1)
	_, exists, err = f.store.CardReceipt(cut.old.Card.RequestId)
	require.NoError(t, err)
	require.False(t, exists)
	cut.assertEncryptedValuesAndBindingsUnchanged(t)
	c.assertValueFree(t, registryRetainedValue)
}

func assertRegistryReceiptMaintenanceDiagnostic(t *testing.T, cut *registryReceiptCut) {
	t.Helper()
	matches := 0
	for _, entry := range cut.c.logs(t) {
		fields := registryStringFields(entry)
		if !registryHasField(fields, cut.pending.Card.SessionId, "session_id", "session") {
			continue
		}
		if !registryHasField(fields, cut.pending.Card.RequestId, "request_id", "request") && !registryHasField(fields, cut.old.Card.RequestId, "request_id", "request") {
			continue
		}
		stage := strings.ToLower(registryFirstField(fields, "stage", "operation_stage", "operation"))
		if !strings.Contains(stage, "receipt") {
			continue
		}
		assert.Equal(t, "error", strings.ToLower(registryFirstField(fields, "level", "severity")), "receipt maintenance failure is actionable Error-level, never silence or Warn")
		category := registryFirstField(fields, "cause_category", "error_category", "reason_code", "category", "cause", "reason", "code")
		assert.Contains(t, []string{"io_error", "invalid_storage_path", "store_unreadable", "read_failed", "storage_read_failed", "is_directory"}, category, "real directory/read fault needs a safe machine-readable cause, not raw arbitrary error text")
		assertRegistryRepairGuidance(t, registryFirstField(fields, "message", "msg")+" "+registryFirstField(fields, "repair", "guidance", "repair_hint", "remediation", "hint"))
		matches++
	}
	assert.Positive(t, matches, "Q-PUBLISH A: cleanup failed after publication but no contextual Error-level diagnosis with safe cause and repair guidance was captured")
}
