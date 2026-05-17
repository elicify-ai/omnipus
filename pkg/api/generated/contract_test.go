//go:build !windows

// Bidirectional contract tests — run with go test ./pkg/api/generated/...
//
// Verifies Go structs marshal to schema-valid JSON (contracts/asyncapi.yaml
// and contracts/components/schemas/*.yaml). The nil-args regression guard
// (TestContract_ToolApprovalRequiredFrame_NilArgsRejected) catches the Ava-chat
// bug class: nil map -> "args":null -> Object.keys(null) crash in SPA.
//
// Manual break test: change cloneStringAnyMap to return nil, run the tests,
// observe the regression guard fail, restore, observe it pass.
//
// Build constraint: !windows because the yamlLoader uses file:// URLs with
// POSIX paths (/absolute/path). Windows file:// URLs require drive-letter
// handling (file:///C:/path) which is not implemented — this project is
// Linux-primary (see CLAUDE.md hard constraints).

package generated

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ── Schema loader setup ──────────────────────────────────────────────────────

var (
	schemaSetupOnce sync.Once
	errSchemaSetup  error

	// asyncapiFilePath is the absolute path to contracts/asyncapi.yaml.
	// Used to build file:// URLs for asyncapi schema fragments.
	asyncapiFilePath string

	// componentSchemaDir is the absolute path to contracts/components/schemas/.
	// Used to build file:// URLs for component schema files.
	componentSchemaDir string

	// sharedCompiler is the singleton compiler with all schemas pre-loaded.
	sharedCompiler *jsonschema.Compiler
)

// contractsDir returns the absolute path to the contracts/ directory.
// Resolved relative to the location of this test file (pkg/api/generated/).
func contractsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed — cannot resolve contracts dir")
	}
	// file is /path/to/pkg/api/generated/contract_test.go
	// contracts/ is three dirs up
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts")
}

// yamlLoader is a URLLoader that reads .yaml files by parsing them with yaml.v3.
// The jsonschema/v6 library's built-in FileLoader only handles JSON; this wrapper
// intercepts .yaml URLs and returns parsed YAML as map[string]any.
type yamlLoader struct{}

func (yamlLoader) Load(rawURL string) (any, error) {
	// Strip the file:// prefix to get the file path.
	// On Linux: file:///absolute/path → after trim: /absolute/path (correct).
	// Windows is excluded via the //go:build !windows tag at the top of this file.
	path := strings.TrimPrefix(rawURL, "file://")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("yamlLoader: read %s: %w", path, err)
	}

	// Try JSON first (some .gen.go files embed JSON); fall back to YAML.
	if len(data) > 0 && data[0] == '{' {
		var doc any
		if jsonErr := json.Unmarshal(data, &doc); jsonErr == nil {
			return doc, nil
		}
	}

	// Parse as YAML.
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("yamlLoader: unmarshal %s: %w", path, err)
	}
	return doc, nil
}

// initSchemas initializes the shared compiler once per test binary run.
// Called lazily from validateAgainstSchema — not from TestMain so tests can
// run individually without requiring the full environment.
func initSchemas(t *testing.T) *jsonschema.Compiler {
	t.Helper()

	schemaSetupOnce.Do(func() {
		cdir := contractsDir()
		asyncapiFilePath = filepath.Join(cdir, "asyncapi.yaml")
		componentSchemaDir = filepath.Join(cdir, "components", "schemas")

		// Verify the contracts directory is accessible before building the compiler.
		if _, statErr := os.Stat(asyncapiFilePath); statErr != nil {
			errSchemaSetup = fmt.Errorf("contracts/asyncapi.yaml not found at %s: %w", asyncapiFilePath, statErr)
			return
		}
		if _, statErr := os.Stat(componentSchemaDir); statErr != nil {
			errSchemaSetup = fmt.Errorf(
				"contracts/components/schemas/ not found at %s: %w",
				componentSchemaDir, statErr,
			)
			return
		}

		c := jsonschema.NewCompiler()

		// Use our YAML-capable loader for file:// URLs.
		c.UseLoader(jsonschema.SchemeURLLoader{
			"file": yamlLoader{},
		})

		sharedCompiler = c
	})

	require.NoError(t, errSchemaSetup, "schema compiler setup failed")
	return sharedCompiler
}

// fileURL converts an absolute file path to a file:// URL string.
func fileURL(absPath string) string {
	return "file://" + filepath.ToSlash(absPath)
}

// validateAgainstAsyncAPISchema validates v against a named schema from asyncapi.yaml.
// schemaName is the key under components.schemas in asyncapi.yaml
// (e.g. "ToolApprovalRequiredFrame", "DoneFrame").
func validateAgainstAsyncAPISchema(t *testing.T, schemaName string, v any) error {
	t.Helper()
	c := initSchemas(t)

	raw, err := json.Marshal(v)
	require.NoError(t, err, "json.Marshal failed for fixture")

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc), "json.Unmarshal of marshaled fixture failed")

	// Compile the schema as a fragment of the asyncapi.yaml document.
	// Fragment path: /components/schemas/<schemaName>
	// URL encodes as: file:///path/to/asyncapi.yaml#/components/schemas/SchemaName
	fragment := "/components/schemas/" + schemaName
	url := fileURL(asyncapiFilePath) + "#" + fragment

	sch, err := c.Compile(url)
	require.NoError(t, err, "could not compile asyncapi schema %q", schemaName)

	return sch.Validate(doc)
}

// validateAgainstComponentSchema validates v against a named component schema file.
// schemaName is the filename without .yaml extension
// (e.g. "Session", "LoginResponse", "ToolApprovalRequiredFrame").
func validateAgainstComponentSchema(t *testing.T, schemaName string, v any) error {
	t.Helper()
	c := initSchemas(t)

	raw, err := json.Marshal(v)
	require.NoError(t, err, "json.Marshal failed for fixture")

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc), "json.Unmarshal of marshaled fixture failed")

	schemaPath := filepath.Join(componentSchemaDir, schemaName+".yaml")
	url := fileURL(schemaPath)

	sch, err := c.Compile(url)
	require.NoError(t, err, "could not compile component schema %q from %s", schemaName, schemaPath)

	return sch.Validate(doc)
}

// validateAgainstComponentSchemaRawJSON validates pre-marshaled JSON bytes against a component schema.
func validateAgainstComponentSchemaRawJSON(t *testing.T, schemaName string, raw []byte) error {
	t.Helper()
	c := initSchemas(t)

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc))

	schemaPath := filepath.Join(componentSchemaDir, schemaName+".yaml")
	url := fileURL(schemaPath)

	sch, err := c.Compile(url)
	require.NoError(t, err, "could not compile component schema %q", schemaName)

	return sch.Validate(doc)
}

// ── Helper assertions ────────────────────────────────────────────────────────

// mustPassComponent asserts the fixture validates against a component schema file.
func mustPassComponent(t *testing.T, schemaName string, fixture any) {
	t.Helper()
	err := validateAgainstComponentSchema(t, schemaName, fixture)
	assert.NoError(t, err, "fixture must validate against component schema %q", schemaName)
}

// mustFailComponent asserts the fixture produces schema-INVALID JSON.
func mustFailComponent(t *testing.T, schemaName string, fixture any, reason string) {
	t.Helper()
	err := validateAgainstComponentSchema(t, schemaName, fixture)
	assert.Error(t, err, "expected validation error for component schema %q — %s", schemaName, reason)
}

// mustPassAsyncAPI asserts the fixture validates against an asyncapi schema.
func mustPassAsyncAPI(t *testing.T, schemaName string, fixture any) {
	t.Helper()
	err := validateAgainstAsyncAPISchema(t, schemaName, fixture)
	assert.NoError(t, err, "fixture must validate against asyncapi schema %q", schemaName)
}

// mustFailAsyncAPI asserts the fixture produces schema-INVALID JSON.
func mustFailAsyncAPI(t *testing.T, schemaName string, fixture any, reason string) {
	t.Helper()
	err := validateAgainstAsyncAPISchema(t, schemaName, fixture)
	assert.Error(t, err, "expected validation error for asyncapi schema %q — %s", schemaName, reason)
}

// ── ToolApprovalRequiredFrame — the Ava-chat bug type ─────────────────────────
// Traces to: contracts/components/schemas/ToolApprovalRequiredFrame.yaml
// Bug: args=nil → JSON "args":null → Object.keys(null) crash in SPA

func TestContract_ToolApprovalRequiredFrame_Populated(t *testing.T) {
	// Traces to: ToolApprovalRequiredFrame.yaml — all required fields, args is non-nil map
	mustPassComponent(t, "ToolApprovalRequiredFrame", FixtureToolApprovalRequiredFrame_Populated())
}

func TestContract_ToolApprovalRequiredFrame_ZeroValue(t *testing.T) {
	// Zero value: type="", approval_id="", args=nil → multiple violations
	// Traces to: ToolApprovalRequiredFrame.yaml — required + minLength constraints
	mustFailComponent(t, "ToolApprovalRequiredFrame", FixtureToolApprovalRequiredFrame_ZeroValue(),
		"zero value has empty required string fields and nil args (marshals to null)")
}

func TestContract_ToolApprovalRequiredFrame_NilArgsRejected(t *testing.T) {
	// THIS IS THE REGRESSION GUARD FOR THE AVA-CHAT BUG.
	//
	// ToolApprovalRequiredFrame.Args is declared as map[string]any (not *map[string]any),
	// so a nil map is valid Go but marshals to "args":null.
	// The schema declares args as: type: object, required: true — no nullable.
	// Therefore "args":null MUST fail schema validation.
	//
	// Traces to: ToolApprovalRequiredFrame.yaml (args field: type: object, no nullable)
	// Traces to: docs/plan/quizzical-marinating-frog.md — Phase 4 contract test spec

	fixture := FixtureToolApprovalRequiredFrame_NilArgs()

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)

	// Verify the wire bytes actually contain "args":null — confirms the fixture
	// exercises the right code path.
	assert.Contains(t, string(raw), `"args":null`,
		"fixture must marshal args=nil to \"args\":null to exercise the bug path")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "ToolApprovalRequiredFrame", raw)
	assert.Error(t, validationErr,
		"args:null MUST fail schema validation — "+
			"ToolApprovalRequiredFrame.args is required+object (non-nullable). "+
			"This is the regression guard for the Ava-chat Object.keys(null) crash.")
}

func TestContract_ToolApprovalRequiredFrame_Edge(t *testing.T) {
	// Edge: empty args {} is valid (object, not null), long approval_id, unicode agent_id
	// Traces to: ToolApprovalRequiredFrame.yaml
	mustPassComponent(t, "ToolApprovalRequiredFrame", FixtureToolApprovalRequiredFrame_Edge())
}

func TestContract_ToolApprovalRequiredFrame_Differentiation(t *testing.T) {
	// Differentiation test: two populated fixtures must produce different JSON.
	// Guards against hardcoded/stub implementations that always return the same bytes.
	// Traces to: Phase 4 quality gates — differentiation test requirement

	f1 := FixtureToolApprovalRequiredFrame_Populated()
	f2 := FixtureToolApprovalRequiredFrame_Edge()

	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)

	assert.NotEqual(t, string(raw1), string(raw2),
		"two different fixtures must produce different JSON (differentiation test)")

	mustPassComponent(t, "ToolApprovalRequiredFrame", f1)
	mustPassComponent(t, "ToolApprovalRequiredFrame", f2)
}

// ── SessionStateFrame — pending_approvals MUST be non-nil array ──────────────
// Traces to: contracts/components/schemas/SessionStateFrame.yaml

func TestContract_SessionStateFrame_Populated(t *testing.T) {
	mustPassComponent(t, "SessionStateFrame", FixtureSessionStateFrame_Populated())
}

func TestContract_SessionStateFrame_ZeroValue(t *testing.T) {
	// Zero value: type="", user_id="", pending_approvals=nil, emitted_at=""
	mustFailComponent(t, "SessionStateFrame", FixtureSessionStateFrame_ZeroValue(),
		"zero value has empty required fields and nil slice (marshals to null)")
}

func TestContract_SessionStateFrame_NilPendingApprovalsRejected(t *testing.T) {
	// nil pending_approvals → JSON null → schema requires type: array
	// Traces to: SessionStateFrame.yaml (pending_approvals: type: array)

	fixture := SessionStateFrame{
		Type:             "session_state",
		UserId:           "user-admin-1",
		EmittedAt:        "2026-05-17T10:00:00Z",
		PendingApprovals: nil, // bug: nil slice marshals to null
	}

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"pending_approvals":null`,
		"nil slice must marshal to null to exercise the bug path")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "SessionStateFrame", raw)
	assert.Error(t, validationErr,
		"pending_approvals:null MUST fail validation — schema requires type: array")
}

func TestContract_SessionStateFrame_EmptyApprovals(t *testing.T) {
	// Empty but non-nil slice is valid — common case when no approvals pending.
	// Traces to: SessionStateFrame.yaml — pending_approvals is array, no minItems
	mustPassComponent(t, "SessionStateFrame", FixtureSessionStateFrame_EmptyApprovals())
}

func TestContract_SessionStateFrame_Edge(t *testing.T) {
	// Edge: multiple pending approvals
	// Traces to: SessionStateFrame.yaml
	mustPassComponent(t, "SessionStateFrame", FixtureSessionStateFrame_Edge())
}

// ── MediaFrame — parts MUST be non-nil, non-empty array ──────────────────────
// Traces to: contracts/components/schemas/MediaFrame.yaml

func TestContract_MediaFrame_Populated(t *testing.T) {
	mustPassComponent(t, "MediaFrame", FixtureMediaFrame_Populated())
}

func TestContract_MediaFrame_ZeroValue(t *testing.T) {
	mustFailComponent(t, "MediaFrame", FixtureMediaFrame_ZeroValue(),
		"zero value has empty required fields and nil parts (marshals to null)")
}

func TestContract_MediaFrame_NilPartsRejected(t *testing.T) {
	// nil parts → JSON null → schema requires array with minItems: 1
	// Traces to: MediaFrame.yaml (parts: type: array, minItems: 1)

	fixture := FixtureMediaFrame_NilParts()

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"parts":null`,
		"nil parts must marshal to null to exercise the bug path")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "MediaFrame", raw)
	assert.Error(t, validationErr,
		"parts:null MUST fail validation — schema requires array with minItems: 1")
}

func TestContract_MediaFrame_Edge(t *testing.T) {
	// Edge: multiple parts, unicode filenames, mixed media types
	// Traces to: MediaFrame.yaml
	mustPassComponent(t, "MediaFrame", FixtureMediaFrame_Edge())
}

// ── ToolCallStartFrame — params MUST be non-nil object ───────────────────────
// Traces to: contracts/components/schemas/ToolCallStartFrame.yaml

func TestContract_ToolCallStartFrame_Populated(t *testing.T) {
	mustPassComponent(t, "ToolCallStartFrame", FixtureToolCallStartFrame_Populated())
}

func TestContract_ToolCallStartFrame_ZeroValue(t *testing.T) {
	mustFailComponent(t, "ToolCallStartFrame", FixtureToolCallStartFrame_ZeroValue(),
		"zero value has empty required fields and nil params (marshals to null)")
}

func TestContract_ToolCallStartFrame_NilParamsRejected(t *testing.T) {
	// params: nil → JSON null → schema requires type: object (non-nullable)
	// Traces to: ToolCallStartFrame.yaml (params: type: object, required)

	fixture := FixtureToolCallStartFrame_NilParams()

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"params":null`,
		"nil params must marshal to null")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "ToolCallStartFrame", raw)
	assert.Error(t, validationErr,
		"params:null MUST fail validation — schema requires type: object")
}

func TestContract_ToolCallStartFrame_Edge(t *testing.T) {
	// Edge: empty params {} is valid (object, not null), very long call_id
	// Traces to: ToolCallStartFrame.yaml
	mustPassComponent(t, "ToolCallStartFrame", FixtureToolCallStartFrame_Edge())
}

// ── DoneFrame ─────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DoneFrame.yaml
// (asyncapi.yaml has DoneFrame inline, which also references DoneStats — test both)

func TestContract_DoneFrame_Populated(t *testing.T) {
	mustPassComponent(t, "DoneFrame", FixtureDoneFrame_Populated())
}

func TestContract_DoneFrame_ZeroValue(t *testing.T) {
	// type="" and session_id="" — both required with minLength:1
	mustFailComponent(t, "DoneFrame", FixtureDoneFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

func TestContract_DoneFrame_NoStats(t *testing.T) {
	// stats is optional — absent is valid
	// Traces to: DoneFrame schema — stats is not in required list
	mustPassComponent(t, "DoneFrame", FixtureDoneFrame_NoStats())
}

func TestContract_DoneFrame_Edge(t *testing.T) {
	mustPassComponent(t, "DoneFrame", FixtureDoneFrame_Edge())
}

// ── ErrorFrame ────────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.ErrorFrame

func TestContract_ErrorFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ErrorFrame", FixtureErrorFrame_Populated())
}

func TestContract_ErrorFrame_ZeroValue(t *testing.T) {
	// type="" and message="" — both required
	mustFailAsyncAPI(t, "ErrorFrame", FixtureErrorFrame_ZeroValue(),
		"zero value has empty required type and message fields")
}

func TestContract_ErrorFrame_Edge(t *testing.T) {
	// Edge: very long error message, no session_id (optional)
	mustPassAsyncAPI(t, "ErrorFrame", FixtureErrorFrame_Edge())
}

// ── TokenFrame ────────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.TokenFrame

func TestContract_TokenFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "TokenFrame", FixtureTokenFrame_Populated())
}

func TestContract_TokenFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "TokenFrame", FixtureTokenFrame_ZeroValue(),
		"zero value has empty required type, session_id, content fields")
}

func TestContract_TokenFrame_Edge(t *testing.T) {
	// Unicode streaming token
	mustPassAsyncAPI(t, "TokenFrame", FixtureTokenFrame_Edge())
}

// ── ToolCallResultFrame ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ToolCallResultFrame.yaml

func TestContract_ToolCallResultFrame_Populated(t *testing.T) {
	mustPassComponent(t, "ToolCallResultFrame", FixtureToolCallResultFrame_Populated())
}

func TestContract_ToolCallResultFrame_ZeroValue(t *testing.T) {
	// type, session_id, tool, call_id, status all empty — multiple violations
	mustFailComponent(t, "ToolCallResultFrame", FixtureToolCallResultFrame_ZeroValue(),
		"zero value has empty required string fields")
}

func TestContract_ToolCallResultFrame_Error(t *testing.T) {
	// Status "error" with nil result is still valid (result oneOf allows any value)
	// Traces to: ToolCallResultFrame.yaml (result: oneOf: [{},...])
	mustPassComponent(t, "ToolCallResultFrame", FixtureToolCallResultFrame_Error())
}

func TestContract_ToolCallResultFrame_Edge(t *testing.T) {
	// String result, no agent_id, no parent_call_id
	mustPassComponent(t, "ToolCallResultFrame", FixtureToolCallResultFrame_Edge())
}

// ── SessionStartedFrame ───────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.SessionStartedFrame

func TestContract_SessionStartedFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "SessionStartedFrame", FixtureSessionStartedFrame_Populated())
}

func TestContract_SessionStartedFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "SessionStartedFrame", FixtureSessionStartedFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

// ── SubagentStartFrame ────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.SubagentStartFrame

func TestContract_SubagentStartFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "SubagentStartFrame", FixtureSubagentStartFrame_Populated())
}

func TestContract_SubagentStartFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "SubagentStartFrame", FixtureSubagentStartFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── SubagentEndFrame ──────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.SubagentEndFrame

func TestContract_SubagentEndFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "SubagentEndFrame", FixtureSubagentEndFrame_Populated())
}

func TestContract_SubagentEndFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "SubagentEndFrame", FixtureSubagentEndFrame_ZeroValue(),
		"zero value has empty required type, session_id, span_id, status fields")
}

// ── ExecApprovalRequestFrame ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ExecApprovalRequestFrame.yaml

func TestContract_ExecApprovalRequestFrame_Populated(t *testing.T) {
	mustPassComponent(t, "ExecApprovalRequestFrame", FixtureExecApprovalRequestFrame_Populated())
}

func TestContract_ExecApprovalRequestFrame_ZeroValue(t *testing.T) {
	mustFailComponent(t, "ExecApprovalRequestFrame", FixtureExecApprovalRequestFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── ReplayMessageFrame ────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.ReplayMessageFrame

func TestContract_ReplayMessageFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ReplayMessageFrame", FixtureReplayMessageFrame_Populated())
}

func TestContract_ReplayMessageFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "ReplayMessageFrame", FixtureReplayMessageFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── RateLimitFrame ────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.RateLimitFrame

func TestContract_RateLimitFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "RateLimitFrame", FixtureRateLimitFrame_Populated())
}

func TestContract_RateLimitFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "RateLimitFrame", FixtureRateLimitFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── AgentSwitchedFrame ────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.AgentSwitchedFrame

func TestContract_AgentSwitchedFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "AgentSwitchedFrame", FixtureAgentSwitchedFrame_Populated())
}

func TestContract_AgentSwitchedFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "AgentSwitchedFrame", FixtureAgentSwitchedFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

// ── TaskStatusChangedFrame ────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.TaskStatusChangedFrame

func TestContract_TaskStatusChangedFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "TaskStatusChangedFrame", FixtureTaskStatusChangedFrame_Populated())
}

func TestContract_TaskStatusChangedFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "TaskStatusChangedFrame", FixtureTaskStatusChangedFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── SystemOverloadFrame ───────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.SystemOverloadFrame

func TestContract_SystemOverloadFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "SystemOverloadFrame", FixtureSystemOverloadFrame_Populated())
}

func TestContract_SystemOverloadFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "SystemOverloadFrame", FixtureSystemOverloadFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

// ── CancelStageFrame ──────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.CancelStageFrame

func TestContract_CancelStageFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "CancelStageFrame", FixtureCancelStageFrame_Populated())
}

func TestContract_CancelStageFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "CancelStageFrame", FixtureCancelStageFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── ReplayWarningFrame ────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.ReplayWarningFrame

func TestContract_ReplayWarningFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ReplayWarningFrame", FixtureReplayWarningFrame_Populated())
}

func TestContract_ReplayWarningFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "ReplayWarningFrame", FixtureReplayWarningFrame_ZeroValue(),
		"zero value has empty required fields")
}

// ── SessionCloseAckFrame ──────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.SessionCloseAckFrame

func TestContract_SessionCloseAckFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "SessionCloseAckFrame", FixtureSessionCloseAckFrame_Populated())
}

func TestContract_SessionCloseAckFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "SessionCloseAckFrame", FixtureSessionCloseAckFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

// ── DevicePairingRequestFrame ─────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.DevicePairingRequestFrame

func TestContract_DevicePairingRequestFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "DevicePairingRequestFrame", FixtureDevicePairingRequestFrame_Populated())
}

func TestContract_DevicePairingRequestFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "DevicePairingRequestFrame", FixtureDevicePairingRequestFrame_ZeroValue(),
		"zero value has empty required type and device_id fields")
}

// ── ExecApprovalResponseAckFrame ──────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.ExecApprovalResponseAckFrame

func TestContract_ExecApprovalResponseAckFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ExecApprovalResponseAckFrame", FixtureExecApprovalResponseAckFrame_Populated())
}

func TestContract_ExecApprovalResponseAckFrame_ZeroValue(t *testing.T) {
	// Only "type" is required; zero value with type="" should fail.
	mustFailAsyncAPI(t, "ExecApprovalResponseAckFrame", FixtureExecApprovalResponseAckFrame_ZeroValue(),
		"zero value has empty required type field")
}

// ── REST response types (OpenAPI) ─────────────────────────────────────────────

// LoginResponse — bearer token response
// Traces to: contracts/components/schemas/LoginResponse.yaml

func TestContract_LoginResponse_Populated(t *testing.T) {
	mustPassComponent(t, "LoginResponse", FixtureLoginResponse_Populated())
}

func TestContract_LoginResponse_ZeroValue(t *testing.T) {
	// token="", role="", username="" — all required, role must be enum value
	mustFailComponent(t, "LoginResponse", FixtureLoginResponse_ZeroValue(),
		"zero value has empty required fields and role doesn't match enum")
}

func TestContract_LoginResponse_Edge(t *testing.T) {
	// role="user", no warning, unicode username
	mustPassComponent(t, "LoginResponse", FixtureLoginResponse_Edge())
}

func TestContract_LoginResponse_Differentiation(t *testing.T) {
	// Two populated fixtures produce different JSON — guards against hardcoded stubs.
	f1 := FixtureLoginResponse_Populated()
	f2 := FixtureLoginResponse_Edge()

	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)

	assert.NotEqual(t, string(raw1), string(raw2),
		"two different fixtures must produce different JSON (differentiation test)")

	mustPassComponent(t, "LoginResponse", f1)
	mustPassComponent(t, "LoginResponse", f2)
}

// Session — core session metadata
// Traces to: contracts/components/schemas/Session.yaml

func TestContract_Session_Populated(t *testing.T) {
	mustPassComponent(t, "Session", FixtureSession_Populated())
}

func TestContract_Session_ZeroValue(t *testing.T) {
	// id="", agent_id="", title="", status="", partitions=nil, created_at year 1
	mustFailComponent(t, "Session", FixtureSession_ZeroValue(),
		"zero value has multiple required field violations and nil partitions")
}

func TestContract_Session_ZeroTimeDetected(t *testing.T) {
	// Specific test: time.Time{} marshals to "0001-01-01T00:00:00Z".
	// While technically valid RFC 3339, it's a sentinel for a zero-value bug.
	// Traces to: Phase 4 spec — "Empty time.Time serialized as 0001-01-01T00:00:00Z"

	fixture := FixtureSession_ZeroValue()
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)

	assert.Contains(t, string(raw), `"0001-01-01T00:00:00Z"`,
		"time.Time{} zero value must marshal to 0001-01-01T00:00:00Z — "+
			"this documents the sentinel value; callers must set real timestamps")
}

func TestContract_Session_Edge(t *testing.T) {
	// archived session, task type, unicode title, empty partitions (valid)
	mustPassComponent(t, "Session", FixtureSession_Edge())
}

func TestContract_Session_NilPartitionsRejected(t *testing.T) {
	// partitions is required + type: array — nil marshals to null → must fail
	// Traces to: Session.yaml (partitions: required, type: array)

	sessionType := SessionType("chat")
	fixture := Session{
		Id:         "550e8400-e29b-41d4-a716-446655440000",
		AgentId:    "jim",
		Title:      "Test session",
		Status:     "active",
		CreatedAt:  time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 5, 17, 10, 1, 0, 0, time.UTC),
		Channel:    "webchat",
		Partitions: nil, // THE BUG: nil slice → JSON null → schema violation
		Type:       &sessionType,
		Stats: struct {
			Cost         float64 `json:"cost"`
			MessageCount int     `json:"message_count"`
			TokensIn     int     `json:"tokens_in"`
			TokensOut    int     `json:"tokens_out"`
			TokensTotal  int     `json:"tokens_total"`
			ToolCalls    int     `json:"tool_calls"`
		}{},
	}

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"partitions":null`,
		"nil Partitions must marshal to null")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "Session", raw)
	assert.Error(t, validationErr,
		"partitions:null MUST fail validation — schema requires type: array")
}

// Agent — agent configuration
// Traces to: contracts/components/schemas/Agent.yaml

func TestContract_Agent_Populated(t *testing.T) {
	mustPassComponent(t, "Agent", FixtureAgent_Populated())
}

func TestContract_Agent_ZeroValue(t *testing.T) {
	// id="", name="", type="" (not in enum), soul="", heartbeat=""
	mustFailComponent(t, "Agent", FixtureAgent_ZeroValue(),
		"zero value has empty required fields including type which must be enum value")
}

func TestContract_Agent_Edge(t *testing.T) {
	// draft status, long ID, unicode name, empty soul (valid for draft)
	mustPassComponent(t, "Agent", FixtureAgent_Edge())
}

// User — user record
// Traces to: contracts/components/schemas/User.yaml

func TestContract_User_Populated(t *testing.T) {
	mustPassComponent(t, "User", FixtureUser_Populated())
}

func TestContract_User_ZeroValue(t *testing.T) {
	// username="", role="" — both required; role must be enum value
	mustFailComponent(t, "User", FixtureUser_ZeroValue(),
		"zero value has empty required fields")
}

func TestContract_User_Edge(t *testing.T) {
	// long username, role=user, no active token, no password
	mustPassComponent(t, "User", FixtureUser_Edge())
}

// HealthResponse — gateway health check
// Traces to: contracts/components/schemas/HealthResponse.yaml

func TestContract_HealthResponse_Populated(t *testing.T) {
	mustPassComponent(t, "HealthResponse", FixtureHealthResponse_Populated())
}

func TestContract_HealthResponse_ZeroValue(t *testing.T) {
	// status="" — required, must be "ok" enum value
	mustFailComponent(t, "HealthResponse", FixtureHealthResponse_ZeroValue(),
		"zero value has empty status field which must be 'ok'")
}

// ── Cross-cutting validation tests ────────────────────────────────────────────

// TestContract_AllFrames_TypeFieldPresent verifies that every populated WS frame
// fixture has a non-empty type field. Missing type means the SPA can't dispatch
// the frame to the right handler — silent data loss.
func TestContract_AllFrames_TypeFieldPresent(t *testing.T) {
	type frameWithType struct {
		name    string
		fixture any
	}

	frames := []frameWithType{
		{"ToolApprovalRequiredFrame", FixtureToolApprovalRequiredFrame_Populated()},
		{"SessionStateFrame", FixtureSessionStateFrame_Populated()},
		{"MediaFrame", FixtureMediaFrame_Populated()},
		{"ToolCallStartFrame", FixtureToolCallStartFrame_Populated()},
		{"DoneFrame", FixtureDoneFrame_Populated()},
		{"ErrorFrame", FixtureErrorFrame_Populated()},
		{"TokenFrame", FixtureTokenFrame_Populated()},
		{"ToolCallResultFrame", FixtureToolCallResultFrame_Populated()},
		{"SessionStartedFrame", FixtureSessionStartedFrame_Populated()},
		{"SubagentStartFrame", FixtureSubagentStartFrame_Populated()},
		{"SubagentEndFrame", FixtureSubagentEndFrame_Populated()},
		{"ExecApprovalRequestFrame", FixtureExecApprovalRequestFrame_Populated()},
		{"ReplayMessageFrame", FixtureReplayMessageFrame_Populated()},
		{"RateLimitFrame", FixtureRateLimitFrame_Populated()},
		{"AgentSwitchedFrame", FixtureAgentSwitchedFrame_Populated()},
		{"TaskStatusChangedFrame", FixtureTaskStatusChangedFrame_Populated()},
		{"SystemOverloadFrame", FixtureSystemOverloadFrame_Populated()},
		{"CancelStageFrame", FixtureCancelStageFrame_Populated()},
		{"ReplayWarningFrame", FixtureReplayWarningFrame_Populated()},
		{"SessionCloseAckFrame", FixtureSessionCloseAckFrame_Populated()},
		{"DevicePairingRequestFrame", FixtureDevicePairingRequestFrame_Populated()},
		{"ExecApprovalResponseAckFrame", FixtureExecApprovalResponseAckFrame_Populated()},
	}

	for _, f := range frames {
		t.Run(f.name, func(t *testing.T) {
			raw, err := json.Marshal(f.fixture)
			require.NoError(t, err)

			var m map[string]any
			require.NoError(t, json.Unmarshal(raw, &m))

			typeVal, ok := m["type"]
			assert.True(t, ok, "frame must have a 'type' field")
			typeStr, isStr := typeVal.(string)
			assert.True(t, isStr, "frame 'type' field must be a string")
			assert.NotEmpty(t, typeStr, "frame 'type' field must be non-empty")
		})
	}
}

// TestContract_NilMapsSerializeAsNull documents Go's JSON marshaling behavior:
// a nil map[string]any marshals to JSON null (not {}).
// This is the root cause of the Ava-chat bug — the test documents the behavior
// so it's visible in the test report and not "magic" knowledge.
func TestContract_NilMapsSerializeAsNull(t *testing.T) {
	type withMap struct {
		Args map[string]any `json:"args"`
	}

	// nil map → null
	raw1, err := json.Marshal(withMap{Args: nil})
	require.NoError(t, err)
	assert.Equal(t, `{"args":null}`, string(raw1),
		"nil map[string]any marshals to JSON null — "+
			"this is the root cause of the Ava-chat Object.keys(null) crash")

	// empty map → {}
	raw2, err := json.Marshal(withMap{Args: map[string]any{}})
	require.NoError(t, err)
	assert.Equal(t, `{"args":{}}`, string(raw2),
		"initialized empty map[string]any marshals to JSON {} (correct)")

	// populated map → object with fields
	raw3, err := json.Marshal(withMap{Args: map[string]any{"key": "value"}})
	require.NoError(t, err)
	assert.Equal(t, `{"args":{"key":"value"}}`, string(raw3),
		"populated map[string]any marshals to JSON object (correct)")

	// Three different inputs produce three different outputs.
	assert.NotEqual(t, string(raw1), string(raw2))
	assert.NotEqual(t, string(raw2), string(raw3))
}

// TestContract_NilSlicesSerializeAsNull documents that nil slices also marshal to null.
func TestContract_NilSlicesSerializeAsNull(t *testing.T) {
	type withSlice struct {
		Items []string `json:"items"`
	}

	// nil slice → null
	raw1, err := json.Marshal(withSlice{Items: nil})
	require.NoError(t, err)
	assert.Equal(t, `{"items":null}`, string(raw1),
		"nil []string marshals to JSON null — callers must init to []string{}")

	// empty slice → []
	raw2, err := json.Marshal(withSlice{Items: []string{}})
	require.NoError(t, err)
	assert.Equal(t, `{"items":[]}`, string(raw2),
		"initialized empty []string marshals to [] (correct)")

	assert.NotEqual(t, string(raw1), string(raw2))
}

// ── Client → server frames (AsyncAPI) ────────────────────────────────────────
//
// These tests cover the 7 client→server frame types that the SPA sends to the
// gateway. Each test validates the schema in both directions (populated passes,
// zero value fails) using mustPassAsyncAPI / mustFailAsyncAPI so both helper
// functions have live callers.

// ── AttachSessionFrame ────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.AttachSessionFrame

func TestContract_AttachSessionFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "AttachSessionFrame", FixtureAttachSessionFrame_Populated())
}

func TestContract_AttachSessionFrame_ZeroValue(t *testing.T) {
	// type="" and session_id="" — both required, session_id has minLength:1
	mustFailAsyncAPI(t, "AttachSessionFrame", FixtureAttachSessionFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

func TestContract_AttachSessionFrame_Edge(t *testing.T) {
	// Long session_id is still valid
	mustPassAsyncAPI(t, "AttachSessionFrame", FixtureAttachSessionFrame_Edge())
}

// ── AuthFrame ─────────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.AuthFrame

func TestContract_AuthFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "AuthFrame", FixtureAuthFrame_Populated())
}

func TestContract_AuthFrame_ZeroValue(t *testing.T) {
	// type="" and token="" — both required; token has minLength:1
	mustFailAsyncAPI(t, "AuthFrame", FixtureAuthFrame_ZeroValue(),
		"zero value has empty required type and token fields (token has minLength:1)")
}

func TestContract_AuthFrame_Edge(t *testing.T) {
	// Single-char token satisfies minLength:1
	mustPassAsyncAPI(t, "AuthFrame", FixtureAuthFrame_Edge())
}

// ── CancelFrame ───────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.CancelFrame

func TestContract_CancelFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "CancelFrame", FixtureCancelFrame_Populated())
}

func TestContract_CancelFrame_ZeroValue(t *testing.T) {
	// type="" and session_id="" — both required, minLength:1
	mustFailAsyncAPI(t, "CancelFrame", FixtureCancelFrame_ZeroValue(),
		"zero value has empty required type and session_id fields")
}

func TestContract_CancelFrame_Edge(t *testing.T) {
	mustPassAsyncAPI(t, "CancelFrame", FixtureCancelFrame_Edge())
}

// ── DevicePairingResponseFrame ────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.DevicePairingResponseFrame

func TestContract_DevicePairingResponseFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "DevicePairingResponseFrame", FixtureDevicePairingResponseFrame_Populated())
}

func TestContract_DevicePairingResponseFrame_ZeroValue(t *testing.T) {
	// type="", device_id="", decision="" — all required; decision must be enum value
	mustFailAsyncAPI(t, "DevicePairingResponseFrame", FixtureDevicePairingResponseFrame_ZeroValue(),
		"zero value has empty required fields and decision is not a valid enum value")
}

func TestContract_DevicePairingResponseFrame_Edge(t *testing.T) {
	// reject is the other valid enum value
	mustPassAsyncAPI(t, "DevicePairingResponseFrame", FixtureDevicePairingResponseFrame_Edge())
}

// ── ExecApprovalResponseFrame ─────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.ExecApprovalResponseFrame

func TestContract_ExecApprovalResponseFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ExecApprovalResponseFrame", FixtureExecApprovalResponseFrame_Populated())
}

func TestContract_ExecApprovalResponseFrame_ZeroValue(t *testing.T) {
	// type="", id="", decision="" — all required; id has minLength:1; decision not in enum
	mustFailAsyncAPI(t, "ExecApprovalResponseFrame", FixtureExecApprovalResponseFrame_ZeroValue(),
		"zero value has empty required fields and decision is not a valid enum value")
}

func TestContract_ExecApprovalResponseFrame_Edge(t *testing.T) {
	// deny is a valid enum value; always is also valid but tested implicitly
	mustPassAsyncAPI(t, "ExecApprovalResponseFrame", FixtureExecApprovalResponseFrame_Edge())
}

// ── MessageFrame ──────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.MessageFrame

func TestContract_MessageFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "MessageFrame", FixtureMessageFrame_Populated())
}

func TestContract_MessageFrame_ZeroValue(t *testing.T) {
	// type="" and content="" — both required; content has minLength:1
	mustFailAsyncAPI(t, "MessageFrame", FixtureMessageFrame_ZeroValue(),
		"zero value has empty required type and content fields (content has minLength:1)")
}

func TestContract_MessageFrame_Edge(t *testing.T) {
	// Large content without session_id (starts a new session) — valid
	mustPassAsyncAPI(t, "MessageFrame", FixtureMessageFrame_Edge())
}

// ── PingFrame ─────────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.PingFrame

func TestContract_PingFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "PingFrame", FixturePingFrame_Populated())
}

func TestContract_PingFrame_ZeroValue(t *testing.T) {
	// type="" — required, must be const "ping"
	mustFailAsyncAPI(t, "PingFrame", FixturePingFrame_ZeroValue(),
		"zero value has empty type field (must be const 'ping')")
}

func TestContract_PingFrame_Edge(t *testing.T) {
	// PingFrame has only the type field; edge = populated (both are minimal)
	mustPassAsyncAPI(t, "PingFrame", FixturePingFrame_Edge())
}
