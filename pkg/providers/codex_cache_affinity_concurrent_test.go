package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

type concurrentAffinityCapture struct {
	body    []byte
	headers []string
	err     error
}

type concurrentAffinityResult struct {
	marker   string
	response *LLMResponse
	err      error
}

// C1: no call may borrow another conversation's affinity while both are active.
func testOverlappingSubscriptionSessionAffinity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	expected := map[string]string{
		"overlap-a":      "session_01M5EZC6E0YK7XZNRJJR50QEZ0",
		"overlap-b":      "session_01M5EZC6E0YK7XZNRJJR50QEZ1",
		"overlap-no-key": "",
	}
	captured := make(chan concurrentAffinityCapture, len(expected))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	baseURL := newConcurrentAffinityServer(t, captured, release)
	provider := newCodexProviderWithBaseURL(affinityFixtureCredential, "fixture-account", baseURL)

	start := make(chan struct{})
	finished := make(chan concurrentAffinityResult, len(expected))
	var ready sync.WaitGroup
	ready.Add(len(expected))
	for marker, key := range expected {
		go func() {
			ready.Done()
			<-start
			var options map[string]any
			if key != "" {
				options = map[string]any{"prompt_cache_key": key}
			}
			response, err := provider.Chat(ctx, []Message{{Role: "user", Content: marker}},
				nil, "gpt-6.1-sol", options)
			finished <- concurrentAffinityResult{marker: marker, response: response, err: err}
		}()
	}
	ready.Wait()
	close(start)

	// The server sends no headers/body until ALL requests arrive. Therefore all
	// three Chat calls overlap; a race run cannot silently serialize this case.
	seen := make(map[string]bool, len(expected))
	for range len(expected) {
		select {
		case request := <-captured:
			assertConcurrentAffinityRequest(t, request, expected, seen)
		case result := <-finished:
			t.Fatalf("%s finished before every request reached the overlap barrier: %v", result.marker, result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if len(seen) != len(expected) {
		t.Errorf("captured %d distinct requests, want %d", len(seen), len(expected))
	}
	releaseAll()
	for range len(expected) {
		result := <-finished
		if result.err != nil {
			t.Errorf("%s concurrent Chat: %v", result.marker, result.err)
			continue
		}
		if result.response == nil {
			t.Errorf("%s concurrent Chat returned a nil response", result.marker)
			continue
		}
		if result.response.Content != "ok" {
			t.Errorf("%s response = %q, want ok", result.marker, result.response.Content)
		}
	}
}

func assertConcurrentAffinityRequest(t *testing.T, request concurrentAffinityCapture, expected map[string]string, seen map[string]bool) {
	t.Helper()
	if request.err != nil {
		t.Fatalf("read concurrent request: %v", request.err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(request.body, &body); err != nil {
		t.Fatalf("decode concurrent request: %v", err)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(body["input"], &input); err != nil || len(input) != 1 {
		t.Fatalf("concurrent input: count=%d, error=%v, want one user marker", len(input), err)
	}
	var marker string
	if err := json.Unmarshal(input[0]["content"], &marker); err != nil {
		t.Fatalf("decode independent request marker: %v", err)
	}
	want, exists := expected[marker]
	if !exists || seen[marker] {
		t.Fatalf("unexpected or duplicate concurrent request marker %q", marker)
	}
	seen[marker] = true
	var wantHeaders []string
	if want != "" {
		wantHeaders = []string{want}
	}
	if !slices.Equal(request.headers, wantHeaders) {
		t.Errorf("%s concurrent session-id = %q, want %q (its own conversation)", marker, request.headers, wantHeaders)
	}
	key, present := body["prompt_cache_key"]
	if want == "" {
		if present {
			t.Errorf("%s body prompt_cache_key present, want absent", marker)
		}
		return
	}
	var gotKey string
	if err := json.Unmarshal(key, &gotKey); err != nil || gotKey != want {
		t.Errorf("%s body prompt_cache_key = %q, error=%v, want %q", marker, gotKey, err, want)
	}
	if len(request.headers) != 1 || request.headers[0] != gotKey {
		t.Errorf("%s header %q does not match its own body prompt_cache_key %q", marker, request.headers, gotKey)
	}
}

func newConcurrentAffinityServer(t *testing.T, captured chan<- concurrentAffinityCapture, release <-chan struct{}) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		captured <- concurrentAffinityCapture{body: body, headers: slices.Clone(r.Header.Values("Session-Id")), err: err}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		reply := "event: response.completed\ndata: " +
			`{"type":"response.completed","response":{"id":"resp_fixture","status":"completed",` +
			`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"
		if _, err := io.WriteString(w, reply); err != nil {
			t.Errorf("write concurrent fixture response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}
