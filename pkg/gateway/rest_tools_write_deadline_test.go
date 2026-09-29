package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// TestAgentToolsReloadWriteDeadlineIsRequestScoped exercises the real HTTP
// transport: a recorder cannot enforce http.Server.WriteTimeout. The short
// deadline stands in for the gateway's 30s default; the slow reload stands in
// for a valid reload that finishes after that default but before its own wait.
func TestAgentToolsReloadWriteDeadlineIsRequestScoped(t *testing.T) {
	const baseWriteTimeout = 200 * time.Millisecond
	const slowReload = 550 * time.Millisecond
	api := buildExecutorTestAPI(t)
	api.agentLoop.SetReloadFunc(func() error {
		api.agentLoop.MarkReloadPending()
		go func() {
			time.Sleep(slowReload)
			api.agentLoop.ClearReloadPending()
		}()
		return nil
	})

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/agents/test-agent/tools", func(w http.ResponseWriter, r *http.Request) {
		(&restAPIUpdateAgentTools{a: api, w: w, r: r, agentID: "test-agent"}).reloadAndRespond()
	})
	mux.HandleFunc("GET /fast", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("slow") {
			time.Sleep(slowReload)
		}
		_, _ = io.WriteString(w, "fast\n")
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flushing unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		time.Sleep(slowReload)
		_, _ = io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = baseWriteTimeout
	srv.Start()
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 3 * time.Second

	t.Run("slow agent-tools reload returns its full response", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/agents/test-agent/tools", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err, "the 200ms server write deadline must not cut off a valid slow agent-tools reload")
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, err, "agent-tools response must arrive in full")
		require.Equal(t, http.StatusOK, resp.StatusCode, "agent-tools response: %s", body)
		var toolsResponse gen.AgentToolsResponse
		require.NoError(t, json.Unmarshal(body, &toolsResponse), "agent-tools response: %s", body)
		require.NotEmpty(t, toolsResponse.Revision, "agent-tools response must contain the stored revision")
	})

	t.Run("normal fast endpoint still responds", func(t *testing.T) {
		resp, err := client.Get(srv.URL + "/fast")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "fast\n", string(body))
	})

	t.Run("normal endpoint stays on the base timeout", func(t *testing.T) {
		resp, err := client.Get(srv.URL + "/fast?slow=1")
		if err != nil {
			t.Logf("base deadline ended the slow normal request: %v", err)
			return
		}
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		if readErr == nil && resp.StatusCode == http.StatusOK && string(body) == "fast\n" {
			t.Fatal("normal endpoint outlived the server's base write timeout")
		}
		t.Logf("base deadline cut off normal response: status=%d body=%q readErr=%v", resp.StatusCode, body, readErr)
	})

	t.Run("SSE-shaped endpoint stays on the base timeout", func(t *testing.T) {
		resp, err := client.Get(srv.URL + "/stream")
		require.NoError(t, err, "first streamed event must reach the client before the base timeout")
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
		if readErr == nil && string(body) == "data: first\n\ndata: second\n\n" {
			t.Fatal("SSE-shaped endpoint outlived the server's base write timeout")
		}
		require.Equal(t, "data: first\n\n", string(body), fmt.Sprintf("stream must stop after its first event (readErr=%v)", readErr))
	})

	t.Run("unsupported deadline still completes reload", func(t *testing.T) {
		w := httptest.NewRecorder()
		require.ErrorIs(t, http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Second)),
			http.ErrNotSupported, "the recorder must actually exercise the deadline-error path")
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", nil)
		(&restAPIUpdateAgentTools{a: api, w: w, r: r, agentID: "test-agent"}).reloadAndRespond()
		require.Equal(t, http.StatusOK, w.Code, "unsupported deadline must not abort the reload: %s", w.Body.String())
		require.Contains(t, w.Body.String(), `"revision":`, "the response still carries the agent revision")
	})
}
