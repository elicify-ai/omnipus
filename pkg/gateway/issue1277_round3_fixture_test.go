package gateway

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// #1277 round-3 chief ruling, 2026-10-14: store failures are fatal regardless
// of configured reference kinds. Values and keys here are synthetic fixtures.
const (
	issue1277R3Ref        = "QA1277_R3_MCP_OR_UNREFERENCED"
	issue1277R3MissingRef = "QA1277_R3_MISSING_MCP"
	issue1277R3Value      = "qa1277-r3-synthetic-store-value"
	issue1277R3Server     = "qa1277-r3-mcp"
	issue1277R3WrongKey   = "wrong-key"
	issue1277R3Corrupt    = "corrupt-file"
)

func newIssue1277R3Fixture(t *testing.T, mcpOnly bool) *issue1277Fixture {
	t.Helper()
	f := newIssue1277Fixture(t)
	t.Cleanup(f.store.Close)
	names, err := f.store.List()
	require.NoError(t, err)
	for _, name := range names {
		require.NoError(t, f.store.Delete(name))
	}
	require.NoError(t, f.store.Set(issue1277R3Ref, issue1277R3Value))
	t.Setenv(issue1277R3Ref, "")
	t.Setenv(issue1277R3MissingRef, "")
	f.cfg.Providers[0].APIKeyRef = ""
	f.cfg.Channels = nil
	f.cfg.Mailboxes = nil
	f.cfg.Voice = config.VoiceConfig{}
	f.cfg.Tools.Web = config.WebToolsConfig{}
	f.cfg.Tools.Skills.Marketplaces = nil
	f.cfg.Tools.MCP.Enabled = false
	f.cfg.Tools.MCP.Servers = nil
	if mcpOnly {
		remoteURL := issue1277R3MCPServer(t)
		f.cfg.Tools.MCP.Enabled = true
		f.cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
			issue1277R3Server: {Enabled: true, Type: "http", URL: remoteURL,
				EnvRefs: map[string]string{"TOKEN": issue1277R3Ref}},
		}
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	issue1277R3RequireRefBoundary(t, f.cfg, mcpOnly)
	names, err = f.store.List()
	require.NoError(t, err)
	require.Equal(t, []string{issue1277R3Ref}, names, "instrument: exactly one sealed entry, no accidental readable witness")
	value, err := f.store.Get(issue1277R3Ref)
	require.NoError(t, err)
	require.Equal(t, issue1277R3Value, value)
	return f
}

func issue1277R3MCPServer(t *testing.T) string {
	t.Helper()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "qa1277-round3", Version: "1.0.0"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name: "qa1277_r3_probe", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "qa1277-r3-ready"}}}, nil
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
	return remote.URL
}

// Inspect the whole serialized config, rather than ResolveAll's incomplete
// enumeration. No non-MCP *_ref may accidentally make the store-fatal check run.
func issue1277R3RequireRefBoundary(t *testing.T, cfg *config.Config, mcpOnly bool) {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	var raw any
	require.NoError(t, json.Unmarshal(data, &raw))
	var refs []string
	var walk func(any, string)
	walk = func(value any, path string) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if strings.HasSuffix(key, "_ref") {
					if ref, ok := child.(string); ok && ref != "" {
						refs = append(refs, path+"."+key+"="+ref)
					}
				}
				walk(child, path+"."+key)
			}
		case []any:
			for _, child := range node {
				walk(child, path)
			}
		}
	}
	walk(raw, "config")
	require.Empty(t, refs, "instrument: no provider/channel/mailbox/search/other non-MCP secret reference is allowed")
	if mcpOnly {
		require.True(t, cfg.Tools.MCP.Enabled)
		require.Len(t, cfg.Tools.MCP.Servers, 1)
		srv := cfg.Tools.MCP.Servers[issue1277R3Server]
		require.True(t, srv.Enabled)
		require.Equal(t, map[string]string{"TOKEN": issue1277R3Ref}, srv.EnvRefs)
	} else {
		require.False(t, cfg.Tools.MCP.Enabled)
		require.Empty(t, cfg.Tools.MCP.Servers)
	}
}

func issue1277R3RequireStoreFailure(t *testing.T, err error, fault string) {
	t.Helper()
	switch fault {
	case issue1277R3WrongKey:
		require.ErrorIs(t, err, credentials.ErrWrongKey, "round-3 F2: wrong whole-store key must refuse, never become MCP-only degradation")
	case issue1277R3Corrupt:
		var syntax *json.SyntaxError
		require.ErrorAs(t, err, &syntax, "round-3 F2: present malformed store must refuse even without non-MCP refs")
		require.Contains(t, strings.ToLower(err.Error()), "corrupt")
	default:
		t.Fatalf("unknown round-3 store fault %q", fault)
	}
	require.Contains(t, strings.ToLower(err.Error()), "store", "the diagnostic must identify a store-level failure")
	require.NotContains(t, err.Error(), issue1277R3Value, "store diagnostics must never expose the stored value")
}

func issue1277R3BreakBootStore(t *testing.T, f *issue1277Fixture, fault string) []byte {
	t.Helper()
	path := filepath.Join(f.home, "credentials.json")
	if fault == issue1277R3WrongKey {
		t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("ff", 32))
		probe := credentials.NewStore(path)
		t.Cleanup(probe.Close)
		err := probe.UnlockWithKey(bytes.Repeat([]byte{0xff}, 32))
		if err == nil {
			_, err = probe.Get(issue1277R3Ref)
		}
		require.ErrorIs(t, err, credentials.ErrWrongKey, "instrument: a genuinely different key cannot open the real sealed entry")
	} else {
		require.Equal(t, issue1277R3Corrupt, fault)
		require.NoError(t, os.WriteFile(path, []byte(`{not-json`), 0o600))
		_, err := f.store.Get(issue1277R3Ref)
		var syntax *json.SyntaxError
		require.ErrorAs(t, err, &syntax, "instrument: the real file, not one entry, is undecodable")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

// A separate real writer rotates the file. The running store deliberately keeps
// its original key; no fake resolver/error or private key-field edit is used.
func issue1277R3BreakRunningStore(t *testing.T, f *issue1277Fixture, fault string) []byte {
	t.Helper()
	path := filepath.Join(f.home, "credentials.json")
	if fault == issue1277R3WrongKey {
		key, err := hex.DecodeString(fixedHexKey)
		require.NoError(t, err)
		writer := credentials.NewStore(path)
		t.Cleanup(writer.Close)
		require.NoError(t, writer.UnlockWithKey(key))
		require.NoError(t, writer.Rotate(bytes.Repeat([]byte{0xff}, 32)))
	} else {
		require.Equal(t, issue1277R3Corrupt, fault)
		require.NoError(t, os.WriteFile(path, []byte(`{not-json`), 0o600))
	}
	_, err := f.store.Get(issue1277R3Ref)
	issue1277R3RequireStoreFailure(t, err, fault)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func issue1277R3ConnectMCP(t *testing.T, runtime issue1277RuntimeState) {
	t.Helper()
	runtime.api.mcpRegistry = tools.NewMCPRegistry()
	runtime.api.agentLoop.SetCentralMCPRegistries(runtime.api.mcpRegistry, tools.NewBuiltinRegistry())
	require.NoError(t, runtime.api.agentLoop.ReconcileMCP(t.Context()))
	issue1277R3RequireConnectedMCP(t, runtime.api)
}

func issue1277R3RequireConnectedMCP(t *testing.T, api *restAPI) {
	t.Helper()
	status, count, reason := api.agentLoop.MCPServerStatus(issue1277R3Server)
	require.Equal(t, "connected", status, "instrument: the real MCP server must be connected")
	require.Equal(t, 1, count, "instrument: exactly the one SDK tool must be discovered")
	require.Empty(t, reason)
}
