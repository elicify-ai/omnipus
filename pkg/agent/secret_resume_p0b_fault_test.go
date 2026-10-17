package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P0-b / FR-062/068/087: a different damaged vault entry cannot strand this
// card's healthy committed references or prevent their reference-only resume.
func TestWU4P0BUnrelatedSensitiveValueDamageDoesNotStrandResume(t *testing.T) {
	f := newSecretWakeAcceptanceFixture(t, false, false)
	var diagnostics []string
	defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
	child := f.launch(t, "p0b-unrelated-damage")
	pending := f.pending(t, child)
	wu4P0DamageUnrelatedEntry(t, f)
	values := map[string]string{"api_key": secretWakeAcceptanceValue}
	_, submitErr := f.al.SecretRequests().Submit(pending.Card.RequestId, pending.Owner, gen.SecretSubmitRequest{Values: &values})
	if submitErr != nil {
		diagnostics = append(diagnostics, submitErr.Error())
	}
	submitted, err := f.al.SecretRequests().Get(pending.Card.RequestId, pending.Owner)
	require.NoError(t, err)
	wu4P0RequireSavedReferences(t, f, submitted)
	readable, scanErr := f.store.SensitiveValues()
	require.Error(t, scanErr, "instrument: the unrelated damaged entry really reaches the complete sensitive-set scan")
	require.Contains(t, readable, secretWakeAcceptanceValue, "the newly committed card value is readable and must stay masked")
	require.Contains(t, readable, wu4P0WitnessVal)
	require.NoError(t, submitErr, "P0-b: unrelated damage must not strand a healthy committed secret continuation")
	wake := f.takeWake(t, pending.Card.RequestId)
	require.Equal(t, "system", wake.Channel)
	require.NoError(t, wu4P0ProcessWake(t, f, wake, &diagnostics))
	calls := f.provider.calls(child.SessionID)
	require.Len(t, calls, 1, "the healthy committed request must reach one actual helper model call")
	requireSecretWakePrompt(t, calls[0], submitted)
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted))
	masked := f.al.GetConfig().SensitiveDataReplacer().Replace("public-control=" + secretWakeAcceptanceValue)
	require.Contains(t, masked, "public-control=")
	require.NotContains(t, masked, secretWakeAcceptanceValue)
}

// Binding B: direct-bus secret wakes have their OWN stable request-id claim;
// they must not create generic marker/Ack records, including on a replay.
func TestWU4P0BDirectBusSecretWakeUsesOnlyMandatoryClaim(t *testing.T) {
	f := newSecretWakeAcceptanceFixture(t, false, false)
	var diagnostics []string
	defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
	child := f.launch(t, "p0b-direct-bus-control")
	submitted, wake := f.submit(t, f.pending(t, child))
	require.NoError(t, wu4P0ProcessWake(t, f, wake, &diagnostics))
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted), "ClaimResume remains mandatory")
	calls := f.provider.calls(child.SessionID)
	require.Len(t, calls, 1)
	requireSecretWakePrompt(t, calls[0], submitted)
	require.NoError(t, wu4P0ProcessWake(t, f, wake, &diagnostics))
	require.Len(t, f.provider.calls(child.SessionID), 1, "duplicate direct-bus delivery must not inject a second turn")
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted))
	wu4P0RequireNoGenericMarker(t, f, submitted, wake.Metadata["steer_message_id"])
	wu4P0RequireSavedReferences(t, f, submitted)
}

// Fault the redundant marker's REAL storage-admission boundary: its ID and
// content together exceed the documented archive envelope bound. The actual
// secret ID, request binding and reference-only payload are left unchanged.
func TestWU4P0BGenericMarkerAppendRefusalCannotStrandClaimedWake(t *testing.T) {
	f := newSecretWakeAcceptanceFixture(t, false, false)
	var diagnostics []string
	defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
	child := f.launch(t, "p0b-marker-cut")
	submitted, wake := f.submit(t, f.pending(t, child))
	messageID := strings.Repeat("m", memory.EncodedLineBound/2+1)
	wake.Metadata["steer_message_id"] = messageID
	probeErr := f.al.GetSessionStore().AppendTranscriptStrict(child.SessionID, session.TranscriptEntry{
		ID: "consumed-" + messageID, Type: session.EntryTypeSystem, Role: "system",
		Content: "consumed " + messageID, AgentID: "omnipus",
	})
	require.ErrorContains(t, probeErr, "envelope bound", "instrument: the generic marker really cannot be published")
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted), "the instrument must not fabricate a secret claim")
	err := wu4P0ProcessWake(t, f, wake, &diagnostics)
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted), "the real mandatory ClaimResume must succeed before this cut")
	if err != nil {
		t.Errorf("binding B: an unpublishable redundant marker must not fail the secret-only continuation; error prefix: %.180s", err.Error())
	}
	calls := f.provider.calls(child.SessionID)
	require.Len(t, calls, 1, "P0-b: a claimed secret must execute once, not be stranded by a later generic marker failure")
	requireSecretWakePrompt(t, calls[0], submitted)
	wu4P0RequireNoGenericMarker(t, f, submitted, messageID)
	wu4P0RequireSavedReferences(t, f, submitted)
}

func TestWU4P0BGenericInboxAckFailureCannotStrandClaimedWake(t *testing.T) {
	f := newSecretWakeAcceptanceFixture(t, false, false)
	var diagnostics []string
	defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
	child := f.launch(t, "p0b-ack-cut")
	submitted, wake := f.submit(t, f.pending(t, child))
	inboxDir := t.TempDir()
	inbox := session.NewMessageInboxStore(inboxDir)
	f.al.SetSessionMessagingStores(inbox, f.al.GetSessionLifecycleStore())
	// Only THIS helper's inbox path is blocked. The distinct parent's inbox
	// remains writable, so ordinary completion delivery is not being faulted.
	blocked := filepath.Join(inboxDir, child.SessionID+".jsonl")
	require.NoError(t, os.Mkdir(blocked, 0o700))
	require.Error(t, inbox.Ack(child.SessionID, []string{wake.Metadata["steer_message_id"]}),
		"instrument: the real Ack path must fail, not silently accept our fault")
	err := wu4P0ProcessWake(t, f, wake, &diagnostics)
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted), "the secret claim precedes the legacy Ack cut")
	assert.NoError(t, err, "binding B: direct-bus secret wakes must bypass the redundant failing Ack")
	calls := f.provider.calls(child.SessionID)
	require.Len(t, calls, 1, "P0-b: a claimed helper must execute once despite a broken unrelated inbox Ack path")
	requireSecretWakePrompt(t, calls[0], submitted)
	wu4P0RequireNoGenericMarker(t, f, submitted, wake.Metadata["steer_message_id"])
	entries, readErr := os.ReadDir(blocked)
	require.NoError(t, readErr)
	require.Empty(t, entries, "bypassing Ack must leave the blocked generic inbox path untouched")
	wu4P0RequireSavedReferences(t, f, submitted)
}

// The B bypass is NOT permission to bypass the actual request-id archive
// publication. Use the existing registration seam to fault only real file I/O.
func TestWU4P0BClaimWriteFailurePreventsHelperModelInput(t *testing.T) {
	f := newSecretWakeAcceptanceFixture(t, false, false)
	var diagnostics []string
	defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
	child := f.launch(t, "p0b-mandatory-claim-cut")
	submitted, wake := f.submit(t, f.pending(t, child))
	path := filepath.Join(f.al.GetSessionStore().BaseDir(), child.SessionID, "transcript.jsonl")
	backup := path + ".p0-claim-backup"
	armed := false
	previous := turnRegisteredTestHook
	turnRegisteredTestHook = func(id string, _ *turnState) {
		if id != child.SessionID {
			return
		}
		require.False(t, armed, "one admitted registration reaches the claim cut")
		require.NoError(t, os.Rename(path, backup))
		require.NoError(t, os.Mkdir(path, 0o700))
		armed = true
	}
	t.Cleanup(func() { turnRegisteredTestHook = previous })
	restore := func() {
		if armed {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
			armed = false
		}
	}
	t.Cleanup(restore)
	err := wu4P0ProcessWake(t, f, wake, &diagnostics)
	wasArmed := armed
	restore()
	require.True(t, wasArmed, "instrument: the filesystem fault must be reached after admission")
	require.Error(t, err, "mandatory ClaimResume publication failure must remain visible")
	require.Empty(t, f.provider.calls(child.SessionID), "no secret continuation may reach the model without a durable claim")
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted))
	wu4P0RequireSavedReferences(t, f, submitted)
	// Remove the storage cut and retry through actual admission, not a direct
	// fabricated claim. A failed publication must not consume the request id.
	turnRegisteredTestHook = previous
	require.NoError(t, wu4P0ProcessWake(t, f, wake, &diagnostics), "an unclaimed failed delivery remains executable after storage is repaired")
	calls := f.provider.calls(child.SessionID)
	require.Len(t, calls, 1)
	requireSecretWakePrompt(t, calls[0], submitted)
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted))
	_, claimed, claimErr := f.al.SecretRequests().ClaimResume(submitted.Card.RequestId, child.SessionID)
	require.NoError(t, claimErr)
	require.False(t, claimed, "the real durable request-id claim remains exactly once")
}
