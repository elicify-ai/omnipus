package mcp

// manager_disconnect_test.go — tests for Manager.DisconnectServer and the
// ServerConnection.Config snapshot ConnectServer now records.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestDisconnectServer_AbsentName verifies the idempotent-absent contract:
// disconnecting a server name that was never connected is a no-op, not an
// error, and does not panic.
//
// BDD:
//
//	Given a fresh Manager with no connected servers
//	When  DisconnectServer is called with a name that was never connected
//	Then  it returns nil and does not panic
func TestDisconnectServer_AbsentName(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	if err := mgr.DisconnectServer("never-connected"); err != nil {
		t.Fatalf("DisconnectServer(absent) = %v, want nil", err)
	}
}

// TestDisconnectServer_FullPath exercises the full connect → snapshot →
// disconnect → idempotent-redisconnect path against the compiled stub MCP
// server.
//
// BDD:
//
//	Given a stub stdio MCP server connected via ConnectServer
//	When  GetServer is called
//	Then  the returned ServerConnection's Config field equals the cfg passed to ConnectServer
//	When  DisconnectServer is called
//	Then  GetServer subsequently reports not-found
//	When  DisconnectServer is called a second time for the same name
//	Then  it returns nil (idempotent)
func TestDisconnectServer_FullPath(t *testing.T) {
	binPath := buildStubServer(t)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	const serverName = "stub-disconnect"
	cfg := config.MCPServerConfig{
		Enabled: true,
		Command: binPath,
		Args:    []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := mgr.ConnectServer(ctx, serverName, cfg); err != nil {
		t.Fatalf("ConnectServer: %v", err)
	}

	conn, ok := mgr.GetServer(serverName)
	if !ok {
		t.Fatalf("GetServer(%q) after ConnectServer: not found", serverName)
	}
	if !reflect.DeepEqual(conn.Config, cfg) {
		t.Errorf("ServerConnection.Config = %+v, want %+v", conn.Config, cfg)
	}

	if err := mgr.DisconnectServer(serverName); err != nil {
		t.Fatalf("DisconnectServer (first call): %v", err)
	}

	if _, ok := mgr.GetServer(serverName); ok {
		t.Errorf("GetServer(%q) still found after DisconnectServer", serverName)
	}

	// Idempotent: disconnecting an already-disconnected name is a no-op.
	if err := mgr.DisconnectServer(serverName); err != nil {
		t.Fatalf("DisconnectServer (second call, already disconnected): %v", err)
	}
}

// TestConnectServer_ClosedManager verifies the belt-and-braces guard: once a
// Manager is closed, ConnectServer refuses to start a new connection rather
// than racing Close's drain and leaking a live subprocess that nothing would
// ever reap.
//
// BDD:
//
//	Given a Manager that has already been Close()d
//	When  ConnectServer is called with a valid stub stdio MCP server config
//	Then  it returns an error
//	And   the server does not appear in GetServers (no leaked connection/subprocess)
func TestConnectServer_ClosedManager(t *testing.T) {
	binPath := buildStubServer(t)

	mgr := NewManager()
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	const serverName = "stub-after-close"
	cfg := config.MCPServerConfig{
		Enabled: true,
		Command: binPath,
		Args:    []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := mgr.ConnectServer(ctx, serverName, cfg); err == nil {
		t.Fatalf("ConnectServer on closed manager = nil error, want error")
	}

	if servers := mgr.GetServers(); len(servers) != 0 {
		t.Errorf(
			"GetServers() after ConnectServer on closed manager = %d entries, want 0 (leaked connection)",
			len(servers),
		)
	}
}
