// Omnipus — #1329 round-2 manual question-action storage failures
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH F1's sample was explicitly adopted by setup-pill-db in response to QA
// Q1=A (2026-10-17). This exact oracle is a dispatch decision, NOT observed
// implementation output. It applies to both valid Answer and valid Cancel.
const qaStorageFailureMessage = "Could not save your answer or cancellation. The card is still open. Check gateway storage, then retry."

// TestAskUserStorageFailureClassificationAndRedaction traces to SFH F1 and
// the 2026-10-17 team-lead dispatch. Handler, registry, validation, store and
// atomic file writer are REAL. Faults sit at the filesystem edge, never in a
// fake Registry.Submit, MetaStore.SetMeta or rejection classifier. The two
// failures also require one fixed message despite different OS causes/paths.
// GREEN, mutations and the proof-of-failability gate are deferred to CHECK.
func TestAskUserStorageFailureClassificationAndRedaction(t *testing.T) {
	for _, action := range []string{"answer", "cancel"} {
		t.Run(action, func(t *testing.T) {
			for _, fault := range []string{"session_directory_is_file", "pending_state_file_is_directory"} {
				t.Run(fault, func(t *testing.T) {
					qaStorageFailureCase(t, action == "cancel", fault)
				})
			}
		})
	}
}

type qaStorageCardSink struct{ cards []string }

func (s *qaStorageCardSink) EmitCard(set *askuser.PendingSet) {
	s.cards = append(s.cards, set.CardID+":"+string(set.Status))
}

func qaStorageFailureCase(t *testing.T, cancel bool, fault string) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	resume, sink := &qaQueueResumeRecorder{}, &qaStorageCardSink{}
	reg := askuser.NewRegistry(store, resume, askuser.Options{Sink: sink})
	t.Cleanup(reg.Quiesce) // no default-safe question, so no long timer is armed.
	const questionSecret = "PRIVATE-QUESTION-CONTENT-1329"
	const contextSecret = "PRIVATE-QUESTION-CONTEXT-1329"
	const answerSecret = "PRIVATE-SUBMITTED-FREE-TEXT-1329"
	head := qaRejectionCard(meta.ID, questionSecret)
	head.Questions[0].Context = contextSecret
	queued := qaRejectionCard(meta.ID, "PRIVATE-QUEUED-QUESTION-1329")
	require.NoError(t, reg.CreatePending(head))
	require.NoError(t, reg.CreatePending(queued))
	pendingPath := filepath.Join(store.BaseDir(), meta.ID, "pending_ask.json")
	before, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	require.Contains(t, string(before), queued.CardID, "instrument: B must really be durably queued")
	initialEmissions := append([]string(nil), sink.cards...)
	require.Equal(t, []string{head.CardID + ":pending"}, initialEmissions)

	var submitted generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(head.CardID, meta.ID, cancel), &submitted))
	if !cancel {
		submitted.Answers[0].Selected = nil
		answer := answerSecret
		submitted.Answers[0].FreeText = &answer // free text is a valid answer, not invalid-option input.
	}
	blocked := pendingPath
	if fault == "session_directory_is_file" {
		blocked = filepath.Dir(pendingPath)
	}
	restore, osCause := qaBlockQuestionStorage(t, blocked, fault == "session_directory_is_file")
	// Test the actual storage instrument too. The patch repeats the existing
	// durable bytes; a failed probe cannot consume A or modify its queue.
	unchanged := string(before)
	saveErr := store.SetMeta(meta.ID, session.MetaPatch{PendingAskJSON: &unchanged})
	require.ErrorContains(t, saveErr, osCause, "instrument: the REAL card-state save must fail for the injected filesystem cause")

	logs := captureSlogJSON(t)
	slog.Error("qa-1329: storage log capture control")
	control := findAskUserGatewayLog(t, logs, "qa-1329: storage log capture control")
	require.Equal(t, "ERROR", control["level"], "instrument: capture must actually retain ERROR records")
	logs.Reset() // the control must never satisfy a production-log assertion.
	h := &WSHandler{askUserReg: reg}
	client, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}
	h.handleAskUserAnswer(client, submitted)

	response := qaAssertCorrelatedError(t, client, other, head.CardID, meta.ID)
	assert.Equal(t, qaStorageFailureMessage, response["message"], "a valid action's storage failure needs the approved fixed, actionable message")
	message, ok := response["message"].(string)
	require.True(t, ok, "the client message must be text")
	r5AssertClean(t, message, store.BaseDir(), blocked, osCause, meta.ID, head.CardID, questionSecret, contextSecret, answerSecret,
		"pending_ask.json", "pending_user_messages", "owner_scope_kind")
	wire, err := json.Marshal(response)
	require.NoError(t, err)
	r5AssertClean(t, string(wire), store.BaseDir(), blocked, osCause, questionSecret, contextSecret, answerSecret,
		"pending_ask.json", "pending_user_messages", "owner_scope_kind")
	select {
	case extra := <-client.sendCh:
		t.Errorf("a failed save must emit one error, not an acceptance or replacement card: %s", extra)
	default:
	}
	qaAssertStorageFailureLog(t, logs.String(), meta.ID, head.CardID, blocked, osCause,
		questionSecret, contextSecret, answerSecret, queued.Questions[0].Question)

	active, visible := reg.PendingForSession(meta.ID)
	require.True(t, visible, "a failed save must leave the active card pending")
	assert.Equal(t, head.CardID, active.CardID)
	assert.Equal(t, askuser.StatusPending, active.Status)
	assert.Empty(t, active.Answers, "a failed Answer must not publish unsaved answers")
	_, queuedVisible := reg.Get(queued.CardID)
	assert.False(t, queuedVisible, "the failed save must not promote B")
	assert.Equal(t, 2, reg.PendingCount(), "a failed save must retain A and B")
	assert.Equal(t, initialEmissions, sink.cards, "a failed save must emit neither terminal A nor replacement B")
	assert.Zero(t, resume.count(), "a failed save must never resume the turn")

	restore()
	after, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a failed save must leave durable state byte-for-byte unchanged")
	// Positive validity/recovery control: exactly the SAME frame, with only
	// storage repaired, must now consume A, promote B and resume once.
	h.handleAskUserAnswer(client, submitted)
	promoted, visible := reg.PendingForSession(meta.ID)
	require.True(t, visible)
	assert.Equal(t, queued.CardID, promoted.CardID, "a deliberate retry after recovery must promote B")
	assert.Equal(t, 1, reg.PendingCount())
	assert.Equal(t, 1, resume.count(), "a successful retry must resume exactly once")
	terminal := "answered"
	if cancel {
		terminal = "cancelled"
	}
	assert.Equal(t, []string{head.CardID + ":pending", head.CardID + ":" + terminal, queued.CardID + ":pending"}, sink.cards)
	select {
	case extra := <-client.sendCh:
		t.Errorf("the valid retry must succeed without another action error: %s", extra)
	default:
	}
}

// qaBlockQuestionStorage changes a REAL filesystem object, rather than chmod
// (which would not bind root or Windows). Its independent OS operation supplies
// the platform's own cause text; no expected client/log copy is copied from the
// handler. Restoration is error-checked and idempotent, including RED teardown.
func qaBlockQuestionStorage(t *testing.T, path string, directoryToFile bool) (restore func(), osCause string) {
	t.Helper()
	preserved := path + ".qa-preserved"
	require.NoError(t, os.Rename(path, preserved))
	restored := false
	restore = func() {
		if restored {
			return
		}
		require.NoError(t, os.RemoveAll(path), "restore: remove the hostile object")
		require.NoError(t, os.Rename(preserved, path), "restore: put the original storage back")
		restored = true
	}
	t.Cleanup(restore)
	var probeErr error
	if directoryToFile {
		require.NoError(t, os.WriteFile(path, []byte("qa-1329: a file cannot be a session directory"), 0o600))
		probeErr = os.MkdirAll(path, 0o700)
	} else {
		require.NoError(t, os.Mkdir(path, 0o700))
		probe, err := os.CreateTemp(filepath.Dir(path), "question-save-probe-*")
		require.NoError(t, err)
		require.NoError(t, probe.Close())
		t.Cleanup(func() { require.NoError(t, os.Remove(probe.Name())) })
		probeErr = os.Rename(probe.Name(), path)
	}
	require.Error(t, probeErr, "instrument: a real filesystem write must fail, including under root")
	cause := errors.Unwrap(probeErr)
	require.NotNil(t, cause, "instrument: filesystem error must retain its underlying OS cause")
	return restore, cause.Error()
}

func qaAssertStorageFailureLog(t *testing.T, logs, sessionID, cardID, blockedPath, osCause string, secrets ...string) {
	t.Helper()
	diagnostics := 0
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record), "captured log must be valid JSON: %s", line)
		assert.NotEqual(t, "invalid_answer", record["reason"], "storage failure must never be classified as invalid_answer: %s", line)
		if record["session_id"] != sessionID || record["card_id"] != cardID {
			continue
		}
		underlying, carriesError := record["error"].(string)
		if !carriesError {
			continue
		}
		diagnostics++
		assert.Equal(t, "ERROR", record["level"], "a state-save failure must be an ERROR, never validation-level noise")
		assert.Contains(t, underlying, blockedPath, "the operator diagnostic must retain the real failing storage path")
		assert.Contains(t, underlying, osCause, "the operator diagnostic must retain the underlying OS cause")
	}
	assert.Greater(t, diagnostics, 0, "missing ERROR diagnostic with submitted session/card ids AND underlying error; captured=%s", logs)
	for _, secret := range secrets {
		assert.NotContains(t, logs, secret, "storage diagnostics must not disclose answers, question text or context")
	}
}
