// audit_test.go — ADR-075 D1 tests 45 and 75 (FR-027, FR-058).
//
// The requirement is short and the failure mode it guards is specific: ADR
// D2.11 rejects FIRST-USE-ONLY auditing by name, because "an event on first use
// of a context an agent did not establish fires once per agent per workspace
// and says nothing about the tenth action, or about which agent made the
// purchase." Every workspace agent now drives the operator's live logins, so
// the tenth action is exactly the one an operator will come looking for.
//
// Hence the assertions here are deliberately shaped to fail on a first-use-only
// implementation: ten write-class calls must produce ten events, and the TENTH
// is asserted by name.

package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// auditLoggerSettable is the pkg/tools auditLoggerAware contract, restated here
// because that interface is unexported in pkg/tools.
type auditLoggerSettable interface {
	SetAuditLogger(logger *audit.Logger)
}

// --- harness ----------------------------------------------------------------

// auditHarness is a real audit.Logger writing to a temp dir, plus the reader
// that pulls the entries back. A real logger rather than a spy on purpose: the
// event has to survive Entry serialisation and the logger's own validation, and
// a spy would not notice a name IsValidEventName rejects.
type auditHarness struct {
	dir string
	log *audit.Logger
}

func newAuditHarness(t *testing.T) *auditHarness {
	t.Helper()
	dir := t.TempDir()
	l, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return &auditHarness{dir: dir, log: l}
}

// entries reads every audit record written so far.
func (h *auditHarness) entries(t *testing.T) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(h.dir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &e), "audit line was not JSON: %s", line)
		out = append(out, e)
	}
	require.NoError(t, sc.Err())
	return out
}

// eventsNamed returns only the entries whose `event` is name.
func (h *auditHarness) eventsNamed(t *testing.T, name string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range h.entries(t) {
		if e["event"] == name {
			out = append(out, e)
		}
	}
	return out
}

// auditToolCtx carries the identities FR-027 requires in the event.
func auditToolCtx(agentID string) context.Context {
	ctx := tools.WithAgentID(context.Background(), agentID)
	return tools.WithTranscriptSessionID(ctx, testTranscriptSessionID)
}

// --- test 45 / 75: every write-class call is recorded -----------------------

// TestAudit_EveryWriteClassCallIsRecorded drives TEN write-class tool calls and
// asserts ten events, each carrying workspace id, agent id, tool name and host.
//
// The tenth is asserted explicitly. That single assertion is what a first-use-
// only implementation fails: it would emit one event and pass any assertion
// phrased as "at least one event was recorded".
func TestAudit_EveryWriteClassCallIsRecorded(t *testing.T) {
	h := newAuditHarness(t)
	m := newTestManagerWithFakeTabs(t)
	res := newFixedResolver(m)
	ctx := auditToolCtx("jim")

	openTool := &OpenTabTool{res: res}
	switchTool := &SwitchTabTool{res: res}
	closeTool := &CloseTabTool{res: res}
	for _, tool := range []auditLoggerSettable{openTool, switchTool, closeTool} {
		tool.SetAuditLogger(h.log)
	}

	// Ten write-class calls. Mixed tools, because the event carries the tool
	// name and a per-tool bug would otherwise hide behind nine good rows.
	// browser_open_tab is called with no url so nothing touches the network.
	var madeCalls int
	for i := 0; i < 4; i++ {
		require.False(t, openTool.Execute(ctx, map[string]any{}).IsError)
		madeCalls++
	}
	for i := 0; i < 3; i++ {
		require.False(t, switchTool.Execute(ctx, map[string]any{"index": float64(i)}).IsError)
		madeCalls++
	}
	for i := 0; i < 3; i++ {
		require.False(t, closeTool.Execute(ctx, map[string]any{"index": float64(0)}).IsError)
		madeCalls++
	}
	require.Equal(t, 10, madeCalls, "the harness itself must make exactly ten write-class calls")

	events := h.eventsNamed(t, audit.EventBrowserAction)
	require.Len(t, events, 10,
		"ten write-class calls must produce TEN events. One event means first-use-only auditing, "+
			"which ADR D2.11 rejects by name: it says nothing about the tenth action or about which "+
			"agent made the purchase")

	// The TENTH, by name. This is the assertion the rejected design fails.
	tenth := events[9]
	assert.Equal(t, "jim", tenth["agent_id"], "the tenth action must still name the agent that took it")
	assert.Equal(t, "browser_close_tab", tenth["tool"])

	// Every event carries the four required fields.
	for i, e := range events {
		assert.Equal(t, "jim", e["agent_id"], "event %d", i)
		assert.NotEmpty(t, e["tool"], "event %d must name the tool", i)
		toolName, ok := e["tool"].(string)
		require.True(t, ok, "event %d has a non-string tool field: %v", i, e["tool"])
		assert.True(t, writeClassBrowserTools[toolName],
			"event %d records %q, which is not a write-class tool", i, toolName)
		details, ok := e["details"].(map[string]any)
		require.True(t, ok, "event %d has no details: %v", i, e)
		assert.Equal(t, testWorkspaceID, details["workspace_id"],
			"event %d must name the workspace whose browser (and whose logins) were driven", i)
		assert.Contains(t, details, "host", "event %d must carry the target host field", i)
	}

	// The instance-creation event fires ONCE across all ten calls — it is a
	// different event answering a different question, not a per-call one.
	assert.Len(t, h.eventsNamed(t, audit.EventBrowserInstanceCreated), 1,
		"browser_instance_created is once per browser instance, not once per call")
}

// TestAudit_ReadOnlyCallsAreNotRecorded — five read-only calls, zero per-call
// action events. Auditing them would bury the seven calls that matter under the
// four that do not.
func TestAudit_ReadOnlyCallsAreNotRecorded(t *testing.T) {
	h := newAuditHarness(t)
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	listTool := &ListTabsTool{res: newFixedResolver(m)}
	listTool.SetAuditLogger(h.log)
	for i := 0; i < 5; i++ {
		require.False(t, listTool.Execute(auditToolCtx("mia"), map[string]any{}).IsError)
	}

	assert.Empty(t, h.eventsNamed(t, audit.EventBrowserAction),
		"read-only browser tools must produce no per-call action events")

	// ...but the browser's existence IS still recorded, exactly once, even
	// though the only tool that ever touched it was read-only. Without this a
	// workspace whose first browser call was browser_screenshot would have no
	// creation record at all.
	assert.Len(t, h.eventsNamed(t, audit.EventBrowserInstanceCreated), 1)
}

// TestAudit_WriteClassSetIsTheControlledResultSet is §14 rule 3 — ONE list, not
// two. It parses this package's own source and asserts, per tool Execute body:
//
//	calls controlledResult  <=>  calls recordBrowserAction
//
// A source-level assertion rather than eleven live tool calls, because four of
// the seven write-class tools (navigate/click/type/evaluate) do real CDP work
// after the emission and cannot be driven without a real Chrome. This closes
// exactly the gap that would otherwise exist: a tool that is control-gated but
// silently unaudited.
func TestAudit_WriteClassSetIsTheControlledResultSet(t *testing.T) {
	gated, audited := executeBodyCallSites(t)

	require.NotEmpty(t, gated, "the parse found no controlledResult call sites — the parse is broken")
	assert.Equal(t, gated, audited,
		"every controlledResult-gated Execute must also record a browser action, and no other one "+
			"may. The write-class set IS the gated set (§14 rule 3); two lists drift, and the drift is "+
			"silent")

	// And the sets agree with the declared maps in audit.go.
	declared := make([]string, 0, len(writeClassBrowserTools))
	for name := range writeClassBrowserTools {
		declared = append(declared, name)
	}
	assert.ElementsMatch(t, declared, toolNamesFor(t, gated),
		"writeClassBrowserTools must list exactly the controlledResult-gated tools")

	// Every browser tool belongs to exactly one of the two declared sets. A
	// NEW tool that belongs to neither is a finding here rather than a silent
	// default into the unaudited half.
	for _, tool := range BrowserBuiltinMetadata() {
		name := tool.Name()
		inWrite := writeClassBrowserTools[name]
		inRead := readOnlyBrowserTools[name]
		assert.True(t, inWrite != inRead,
			"%s is in %v write-class and %v read-only sets — every browser tool must be in exactly "+
				"one, or its audit treatment is undecided", name, inWrite, inRead)
	}
}

// executeBodyCallSites parses every non-test .go file in this package and
// returns the receiver type names of the Execute methods that call
// controlledResult, and those that call recordBrowserAction.
func executeBodyCallSites(t *testing.T) (gated, audited []string) {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, e.Name(), nil, parser.SkipObjectResolution)
		require.NoError(t, perr, e.Name())
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Execute" || fn.Recv == nil || fn.Body == nil {
				continue
			}
			recv := receiverTypeName(fn)
			if recv == "" {
				continue
			}
			var hasGate, hasAudit bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.Ident:
					if f.Name == "controlledResult" {
						hasGate = true
					}
				case *ast.SelectorExpr:
					if f.Sel.Name == "recordBrowserAction" {
						hasAudit = true
					}
				}
				return true
			})
			if hasGate {
				gated = append(gated, recv)
			}
			if hasAudit {
				audited = append(audited, recv)
			}
		}
	}
	return gated, audited
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if len(fn.Recv.List) == 0 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	id, ok := typ.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// toolNamesFor maps receiver type names to the tool names those types report.
func toolNamesFor(t *testing.T, receivers []string) []string {
	t.Helper()
	byType := map[string]string{}
	for _, tool := range BrowserBuiltinMetadata() {
		typ := reflect.TypeOf(tool)
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		byType[typ.Name()] = tool.Name()
	}
	out := make([]string, 0, len(receivers))
	for _, r := range receivers {
		name, ok := byType[r]
		require.True(t, ok, "no browser tool metadata for receiver type %s", r)
		out = append(out, name)
	}
	return out
}

// --- test 75: the event names are viewer-safe (FR-058) ----------------------

// introducedAuditEventNames is the full set of audit event names THIS change
// introduces — the scope FR-058 fixes.
var introducedAuditEventNames = []string{
	audit.EventBrowserInstanceCreated,
	audit.EventBrowserAction,
}

// viewerSafeEventName is FR-058's pattern. NOTE it is the SPEC's pattern
// (^[a-z_]+$), which is strictly narrower than the AuditEntry contract's own
// ^[a-z_.]+$ after issue #667 widened it. The two names introduced here satisfy
// BOTH, so no adjudication between spec and contract is needed — see
// EventBrowserInstanceCreated's doc comment in pkg/audit/events.go.
var viewerSafeEventName = regexp.MustCompile(`^[a-z_]+$`)

// TestAudit_EventNamesMatchViewerPattern asserts the introduced names match,
// AND that the check can fail — a deliberately dotted fixture name must be
// REJECTED by the same predicate. A pattern check that accepts everything is
// checking nothing, which is how #667 shipped in the first place.
func TestAudit_EventNamesMatchViewerPattern(t *testing.T) {
	require.NotEmpty(t, introducedAuditEventNames, "the set under test must not be empty")

	for _, name := range introducedAuditEventNames {
		assert.True(t, viewerSafeEventName.MatchString(name),
			"audit event name %q does not match %s — FR-058", name, viewerSafeEventName)
		assert.True(t, audit.IsValidEventName(audit.EventName(name)),
			"audit event name %q is not in pkg/audit's recognised vocabulary, so every entry it "+
				"writes trips the unknown-event warn path", name)
	}

	// THE FALSIFICATION. Each of these must FAIL the predicate. If any of them
	// passes, the predicate is not discriminating and the assertions above are
	// worthless.
	for _, bad := range []string{
		"browser.action",          // dotted — the shape FR-058 excludes
		"Browser_Action",          // capitals
		"browser-action",          // hyphen
		"browser_action_2",        // digit
		"browser_action ",         // trailing space
		"",                        // empty
		"browser_action\nsmuggle", // embedded newline
	} {
		assert.False(t, viewerSafeEventName.MatchString(bad),
			"%q must NOT satisfy the viewer-safe pattern; a check that accepts it is checking nothing", bad)
	}
}
