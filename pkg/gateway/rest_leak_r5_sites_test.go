// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

// Round 5 follow-up: the five fix sites that had no handler-level test. Each
// drives the real handler with a stub that fails with a hostile cause (an
// absolute path, an OS error, an address) and asserts the client-visible text is
// fixed. The same r5AssertClean fragments as rest_leak_r5_test.go apply.

type failingTranscriber struct{ cause error }

func (failingTranscriber) Name() string { return "stub-transcriber" }
func (f failingTranscriber) Transcribe(context.Context, string) (*voice.TranscriptionResponse, error) {
	return nil, f.cause
}

// Transcription 502: the provider's error never reaches the client.
func TestR5_Sites_TranscriptionFailureIsFixedText(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	hostile := errors.New("provider call to https://10.1.2.3:8443/v1/audio failed: open /secret/home/audio.tmp: permission denied")
	api.agentLoop.SetTranscriber(failingTranscriber{cause: hostile})

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("audio", "clip.webm")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("not really audio"))
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice/transcribe", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	api.HandleTranscribe(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "transcription failed", "the failure is still reported")
	r5AssertClean(t, w.Body.String(), "10.1.2.3", "secret", "audio.tmp")
}

// Entitlement 502: an upstream that cannot be reached answers fixed text - no
// address, no dial error.
func TestR5_Sites_EntitlementUpstreamFailureIsFixedText(t *testing.T) {
	const ref = "R5_ENTITLEMENT_KEY"
	t.Setenv(ref, "sk-r5-secret")
	stub := newEntitlementStub(t, "gpt-a")
	base := stub.srv.URL
	stub.srv.Close() // nothing listens any more: the dial fails with the address in its error
	api := newEntitlementAPI(t, entitlementRow("openai", "openai-compatible", base, ref, false))

	w := postEntitlement(t, api, "openai")

	require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "could not fetch upstream model list", "the failure is still reported")
	r5AssertClean(t, w.Body.String(), "127.0.0.1", "dial", "connection refused", "sk-r5-secret", strings.TrimPrefix(base, "http://"))
}

// Capture-offer frame: a negotiation failure reaches the browser as an error
// frame with fixed text. This is the "current error" path of the identity test,
// with a hostile cause.
func TestR5_Sites_CaptureOfferFailureFrameIsFixedText(t *testing.T) {
	writer, client := captureWriterPair(t)
	negotiated := make(chan context.Context, 4)
	relay := &wireIngestRelay{entered: make(chan wireIngestOffer, 4)}
	relay.negotiate = func(ctx context.Context, _ wireIngestOffer) (string, error) {
		negotiated <- ctx
		return "", errors.New("encoder open /secret/home/enc.sock: permission denied (peer 10.9.8.7)")
	}
	cs, err := browser.NewCaptureSessionWithDeps(nil, "answer", relay, nil, nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	source, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, epoch, err := cs.BindIngestContext(source, func(string, *string, int, int, int) error { return nil }, func() {})
	require.NoError(t, err)
	id, generation, target := 9, 1, "page-a"
	offer := queuedCaptureOffer{
		frame:    generated.BrowserCaptureOfferFrame{Sdp: "sdp", OfferId: &id, CaptureGeneration: &generation, TargetId: &target},
		deadline: time.Now().Add(2 * time.Second),
	}

	result := make(chan error, 1)
	go func() { result <- writer.answerCaptureOffer(source, cs, epoch, offer) }()
	select {
	case <-negotiated:
	case <-time.After(2 * time.Second):
		t.Fatal("the offer never reached capture admission")
	}
	select {
	case err := <-result:
		require.Error(t, err, "a failed offer still returns its error to the caller")
	case <-time.After(2 * time.Second):
		t.Fatal("the answer did not finish")
	}

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := client.ReadMessage()
	require.NoError(t, err)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(raw, &frame))
	assert.Equal(t, "error", frame["type"], string(raw))
	assert.Contains(t, frame["message"], "capture ingest offer failed", "the failure is still reported")
	r5AssertClean(t, string(raw), "secret", "enc.sock", "10.9.8.7")
}

// failingResumeDispatcher accepts the answer and then fails to resume the turn.
type failingResumeDispatcher struct{ cause error }

func (f failingResumeDispatcher) DispatchResume(*askuser.PendingSet, string) error { return f.cause }

// Ask-user resume-failed frame: the answer was accepted but the turn could not
// resume; the frame says so without the dispatcher's cause.
func TestR5_Sites_AskUserResumeFailureFrameIsFixedText(t *testing.T) {
	reg := askuser.NewRegistry(nil, failingResumeDispatcher{
		cause: errors.New("publish to /secret/home/bus.sock failed: permission denied")}, askuser.Options{})
	require.NoError(t, reg.CreatePending(askSetFixture()))
	t.Cleanup(reg.Quiesce)
	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"ask_user_answer","card_id":"ask_1","session_id":"session_owner_1",
		"answers":[{"header":"Scope","selected":["Only unanswered"]},{"header":"Deploy","selected":["Staging"]}]
	}`), &frame))
	wc := &wsConn{userID: "daniel", sendCh: make(chan []byte, 4)}

	(&WSHandler{askUserReg: reg}).handleAskUserAnswer(wc, frame)

	frames := drainConnFrames(wc)
	require.Contains(t, frames, `"type":"error"`, frames)
	assert.Contains(t, frames, "ask_user_answer", "the failure is still reported")
	r5AssertClean(t, frames, "secret", "bus.sock")
}
