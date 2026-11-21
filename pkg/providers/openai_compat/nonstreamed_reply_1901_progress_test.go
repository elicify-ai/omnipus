package openai_compat

// Issue #1901, round 3 (CHECK findings F1–F3 in
// /workspace/logs/1901/c2-audit-report.md): the round-2 suite could not kill
// three mutants of the JSON-stall implementation (provider.go::
// stallArmingBody.Read, jsonBodyReadError). These tests close those gaps.
// They are mutation-killing tests: all three PASS on the reviewed head, and
// each kills exactly its named mutant in a scratch clone
// (/workspace/logs/1901/r3-test-mut-*.log). Oracles are derived from the
// issue contract and the stall decision (founder decision 2026-09-14, UAT
// E-15c: a call that keeps delivering, however slowly, is never cut; a call
// receiving nothing for the limit is aborted), from the ChatStream /
// stallArmingBody / jsonBodyReadError doc comments, and from the SSE path's
// established stall error identity — never from observed behaviour.
//
// F1 — progress re-arms the silence clock on every non-empty JSON body read.
// A reply delivered in many chunks, EVERY inter-chunk gap shorter than the
// limit but the TOTAL longer than it, must be accepted intact (content,
// finish reason, the accumulated-text onChunk contract). Kills the
// "remove stallArmingBody" mutant: without re-arming, the watch fires at one
// limit mid-body and the call is cut with the stall error instead of
// succeeding.
//
// F2 — zero-byte reads are NOT progress: only real bytes re-arm the clock.
// A body whose reads complete as (0, nil) forever is total silence to the
// monitor and must be cut by the limit with the typed stall error; a
// positive control delivering real bytes across more than one limit must
// not be cut. Kills the "arm without the n > 0 guard" mutant: with it,
// zero-byte reads reset the clock forever, the watch never fires, and the
// call survives the limit only to die at bufio's 100-consecutive-empty-read
// guard with a raw io.ErrNoProgress error instead of the typed stall error.
// stallArmingBody is installed by ChatStream itself, so the controlled body
// is reached through ChatStream via a custom http.RoundTripper (ChatStream's
// streamingTransport returns a non-*http.Transport unchanged).
//
// F3 — caller cancellation wins over stall classification: cancelling the
// caller context while a JSON body read is incomplete returns the caller's
// context error (errors.Is(err, context.Canceled)), never a stall error —
// including when the stall watch has ALSO fired (the read is held open
// across the monitor's close, so the fixture itself enforces the ordering
// "watch fired, then caller cancelled"). Kills the "delete the ctx.Err()
// branch" mutant in jsonBodyReadError. The controlled body's closed-read
// error deliberately carries NO cancellation wording, so a mutant that
// returns the raw read error cannot pass by coincidence; the real-transport
// control subtest CAN pass under that mutant (Go's transport wraps the real
// context error into the read failure), which is why it is an instrument
// control, not a kill.
//
// Timing ratios, not absolute times (technique of
// nonstreamed_reply_1901_stall_test.go): the configured 100 ms silence limit
// stands in for the shipped 5-minute default. Gaps sit 2.5x–5x below the
// limit so scheduling noise cannot turn progress into silence; totals sit
// 2x+ above it so the property "lasted longer than one limit" is genuinely
// exercised.
//
// Hygiene: every call runs through a bounded goroutine reporting on a
// BUFFERED channel, so a hung call released by cleanup never blocks on send;
// hard deadlines fail THIS test, not the suite. The custom RoundTripper
// needs no server; the real-server handlers always return, so a plain
// deferred srv.Close() suffices.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

const (
	// progressChunkGap paces the progressing fixture: 25% of the silence
	// limit, so a real progress gap survives scheduling noise with 4x
	// headroom and can never be mistaken for silence.
	progressChunkGap = 25 * time.Millisecond
	// progressChunkCount is the number of chunks: 9 gaps of 25 ms ≈ 225 ms
	// total — more than TWICE the silence limit in total while every gap
	// stays below it. That combination (total > limit, gaps < limit) is
	// exactly the F1 property the removed wrapper would break.
	progressChunkCount = 9
	// controlChunkGap paces the positive control (F2): the brief's example
	// ratio, 40% of the limit. The scripted body has no transport jitter,
	// so the 2.5x headroom is deterministic.
	controlChunkGap = 40 * time.Millisecond
	// controlChunkCount delivers 5 chunks ≈ 200 ms — past one limit while
	// every gap stays below it.
	controlChunkCount = 5
	// zeroReadGap paces the zero-byte reader: ~10 zero reads fit inside one
	// limit, far below bufio's 100-consecutive-empty-read io.ErrNoProgress
	// guard — so on the REAL implementation the watch fires first, and only
	// the guard-removal mutant can reach the guard's raw error.
	zeroReadGap = 10 * time.Millisecond
	// cancelBeforeStall is 20% of the limit: the cancellation wins WITHOUT
	// the watch having fired (5x headroom before the watch would fire).
	cancelBeforeStall = 20 * time.Millisecond
	// cancelAfterStall is two limits: the watch has long fired when this
	// cancellation arrives, so cancellation must outrank an already-typed
	// stall, not merely avoid it.
	cancelAfterStall = 2 * jsonStallLimit
	// controlReplyDelay delays the real-server control's body three times
	// past its cancellation point, so the cancel always wins the race.
	controlReplyDelay = 3 * cancelBeforeStall
)

// errBodyClosedByMonitor is the closed-response-body read error a real
// net/http body produces when the stall monitor or the context goroutine
// closes it under a blocked read (common.IsBodyClosedStreamError matches its
// text). It deliberately carries no cancellation wording: a mutant that
// drops the context branch of jsonBodyReadError and returns the raw error
// must not pass the cancellation tests by coincidence.
var errBodyClosedByMonitor = errors.New("http: read on closed response body")

// scriptedBodyRoundTripper answers every request with one fixed response
// whose body the test fully controls. ChatStream's streamingTransport hands
// a non-*http.Transport through unchanged, so the provider sees exactly this
// response: real parser, real stall watch, controlled HTTP edge.
type scriptedBodyRoundTripper struct {
	status int
	header http.Header
	body   io.ReadCloser
}

func (rt *scriptedBodyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", rt.status, http.StatusText(rt.status)),
		StatusCode: rt.status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     rt.header,
		Body:       rt.body,
		Request:    req,
	}, nil
}

// customBodyProvider builds a real provider (real parser, real stall watch,
// the configured test silence limit) whose HTTP layer is a scripted
// RoundTripper with the given controlled body. The base URL is only a
// placeholder; the RoundTripper never dials.
func customBodyProvider(t *testing.T, body io.ReadCloser) *Provider {
	t.Helper()
	p := mustNewProvider(t, "key", "http://issue1901.test", "", WithStreamStallTimeout(jsonStallLimit))
	p.httpClient = &http.Client{Transport: &scriptedBodyRoundTripper{
		status: http.StatusOK,
		header: http.Header{"Content-Type": []string{"application/json"}},
		body:   body,
	}}
	return p
}

// runChatStreamBoundedCtx is runChatStreamBounded with a caller-supplied
// context (the cancellation tests need one they can cancel) and the JSON
// path's onChunk callback. Same contract: buffered result channel, hard
// deadline, assertions stay in the test body.
func runChatStreamBoundedCtx(
	t *testing.T, p *Provider, ctx context.Context, deadline time.Duration, onChunk func(string),
) (res chatStreamResult, hung bool, elapsed time.Duration) {
	t.Helper()
	ch := make(chan chatStreamResult, 1)
	start := time.Now()
	go func() {
		resp, err := p.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}},
			nil, "test-model", nil, onChunk, nil)
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

// jsonCompletionBody builds one complete chat.completion body carrying the
// given content (the issue's reply shape, finish_reason "stop").
func jsonCompletionBody(t *testing.T, content string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"object": "chat.completion",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
	})
	if err != nil {
		t.Fatalf("fixture: marshalling the chat.completion body failed: %v", err)
	}
	return raw
}

// splitIntoChunks cuts a body into exactly count chunks (never empty) so the
// fixture can drip it out one read at a time.
func splitIntoChunks(body []byte, count int) []string {
	chunkSize := (len(body) + count - 1) / count
	var chunks []string
	for offset := 0; offset < len(body); offset += chunkSize {
		end := offset + chunkSize
		if end > len(body) {
			end = len(body)
		}
		chunks = append(chunks, string(body[offset:end]))
	}
	return chunks
}

// TestIssue1901_ChatStream_JSONProgressBeyondOneSilenceLimitStillAccepted
// (CHECK F1): a plain JSON reply delivered in 9 chunks 25 ms apart runs
// ~225 ms — more than TWICE the 100 ms silence limit in total — while every
// inter-chunk gap stays below it. Each non-empty body read re-arms the
// watch, so the call must be ACCEPTED intact, never cut, however long the
// total takes. Without the re-arming wrapper the watch fires at one limit
// mid-body and the call fails with the stall error instead.
func TestIssue1901_ChatStream_JSONProgressBeyondOneSilenceLimitStillAccepted(t *testing.T) {
	content := strings.Repeat("abcdefghij", 50) // 500 content bytes, fixed by the fixture
	chunks := splitIntoChunks(jsonCompletionBody(t, content), progressChunkCount)
	if len(chunks) != progressChunkCount {
		t.Fatalf("fixture: body split into %d chunks, want %d", len(chunks), progressChunkCount)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		for _, chunk := range chunks {
			time.Sleep(progressChunkGap)
			if _, err := w.Write([]byte(chunk)); err != nil {
				return // the client side is gone; nothing left to deliver
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()
	p := jsonStallProvider(t, srv.URL)

	var delivered []string
	res, hung, elapsed := runChatStreamBoundedCtx(t, p, context.Background(), jsonStallHardDeadline,
		func(accumulated string) { delivered = append(delivered, accumulated) })
	if hung {
		t.Fatalf("ChatStream() did not return within %s — a reply that keeps delivering must never be cut, and here never finished (the re-arming wrapper is what keeps slow progress alive)", jsonStallHardDeadline)
	}
	if res.err != nil {
		t.Fatalf("ChatStream() error = %v, want nil: a progressing reply (total ~%s > limit %s, every gap %s < limit) must be accepted, not cut by the stall monitor",
			res.err, progressChunkGap*time.Duration(progressChunkCount), jsonStallLimit, progressChunkGap)
	}
	if res.resp == nil {
		t.Fatal("ChatStream() resp = nil, want a response")
	}
	if res.resp.Content != content {
		t.Errorf("resp.Content = %d bytes, want the full %d-byte content: a progressing reply must arrive intact, not truncated at the stall cut",
			len(res.resp.Content), len(content))
	}
	if res.resp.FinishReason != "stop" {
		t.Errorf("resp.FinishReason = %q, want %q", res.resp.FinishReason, "stop")
	}
	if elapsed < progressChunkGap*time.Duration(progressChunkCount-1) {
		t.Fatalf("call returned after %s, before the fixture's %d gaps (%s each) could elapse — the fixture did not actually drip the body", elapsed, progressChunkCount-1, progressChunkGap)
	}
	if elapsed <= jsonStallLimit {
		t.Fatalf("call returned after %s, within one silence limit — the fixture must outlast the limit for this test to prove anything", elapsed)
	}
	if len(delivered) != 1 || delivered[0] != content {
		t.Fatalf("onChunk accumulated calls = %d, want exactly one call carrying the full %d-byte content (the JSON path's accumulated-text contract)",
			len(delivered), len(content))
	}
}

// zeroProgressBody is a body whose reads complete as (0, nil) — "call again,
// nothing there" — every gap, never delivering a byte. After Close (the
// stall monitor, or the context goroutine) reads return the same closed-body
// error a real net/http body produces.
type zeroProgressBody struct {
	closed atomic.Bool
	gap    time.Duration
}

func (b *zeroProgressBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, errBodyClosedByMonitor
	}
	time.Sleep(b.gap)
	return 0, nil
}

func (b *zeroProgressBody) Close() error {
	b.closed.Store(true)
	return nil
}

// scriptedChunksBody delivers predetermined chunks, one per read, with a
// delay before each — real byte progress at a controlled pace — and EOF
// after the last. Close makes further reads return the closed-body error.
// The chunk slice is touched only by the single parser goroutine; the closed
// flag is what the closers race with.
type scriptedChunksBody struct {
	closed atomic.Bool
	gap    time.Duration
	chunks []string
}

func (b *scriptedChunksBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, errBodyClosedByMonitor
	}
	if len(b.chunks) == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.gap)
	chunk := b.chunks[0]
	b.chunks = b.chunks[1:]
	return copy(p, chunk), nil
}

func (b *scriptedChunksBody) Close() error {
	b.closed.Store(true)
	return nil
}

// TestIssue1901_ChatStream_ZeroByteReadsDoNotReArmStallClock (CHECK F2):
// zero-byte reads are not progress. A body that keeps answering reads with
// (0, nil) is TOTAL SILENCE to the monitor — no byte arrived — so the call
// must be cut by the configured limit with the typed stall error, exactly
// like a body that never answers at all. With the n > 0 guard removed the
// zero-byte reads would reset the clock forever and the call would instead
// die much later on bufio's empty-read guard with a raw error. The positive
// control proves the same harness accepts real byte progress past one limit,
// so the zero-byte cut above is about silence, not about the fixture.
func TestIssue1901_ChatStream_ZeroByteReadsDoNotReArmStallClock(t *testing.T) {
	t.Run("zero-byte reads are silence and are cut by the limit", func(t *testing.T) {
		p := customBodyProvider(t, &zeroProgressBody{gap: zeroReadGap})

		res, hung, elapsed := runChatStreamBoundedCtx(t, p, context.Background(), jsonStallHardDeadline, nil)
		if hung {
			t.Fatalf("ChatStream() did not return within %s — a body answering every read with (0, nil) delivered NO bytes, so the silence limit must cut it, not hang forever (the n > 0 guard is what keeps zero-byte reads from counting as progress)", jsonStallHardDeadline)
		}
		wantStallError(t, elapsed, res.err)
	})

	t.Run("control: real byte progress across more than one limit is not cut", func(t *testing.T) {
		content := "real bytes are progress, zero bytes are not"
		chunks := splitIntoChunks(jsonCompletionBody(t, content), controlChunkCount)
		if len(chunks) != controlChunkCount {
			t.Fatalf("fixture: body split into %d chunks, want %d", len(chunks), controlChunkCount)
		}
		p := customBodyProvider(t, &scriptedChunksBody{gap: controlChunkGap, chunks: chunks})

		res, hung, elapsed := runChatStreamBoundedCtx(t, p, context.Background(), jsonStallHardDeadline, nil)
		if hung {
			t.Fatalf("ChatStream() did not return within %s — the control delivers real bytes every %s and must complete", jsonStallHardDeadline, controlChunkGap)
		}
		if res.err != nil {
			t.Fatalf("ChatStream() error = %v, want nil: the control proves the same harness accepts a body that delivers real bytes past one limit (total ~%s, gaps %s), so the zero-byte cut is about silence, not about the fixture",
				res.err, controlChunkGap*time.Duration(controlChunkCount), controlChunkGap)
		}
		if res.resp == nil || res.resp.Content != content {
			t.Fatalf("resp.Content = %v, want %q: the control reply must arrive intact", res.resp, content)
		}
		if res.resp.FinishReason != "stop" {
			t.Errorf("resp.FinishReason = %q, want %q", res.resp.FinishReason, "stop")
		}
		if elapsed < controlChunkGap*time.Duration(controlChunkCount-1) {
			t.Fatalf("control returned after %s, before its %d gaps (%s each) could elapse — the fixture did not actually drip", elapsed, controlChunkCount-1, controlChunkGap)
		}
	})
}

// gatedJSONBody delivers one short prefix of a JSON body, then blocks the
// read until it has been closed unblockOn times. Holding the read open
// across the stall monitor's close (unblockOn: 2) is what enforces the
// ordering "watch fired, THEN caller cancelled" for the fired-watch case:
// the classification then runs with watch.Fired() already true and the
// caller's context already cancelled, and must still return the context
// error.
type gatedJSONBody struct {
	closed     atomic.Bool
	closes     atomic.Int32
	unblockOn  int32
	unblocked  chan struct{}
	closeOnce  sync.Once
	prefix     string
	prefixSent atomic.Bool
}

func (b *gatedJSONBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, errBodyClosedByMonitor
	}
	if !b.prefixSent.Swap(true) && b.prefix != "" {
		return copy(p, b.prefix), nil
	}
	<-b.unblocked
	return 0, errBodyClosedByMonitor
}

func (b *gatedJSONBody) Close() error {
	if b.closes.Add(1) >= b.unblockOn {
		b.closeOnce.Do(func() { close(b.unblocked) })
	}
	b.closed.Store(true)
	return nil
}

// TestIssue1901_ChatStream_CallerCancellationWinsOverStallClassification
// (CHECK F3): the caller's cancellation outranks stall classification. A
// user who cancels mid-read gets context.Canceled back — never a stall
// error, never a raw transport error — including when the stall watch has
// already fired for the same call. jsonBodyReadError must consult the
// caller's context FIRST: without that branch the first two subtests see the
// raw closed-body error (which carries no cancellation text) or the typed
// stall error instead. The third subtest is an instrument control on the
// real transport: a cancellation racing a delayed-but-complete JSON reply
// must still surface as the context error.
func TestIssue1901_ChatStream_CallerCancellationWinsOverStallClassification(t *testing.T) {
	t.Run("cancel while the JSON body read is incomplete", func(t *testing.T) {
		// The watch must NOT have fired (cancel at 20% of the limit); the
		// raw read error is the neutral closed-body error, so nothing in the
		// error chain mentions cancellation — only the ctx.Err() branch can
		// produce context.Canceled here.
		p := customBodyProvider(t, &gatedJSONBody{
			unblockOn: 1,
			prefix:    jsonReplyBody[:20], // an incomplete JSON read
			unblocked: make(chan struct{}),
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(cancelBeforeStall, cancel)

		res, hung, _ := runChatStreamBoundedCtx(t, p, ctx, jsonStallHardDeadline, nil)
		if hung {
			t.Fatalf("ChatStream() did not return within %s — a caller cancellation must release an incomplete JSON read", jsonStallHardDeadline)
		}
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("ChatStream() error = %v, want errors.Is(err, context.Canceled): the caller cancelled mid-read, so the caller's context error must win over stall and raw-read classification", res.err)
		}
		if errors.Is(res.err, common.ErrStreamStalled) {
			t.Fatalf("ChatStream() error = %v: a cancelled call must not be reported as a provider stall (the watch had not even fired)", res.err)
		}
	})

	t.Run("cancel after the stall watch has fired", func(t *testing.T) {
		// unblockOn: 2 holds the read open across the monitor's close, so by
		// the time the read error surfaces BOTH the watch has fired (at one
		// limit) and the caller has cancelled (at two limits). Cancellation
		// must still win over the already-typed stall.
		p := customBodyProvider(t, &gatedJSONBody{
			unblockOn: 2,
			prefix:    jsonReplyBody[:20],
			unblocked: make(chan struct{}),
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(cancelAfterStall, cancel)

		res, hung, _ := runChatStreamBoundedCtx(t, p, ctx, jsonStallHardDeadline, nil)
		if hung {
			t.Fatalf("ChatStream() did not return within %s — the second close (the cancelled caller's) must release the read", jsonStallHardDeadline)
		}
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("ChatStream() error = %v, want errors.Is(err, context.Canceled): even with the stall watch fired, the caller's cancellation wins", res.err)
		}
		if errors.Is(res.err, common.ErrStreamStalled) {
			t.Fatalf("ChatStream() error = %v: a cancelled call must not be reported as a provider stall, even when the watch fired", res.err)
		}
	})

	t.Run("control: cancellation during a delayed JSON reply returns the context error", func(t *testing.T) {
		// Real transport, real server: the body would have completed at 3x
		// the cancellation point. Instrument check — cancellation reaches and
		// releases a JSON-path call that was otherwise going to succeed.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(controlReplyDelay)
			_, _ = w.Write(jsonCompletionBody(t, "too late, already cancelled"))
		}))
		defer srv.Close()
		p := jsonStallProvider(t, srv.URL)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(cancelBeforeStall, cancel)

		res, hung, _ := runChatStreamBoundedCtx(t, p, ctx, jsonStallHardDeadline, nil)
		if hung {
			t.Fatalf("ChatStream() did not return within %s — a cancellation during a delayed JSON reply must release the call", jsonStallHardDeadline)
		}
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("ChatStream() error = %v, want errors.Is(err, context.Canceled): the cancelled control must surface the caller's context error on the real transport too", res.err)
		}
	})
}
