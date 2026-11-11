package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/health"
)

const reloadDeadlineBootPath = "/api/v1/reload-deadline-test"

// Exercise the production listener-preparation phase, not a hand-built copy of
// its middleware order. No test here installs reloadWaitWriteDeadlineMiddleware.
func TestReloadWaitWriteDeadlineMiddleware_PrepareListenerInstallsInsideCSRF(t *testing.T) {
	const responseBody = "boot-chain saved\n"
	const writeTimeout = 50 * time.Millisecond
	const handlerDelay = 4 * writeTimeout // Outlast the short real server timeout.
	const safetyTimeout = 5 * time.Second
	writes := make(chan error, 2)
	root := prepareReloadDeadlineBootHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(handlerDelay)
		_, err := io.WriteString(w, responseBody)
		writes <- err
	}))

	t.Run("accepted_save_extends_in_installed_chain", func(t *testing.T) {
		writer := &reloadDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		started := time.Now()
		root.ServeHTTP(writer, reloadDeadlineBootRequest(t, reloadDeadlineBootPath, true))
		require.Equal(t, http.StatusOK, writer.Code)
		require.Equal(t, responseBody, writer.Body.String())
		require.NoError(t, <-writes)
		require.Len(t, writer.deadlines, 1, "prepareListener must install the reload-wait middleware")
		require.GreaterOrEqual(t, writer.deadlines[0].Sub(started), reloadWaitTimeout)
	})
	t.Run("csrf_rejected_save_keeps_default_deadline", func(t *testing.T) {
		writer := &reloadDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		root.ServeHTTP(writer, reloadDeadlineBootRequest(t, reloadDeadlineBootPath, false))
		require.Equal(t, http.StatusForbidden, writer.Code)
		require.JSONEq(t, `{"error":"csrf cookie missing"}`, writer.Body.String())
		require.Empty(t, writer.deadlines, "CSRF must reject the request before any deadline extension")
		require.Empty(t, writes, "a CSRF rejection must not dispatch the save handler")
	})
	t.Run("installed_chain_delivers_slow_save_over_TCP", func(t *testing.T) {
		baseURL := startReloadWaitDeadlineTestServer(t, root, writeTimeout, safetyTimeout)
		transport := &http.Transport{DisableKeepAlives: true}
		t.Cleanup(transport.CloseIdleConnections)
		client := &http.Client{Transport: transport, Timeout: safetyTimeout}
		response, err := client.Do(reloadDeadlineBootRequest(t, baseURL+reloadDeadlineBootPath, true))
		require.NoError(t, err, "the boot-installed chain must deliver a save beyond the server WriteTimeout")
		require.NotNil(t, response)
		t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Equal(t, responseBody, string(body))
		require.NoError(t, <-writes)
	})
}

func reloadDeadlineBootRequest(t *testing.T, target string, validCSRF bool) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, target, nil)
	require.NoError(t, err)
	if validCSRF {
		// The real double-submit gate compares these values, not an auth session.
		const token = "reload-deadline-test-csrf"
		request.AddCookie(&http.Cookie{Name: middleware.CSRFCookieNameHTTP, Value: token})
		request.Header.Set(middleware.CSRFHeaderName, token)
	}
	return request
}

func prepareReloadDeadlineBootHandler(t *testing.T, terminal http.Handler) http.Handler {
	t.Helper()
	api := buildExecutorTestAPI(t)
	messageBus := bus.NewMessageBus()
	manager, err := channels.NewManager(api.agentLoop.GetConfig(), credentials.SecretBundle{}, messageBus, nil)
	require.NoError(t, err)

	// Reserve the port: prepareListener builds the real chain, then StartAll
	// fails its bind before any Serve goroutine can read the handler. This lets
	// the existing wrapping API expose the chain without replacing a live one.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, occupied.Close()) })
	healthServer := health.NewServer("127.0.0.1", 0)
	manager.SetupHTTPServer(occupied.Addr().String(), healthServer)
	manager.RegisterHTTPHandler(reloadDeadlineBootPath, terminal)
	t.Cleanup(func() { require.NoError(t, manager.StopAll(context.Background())) })
	stage := &setupAndStartServicesState{
		ctx: context.Background(), cfg: api.agentLoop.GetConfig(), api: api,
		agentLoop:       api.agentLoop,
		runningServices: &services{ChannelManager: manager, HealthServer: healthServer},
	}
	_, stopped, err := stage.prepareListener()
	require.True(t, stopped, "fixture must stop before the occupied listener can serve")
	require.ErrorContains(t, err, "bind shared HTTP server", "only the controlled bind failure is expected")

	var installed http.Handler
	require.NoError(t, manager.WrapHTTPHandler(func(root http.Handler) http.Handler {
		installed = root
		return root // Identity observation: do not add or remove any middleware.
	}))
	require.NotNil(t, installed, "capture the actual handler prepared for the production server")
	return installed
}
