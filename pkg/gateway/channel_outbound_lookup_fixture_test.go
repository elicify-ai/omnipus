package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	discordchannel "github.com/elicify-ai/omnipus/pkg/channels/discord"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/stretchr/testify/require"
)

func t33AsyncPut(t *testing.T, ctx context.Context, f *wu6Fixture, id, policy string, targets []string, revision string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(gen.ChannelOutboundUpdateRequest{Policy: gen.ChannelOutboundUpdateRequestPolicy(policy), Targets: targets, ExpectedRevision: revision})
	require.NoError(t, err)
	return t33AsyncRequest(ctx, f, http.MethodPut, "/api/v1/channels/"+id+"/outbound", string(raw))
}

func t33AsyncRequest(ctx context.Context, f *wu6Fixture, method, path, body string) <-chan *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t14Bearer)
	req.RemoteAddr = uniqueTestSourceIP()
	ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, f.al.GetConfig())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, req.WithContext(ctx))
		done <- rec
		close(done)
	}()
	return done
}

func t33CleanupRequest(t *testing.T, done <-chan *httptest.ResponseRecorder, release func()) {
	t.Helper()
	t.Cleanup(func() {
		release()
		select {
		case <-done: // closed also when the main assertion already consumed the result.
		case <-time.After(6 * time.Second):
			t.Error("instrument: released HTTP writer did not finish before fixture cleanup")
		}
	})
}

func t33Await(t *testing.T, done <-chan *httptest.ResponseRecorder, bound time.Duration, why string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-done:
		return rec
	case <-time.After(bound):
		t.Fatal(why)
		return nil
	}
}

func t33Enter(t *testing.T, entered <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal(why)
	}
}

// HTTP observations at the external service boundary, not gateway wire types.
// Only discord.com's HTTP is intercepted; fixture-local HTTP uses the original
// transport. Tests are serial and restore the global external boundary.
type t33Network struct {
	mu                sync.Mutex
	paths             []string
	active, maxActive int
	previous          http.RoundTripper
	handle            func(*http.Request, int) (*http.Response, error)
}

func (n *t33Network) RoundTrip(req *http.Request) (*http.Response, error) {
	if host := req.URL.Hostname(); host != "discord.com" {
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return n.previous.RoundTrip(req)
		}
		return nil, fmt.Errorf("instrument refuses unconfigured external host %q", host)
	}
	n.mu.Lock()
	index := len(n.paths)
	n.paths = append(n.paths, req.Method+" "+req.URL.Path)
	n.active++
	if n.active > n.maxActive {
		n.maxActive = n.active
	}
	n.mu.Unlock()
	defer func() { n.mu.Lock(); n.active--; n.mu.Unlock() }()
	if n.handle == nil {
		return nil, fmt.Errorf("unexpected external Discord call %s", req.URL.Path)
	}
	return n.handle(req, index)
}

func (n *t33Network) snapshot() ([]string, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string{}, n.paths...), n.maxActive
}

func t33ExternalHTTP(t *testing.T) *t33Network {
	t.Helper()
	previous := http.DefaultTransport
	require.NotNil(t, previous)
	n := &t33Network{previous: previous}
	http.DefaultTransport = n
	t.Cleanup(func() { http.DefaultTransport = previous })
	return n
}

func t33HTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Request: req, Body: io.NopCloser(strings.NewReader(body))}
}

func t33GroupResponse(req *http.Request) *http.Response {
	id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
	return t33HTTPResponse(req, http.StatusOK, `{"id":"`+id+`","type":0}`)
}

type t33ClosedBody struct {
	io.ReadCloser
	closeSignal func()
}

func (b *t33ClosedBody) Close() error { err := b.ReadCloser.Close(); b.closeSignal(); return err }

func t33RealDiscord(t *testing.T, f *wu6Fixture, id string) channels.OutboundTargetResolver {
	t.Helper()
	// Ensure applyDiscordProxy leaves the actual session's default HTTP edge;
	// never replace the limiter, its sleep, or per-call retry settings.
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	if id != "discord" {
		t13Seed(t, f, id, "discord", true, "agent_may_start", []string{"discord:222"})
	}
	ch, err := discordchannel.NewDiscordChannel(config.DiscordConfig{TokenRef: "QA_T33_TOKEN"}, credentials.SecretBundle{"QA_T33_TOKEN": "qa-t33-synthetic-token"}, f.bus)
	require.NoError(t, err)
	resolver, ok := any(ch).(channels.OutboundTargetResolver)
	if !ok {
		t.Fatal("BLOCKED: real DiscordChannel.OutboundTargetResolver not implemented on WP-2 — required by T-33/S-46/S-46b/T-9; a fake resolver cannot verify the actual limiter or process-wide wrapper")
	}
	ch.SetRunning(true) // fake-session fixture; no live Discord gateway/Open.
	f.svc.ChannelManager.RegisterChannel(id, ch)
	return resolver
}
