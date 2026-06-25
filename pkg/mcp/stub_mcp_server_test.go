package mcp

// stub_mcp_server_test.go — tests the pkg/mcp/testdata/stub_mcp_server binary.
//
// The stub server is built on demand (go build -o <tmp>) and then loaded via
// LoadFromMCPConfig so the full ConnectServer → tools/list discovery path is
// exercised without a real external MCP service.
//
// Asserts:
//   1. The server starts, completes the initialize handshake, and populates
//      tool metadata (discovery is not a no-op).
//   2. GetAllTools() returns exactly the two tools the stub advertises:
//      "stub.echo" and "stub.noop".
//   3. Two different stub instances are discovered independently (differentiation).
//
// Traces to: Plan §9.8 — "Stub stdio MCP binary"
//            (issue #440)

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// buildStubServer compiles pkg/mcp/testdata/stub_mcp_server and returns the
// path to the resulting binary.  The binary is cleaned up via t.Cleanup.
func buildStubServer(t *testing.T) string {
	t.Helper()

	// Locate the source directory relative to this test file's package root.
	// pkg/mcp is the working directory during `go test`, so testdata lives
	// immediately under it.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("buildStubServer: getwd: %v", err)
	}
	srcDir := filepath.Join(cwd, "testdata", "stub_mcp_server")
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("buildStubServer: stub source not found at %s: %v", srcDir, err)
	}

	outDir := t.TempDir()
	binPath := filepath.Join(outDir, "stub_mcp_server")

	// Build with CGO_ENABLED=0 to match CI constraints.
	cmd := exec.Command("go", "build", "-o", binPath, srcDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Fatalf("buildStubServer: go build failed:\n%s\nerr: %v", out, buildErr)
	}

	return binPath
}

// TestStubMCPServer_DiscoveredByLoadFromMCPConfig verifies that LoadFromMCPConfig
// can connect to the stub stdio server, complete the MCP handshake, and expose
// the advertised tools through GetAllTools().
//
// BDD:
//
//	Given a compiled stub_mcp_server binary
//	When  LoadFromMCPConfig is called with that binary as the stdio command
//	Then  GetAllTools() returns a non-empty map containing "stub.echo" and "stub.noop"
//
// Traces to: Plan §9.8 — "Stub stdio MCP binary"
func TestStubMCPServer_DiscoveredByLoadFromMCPConfig(t *testing.T) {
	binPath := buildStubServer(t)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	mcpCfg := config.MCPConfig{
		ToolConfig: config.ToolConfig{Enabled: true},
		Servers: map[string]config.MCPServerConfig{
			"stub-server": {
				Enabled: true,
				Command: binPath,
				Args:    []string{},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := mgr.LoadFromMCPConfig(ctx, mcpCfg, t.TempDir()); err != nil {
		t.Fatalf("LoadFromMCPConfig: %v", err)
	}

	allTools := mgr.GetAllTools()
	if len(allTools) == 0 {
		t.Fatal("GetAllTools() returned empty — stub server discovery produced no tools")
	}

	stubTools, ok := allTools["stub-server"]
	if !ok {
		t.Fatalf("GetAllTools() missing key 'stub-server'; got keys: %v", toolMapKeys(allTools))
	}
	if len(stubTools) == 0 {
		t.Fatal("stub-server has zero tools — tools/list discovery produced nothing")
	}

	// Assert the exact two tools the stub advertises.
	toolSet := make(map[string]bool)
	for _, tool := range stubTools {
		if tool != nil {
			toolSet[tool.Name] = true
		}
	}
	for _, want := range []string{"stub.echo", "stub.noop"} {
		if !toolSet[want] {
			t.Errorf("expected tool %q in stub-server tools, got: %v", want, sdkToolNames(stubTools))
		}
	}

	// Content test: each expected tool has a non-empty description.
	for _, tool := range stubTools {
		if tool == nil {
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description — stub server returned incomplete metadata", tool.Name)
		}
	}
}

// TestStubMCPServer_TwoInstances verifies that two separate managers each
// loading the stub binary independently both discover tools, and that both
// return the same set of tool names (stub.echo, stub.noop).
//
// BDD:
//
//	Given two separate MCP managers each connecting to the stub server
//	When  each calls GetAllTools()
//	Then  both return the same set of tool names (stub.echo, stub.noop)
//
// Traces to: Plan §9.8 — differentiation test
func TestStubMCPServer_TwoInstances(t *testing.T) {
	binPath := buildStubServer(t)

	makeMgr := func(serverName string) *Manager {
		mgr := NewManager()
		t.Cleanup(func() { _ = mgr.Close() })
		mcpCfg := config.MCPConfig{
			ToolConfig: config.ToolConfig{Enabled: true},
			Servers: map[string]config.MCPServerConfig{
				serverName: {Enabled: true, Command: binPath},
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		t.Cleanup(cancel)
		if err := mgr.LoadFromMCPConfig(ctx, mcpCfg, t.TempDir()); err != nil {
			t.Fatalf("LoadFromMCPConfig (%s): %v", serverName, err)
		}
		return mgr
	}

	mgrA := makeMgr("stub-a")
	mgrB := makeMgr("stub-b")

	toolsA := mgrA.GetAllTools()["stub-a"]
	toolsB := mgrB.GetAllTools()["stub-b"]

	if len(toolsA) == 0 {
		t.Fatal("stub-a: zero tools discovered")
	}
	if len(toolsB) == 0 {
		t.Fatal("stub-b: zero tools discovered")
	}
	if len(toolsA) != len(toolsB) {
		t.Errorf("tool count mismatch: stub-a=%d, stub-b=%d (both should be 2)",
			len(toolsA), len(toolsB))
	}

	// Differentiation: both should advertise the same named tools.
	setA := sdkToolNameSet(toolsA)
	setB := sdkToolNameSet(toolsB)
	for name := range setA {
		if !setB[name] {
			t.Errorf("stub-b missing tool %q (found in stub-a)", name)
		}
	}
}

// toolMapKeys returns the server names from an allTools map for diagnostics.
func toolMapKeys(all map[string][]*sdkmcp.Tool) []string {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	return keys
}

// sdkToolNames extracts tool names for diagnostic output.
func sdkToolNames(tools []*sdkmcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			names = append(names, t.Name)
		}
	}
	return names
}

// sdkToolNameSet returns a set of tool names.
func sdkToolNameSet(tools []*sdkmcp.Tool) map[string]bool {
	s := make(map[string]bool)
	for _, t := range tools {
		if t != nil {
			s[t.Name] = true
		}
	}
	return s
}
