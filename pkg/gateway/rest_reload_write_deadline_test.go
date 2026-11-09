package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The regression brief requires full responses for slow API saves, with a
// no-middleware control proving the server really enforces its WriteTimeout.
// GET and non-API PUT requests must retain that default timeout.
func TestReloadWaitWriteDeadlineMiddleware_SlowResponses(t *testing.T) {
	// Straddle the real server deadline without shortening reloadWaitTimeout or
	// setting a write deadline in the test: only the real middleware may extend it.
	const writeTimeout = 50 * time.Millisecond
	const handlerDelay = 4 * writeTimeout
	const safetyTimeout = 5 * time.Second // Hang guard, not the response oracle.
	const responseBody = "saved; reload complete\n"

	cases := []struct {
		name          string
		method        string
		path          string
		useMiddleware bool
		wantResponse  bool
	}{
		{"PUT_API_without_middleware_drops_response", http.MethodPut, "/api/v1/agents", false, false},
		{"PUT_API_with_middleware_returns_full_response", http.MethodPut, "/api/v1/agents", true, true},
		{"POST_API_without_middleware_drops_response", http.MethodPost, "/api/v1/agents", false, false},
		{"POST_API_with_middleware_returns_full_response", http.MethodPost, "/api/v1/agents", true, true},
		{"GET_API_with_middleware_keeps_default_timeout", http.MethodGet, "/api/v1/agents", true, false},
		{"PUT_outside_API_with_middleware_keeps_default_timeout", http.MethodPut, "/outside/api/v1/agents", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handlerWrites := make(chan error, 1)
			var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(handlerDelay)
				w.WriteHeader(http.StatusOK)
				_, err := io.WriteString(w, responseBody)
				handlerWrites <- err
			})
			if tc.useMiddleware {
				handler = reloadWaitWriteDeadlineMiddleware(handler)
			}
			baseURL := startReloadWaitDeadlineTestServer(t, handler, writeTimeout, safetyTimeout)

			// A fresh, direct connection prevents a retry or a stale keepalive
			// connection from supplying the expected EOF for the wrong reason.
			transport := &http.Transport{DisableKeepAlives: true}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: safetyTimeout}
			request, err := http.NewRequest(tc.method, baseURL+tc.path, nil)
			require.NoError(t, err)
			response, requestErr := client.Do(request)
			if response != nil {
				t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
			}

			var writeErr error
			select {
			case writeErr = <-handlerWrites:
			case <-time.After(safetyTimeout):
				t.Fatal("slow handler never completed; cannot attribute the response to the write deadline")
			}
			if tc.wantResponse {
				require.NoError(t, requestErr, "middleware must deliver the slow save response beyond the server WriteTimeout")
				require.NoError(t, writeErr, "the protected handler must finish writing its response")
				require.NotNil(t, response)
				require.Equal(t, http.StatusOK, response.StatusCode)
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err, "the complete save response must be readable")
				require.Equal(t, responseBody, string(body), "no part of the slow save response may be dropped")
				return
			}
			require.Nil(t, response, "the expired server write deadline must prevent an HTTP response")
			require.ErrorIs(t, requestErr, io.EOF, "the server must drop the connection, not hit the client hang guard")
		})
	}
}

func startReloadWaitDeadlineTestServer(t *testing.T, handler http.Handler, writeTimeout, safetyTimeout time.Duration) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{
		Handler:           handler,
		WriteTimeout:      writeTimeout,
		ReadHeaderTimeout: safetyTimeout,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), safetyTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("could not shut down the deadline test server: %v", err)
			require.NoError(t, server.Close(), "force-close after failed shutdown")
		}
		require.ErrorIs(t, <-served, http.ErrServerClosed, "the real HTTP server must stop cleanly")
	})
	return "http://" + listener.Addr().String()
}
