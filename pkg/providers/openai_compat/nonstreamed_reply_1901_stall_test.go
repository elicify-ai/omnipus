package openai_compat

// Issue #1901, round 2 (CHECK finding on the round-1 fix f747db8d4): the
// non-streamed-JSON branch of ChatStream (provider.go::ChatStream,
// acceptNonStreamedJSONReply) returns BEFORE common.WatchStreamStall is
// armed. The streaming client has no overall timeout and the transport's
// connection guard bounds only the response HEADERS, so a provider that
// sends 200 + Content-Type application/json and then stalls — no body at
// all, or a partial body — is never cut: common.ReadAndParseResponse blocks
// forever in its buffered peek/read. These tests hold the round-1 decision
// (the JSON reply is accepted) alongside the streaming stall contract
// (founder decision 2026-09-14, UAT E-15c): a call receiving NO bytes for
// the silence limit is aborted as a provider stall; a call that delivers,
// however slowly, is never cut.
//
// Oracles (none read from the implementation's behaviour):
//   - Stall identity: the SAME stall error the SSE path returns, asserted
//     the way stream_stall_test.go::TestChatStream_TotallySilentStreamIsAbortedAsAStall
//     asserts it — errors.Is(err, common.ErrStreamStalled), a typed
//     *common.StallError whose SilentFor is the CONFIGURED limit, and it
//     must not be reported as a transient streaming read error.
//   - Bounded time: ChatStream must return within the test's hard deadline
//     (5 s, standing in for "the turn is not hung"); a hang FAILS the test.
//     The stall may not fire before the configured silence limit has
//     actually elapsed.
//   - Control (slow-but-complete): a JSON reply completing WELL inside the
//     silence limit must still be accepted with content, finish reason and
//     the text delivered to onChunk (#1901 round-1 decision; onChunk's
//     accumulated-text contract per the ChatStream doc comment).
//
// Timing ratios, not absolute times (technique of stream_stall_test.go):
// the configured silence limit stands in for the shipped 5-minute default;
// the control's deliberate delay is 20% of the limit.
//
// Goroutine hygiene: ChatStream runs in a goroutine reporting through a
// BUFFERED channel, so a call that hangs and is later released by cleanup
// can still complete without blocking on send. Cleanup releases the
// handler FIRST and closes the server SECOND — httptest.Server.Close waits
// for outstanding requests, so the reverse order would hang the cleanup —
// which unblocks the abandoned call's read so it exits instead of leaking
// into later tests.
//
// Deferred to CHECK (separate instance, per process): mutation kills for
// tests 1 and 2, the green run after the fix, and the Proof-of-failability
// items beyond "seen red for the right reason".

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

const (
	// jsonStallLimit is the test's configured silence limit — the stand-in
	// for the shipped common.DefaultStreamStallTimeout (5 min). Assertions
	// derive their numbers from this constant, not from raw literals.
	jsonStallLimit = 100 * time.Millisecond
	// jsonStallHardDeadline bounds how long ChatStream may take before the
	// test declares a hang. A few seconds: far above the silence limit and
	// its scheduling noise, far below the suite timeout, so a hang fails
	// THIS test, not the run.
	jsonStallHardDeadline = 5 * time.Second
	// jsonSlowDelay is the control's deliberate provider delay — 20% of the
	// silence limit: delivered, therefore never cut, whatever arm-time
	// detail the fix chooses.
	jsonSlowDelay = 20 * time.Millisecond
)

// jsonReplyBody is the round-1 issue example: a complete, valid
// non-streamed chat.completion body.
const jsonReplyBody = `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"plain answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`

// stallingJSONServer serves HTTP 200 with Content-Type application/json,
// flushes the headers immediately, then writes each chunk of chunks
// (flushing after each) and finally blocks until release is closed — the
// shape of a provider that committed the headers and then went mute before
// (or partway through) the body. Cleanup releases the handler and closes
// the server in that order: Close waits for outstanding requests, so the
// handler must be unblocked first, which in turn unblocks the client's
// abandoned read when a test has already failed on its deadline.
func stallingJSONServer(t *testing.T, chunks ...string) (*httptest.Server, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		for _, chunk := range chunks {
			if _, err := w.Write([]byte(chunk)); err != nil {
				break
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv, release
}

// jsonStallProvider builds a real provider with the shortened silence limit.
func jsonStallProvider(t *testing.T, url string) *Provider {
	t.Helper()
	return mustNewProvider(t, "key", url, "", WithStreamStallTimeout(jsonStallLimit))
}

// chatStreamResult carries one bounded ChatStream call's outcome. Buffered
// result channel: a hung call released by cleanup must never block on send.
type chatStreamResult struct {
	resp *LLMResponse
	err  error
}

// runChatStreamBounded calls p.ChatStream in a goroutine and waits at most
// deadline for it. It reports the outcome and the elapsed time; hung=true
// means the call did not return within the deadline (the defect under
// test). The goroutine never touches t; assertions stay in the test body.
func runChatStreamBounded(t *testing.T, p *Provider, deadline time.Duration) (res chatStreamResult, hung bool, elapsed time.Duration) {
	t.Helper()
	ch := make(chan chatStreamResult, 1)
	start := time.Now()
	go func() {
		resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
			nil, "test-model", nil, nil, nil)
		ch <- chatStreamResult{resp: resp, err: err}
	}()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r, false, time.Since(start)
	case <-timer.C:
		return chatStreamResult{}, true, time.Since(start)
	}
}

// wantStallError asserts the shared stall contract at the ChatStream
// boundary: bounded return, the typed stall error with the configured
// limit, and no transient streaming-read-error masquerade. Kept in one
// place so tests 1 and 2 assert the oracle identically.
func wantStallError(t *testing.T, elapsed time.Duration, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("ChatStream() error = nil, want the typed stall error: a JSON reply that stalls must be cut by the silence limit, not succeed or hang")
	}
	if elapsed < jsonStallLimit {
		t.Fatalf("ChatStream() returned after %s, before the %s silence limit had elapsed — something other than the stall monitor cut the call", elapsed, jsonStallLimit)
	}
	if !errors.Is(err, common.ErrStreamStalled) {
		t.Fatalf("ChatStream() error = %v, want errors.Is(err, common.ErrStreamStalled): the JSON path must report the SAME stall error the SSE path returns", err)
	}
	var se *common.StallError
	if !errors.As(err, &se) {
		t.Fatalf("ChatStream() error = %v, want a typed *common.StallError (as the SSE path returns)", err)
	}
	if se.SilentFor != jsonStallLimit {
		t.Fatalf("StallError.SilentFor = %s, want the configured limit %s", se.SilentFor, jsonStallLimit)
	}
	if strings.Contains(err.Error(), "streaming read error") {
		t.Fatalf("a stall must not be reported as a transient streaming read error: %v", err)
	}
}

// TestIssue1901_ChatStream_JSONHeadersThenStallIsCut: a provider commits
// 200 + application/json headers and then sends NO body bytes. The
// connection guard covers only the headers, and the streaming client has no
// overall timeout, so the stall monitor is the only thing that can cut this
// call. It must, within the silence limit, with the SSE path's stall error.
func TestIssue1901_ChatStream_JSONHeadersThenStallIsCut(t *testing.T) {
	srv, _ := stallingJSONServer(t) // headers only, then total body silence
	p := jsonStallProvider(t, srv.URL)

	res, hung, elapsed := runChatStreamBounded(t, p, jsonStallHardDeadline)
	if hung {
		t.Fatalf("ChatStream() did not return within %s — a JSON reply with headers but no body hangs forever (the defect: the stall monitor is never armed on the JSON path)", jsonStallHardDeadline)
	}
	wantStallError(t, elapsed, res.err)
}

// TestIssue1901_ChatStream_JSONPartialBodyThenStallIsCut: the provider
// commits the headers and the FIRST HALF of a valid JSON body, then goes
// mute. Bytes have arrived, but the body is incomplete and nothing re-arms
// a clock on this path, so the same silence limit must cut the call with
// the same stall error.
func TestIssue1901_ChatStream_JSONPartialBodyThenStallIsCut(t *testing.T) {
	// First half of the complete body, cut mid-structure: fewer bytes than
	// the parser's peek window, so the read is genuinely still waiting.
	partial := jsonReplyBody[:len(jsonReplyBody)/2]
	srv, _ := stallingJSONServer(t, partial)
	p := jsonStallProvider(t, srv.URL)

	res, hung, elapsed := runChatStreamBounded(t, p, jsonStallHardDeadline)
	if hung {
		t.Fatalf("ChatStream() did not return within %s — a JSON reply with a partial body then silence hangs forever (the defect: the stall monitor is never armed on the JSON path)", jsonStallHardDeadline)
	}
	wantStallError(t, elapsed, res.err)
}

// TestIssue1901_ChatStream_JSONSlowButCompleteStillAccepted: the control.
// The provider waits a little (well inside the silence limit) and then
// delivers the COMPLETE valid JSON body. The call must still be accepted —
// content, finish reason, and the text through onChunk — so the stall cut
// cannot become a wall-clock limit that breaks slow-but-delivering replies.
// Green before and after the fix.
func TestIssue1901_ChatStream_JSONSlowButCompleteStillAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(jsonSlowDelay)
		_, _ = w.Write([]byte(jsonReplyBody))
	}))
	defer srv.Close()
	p := jsonStallProvider(t, srv.URL)

	var chunks []string
	start := time.Now()
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "test-model", nil, func(accumulated string) { chunks = append(chunks, accumulated) }, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v, want nil: a JSON reply completing within the silence limit (%s delay vs %s limit) must still be accepted", err, jsonSlowDelay, jsonStallLimit)
	}
	if resp == nil {
		t.Fatal("ChatStream() resp = nil, want a response")
	}
	if resp.Content != "plain answer" {
		t.Errorf("resp.Content = %q, want %q: the complete reply must be accepted intact", resp.Content, "plain answer")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("resp.FinishReason = %q, want %q (body finish_reason, unchanged by normalization)", resp.FinishReason, "stop")
	}
	if elapsed := time.Since(start); elapsed < jsonSlowDelay {
		t.Errorf("call finished after %s, before the fixture's %s delay — the fixture did not actually delay", elapsed, jsonSlowDelay)
	}
	if len(chunks) == 0 || chunks[len(chunks)-1] != "plain answer" {
		t.Fatalf("last onChunk accumulated = %q (over %d calls), want %q (accumulated-text contract)", chunks, len(chunks), "plain answer")
	}
}
