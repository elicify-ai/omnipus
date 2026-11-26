//go:build goolm && stdjson

package gateway

// Q4 (fix round 4, 2026-11-26): C-26 "Decision time" and the code-paths row "expiry sweep (new, in
// pkg/agent, which can reach both the store and the proxy) - at each end time, revokes that agent's
// live egress tokens", Dataset D17 row 9 (S-121: the hour passes during a running curl loop; its new
// connection is refused). The sweep itself (AgentLoop.RunAllowanceExpirySweep) is pinned in pkg/agent.
// This file pins the one thing that makes it run in a real gateway: the gateway's start launches it,
// in the background, on the agent loop's context. Without that launch an ended allowance leaves the
// running command's token working, and no test of the sweep itself would notice.
//
// Why a source reader and not a call: runContextWithOptions.startBackgroundServices also starts the
// retention goroutines, which read package-level retention*SweepFn variables that other tests in this
// package write (a data race under -race), so no test can call it. The reader parses this package's
// production files, with no type information, and follows the launch from the gateway's start
// function: RunContextWithOptions calls rc.startBackgroundServices(), and startBackgroundServices
// starts RunAllowanceExpirySweep in a go statement on rc.agentLoopCtx, either itself or through a
// method of rc that it calls. It does not name that method, so the row holds on a head with the
// backend's seam and on one without it. What the launched sweep does is run in
// timed_allowance_sweep_seam_red_test.go (the seam) and in pkg/agent (the sweep).
//
// Honest limits: the reader sees that a call is present, that it sits in a go statement and that it
// is handed rc.agentLoopCtx. It cannot see that the call is reached at run time (a branch that never
// runs), and it is a structural check of source text, not a behavioural one; the behavioural row of
// the seam file runs the launch itself. The instrument rows prove the reader finds each shape it must
// find and rejects each way of not launching.
//
// Mutations for the independent CHECK: remove the launch; run the sweep without `go`; hand it rc.ctx
// instead of rc.agentLoopCtx; remove the seam's call from startBackgroundServices; remove
// rc.startBackgroundServices() from RunContextWithOptions. GREEN and mutation proof are deferred to
// CHECK. There is no behavioural RED for this row on the RED baseline 04ea85098: its gateway.go
// already launches the sweep, so the row passes there (reported to team-lead with the run).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sweepLaunchEntry   = "RunContextWithOptions"
	sweepLaunchStarter = "startBackgroundServices"
	sweepLaunchCallee  = "RunAllowanceExpirySweep"
)

// sweepLaunchSyntheticSource has one function per shape the reader must classify. It is parsed, never
// compiled, so the fields and types it mentions need not exist.
const sweepLaunchSyntheticSource = `package gateway

type runContextWithOptions struct{}

func (rc *runContextWithOptions) direct() { go rc.agentLoop.Sweep(rc.agentLoopCtx) }

func (rc *runContextWithOptions) literal() {
	go func() {
		defer close(rc.done)
		rc.agentLoop.Sweep(rc.agentLoopCtx)
	}()
}

func (rc *runContextWithOptions) viaMethod() { rc.literal() }

func (rc *runContextWithOptions) twoHops() { rc.viaMethod() }

func (rc *runContextWithOptions) synchronous() { rc.agentLoop.Sweep(rc.agentLoopCtx) }

func (rc *runContextWithOptions) wrongContext() { go rc.agentLoop.Sweep(rc.ctx) }

func (rc *runContextWithOptions) otherCallee() { go rc.agentLoop.Other(rc.agentLoopCtx) }

func (rc *runContextWithOptions) cycleA() { rc.cycleB() }

func (rc *runContextWithOptions) cycleB() { rc.cycleA() }

func (rc *runContextWithOptions) otherReceiver(x *thing) { x.literal() }

type otherState struct{}

func (rc *otherState) onlyOnOtherType() { go rc.agentLoop.Sweep(rc.agentLoopCtx) }

func (rc *otherState) direct() {}

func Entry() {
	rc := &runContextWithOptions{}
	rc.viaMethod()
}
`

func TestTimedAllowance_GatewayStartLaunchesTheSweep(t *testing.T) {
	t.Run("C-26 Decision time: the gateway's start launches the expiry sweep in the background on the agent loop's context", testGatewayStartLaunchesTheSweep)
	t.Run("instrument: the source reader follows a launch through methods and rejects each way of not launching", testSweepLaunchReaderInstrument)
	t.Run("instrument: on the real production files the reader sees the agent loop's own launch and no launch that does not exist", testSweepLaunchReaderOnRealFiles)
}

func testGatewayStartLaunchesTheSweep(t *testing.T) {
	src := parseSweepLaunchSources(t)
	entry := src.funcs[sweepLaunchEntry]
	require.NotNil(t, entry, "instrument: the gateway's start function is found in the production files")
	assert.True(t, sweepLaunchCallsOnRc(entry.Body, sweepLaunchStarter),
		"the gateway's start (RunContextWithOptions) must call rc.startBackgroundServices(): no background service, the sweep included, starts without it")
	chain := src.launchChain(sweepLaunchStarter, sweepLaunchCallee)
	assert.NotEmpty(t, chain,
		"C-26 / D17 row 9: startBackgroundServices must start AgentLoop.RunAllowanceExpirySweep in a go statement on rc.agentLoopCtx, "+
			"itself or through a method of rc it calls; without it an ended allowance never revokes the running command's token")
	t.Logf("SWEEP_LAUNCH_CHAIN: %s -> %s -> go %s(rc.agentLoopCtx)", sweepLaunchEntry, strings.Join(chain, " -> "), sweepLaunchCallee)
}

func testSweepLaunchReaderInstrument(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", sweepLaunchSyntheticSource, parser.SkipObjectResolution)
	require.NoError(t, err)
	synthetic := newSweepLaunchSources()
	synthetic.add(file)
	require.Contains(t, synthetic.funcs, "Entry", "instrument: the synthetic top-level function is recorded")
	for _, row := range []struct {
		name string
		want []string
		why  string
	}{
		{"direct", []string{"direct"}, "a go statement that calls the sweep on rc.agentLoopCtx; a later method of another type with the same name must not replace it"},
		{"literal", []string{"literal"}, "a go statement running a function literal that calls it"},
		{"viaMethod", []string{"viaMethod", "literal"}, "a call to a method of rc that launches"},
		{"twoHops", []string{"twoHops", "viaMethod", "literal"}, "two methods deep"},
		{"synchronous", nil, "no go statement: the start would block"},
		{"wrongContext", nil, "the sweep handed rc.ctx instead of rc.agentLoopCtx"},
		{"otherCallee", nil, "a different method launched"},
		{"cycleA", nil, "mutually calling methods end and find nothing"},
		{"otherReceiver", nil, "a call on another variable than rc is not followed"},
		{"onlyOnOtherType", nil, "a method of another type is not recorded, whatever its receiver is called"},
		{"missing", nil, "a method that does not exist"},
	} {
		assert.Equal(t, row.want, synthetic.launchChain(row.name, "Sweep"), "%s: %s", row.name, row.why)
	}
	assert.NotContains(t, synthetic.methods, "onlyOnOtherType", "a method of another type is not recorded")
	assert.True(t, sweepLaunchCallsOnRc(synthetic.funcs["Entry"].Body, "viaMethod"), "Entry calls rc.viaMethod()")
	assert.False(t, sweepLaunchCallsOnRc(synthetic.funcs["Entry"].Body, "literal"), "Entry does not call rc.literal()")
}

func testSweepLaunchReaderOnRealFiles(t *testing.T) {
	src := parseSweepLaunchSources(t)
	require.Contains(t, src.methods, sweepLaunchStarter, "instrument: the reader recorded rc.startBackgroundServices from the real files")
	assert.Equal(t, []string{sweepLaunchStarter}, src.launchChain(sweepLaunchStarter, "Run"),
		"instrument: the same reader sees the agent loop's own launch, `go func() { ... rc.agentLoop.Run(rc.agentLoopCtx) }()`")
	assert.Nil(t, src.launchChain(sweepLaunchStarter, "RunNoSuchSweepForTheInstrument"),
		"instrument: a launch that does not exist is not found")
}

// sweepLaunchSources is the production half of this package, parsed without type information: the
// methods of runContextWithOptions and the plain functions, each by name.
type sweepLaunchSources struct {
	methods map[string]*ast.FuncDecl
	funcs   map[string]*ast.FuncDecl
}

func newSweepLaunchSources() *sweepLaunchSources {
	return &sweepLaunchSources{methods: map[string]*ast.FuncDecl{}, funcs: map[string]*ast.FuncDecl{}}
}

// add records the functions of one parsed file: a method whose receiver is runContextWithOptions
// under methods, a function without a receiver under funcs. A method of another type, such as
// setupAndStartServicesState.startBackgroundServices, is not recorded: a name alone must not stand
// for the wrong type.
func (s *sweepLaunchSources) add(file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		switch {
		case fn.Recv == nil:
			s.funcs[fn.Name.Name] = fn
		case sweepLaunchReceiver(fn) == "runContextWithOptions":
			s.methods[fn.Name.Name] = fn
		}
	}
}

// parseSweepLaunchSources parses the non-test Go files of the package directory, where go test runs.
func parseSweepLaunchSources(t *testing.T) *sweepLaunchSources {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	src := newSweepLaunchSources()
	var parsed []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, "the production file %s parses", name)
		src.add(file)
		parsed = append(parsed, name)
	}
	require.Contains(t, parsed, "gateway.go", "instrument: the package's production files were found (the test runs in the package directory)")
	return src
}

func sweepLaunchReceiver(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	ident, ok := typ.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

// launchChain returns the methods through which name starts callee in the background on
// rc.agentLoopCtx: [name] when name holds the go statement itself, [name, m, ...] when a method m of
// rc that it calls (to three hops) does. It returns nil when it finds no such launch.
func (s *sweepLaunchSources) launchChain(name, callee string) []string {
	return s.followLaunch(name, callee, map[string]bool{}, 0)
}

func (s *sweepLaunchSources) followLaunch(name, callee string, seen map[string]bool, hops int) []string {
	fn := s.methods[name]
	if fn == nil || seen[name] || hops > 3 {
		return nil
	}
	seen[name] = true
	if sweepLaunchHasGoStmt(fn.Body, callee) {
		return []string{name}
	}
	var chain []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if chain != nil {
			return false
		}
		method, ok := sweepLaunchRcCall(n)
		if !ok {
			return true
		}
		if rest := s.followLaunch(method, callee, seen, hops+1); rest != nil {
			chain = append([]string{name}, rest...)
		}
		return chain == nil
	})
	return chain
}

// sweepLaunchRcCall returns the method name of a call of the form rc.<method>(...).
func sweepLaunchRcCall(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if recv, isIdent := sel.X.(*ast.Ident); !isIdent || recv.Name != "rc" {
		return "", false
	}
	return sel.Sel.Name, true
}

func sweepLaunchCallsOnRc(body ast.Node, method string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if name, ok := sweepLaunchRcCall(n); ok && name == method {
			found = true
		}
		return !found
	})
	return found
}

// sweepLaunchHasGoStmt reports whether body holds a go statement that runs <x>.<callee>(rc.agentLoopCtx),
// as the statement's own call or inside the function literal it runs.
func sweepLaunchHasGoStmt(body ast.Node, callee string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return !found
		}
		ast.Inspect(goStmt.Call, func(m ast.Node) bool {
			if call, isCall := m.(*ast.CallExpr); isCall && sweepLaunchIsCalleeOnLoopCtx(call, callee) {
				found = true
			}
			return !found
		})
		return false
	})
	return found
}

func sweepLaunchIsCalleeOnLoopCtx(call *ast.CallExpr, callee string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != callee || len(call.Args) != 1 {
		return false
	}
	arg, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || arg.Sel.Name != "agentLoopCtx" {
		return false
	}
	base, ok := arg.X.(*ast.Ident)
	return ok && base.Name == "rc"
}
