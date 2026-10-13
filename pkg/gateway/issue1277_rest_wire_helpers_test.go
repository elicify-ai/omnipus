package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"unicode/utf8"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// Contract-first oracle: #1277 founder decision 2026-10-12 and WU-0a
// 82ce6aea6::McpServer.status_reason / Mailbox.status+status_reason.
// The contract branch is deliberately NOT merged. Existing generated types
// still decode the established response; raw JSON assertions watch the newly
// declared bytes without creating a parallel hand-written wire struct.
const (
	issue1277WireBadMCPRef   = "QA1277_WIRE_MCP_BAD"
	issue1277WireGoodMCPRef  = "QA1277_WIRE_MCP_GOOD"
	issue1277WireBadMailRef  = "QA1277_WIRE_MAIL_BAD"
	issue1277WireGoodMailRef = "QA1277_WIRE_MAIL_GOOD"
	issue1277WireBadProvRef  = "QA1277_WIRE_PROVIDER_BAD"
	issue1277WireSecret      = "qa1277-synthetic-wire-secret"
)

// Only a network edge: preserve the existing LINE/model protocol fixture and
// additionally admit this test's real loopback MCP protocol server.
type issue1277WireTransport struct {
	previous http.RoundTripper
	host     string
	direct   *http.Transport
}

func (tr *issue1277WireTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == tr.host {
		return tr.direct.RoundTrip(r)
	}
	return tr.previous.RoundTrip(r)
}

func issue1277WireSecretState(t *testing.T, f *issue1277Fixture, ref, state string) {
	t.Helper()
	t.Setenv(ref, "")
	switch state {
	case "healthy":
		require.NoError(t, f.store.Set(ref, issue1277WireSecret))
	case "missing":
		_, err := f.store.Get(ref)
		var missing *credentials.NotFoundError
		require.ErrorAs(t, err, &missing)
		require.Equal(t, ref, missing.Name)
	case "removed":
		require.NoError(t, f.store.Set(ref, issue1277WireSecret))
		require.NoError(t, f.store.Delete(ref))
		_, err := f.store.Get(ref)
		var missing *credentials.NotFoundError
		require.ErrorAs(t, err, &missing)
		require.Equal(t, ref, missing.Name)
	case "unreadable":
		require.NoError(t, f.store.Set(ref, issue1277WireSecret))
		issue1277TamperSingleEntry(t, f, ref)
		_, err := f.store.Get(ref)
		var unreadable *credentials.EntryAuthError
		require.ErrorAs(t, err, &unreadable)
		require.Equal(t, ref, unreadable.Name)
	default:
		t.Fatalf("unknown #1277 fixture state %q", state)
	}
	value, err := f.store.Get(issue1277ProviderRef)
	require.NoError(t, err, "instrument: a single bad entry must not corrupt the whole store")
	require.Equal(t, issue1277ProviderValue, value)
}

func newIssue1277WireMCP(t *testing.T, state string) (*issue1277Fixture, *restAPI) {
	t.Helper()
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
	require.NoError(t, f.store.Set(issue1277WireGoodMCPRef, issue1277WireSecret))
	issue1277WireSecretState(t, f, issue1277WireBadMCPRef, state)

	// SDK's published raw-schema server API; one actual discovered tool.
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "qa1277-wire", Version: "1.0.0"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name: "qa1277_probe", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "qa1277-wire-ready"}}}, nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	remote := httptest.NewServer(handler)
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	require.NoError(t, err)
	previous := http.DefaultTransport
	direct := &http.Transport{}
	http.DefaultTransport = &issue1277WireTransport{previous: previous, host: endpoint.Host, direct: direct}
	t.Cleanup(func() { http.DefaultTransport = previous; direct.CloseIdleConnections() })

	f.cfg.Tools.MCP.Enabled = true
	f.cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"qa1277-wire-bad":  {Enabled: true, Type: "http", URL: remote.URL + "/bad", EnvRefs: map[string]string{"TOKEN": issue1277WireBadMCPRef}},
		"qa1277-wire-good": {Enabled: true, Type: "http", URL: remote.URL + "/good", EnvRefs: map[string]string{"TOKEN": issue1277WireGoodMCPRef}},
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	api := f.startAPI(t, cfg, bundle)
	api.mcpRegistry = tools.NewMCPRegistry()
	api.agentLoop.SetCentralMCPRegistries(api.mcpRegistry, tools.NewBuiltinRegistry())
	require.NoError(t, api.agentLoop.ReconcileMCP(t.Context()), "one bad connector must not abort reconciliation")
	status, count, reason := api.agentLoop.MCPServerStatus("qa1277-wire-good")
	require.Equal(t, "connected", status, "instrument: the healthy peer must connect to the real protocol server")
	require.Equal(t, 1, count, "instrument: the one SDK tool must actually be discovered and registered")
	require.Empty(t, reason)
	return f, api
}

func newIssue1277WireMailbox(t *testing.T, state string) (*issue1277Fixture, *restAPI) {
	t.Helper()
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
	require.NoError(t, f.store.Set(issue1277WireGoodMailRef, issue1277WireSecret))
	cfg, bundle, _ := f.boot(t)
	issue1277WireSecretState(t, f, issue1277WireBadMailRef, state)
	// Isolate GET serialization from the old fatal boot guard, already proven
	// by the core RED pack. Config setup happens before any runtime reads it.
	cfg.Mailboxes = config.MailboxesConfig{
		"main": {
			"default": {Enabled: true, WorkspaceID: "default", PasswordRef: issue1277WireBadMailRef,
				IMAPHost: "127.0.0.1", SMTPHost: "127.0.0.1", Username: "bad@example.invalid"},
			"healthy": {Enabled: true, WorkspaceID: "healthy", PasswordRef: issue1277WireGoodMailRef,
				IMAPHost: "127.0.0.1", SMTPHost: "127.0.0.1", Username: "good@example.invalid"},
		},
	}
	api := f.startAPI(t, cfg, bundle)
	return f, api
}

func issue1277WireMCPRows(t *testing.T, api *restAPI) map[string]map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil))
	require.Equal(t, http.StatusOK, w.Code, "a scoped error must not make the whole MCP listing unavailable")
	var generated []gen.McpServer
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &generated))
	return issue1277WireIndexRows(t, w.Body.Bytes(), "id", len(generated))
}

func issue1277WireMailboxRows(t *testing.T, api *restAPI) map[string]map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	api.listMailboxes(w, httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes", nil))
	require.Equal(t, http.StatusOK, w.Code, "one unreadable mailbox must not hide the healthy mailbox listing; body=%s", w.Body.String())
	var generated gen.MailboxListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &generated))
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return issue1277WireIndexRows(t, body["mailboxes"], "workspace_id", len(generated.Mailboxes))
}

func issue1277WireProviderRows(t *testing.T, api *restAPI) map[string]map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	ctx := context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: "qa1277-wire"})
	api.HandleProviders(w, isolateRateLimit(t, r.WithContext(ctx)))
	require.Equal(t, http.StatusOK, w.Code)
	var generated []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &generated))
	return issue1277WireIndexRows(t, w.Body.Bytes(), "id", len(generated))
}

func issue1277WireIndexRows(t *testing.T, data []byte, key string, generatedCount int) map[string]map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &rows))
	require.Len(t, rows, generatedCount, "raw and generated decoders must see the same response entries")
	indexed := make(map[string]map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		id := issue1277WireString(t, row, key)
		require.NotContains(t, indexed, id, "wire rows must not silently collapse duplicate integration identities")
		indexed[id] = row
	}
	return indexed
}

func issue1277WireString(t *testing.T, row map[string]json.RawMessage, key string) string {
	t.Helper()
	require.Contains(t, row, key, "#1277: the real REST JSON is missing required diagnostic field %q", key)
	require.NotEqual(t, "null", string(row[key]), "diagnostic field %q must be a string, not null", key)
	var value string
	require.NoError(t, json.Unmarshal(row[key], &value), "wire field %q must be a JSON string", key)
	return value
}

func issue1277WireRefReason(t *testing.T, row map[string]json.RawMessage, field, ref string) {
	t.Helper()
	reason := issue1277WireString(t, row, field)
	require.Contains(t, reason, ref, "the visible error must name the exact failing configured reference")
	// WU-0a status_reason minLength/maxLength are Unicode-character bounds.
	if field == "status_reason" {
		require.GreaterOrEqual(t, utf8.RuneCountInString(reason), 1)
		require.LessOrEqual(t, utf8.RuneCountInString(reason), 1000)
	}
	require.NotContains(t, reason, issue1277WireSecret, "only the reference name, never a secret value, belongs in an error")
}
