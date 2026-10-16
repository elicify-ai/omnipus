package anthropicprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

func TestCachingFixStreamCancellationAndStallKeepUsage(t *testing.T) {
	for _, cancelCall := range []bool{true, false} {
		name := "stall"
		if cancelCall {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, evMessageStart+evBlockStart+evTextDelta)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(srv.Close)
			p := NewProviderWithBaseURL("fixture", srv.URL).WithStreamStallTimeout(25 * time.Millisecond)
			_, err := p.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil, func(string) {
				if cancelCall {
					cancel()
				}
			}, nil)
			if err == nil {
				t.Fatal("aborted stream became success")
			}
			if cancelCall && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost caller-cancel identity: %v", err)
			}
			if !cancelCall && !errors.Is(err, common.ErrStreamStalled) {
				t.Fatalf("lost stall identity: %v", err)
			}
			var billed *common.StreamProviderError
			if !errors.As(err, &billed) || billed.Usage == nil || *billed.Usage != (UsageInfo{PromptTokens: 9, TotalTokens: 9}) {
				t.Fatalf("aborted stream lost its opening9tokens: %+v / %v", billed, err)
			}
		})
	}
}

func TestCachingFixStreamMissingAndKnownZeroUsageDiffer(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "missing"
		usage := ""
		if known {
			name = "known_zero"
			usage = `,"usage":{"input_tokens":0,"output_tokens":0}`
		}
		t.Run(name, func(t *testing.T) {
			opening := `event: message_start` + "\ndata: " + `{"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","content":[],"model":"fixture"` + usage + "}}\n\n"
			p := anthropicScriptedStream(t, opening)
			_, err := p.ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil, nil, nil)
			var billed *common.StreamProviderError
			if err == nil || !errors.As(err, &billed) {
				t.Fatalf("incomplete stream was not a typed failure: %v", err)
			}
			if (billed.Usage != nil) != known {
				t.Fatalf("known%t has usage=%+v; missing and known zero must differ", known, billed.Usage)
			}
			if known && *billed.Usage != (UsageInfo{}) {
				t.Fatalf("known zero invented tokens: %+v", billed.Usage)
			}
		})
	}
}
