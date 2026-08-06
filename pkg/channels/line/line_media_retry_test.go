package line

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// serverErrorResponse builds a 500 LINE API response, which callAPI classifies
// as channels.ErrTemporary via channels.ClassifySendError.
func serverErrorResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"message":"internal error"}`))),
		Header:     make(http.Header),
	}
}

// TestSendMedia_MidLoopFailureAfterPartialSuccessIsPermanent is the CRITICAL
// sendfile-fix review regression test for LINE: LINE never uploads real
// media (the Messaging API requires public URLs), so each part's caption/
// filename text goes out via the Push API instead. Part 1's push succeeds;
// part 2's push fails with a 500, which callAPI/ClassifySendError classifies
// as channels.ErrTemporary. Before the fix, SendMedia returned that
// ErrTemporary straight through with no awareness that part 1's text already
// sent, so Manager.sendMediaWithRetry would retry the WHOLE message —
// resending part 1's caption text, duplicating it for the user. The fix must
// classify this permanent (channels.ErrSendFailed) instead.
func TestSendMedia_MidLoopFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })
	ch.SetMediaStore(media.NewFileMediaStore())

	pushCalls := 0
	ch.apiClient.Transport = lineRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		pushCalls++
		if pushCalls == 2 {
			return serverErrorResponse(), nil
		}
		return okResponse(), nil
	})

	msg := bus.OutboundMediaMessage{
		Channel: "line",
		ChatID:  "U1",
		Parts: []bus.MediaPart{
			{Type: "image", Filename: "a.jpg", Caption: "first part"},
			{Type: "image", Filename: "b.jpg", Caption: "second part"},
		},
	}

	err := ch.SendMedia(context.Background(), msg)

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's push failure")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if pushCalls != 2 {
		t.Fatalf("push API calls = %d, want 2 (part 1 succeeded, part 2 attempted then failed)", pushCalls)
	}
}

// TestSendMedia_FirstPartFailureWithNothingSentIsTemporary is the control
// case: the very first part's push fails (sentCount == 0, nothing has
// reached LINE yet). The failure must keep its ErrTemporary classification
// so sendMediaWithRetry still retries — a bare retry here cannot duplicate
// anything.
func TestSendMedia_FirstPartFailureWithNothingSentIsTemporary(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })
	ch.SetMediaStore(media.NewFileMediaStore())

	pushCalls := 0
	ch.apiClient.Transport = lineRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		pushCalls++
		return serverErrorResponse(), nil
	})

	msg := bus.OutboundMediaMessage{
		Channel: "line",
		ChatID:  "U1",
		Parts: []bus.MediaPart{
			{Type: "image", Filename: "a.jpg", Caption: "only part"},
		},
	}

	err := ch.SendMedia(context.Background(), msg)

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from the push failure")
	}
	if !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v, want ErrTemporary (nothing sent yet, safe to retry)", err)
	}
	if errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v must not be classified permanent when nothing was sent", err)
	}
	if pushCalls != 1 {
		t.Fatalf("push API calls = %d, want 1", pushCalls)
	}
}
