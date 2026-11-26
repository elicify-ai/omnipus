//go:build goolm && stdjson

package gateway

// Team-lead round 11, item 2 (Skills & Tools #1963; test author Part H,
// finding 4). C-26, Dataset D17 row 9: a timed address allowance that ends
// during a running command revokes the command's egress token, which only
// happens while AgentLoop.RunAllowanceExpirySweep runs. These tests pin that
// the gateway starts it (startBackgroundServices launches it, and only through
// the named seam startAllowanceExpirySweep) and that it stops when the agent
// loop's context ends.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowanceExpirySweep_RunsUntilTheAgentLoopContextEnds(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		List:     []config.AgentConfig{{ID: "mia"}},
	}}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	rc := &runContextWithOptions{agentLoop: mustAgentLoop(t, cfg, msgBus, adr093IdleProvider{})}
	rc.agentLoopCtx, rc.agentLoopCancel = context.WithCancel(context.Background())
	t.Cleanup(rc.agentLoopCancel)

	done := rc.startAllowanceExpirySweep()
	require.NotNil(t, done)
	select {
	case <-done:
		t.Fatal("the sweep stopped while the agent loop's context was still live")
	case <-time.After(1500 * time.Millisecond): // longer than one sweep interval (1 s)
	}
	rc.agentLoopCancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not stop after the agent loop's context ended")
	}
}

func TestStartBackgroundServices_StartsTheAllowanceExpirySweep(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gateway.go", nil, 0)
	require.NoError(t, err)
	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "startBackgroundServices" && fn.Recv != nil && len(fn.Recv.List) == 1 {
			if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
				if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "runContextWithOptions" {
					body = fn.Body
				}
			}
		}
	}
	require.NotNil(t, body, "instrument: gateway.go declares runContextWithOptions.startBackgroundServices")
	calls := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "startAllowanceExpirySweep" {
				calls++
			}
		}
		return true
	})
	assert.Equal(t, 1, calls, "the gateway start launches the allowance expiry sweep once, through startAllowanceExpirySweep")

	// Only the seam starts the sweep: no other gateway source calls it directly.
	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, sources, "instrument: the gateway sources are found")
	var direct []string
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") || name == "allowance_sweep_start.go" {
			continue
		}
		src, readErr := os.ReadFile(name)
		require.NoError(t, readErr)
		if strings.Contains(string(src), "RunAllowanceExpirySweep(") {
			direct = append(direct, name)
		}
	}
	assert.Empty(t, direct, "the sweep is started only through startAllowanceExpirySweep")
	seam, err := os.ReadFile("allowance_sweep_start.go")
	require.NoError(t, err)
	require.Contains(t, string(seam), "RunAllowanceExpirySweep(rc.agentLoopCtx)", "instrument: the seam runs the sweep on the agent loop's context")
}
