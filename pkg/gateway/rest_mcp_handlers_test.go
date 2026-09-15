// Tests for MCP backend handlers G6–G9.
//
//   - G6: listMCPServers reflects live manager status + tool_count + enabled.
//   - G7: testMCPServer returns success=false gracefully for an unreachable server.
//   - G8: patchMCPServer merges partial update (omitted fields preserved, enabled toggled).
//   - G9: addMCPServer persists headers, env_file.

package gateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/stretchr/testify/require"
)

// buildStubMCPServer compiles pkg/mcp/testdata/stub_mcp_server (a real,
// minimal stdio MCP server — see pkg/mcp/stub_mcp_server_test.go's
// buildStubServer, which builds the identical binary for pkg/mcp's own
// tests) and returns the path to the resulting binary. Building a genuinely
// connectable server — rather than only asserting the failure path — lets
// TestTestMCPServer_SuccessHealsDisconnectedEnabledServer prove the heal
// end-to-end: a real successful /test call actually reconciles the live
// manager, not just a mocked "success" response.
func buildStubMCPServer(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	require.NoError(t, err, "buildStubMCPServer: getwd")
	// pkg/gateway is this package's directory during `go test`; the stub
	// server source lives under the sibling pkg/mcp package's testdata.
	srcDir := filepath.Join(cwd, "..", "mcp", "testdata", "stub_mcp_server")
	_, err = os.Stat(filepath.Join(srcDir, "main.go"))
	require.NoError(t, err, "buildStubMCPServer: stub source not found at %s", srcDir)

	outDir := t.TempDir()
	binPath := filepath.Join(outDir, "stub_mcp_server")

	cmd := exec.Command("go", "build", "-o", binPath, srcDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()
	require.NoError(t, buildErr, "buildStubMCPServer: go build failed:\n%s", out)

	return binPath
}

// mcpToolByPrefix returns the Name() of the first tool registered on inst
// whose name starts with prefix, or "" if none matches. Used to find the
// sanitized/hashed name an MCPTool ends up with (tools.NewMCPTool.Name()
// appends a hash suffix whenever sanitization is lossy, e.g. "." in an MCP
// tool name like "stub.echo") without hardcoding that derivation here.
func mcpToolByPrefix(inst *agent.AgentInstance, prefix string) string {
	for _, tl := range inst.Tools.GetAll() {
		if strings.HasPrefix(tl.Name(), prefix) {
			return tl.Name()
		}
	}
	return ""
}
