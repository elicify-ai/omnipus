package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Observe the actual writer parked in the actual mutex acquisition, not a
// stopwatch or a mocked save callback. The fixture admits only one such writer.
func q7OutboundWriterWaitingForLock() bool {
	buffer := make([]byte, 1<<20)
	n := runtime.Stack(buffer, true)
	for _, stack := range strings.Split(string(buffer[:n]), "\n\n") {
		if strings.Contains(stack, "gateway.(*restAPI).commitOutboundChange(") &&
			(strings.Contains(stack, "sync.(*Mutex).Lock(") || strings.Contains(stack, "sync.(*Mutex).lockSlow(") || strings.Contains(stack, "internal/sync.(*Mutex).lockSlow(")) {
			return true
		}
	}
	return false
}

// R-S2 / FR-019/020: cancellation while queued behind configMu cannot commit
// later. Both real entry paths share the locked writer. Stale-token tests are
// deliberately retained separately: no competing mutation changes this token.
func TestQ7FollowupS2_CanceledLockWaitWritesNeitherDiskNorLive(t *testing.T) {
	for _, path := range []string{"tool", "REST"} {
		t.Run(path, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			before := t13Before(t, f)
			revision := t14Read(t, f, "telegram").Revision
			args := q7Args("telegram", "agent_may_start", revision, []string{"222"})
			ctx, cancel := context.WithCancel(context.Background())
			ctx = tools.WithAgentID(ctx, "omnipus")
			ctx = tools.WithTranscriptSessionID(ctx, "q7-s2-lock-wait")
			ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, f.al.GetConfig())
			require.False(t, q7OutboundWriterWaitingForLock(), "instrument: no writer was already waiting")

			tool, exists := f.api.builtinRegistry.Get("set_channel_outbound")
			require.True(t, exists)
			raw, err := json.Marshal(gen.ChannelOutboundUpdateRequest{
				Policy: gen.ChannelOutboundUpdateRequestPolicyAgentMayStart, Targets: []string{"222"}, ExpectedRevision: revision,
			})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/outbound", strings.NewReader(string(raw))).WithContext(ctx)
			req.RemoteAddr = uniqueTestSourceIP()
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+t14Bearer)
			rec := httptest.NewRecorder()
			var result *tools.ToolResult
			done := make(chan struct{})
			f.api.configMu.Lock()
			var once sync.Once
			unlock := func() { once.Do(f.api.configMu.Unlock) }
			t.Cleanup(func() {
				cancel()
				unlock()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("S2: writer goroutine did not drain before fixture teardown")
				}
			})
			go func() {
				defer close(done)
				if path == "tool" {
					result = tool.Execute(ctx, args)
				} else {
					f.mux.ServeHTTP(rec, req)
				}
			}()
			require.Eventually(t, q7OutboundWriterWaitingForLock, 10*time.Second, time.Millisecond,
				"instrument: writer must reach the held configuration lock before cancellation")
			require.NoError(t, ctx.Err(), "instrument: request is still live when the writer parks")
			cancel()
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			unlock() // No other write; the channel revision is still current.
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("S2: canceled writer did not return after unlock")
			}
			disk, err := os.ReadFile(f.f1277.configPath)
			require.NoError(t, err)
			assert.Equal(t, before.disk, disk, "S2: canceled lock waiter must not write durable config")
			assert.Equal(t, before.live.Channels, f.al.GetConfig().Channels, "S2: canceled lock waiter must not publish live outbound permissions")
			assert.Equal(t, revision, t14Read(t, f, "telegram").Revision, "S2: no write means no revision advance")
			if path == "tool" {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "S2: canceled tool request cannot claim a successful save")
			} else {
				assert.NotEqual(t, http.StatusOK, rec.Code, "S2: canceled REST request cannot claim a successful save")
			}
		})
	}
}
