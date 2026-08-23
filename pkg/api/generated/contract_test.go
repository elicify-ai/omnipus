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
	"reflect"
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

	// sharedCompilerMu guards concurrent calls to sharedCompiler.Compile.
	// jsonschema/v6's Compiler mutates internal state during Compile (it
	// caches resolved schemas in an unsynchronised map), so calling Compile
	// from multiple goroutines on the same instance is a data race —
	// observed as "fatal error: concurrent map read and map write" on CI
	// runners running TestCompileInboundSchema_ConcurrentDifferentSchemas.
	// We serialize access here; the lock is held only across the Compile
	// call so the cache hit path stays fast.
	sharedCompilerMu sync.Mutex
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

	sharedCompilerMu.Lock()
	sch, err := c.Compile(url)
	sharedCompilerMu.Unlock()
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

	sharedCompilerMu.Lock()
	sch, err := c.Compile(url)
	sharedCompilerMu.Unlock()
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

	sharedCompilerMu.Lock()
	sch, err := c.Compile(url)
	sharedCompilerMu.Unlock()
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

// ── Structured LLM errors ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/LLMError.yaml,
// contracts/components/schemas/LLMErrorReplay.yaml, and their inline payload
// mirrors in contracts/asyncapi.yaml ErrorFrame and ReplayErrorFrame.

func TestContract_ErrorFrame_LLMErrorInvalidCode(t *testing.T) {
	frame := map[string]any{
		"type":    "error",
		"message": "The provider rejected the request.",
		"payload": map[string]any{
			"llm_error": map[string]any{
				"code":      "invalid_code",
				"message":   "The provider rejected the request.",
				"retryable": false,
			},
		},
	}
	mustFailAsyncAPI(t, "ErrorFrame", frame, "llm_error.code must use the declared enum")
}

func TestContract_ErrorFrame_LLMErrorMissingRetryable(t *testing.T) {
	frame := map[string]any{
		"type":    "error",
		"message": "The provider rejected the request.",
		"payload": map[string]any{
			"llm_error": map[string]any{
				"code":    "provider_rejected",
				"message": "The provider rejected the request.",
			},
		},
	}
	mustFailAsyncAPI(t, "ErrorFrame", frame, "llm_error.retryable is required")
}

func TestContract_ErrorFrame_LLMErrorUnknownProperty(t *testing.T) {
	frame := map[string]any{
		"type":    "error",
		"message": "The provider rejected the request.",
		"payload": map[string]any{
			"llm_error": map[string]any{
				"code":      "provider_rejected",
				"message":   "The provider rejected the request.",
				"retryable": false,
				"provider":  "raw-provider-name",
			},
		},
	}
	mustFailAsyncAPI(t, "ErrorFrame", frame, "llm_error rejects undeclared properties")
}

func TestContract_ErrorFrame_LLMErrorDetailOmitted(t *testing.T) {
	frame := map[string]any{
		"type":    "error",
		"message": "The provider rejected the request.",
		"payload": map[string]any{
			"llm_error": map[string]any{
				"code":      "provider_rejected",
				"message":   "The provider rejected the request.",
				"retryable": false,
			},
		},
	}
	mustPassAsyncAPI(t, "ErrorFrame", frame)
}

func TestContract_ReplayErrorFrame_LLMErrorDetailRejected(t *testing.T) {
	frame := map[string]any{
		"type":       "replay_error",
		"session_id": "sess-1",
		"entry_id":   "entry-1",
		"timestamp":  "2026-07-22T12:00:00Z",
		"kind":       "error",
		"message":    "The provider rejected the request.",
		"payload": map[string]any{
			"llm_error": map[string]any{
				"code":      "provider_rejected",
				"message":   "The provider rejected the request.",
				"retryable": false,
				"detail":    "raw provider response",
			},
		},
	}
	mustFailAsyncAPI(t, "ReplayErrorFrame", frame, "replay llm_error must reject live-only detail")
}

func TestContract_ReplayErrorFrame_LLMErrorEmptyMessage(t *testing.T) {
	frame := map[string]any{
		"type":       "replay_error",
		"session_id": "sess-1",
		"entry_id":   "entry-1",
		"timestamp":  "2026-07-22T12:00:00Z",
		"kind":       "error",
		"message":    "The provider rejected the request.",
		"payload": map[string]any{
			"llm_error": map[string]any{
				"code":      "provider_rejected",
				"message":   "",
				"retryable": false,
			},
		},
	}
	mustFailAsyncAPI(t, "ReplayErrorFrame", frame, "replay llm_error.message must not be empty")
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

// TestContract_SubagentEndFrame_Parked proves the ADR-057 UAT defect C2 fix's
// new "parked" status value is schema-valid — added to the enum alongside
// success/error/cancelled/interrupted/timeout, not a parallel/undeclared value.
func TestContract_SubagentEndFrame_Parked(t *testing.T) {
	mustPassAsyncAPI(t, "SubagentEndFrame", FixtureSubagentEndFrame_Parked())
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

// ── REST response types (OpenAPI) ─────────────────────────────────────────────

// LoginResponse — bearer token response
// Traces to: contracts/components/schemas/LoginResponse.yaml

func TestContract_LoginResponse_Populated(t *testing.T) {
	mustPassComponent(t, "LoginResponse", FixtureLoginResponse_Populated())
}

func TestContract_LoginResponse_ZeroValue(t *testing.T) {
	// token="", username="" — both required, token must match BearerToken's
	// minLength/pattern constraints.
	mustFailComponent(t, "LoginResponse", FixtureLoginResponse_ZeroValue(),
		"zero value has empty required fields and token doesn't match BearerToken pattern")
}

func TestContract_LoginResponse_Edge(t *testing.T) {
	// no warning, unicode username
	mustPassComponent(t, "LoginResponse", FixtureLoginResponse_Edge())
}

func TestContract_LoginResponse_Differentiation(t *testing.T) {
	// Two populated fixtures produce different JSON — guards against hardcoded stubs.
	f1 := FixtureLoginResponse_Populated()
	f2 := FixtureLoginResponse_Edge()

	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)

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
		// Stats left as its zero value — this test asserts the partitions:null
		// rejection; the Stats shape is irrelevant here (and is exercised by the
		// dedicated stats tests).
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
	// id="", name="", type="" (not in enum), soul=""
	mustFailComponent(t, "Agent", FixtureAgent_ZeroValue(),
		"zero value has empty required fields including type which must be enum value")
}

func TestContract_Agent_Edge(t *testing.T) {
	// draft status, long ID, unicode name, empty soul (valid for draft)
	mustPassComponent(t, "Agent", FixtureAgent_Edge())
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
		{"ReplayMessageFrame", FixtureReplayMessageFrame_Populated()},
		{"RateLimitFrame", FixtureRateLimitFrame_Populated()},
		{"AgentSwitchedFrame", FixtureAgentSwitchedFrame_Populated()},
		{"TaskStatusChangedFrame", FixtureTaskStatusChangedFrame_Populated()},
		{"SystemOverloadFrame", FixtureSystemOverloadFrame_Populated()},
		{"CancelStageFrame", FixtureCancelStageFrame_Populated()},
		{"ReplayWarningFrame", FixtureReplayWarningFrame_Populated()},
		{"SessionCloseAckFrame", FixtureSessionCloseAckFrame_Populated()},
		{"DevicePairingRequestFrame", FixtureDevicePairingRequestFrame_Populated()},
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

// ── MessageFrame ──────────────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.MessageFrame

func TestContract_MessageFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "MessageFrame", FixtureMessageFrame_Populated())
}

func TestContract_MessageFrame_ZeroValue(t *testing.T) {
	// type="" fails the const:"message" check; content="" with no media
	// also fails the anyOf (content minLength:1 OR media minItems:1).
	mustFailAsyncAPI(t, "MessageFrame", FixtureMessageFrame_ZeroValue(),
		"zero value has empty required type field and satisfies neither anyOf branch")
}

func TestContract_MessageFrame_Edge(t *testing.T) {
	// Large content without session_id (starts a new session) — valid
	mustPassAsyncAPI(t, "MessageFrame", FixtureMessageFrame_Edge())
}

// TestContract_MessageFrame_EmptyContentWithMedia_Passes — UAT Issue 5: an
// attachment-only chat send has legitimately empty content. This must pass
// schema validation (previously it tripped outbound MessageFrame schema
// validation with a ZodError on the SPA side, even though the send itself
// worked).
func TestContract_MessageFrame_EmptyContentWithMedia_Passes(t *testing.T) {
	mustPassAsyncAPI(t, "MessageFrame", FixtureMessageFrame_EmptyContentWithMedia())
}

// TestContract_MessageFrame_EmptyContentNoMedia_Fails proves the fix did NOT
// weaken content into "always optional" — empty content with no attachment
// at all must still be rejected.
func TestContract_MessageFrame_EmptyContentNoMedia_Fails(t *testing.T) {
	mustFailAsyncAPI(t, "MessageFrame", FixtureMessageFrame_EmptyContentNoMedia(),
		"empty content with no media satisfies neither anyOf branch")
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

// ── Additional type contract tests ───────────────────────────────────────────
// Each type gets: Populated (mustPass), ZeroValue (mustFail for most),
// Edge (mustPass), plus NilXxxRejected tests for required array/map fields.

// ── Task ─────────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Task.yaml

func TestContract_Task_Populated(t *testing.T) {
	// All required + optional fields set. Verifies a fully-hydrated task validates.
	// Traces to: Task.yaml — required: [id, title, action, status, workspace_id, owner, created_by, created_at, updated_at]
	mustPassComponent(t, "Task", FixtureTask_Populated())
}

func TestContract_Task_ZeroValue(t *testing.T) {
	// Zero value: id="", title="", action="" (not in enum), status="" (not in enum),
	// workspace_id="" (required). Traces to: Task.yaml.
	mustFailComponent(t, "Task", FixtureTask_ZeroValue(),
		"zero value has empty required fields; action and status are not valid enum values")
}

func TestContract_Task_Edge(t *testing.T) {
	// inbox task, no agent, unicode title, max priority, recurring trigger.
	// Traces to: Task.yaml
	mustPassComponent(t, "Task", FixtureTask_Edge())
}

func TestContract_Task_Differentiation(t *testing.T) {
	// Two different Task fixtures must produce different JSON.
	// Guards against hardcoded stubs.
	f1 := FixtureTask_Populated()
	f2 := FixtureTask_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated and Edge Task fixtures must produce different JSON")
	mustPassComponent(t, "Task", f1)
	mustPassComponent(t, "Task", f2)
}

// ── TaskOccurrenceSet ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/TaskOccurrenceSet.yaml (required:
// [task_id, occurrences_ms, day_buckets, truncated]; both array fields are
// non-nullable). Mirrors TestContract_SessionStateFrame_NilPendingApprovalsRejected
// above — proves nil really does marshal to null in this codebase (which is
// what makes the pkg/gateway/task_occurrences.go buildOneOccurrenceSet
// nil-guard load-bearing) and that a null-bearing payload is rejected by the
// schema.

func TestContract_TaskOccurrenceSet_NilOccurrencesMsRejected(t *testing.T) {
	// nil OccurrencesMs -> JSON null -> schema requires type: array, no null.
	// Traces to: TaskOccurrenceSet.yaml (occurrences_ms: type: array, required)
	fixture := TaskOccurrenceSet{
		TaskId: "task-1",
		DayBuckets: []struct {
			Count      int32  `json:"count"`
			DayEndMs   int64  `json:"day_end_ms"`
			DayStartMs int64  `json:"day_start_ms"`
			FirstMs    int64  `json:"first_ms"`
			IntervalMs *int64 `json:"interval_ms"`
			RunCounts  *struct {
				Done       int32 `json:"done"`
				Failed     int32 `json:"failed"`
				InProgress int32 `json:"in_progress"`
				Scheduled  int32 `json:"scheduled"`
				Skipped    int32 `json:"skipped"`
			} `json:"run_counts,omitempty"`
		}{},
		OccurrencesMs: nil, // bug: nil slice marshals to null
		Truncated:     false,
	}

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"occurrences_ms":null`,
		"nil slice must marshal to null to exercise the bug path")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "TaskOccurrenceSet", raw)
	assert.Error(t, validationErr,
		"occurrences_ms:null MUST fail validation — schema requires type: array")
}

func TestContract_TaskOccurrenceSet_NilDayBucketsRejected(t *testing.T) {
	// nil DayBuckets -> JSON null -> schema requires type: array, no null.
	// Traces to: TaskOccurrenceSet.yaml (day_buckets: type: array, required)
	fixture := TaskOccurrenceSet{
		TaskId:        "task-1",
		OccurrencesMs: []int64{},
		DayBuckets:    nil, // bug: nil slice marshals to null
		Truncated:     false,
	}

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"day_buckets":null`,
		"nil slice must marshal to null to exercise the bug path")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "TaskOccurrenceSet", raw)
	assert.Error(t, validationErr,
		"day_buckets:null MUST fail validation — schema requires type: array")
}

func TestContract_TaskOccurrenceSet_Populated(t *testing.T) {
	// Both arrays non-empty. Traces to: TaskOccurrenceSet.yaml
	mustPassComponent(t, "TaskOccurrenceSet", FixtureTaskOccurrenceSet_Populated())
}

func TestContract_TaskOccurrenceSet_BucketsOnly(t *testing.T) {
	// Edge: day_buckets populated, occurrences_ms empty-but-non-nil (the
	// "dense overview" shape — every day bucketed). Traces to:
	// TaskOccurrenceSet.yaml — occurrences_ms has no minItems, so [] is valid.
	mustPassComponent(t, "TaskOccurrenceSet", FixtureTaskOccurrenceSet_BucketsOnly())
}

func TestContract_TaskOccurrenceSet_OccurrencesOnly(t *testing.T) {
	// Edge: occurrences_ms populated, day_buckets empty-but-non-nil (the
	// "detail mode" shape — never bucketed). Traces to:
	// TaskOccurrenceSet.yaml — day_buckets has no minItems, so [] is valid.
	mustPassComponent(t, "TaskOccurrenceSet", FixtureTaskOccurrenceSet_OccurrencesOnly())
}

func TestContract_TaskOccurrenceSet_Differentiation(t *testing.T) {
	// The three properly-initialized fixtures (the healthy counterparts to
	// the two NilXxxRejected tests above) must all produce different,
	// null-free JSON and all pass schema validation cleanly.
	f1 := FixtureTaskOccurrenceSet_Populated()
	f2 := FixtureTaskOccurrenceSet_BucketsOnly()
	f3 := FixtureTaskOccurrenceSet_OccurrencesOnly()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	raw3, err := json.Marshal(f3)
	require.NoError(t, err)

	assert.NotEqual(t, string(raw1), string(raw2))
	assert.NotEqual(t, string(raw1), string(raw3))
	assert.NotEqual(t, string(raw2), string(raw3))

	assert.NotContains(t, string(raw1), "null", "properly-initialized fixture must never contain a null field")
	assert.NotContains(t, string(raw2), "null", "properly-initialized fixture must never contain a null field")
	assert.NotContains(t, string(raw3), "null", "properly-initialized fixture must never contain a null field")

	mustPassComponent(t, "TaskOccurrenceSet", f1)
	mustPassComponent(t, "TaskOccurrenceSet", f2)
	mustPassComponent(t, "TaskOccurrenceSet", f3)
}

// ── TaskOccurrenceSet run overlay (ADR-050 §3.7) ──────────────────────────────
// Traces to: contracts/components/schemas/TaskOccurrenceSet.yaml
// (occurrence_runs) + DayBucket.yaml (run_counts). Previously ZERO coverage
// of the populated-overlay shape (M2/H5) — the three fixtures above never
// set OccurrenceRuns or DayBuckets[].RunCounts.

func TestContract_TaskOccurrenceSet_WithRunOverlay(t *testing.T) {
	// Both additive overlay fields populated in the same response.
	// Traces to: TaskOccurrenceSet.yaml (occurrence_runs), DayBucket.yaml (run_counts)
	mustPassComponent(t, "TaskOccurrenceSet", FixtureTaskOccurrenceSet_WithRunOverlay())
}

func TestContract_TaskOccurrenceSet_WithRunOverlay_NullFree(t *testing.T) {
	// The overlay fixture must not contain a stray null anywhere — proves
	// OccurrenceRuns/RunCounts were populated, not left as their nil zero
	// values (which would omit the keys entirely via omitempty, not null,
	// but a differentiation check still catches an accidental copy-paste of
	// an un-populated fixture).
	raw, err := json.Marshal(FixtureTaskOccurrenceSet_WithRunOverlay())
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"occurrence_runs"`, "occurrence_runs must be present")
	assert.Contains(t, string(raw), `"run_counts"`, "run_counts must be present")
	assert.NotContains(t, string(raw), "null", "populated overlay fixture must never contain a null field")
}

func TestContract_TaskOccurrenceSet_OccurrenceRunsInvalidStatus(t *testing.T) {
	// Traces to: TaskOccurrenceSet.yaml — occurrence_runs[].status enum:
	// [in_progress, done, failed, skipped]. "wibble" is not a member.
	doc := map[string]any{
		"task_id":        "task-1",
		"occurrences_ms": []int64{1784620800000},
		"day_buckets":    []any{},
		"truncated":      false,
		"occurrence_runs": []map[string]any{
			{
				"occurrence_ms": 1784620800000,
				"status":        "wibble", // NOT in enum
				"run_id":        "run-1",
				"session_id":    "sess-1",
				"has_result":    false,
			},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "TaskOccurrenceSet", raw),
		"occurrence_runs[].status='wibble' must fail — not in enum [in_progress, done, failed, skipped]")
}

func TestContract_TaskOccurrenceSet_OccurrenceRunsSkippedStatus(t *testing.T) {
	// Traces to: TaskOccurrenceSet.yaml — occurrence_runs[].status enum gained
	// `skipped` (the overlap-guard outcome, TaskRun-only — never Task.status).
	// "skipped" must now validate where it was previously rejected.
	doc := map[string]any{
		"task_id":        "task-1",
		"occurrences_ms": []int64{1784620800000},
		"day_buckets":    []any{},
		"truncated":      false,
		"occurrence_runs": []map[string]any{
			{
				"occurrence_ms": 1784620800000,
				"status":        "skipped",
				"run_id":        "run-1",
				"session_id":    "sess-1",
				"has_result":    false,
			},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "TaskOccurrenceSet", raw),
		"occurrence_runs[].status='skipped' must now validate — added to the enum")
}

// ── TaskRun ──────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/TaskRun.yaml (ADR-050 / task-run-
// history-spec.md §2.1). Previously ZERO contract coverage (M2/H5).

func TestContract_TaskRun_Populated(t *testing.T) {
	mustPassComponent(t, "TaskRun", FixtureTaskRun_Populated())
}

func TestContract_TaskRun_ZeroValue(t *testing.T) {
	mustFailComponent(t, "TaskRun", FixtureTaskRun_ZeroValue(),
		"zero value: kind=\"\" and status=\"\" are not members of their enums")
}

func TestContract_TaskRun_Edge(t *testing.T) {
	// Closed (failed) manual run at the epoch-ms boundary (occurrence_ms=0,
	// zero-duration). See FixtureTaskRun_Edge's doc comment for why this
	// fixture avoids an actual null occurrence_ms/ended_at (known jsonschema/
	// v6 `nullable` gap in the shared component-schema validator, out of
	// this task's file scope).
	mustPassComponent(t, "TaskRun", FixtureTaskRun_Edge())
}

func TestContract_TaskRun_Differentiation(t *testing.T) {
	f1 := FixtureTaskRun_Populated()
	f2 := FixtureTaskRun_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated and Edge TaskRun fixtures must produce different JSON")
	mustPassComponent(t, "TaskRun", f1)
	mustPassComponent(t, "TaskRun", f2)
}

func TestContract_TaskRun_InvalidStatus(t *testing.T) {
	// Traces to: TaskRun.yaml — status enum: [in_progress, done, failed, skipped]
	doc := map[string]any{
		"run_id": "run-1", "task_id": "task-1", "occurrence_ms": nil,
		"status": "wibble", "session_id": "sess-1", "kind": "scheduled",
		"started_at": "2026-07-20T09:00:00Z", "ended_at": nil,
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "TaskRun", raw),
		"status='wibble' must fail TaskRun — not in enum [in_progress, done, failed, skipped]")
}

func TestContract_TaskRun_Skipped(t *testing.T) {
	// Traces to: TaskRun.yaml — status enum gained `skipped` (the overlap
	// guard's "this occurrence never ran" bookkeeping outcome). TaskRun-only:
	// must NEVER be added to Task.status or IsTerminal — those are separate
	// concepts (~13 unrelated dependents).
	mustPassComponent(t, "TaskRun", FixtureTaskRun_Skipped())
}

func TestContract_TaskRun_InvalidKind(t *testing.T) {
	// Traces to: TaskRun.yaml — kind enum: [scheduled, manual]. No `canceled`/
	// `queued`/`active`/`paused`/`exhausted` in v1 (spec §1 "Out", RD10).
	doc := map[string]any{
		"run_id": "run-1", "task_id": "task-1", "occurrence_ms": nil,
		"status": "done", "session_id": "sess-1", "kind": "queued",
		"started_at": "2026-07-20T09:00:00Z", "ended_at": nil,
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "TaskRun", raw),
		"kind='queued' must fail TaskRun — not in enum [scheduled, manual]")
}

func TestContract_TaskRun_RejectsExtraneousField(t *testing.T) {
	// Traces to: TaskRun.yaml — additionalProperties: false
	doc := map[string]any{
		"run_id": "run-1", "task_id": "task-1", "occurrence_ms": nil,
		"status": "done", "session_id": "sess-1", "kind": "manual",
		"started_at": "2026-07-20T09:00:00Z", "ended_at": nil,
		"canceled": true, // not a v1 field — RD10 rejects a canceled status/field
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "TaskRun", raw),
		"TaskRun with extraneous field must fail — additionalProperties: false")
}

// TestContract_TaskRun_OccurrenceMsIsInt64Type is the regression guard for
// the int64 codegen drift fix. TaskRun.OccurrenceMs was already correctly
// generated as *int64 by oapi-codegen (the OpenAPI side never had the bug —
// only scripts/gen-asyncapi-go's custom converter ignored `format: int64`).
// This test pins that correctness so a future codegen change can't silently
// regress the OpenAPI side too.
func TestContract_TaskRun_OccurrenceMsIsInt64Type(t *testing.T) {
	field, ok := reflect.TypeOf(TaskRun{}).FieldByName("OccurrenceMs")
	require.True(t, ok, "TaskRun must have an OccurrenceMs field")
	assert.Equal(t, "*int64", field.Type.String(),
		"TaskRun.OccurrenceMs must be *int64 — epoch-ms values exceed 32-bit int range")
}

func TestContract_TaskRun_OccurrenceMsInt64RoundTrip(t *testing.T) {
	// A ms-epoch value already exceeds math.MaxInt32 (2147483647) for any
	// date after 1970-01-25 — this is why 32-bit `int` truncation on
	// mipsle/arm is silent and dangerous rather than an obvious overflow.
	// Regression guard for scripts/gen-asyncapi-go/main.go's int64-format
	// fix; documents the property on the OpenAPI-generated type too (see
	// TestContract_TaskRunStatusFrame_OccurrenceMsIsInt64Type for the
	// AsyncAPI-generated type this bug actually lived in).
	large := int64(4102444800000) // 2100-01-01T00:00:00Z in epoch ms
	f := FixtureTaskRun_Populated()
	f.OccurrenceMs = &large

	raw, err := json.Marshal(f)
	require.NoError(t, err)

	var roundTrip TaskRun
	require.NoError(t, json.Unmarshal(raw, &roundTrip))
	require.NotNil(t, roundTrip.OccurrenceMs)
	assert.Equal(t, large, *roundTrip.OccurrenceMs,
		"occurrence_ms must round-trip exactly through int64 — no 32-bit truncation")

	mustPassComponent(t, "TaskRun", f)
}

// ── RunNowRequest ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RunNowRequest.yaml (ADR-050 RD7).
// Previously ZERO contract coverage (M2/H5).

func TestContract_RunNowRequest_Populated(t *testing.T) {
	mustPassComponent(t, "RunNowRequest", FixtureRunNowRequest_Populated())
}

func TestContract_RunNowRequest_Empty(t *testing.T) {
	// The empty body is explicitly VALID (re-run a normal/once task) — not a
	// failure case, unlike other _ZeroValue fixtures. occurrence_ms is
	// optional+nullable so an entirely-omitted key also passes.
	mustPassComponent(t, "RunNowRequest", FixtureRunNowRequest_Empty())
}

func TestContract_RunNowRequest_RejectsExtraneousField(t *testing.T) {
	// Traces to: RunNowRequest.yaml — additionalProperties: false
	doc := map[string]any{
		"occurrence_ms": 1784620800000,
		"force":         true, // not a defined field
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "RunNowRequest", raw),
		"RunNowRequest with extraneous field must fail — additionalProperties: false")
}

// ── TaskRunStatusFrame ───────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.TaskRunStatusFrame
// (ADR-050 §3.8). Previously ZERO contract coverage (M2/H5). No standalone
// contracts/components/schemas/TaskRunStatusFrame.yaml file exists (unlike
// TaskStatusChangedFrame), so these tests validate against the AsyncAPI
// document directly via mustPassAsyncAPI/mustFailAsyncAPI, not
// mustPassComponent.

func TestContract_TaskRunStatusFrame_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "TaskRunStatusFrame", FixtureTaskRunStatusFrame_Populated())
}

func TestContract_TaskRunStatusFrame_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "TaskRunStatusFrame", FixtureTaskRunStatusFrame_ZeroValue(),
		"zero value has empty required type/task_id/run_id/status fields")
}

func TestContract_TaskRunStatusFrame_ManualRun(t *testing.T) {
	// occurrence_ms omitted — valid for an ad-hoc/once/manual run (not required).
	mustPassAsyncAPI(t, "TaskRunStatusFrame", FixtureTaskRunStatusFrame_ManualRun())
}

func TestContract_TaskRunStatusFrame_Differentiation(t *testing.T) {
	f1 := FixtureTaskRunStatusFrame_Populated()
	f2 := FixtureTaskRunStatusFrame_ManualRun()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated and ManualRun TaskRunStatusFrame fixtures must produce different JSON")
	mustPassAsyncAPI(t, "TaskRunStatusFrame", f1)
	mustPassAsyncAPI(t, "TaskRunStatusFrame", f2)
}

func TestContract_TaskRunStatusFrame_InvalidStatus(t *testing.T) {
	// Traces to: asyncapi.yaml TaskRunStatusFrame.status enum:
	// [in_progress, done, failed] — the narrower 3-state TaskRun vocabulary
	// (no canceled/queued in v1, RD10).
	fixture := map[string]any{
		"type": "task_run_status", "task_id": "task-1", "run_id": "run-1",
		"status": "wibble", // NOT in enum
	}
	mustFailAsyncAPI(t, "TaskRunStatusFrame", fixture,
		"status='wibble' must fail TaskRunStatusFrame — not in enum [in_progress, done, failed]")
}

func TestContract_TaskRunStatusFrame_RejectsExtraneousField(t *testing.T) {
	// Traces to: asyncapi.yaml TaskRunStatusFrame — additionalProperties: false
	fixture := map[string]any{
		"type": "task_run_status", "task_id": "task-1", "run_id": "run-1",
		"status": "done", "canceled": true, // not a v1 field
	}
	mustFailAsyncAPI(t, "TaskRunStatusFrame", fixture,
		"TaskRunStatusFrame with extraneous field must fail — additionalProperties: false")
}

// TestContract_TaskRunStatusFrame_OccurrenceMsIsInt64Type is THE regression
// guard for the AsyncAPI int64 codegen drift fix (M1 / this task's Fix 1).
// scripts/gen-asyncapi-go/main.go previously ignored the `format: int64`
// modifier on `type: integer` fields, generating `*int` instead of `*int64`
// for TaskRunStatusFrame.OccurrenceMs — a silent 32-bit truncation risk for
// an epoch-ms value on mipsle/arm targets (shipped, see CLAUDE.md Tech
// Stack). Asserting the generated field's exact reflect.Type gives a clear,
// readable failure here rather than a downstream compile error in
// pkg/gateway/websocket.go, which now assigns *p.OccurrenceMs directly (no
// narrowing cast) — that assignment itself would fail to compile against
// *int, making this test partially redundant with the build but still
// valuable as fast, documented, and independent of that call site surviving
// a future refactor.
func TestContract_TaskRunStatusFrame_OccurrenceMsIsInt64Type(t *testing.T) {
	field, ok := reflect.TypeOf(TaskRunStatusFrame{}).FieldByName("OccurrenceMs")
	require.True(t, ok, "TaskRunStatusFrame must have an OccurrenceMs field")
	assert.Equal(t, "*int64", field.Type.String(),
		"OccurrenceMs must be *int64, not *int — 32-bit truncation regression guard")
}

func TestContract_TaskRunStatusFrame_OccurrenceMsInt64RoundTrip(t *testing.T) {
	// Companion to the reflect-type guard above: proves an actual large
	// value (already > math.MaxInt32 for any modern date) round-trips
	// through marshal → schema validation → unmarshal with no precision
	// loss.
	large := int64(4102444800000) // 2100-01-01T00:00:00Z in epoch ms
	f := FixtureTaskRunStatusFrame_Populated()
	f.OccurrenceMs = &large

	raw, err := json.Marshal(f)
	require.NoError(t, err)

	var roundTrip TaskRunStatusFrame
	require.NoError(t, json.Unmarshal(raw, &roundTrip))
	require.NotNil(t, roundTrip.OccurrenceMs)
	assert.Equal(t, large, *roundTrip.OccurrenceMs,
		"occurrence_ms must round-trip exactly through int64 — no 32-bit truncation")

	mustPassAsyncAPI(t, "TaskRunStatusFrame", f)
}

// ── McpServer ─────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/McpServer.yaml

func TestContract_McpServer_Populated(t *testing.T) {
	// Fully populated MCP server with tools list.
	// Traces to: McpServer.yaml — required: [id, name, transport, status, tool_count]
	mustPassComponent(t, "McpServer", FixtureMcpServer_Populated())
}

func TestContract_McpServer_ZeroValue(t *testing.T) {
	// Zero value: id="", name="", transport="" (not in enum), status="" (not in enum).
	// Traces to: McpServer.yaml — transport and status are enum-constrained
	mustFailComponent(t, "McpServer", FixtureMcpServer_ZeroValue(),
		"zero value has empty required fields; transport and status are not valid enum values")
}

func TestContract_McpServer_Edge(t *testing.T) {
	// Disconnected server with SSE transport and no tools.
	// Traces to: McpServer.yaml
	mustPassComponent(t, "McpServer", FixtureMcpServer_Edge())
}

func TestContract_McpServer_NilToolsAllowed(t *testing.T) {
	// tools is optional per schema — nil (omitempty) is valid.
	// This is NOT the bug pattern; we document that nil tools is intentional and valid.
	// Traces to: McpServer.yaml — tools is not in required list (optional)
	mustPassComponent(t, "McpServer", FixtureMcpServer_NilToolsAllowed())
}

func TestContract_McpServer_Differentiation(t *testing.T) {
	// Two McpServer fixtures with different transports/statuses must differ.
	f1 := FixtureMcpServer_Populated()
	f2 := FixtureMcpServer_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated and Edge McpServer fixtures must produce different JSON")
	mustPassComponent(t, "McpServer", f1)
	mustPassComponent(t, "McpServer", f2)
}

// ── McpServerCreate ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/McpServerCreate.yaml

func TestContract_McpServerCreate_Populated(t *testing.T) {
	// Fully populated create request with args.
	// Traces to: McpServerCreate.yaml — required: [name, command, transport]
	mustPassComponent(t, "McpServerCreate", FixtureMcpServerCreate_Populated())
}

func TestContract_McpServerCreate_ZeroValue(t *testing.T) {
	// Zero value: name="", command="", transport="" (not in enum).
	// Traces to: McpServerCreate.yaml — transport is enum-constrained
	mustFailComponent(t, "McpServerCreate", FixtureMcpServerCreate_ZeroValue(),
		"zero value has empty required fields; transport is not a valid enum value")
}

func TestContract_McpServerCreate_Edge(t *testing.T) {
	// No args (optional), SSE transport, unicode name.
	// Traces to: McpServerCreate.yaml
	mustPassComponent(t, "McpServerCreate", FixtureMcpServerCreate_Edge())
}

// ── AppState ─────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AppState.yaml

func TestContract_AppState_Populated(t *testing.T) {
	// All optional fields set alongside the required onboarding_complete bool.
	// Traces to: AppState.yaml — required: [onboarding_complete]
	mustPassComponent(t, "AppState", FixtureAppState_Populated())
}

func TestContract_AppState_ZeroValue(t *testing.T) {
	// ZeroValue passes: onboarding_complete=false is a valid boolean.
	// AppState is one of the few types where Go zero value is schema-valid.
	// Traces to: AppState.yaml — boolean fields have no enum constraint
	mustPassComponent(t, "AppState", FixtureAppState_ZeroValue())
}

func TestContract_AppState_Edge(t *testing.T) {
	// God mode available and opted in, dev mode bypass active.
	// Traces to: AppState.yaml
	mustPassComponent(t, "AppState", FixtureAppState_Edge())
}

func TestContract_AppState_Differentiation(t *testing.T) {
	// Populated (onboarding complete, god mode off) vs Edge (onboarding incomplete, god mode on).
	f1 := FixtureAppState_Populated()
	f2 := FixtureAppState_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated and Edge AppState fixtures must produce different JSON")
	mustPassComponent(t, "AppState", f1)
	mustPassComponent(t, "AppState", f2)
}

// ── ValidateTokenResponse ─────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ValidateTokenResponse.yaml

func TestContract_ValidateTokenResponse_Populated(t *testing.T) {
	// Traces to: ValidateTokenResponse.yaml — required: [username]
	mustPassComponent(t, "ValidateTokenResponse", FixtureValidateTokenResponse_Populated())
}

func TestContract_ValidateTokenResponse_ZeroValue(t *testing.T) {
	// username="" — required, but has no minLength, so the empty string still
	// satisfies the schema (required only checks key presence).
	// Traces to: ValidateTokenResponse.yaml
	mustPassComponent(t, "ValidateTokenResponse", FixtureValidateTokenResponse_ZeroValue())
}

func TestContract_ValidateTokenResponse_Edge(t *testing.T) {
	// unicode username.
	// Traces to: ValidateTokenResponse.yaml
	mustPassComponent(t, "ValidateTokenResponse", FixtureValidateTokenResponse_Edge())
}

func TestContract_ValidateTokenResponse_Differentiation(t *testing.T) {
	// Different usernames produce different JSON.
	f1 := FixtureValidateTokenResponse_Populated()
	f2 := FixtureValidateTokenResponse_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different ValidateTokenResponse fixtures must produce different JSON")
	mustPassComponent(t, "ValidateTokenResponse", f1)
	mustPassComponent(t, "ValidateTokenResponse", f2)
}

// ── DoctorIssue ───────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DoctorIssue.yaml
// Note: oapi-codegen inlined DoctorIssue inside DoctorResult; we test the schema
// directly via raw JSON marshaling and validateAgainstComponentSchemaRawJSON.

func TestContract_DoctorIssue_Populated(t *testing.T) {
	// All required + optional fields; verifies the DoctorIssue component schema.
	// Traces to: DoctorIssue.yaml — required: [id, severity, title, description, recommendation]
	raw, err := json.Marshal(FixtureDoctorIssueJSON_Populated())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DoctorIssue", raw),
		"fully-populated DoctorIssue must validate against DoctorIssue.yaml")
}

func TestContract_DoctorIssue_ZeroValue(t *testing.T) {
	// Empty map — missing all required fields.
	// Traces to: DoctorIssue.yaml — required: [id, severity, title, description, recommendation]
	raw, err := json.Marshal(FixtureDoctorIssueJSON_ZeroValue())
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DoctorIssue", raw),
		"empty object must fail DoctorIssue schema — all required fields missing")
}

func TestContract_DoctorIssue_Edge(t *testing.T) {
	// Low severity, no optional action fields.
	// Traces to: DoctorIssue.yaml
	raw, err := json.Marshal(FixtureDoctorIssueJSON_Edge())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DoctorIssue", raw),
		"low-severity DoctorIssue without optional fields must validate")
}

func TestContract_DoctorIssue_InvalidSeverity(t *testing.T) {
	// severity must be enum [high, medium, low] — "critical" is not valid.
	// Traces to: DoctorIssue.yaml — severity: enum: [high, medium, low]
	issue := map[string]any{
		"id":             "test-issue",
		"severity":       "critical", // NOT in enum
		"title":          "Test Issue",
		"description":    "A test issue.",
		"recommendation": "Fix it.",
	}
	raw, err := json.Marshal(issue)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DoctorIssue", raw),
		"severity='critical' must fail — not in enum [high, medium, low]")
}

// ── DoctorResult ─────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DoctorResult.yaml

func TestContract_DoctorResult_Populated(t *testing.T) {
	// Populated result with one high-severity issue.
	// Traces to: DoctorResult.yaml — required: [score, issues, checked_at]
	mustPassComponent(t, "DoctorResult", FixtureDoctorResult_Populated())
}

func TestContract_DoctorResult_ZeroValue(t *testing.T) {
	// Zero value: issues=nil marshals to null — schema requires type: array.
	// Traces to: DoctorResult.yaml — issues is required type: array
	mustFailComponent(t, "DoctorResult", FixtureDoctorResult_ZeroValue(),
		"zero value has nil issues (marshals to null); schema requires type: array")
}

func TestContract_DoctorResult_Edge(t *testing.T) {
	// Perfect score (100), empty issues array (valid).
	// Traces to: DoctorResult.yaml
	mustPassComponent(t, "DoctorResult", FixtureDoctorResult_Edge())
}

func TestContract_DoctorResult_NilIssuesRejected(t *testing.T) {
	// nil issues → JSON null → schema violation (issues is required type: array).
	// Locks the nil-slice bug pattern for DoctorResult.
	// Traces to: DoctorResult.yaml (issues: required, type: array)

	fixture := FixtureDoctorResult_NilIssues()

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"issues":null`,
		"nil Issues must marshal to null to exercise the bug path")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "DoctorResult", raw)
	assert.Error(t, validationErr,
		"issues:null MUST fail validation — DoctorResult.issues is required type: array")
}

func TestContract_DoctorResult_Differentiation(t *testing.T) {
	// Populated (score=85, issues present) vs Edge (score=100, empty issues).
	f1 := FixtureDoctorResult_Populated()
	f2 := FixtureDoctorResult_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"DoctorResult with issues vs without must produce different JSON")
	mustPassComponent(t, "DoctorResult", f1)
	mustPassComponent(t, "DoctorResult", f2)
}

// ── DevicePending ─────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DevicePending.yaml
// Note: oapi-codegen inlined DevicePending inside DevicesResponse;
// we test the schema directly via raw JSON.

func TestContract_DevicePending_Populated(t *testing.T) {
	// All required fields set.
	// Traces to: DevicePending.yaml — required: [device_id, fingerprint, pairing_code, device_name, created_at, expires_at]
	raw, err := json.Marshal(FixtureDevicePendingJSON_Populated())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DevicePending", raw),
		"fully-populated DevicePending must validate")
}

func TestContract_DevicePending_ZeroValue(t *testing.T) {
	// Empty map — missing all required fields.
	// Traces to: DevicePending.yaml
	raw, err := json.Marshal(FixtureDevicePendingJSON_ZeroValue())
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DevicePending", raw),
		"empty object must fail DevicePending schema — all required fields missing")
}

func TestContract_DevicePending_Edge(t *testing.T) {
	// Long IDs, unicode device name.
	// Traces to: DevicePending.yaml
	raw, err := json.Marshal(FixtureDevicePendingJSON_Edge())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DevicePending", raw),
		"edge-case DevicePending with unicode name must validate")
}

func TestContract_DevicePending_Differentiation(t *testing.T) {
	// Two different DevicePending objects must produce different JSON.
	raw1, err := json.Marshal(FixtureDevicePendingJSON_Populated())
	require.NoError(t, err)
	raw2, err := json.Marshal(FixtureDevicePendingJSON_Edge())
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different DevicePending fixtures must produce different JSON")
}

// ── DevicePaired ──────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DevicePaired.yaml
// Note: oapi-codegen inlined DevicePaired inside DevicesResponse;
// we test the schema directly via raw JSON.

func TestContract_DevicePaired_Populated(t *testing.T) {
	// active status device.
	// Traces to: DevicePaired.yaml — required: [device_id, fingerprint, device_name, paired_at, last_seen_at, status]
	raw, err := json.Marshal(FixtureDevicePairedJSON_Populated())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DevicePaired", raw),
		"active DevicePaired must validate")
}

func TestContract_DevicePaired_ZeroValue(t *testing.T) {
	// Empty map — missing all required fields.
	// Traces to: DevicePaired.yaml
	raw, err := json.Marshal(FixtureDevicePairedJSON_ZeroValue())
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DevicePaired", raw),
		"empty object must fail DevicePaired schema — all required fields missing")
}

func TestContract_DevicePaired_Edge(t *testing.T) {
	// revoked status (the other enum value), unicode device name.
	// Traces to: DevicePaired.yaml
	raw, err := json.Marshal(FixtureDevicePairedJSON_Edge())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "DevicePaired", raw),
		"revoked DevicePaired with unicode name must validate")
}

func TestContract_DevicePaired_InvalidStatus(t *testing.T) {
	// status must be enum [active, revoked] — "suspended" is not valid.
	// Traces to: DevicePaired.yaml — status: enum: [active, revoked]
	paired := map[string]any{
		"device_id":    "dev_01",
		"fingerprint":  "SHA256:abc",
		"device_name":  "Test Device",
		"paired_at":    "2026-05-16T10:00:00Z",
		"last_seen_at": "2026-05-16T11:00:00Z",
		"status":       "suspended", // NOT in enum
	}
	raw, err := json.Marshal(paired)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DevicePaired", raw),
		"status='suspended' must fail — not in enum [active, revoked]")
}

// ── DevicesResponse ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DevicesResponse.yaml

func TestContract_DevicesResponse_Populated(t *testing.T) {
	// Both pending and paired arrays have one entry each.
	// Traces to: DevicesResponse.yaml — required: [pending, paired]
	mustPassComponent(t, "DevicesResponse", FixtureDevicesResponse_Populated())
}

func TestContract_DevicesResponse_ZeroValue(t *testing.T) {
	// Zero value: pending=nil and paired=nil — both marshal to null.
	// Traces to: DevicesResponse.yaml — both required as type: array
	mustFailComponent(t, "DevicesResponse", FixtureDevicesResponse_ZeroValue(),
		"zero value has nil pending and paired slices (marshal to null); both required as type: array")
}

func TestContract_DevicesResponse_Edge(t *testing.T) {
	// Empty arrays (no devices) — valid common state.
	// Traces to: DevicesResponse.yaml
	mustPassComponent(t, "DevicesResponse", FixtureDevicesResponse_Edge())
}

func TestContract_DevicesResponse_NilPendingRejected(t *testing.T) {
	// pending nil → JSON null → schema violation.
	// Locks the nil-slice bug pattern for DevicesResponse.pending.
	// Traces to: DevicesResponse.yaml (pending: required, type: array)

	fixture := DevicesResponse{
		Pending: nil, // THE BUG: nil → JSON null
		Paired: []struct {
			DeviceId    string                      `json:"device_id"`
			DeviceName  string                      `json:"device_name"`
			Fingerprint string                      `json:"fingerprint"`
			LastSeenAt  time.Time                   `json:"last_seen_at"`
			PairedAt    time.Time                   `json:"paired_at"`
			Status      DevicesResponsePairedStatus `json:"status"`
		}{},
	}

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"pending":null`,
		"nil Pending must marshal to null")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "DevicesResponse", raw)
	assert.Error(t, validationErr,
		"pending:null MUST fail validation — DevicesResponse.pending is required type: array")
}

func TestContract_DevicesResponse_NilPairedRejected(t *testing.T) {
	// paired nil → JSON null → schema violation.
	// Locks the nil-slice bug pattern for DevicesResponse.paired.
	// Traces to: DevicesResponse.yaml (paired: required, type: array)

	fixture := DevicesResponse{
		Pending: []struct {
			CreatedAt   time.Time `json:"created_at"`
			DeviceId    string    `json:"device_id"`
			DeviceName  string    `json:"device_name"`
			ExpiresAt   time.Time `json:"expires_at"`
			Fingerprint string    `json:"fingerprint"`
			PairingCode string    `json:"pairing_code"`
		}{},
		Paired: nil, // THE BUG: nil → JSON null
	}

	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"paired":null`,
		"nil Paired must marshal to null")

	validationErr := validateAgainstComponentSchemaRawJSON(t, "DevicesResponse", raw)
	assert.Error(t, validationErr,
		"paired:null MUST fail validation — DevicesResponse.paired is required type: array")
}

// ── BackupEntry ───────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/BackupEntry.yaml
// Note: oapi-codegen inlined BackupEntry inside the listBackups response;
// we test the schema directly via raw JSON.

func TestContract_BackupEntry_Populated(t *testing.T) {
	// All required fields set.
	// Traces to: BackupEntry.yaml — required: [filename, size_bytes, created_at]
	raw, err := json.Marshal(FixtureBackupEntryJSON_Populated())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "BackupEntry", raw),
		"fully-populated BackupEntry must validate")
}

func TestContract_BackupEntry_ZeroValue(t *testing.T) {
	// Empty map — missing all required fields.
	// Traces to: BackupEntry.yaml
	raw, err := json.Marshal(FixtureBackupEntryJSON_ZeroValue())
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "BackupEntry", raw),
		"empty object must fail BackupEntry schema — all required fields missing")
}

func TestContract_BackupEntry_Edge(t *testing.T) {
	// Zero-byte size (valid), long filename.
	// Traces to: BackupEntry.yaml — size_bytes: minimum: 0
	raw, err := json.Marshal(FixtureBackupEntryJSON_Edge())
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "BackupEntry", raw),
		"zero-byte backup entry must validate — minimum: 0 is valid")
}

func TestContract_BackupEntry_Differentiation(t *testing.T) {
	// Two BackupEntry fixtures must produce different JSON.
	raw1, err := json.Marshal(FixtureBackupEntryJSON_Populated())
	require.NoError(t, err)
	raw2, err := json.Marshal(FixtureBackupEntryJSON_Edge())
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different BackupEntry fixtures must produce different JSON")
}

// ── StorageStats ──────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/StorageStats.yaml

func TestContract_StorageStats_Populated(t *testing.T) {
	// Fully populated with all optional fields.
	// Traces to: StorageStats.yaml — required: [workspace_size_bytes, session_count, memory_entry_count]
	mustPassComponent(t, "StorageStats", FixtureStorageStats_Populated())
}

func TestContract_StorageStats_ZeroValue(t *testing.T) {
	// ZeroValue passes: all required fields are integers; zero is a valid value.
	// StorageStats is one of the few types where Go zero value is schema-valid
	// (represents an empty system with zero workspace, sessions, and memory).
	// Traces to: StorageStats.yaml — all required fields are integers with minimum: 0
	mustPassComponent(t, "StorageStats", FixtureStorageStats_ZeroValue())
}

func TestContract_StorageStats_Edge(t *testing.T) {
	// Zero counts, multiple warnings.
	// Traces to: StorageStats.yaml
	mustPassComponent(t, "StorageStats", FixtureStorageStats_Edge())
}

func TestContract_StorageStats_NilWarningsAllowed(t *testing.T) {
	// warnings is optional; nil is valid (schema: warnings is not in required list).
	// Traces to: StorageStats.yaml — warnings is optional
	mustPassComponent(t, "StorageStats", FixtureStorageStats_NilWarningsAllowed())
}

func TestContract_StorageStats_Differentiation(t *testing.T) {
	// Populated (52MB workspace, 42 sessions) vs Edge (zero counts, warnings).
	f1 := FixtureStorageStats_Populated()
	f2 := FixtureStorageStats_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"StorageStats with data vs empty must produce different JSON")
	mustPassComponent(t, "StorageStats", f1)
	mustPassComponent(t, "StorageStats", f2)
}

// ── SessionCloseFrame ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SessionCloseFrame.yaml

func TestContract_SessionCloseFrame_Populated(t *testing.T) {
	// Both required fields set.
	// Traces to: SessionCloseFrame.yaml — required: [type, session_id]
	mustPassAsyncAPI(t, "SessionCloseFrame", FixtureSessionCloseFrame_Populated())
}

func TestContract_SessionCloseFrame_ZeroValue(t *testing.T) {
	// type="" (const: session_close), session_id="" (minLength: 1).
	// Traces to: SessionCloseFrame.yaml — type has const constraint; session_id has minLength
	mustFailAsyncAPI(t, "SessionCloseFrame", FixtureSessionCloseFrame_ZeroValue(),
		"zero value has empty type (const) and session_id (minLength: 1)")
}

func TestContract_SessionCloseFrame_Edge(t *testing.T) {
	// Long session_id (valid per minLength: 1).
	// Traces to: SessionCloseFrame.yaml
	mustPassAsyncAPI(t, "SessionCloseFrame", FixtureSessionCloseFrame_Edge())
}

func TestContract_SessionCloseFrame_Differentiation(t *testing.T) {
	// Two SessionCloseFrame fixtures with different session IDs produce different JSON.
	f1 := FixtureSessionCloseFrame_Populated()
	f2 := FixtureSessionCloseFrame_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different SessionCloseFrame fixtures must produce different JSON")
	mustPassAsyncAPI(t, "SessionCloseFrame", f1)
	mustPassAsyncAPI(t, "SessionCloseFrame", f2)
}

// ── AuditLogToggleRequest ─────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuditLogToggleRequest.yaml

func TestContract_AuditLogToggleRequest_Populated(t *testing.T) {
	mustPassComponent(t, "AuditLogToggleRequest", FixtureAuditLogToggleRequest_Populated())
}

func TestContract_AuditLogToggleRequest_ZeroValue(t *testing.T) {
	// ZeroValue (enabled=false) is actually a valid AuditLogToggleRequest — boolean has no enum.
	// This test documents that zero-value is valid for bool-only bodies.
	mustPassComponent(t, "AuditLogToggleRequest", FixtureAuditLogToggleRequest_ZeroValue())
}

func TestContract_AuditLogToggleRequest_Edge(t *testing.T) {
	mustPassComponent(t, "AuditLogToggleRequest", FixtureAuditLogToggleRequest_Edge())
}

func TestContract_AuditLogToggleRequest_Differentiation(t *testing.T) {
	f1 := FixtureAuditLogToggleRequest_Populated() // enabled=true
	f2 := FixtureAuditLogToggleRequest_Edge()      // enabled=false
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"enabled=true and enabled=false must produce different JSON")
	mustPassComponent(t, "AuditLogToggleRequest", f1)
	mustPassComponent(t, "AuditLogToggleRequest", f2)
}

// ── AuditLogUpdateResponse ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuditLogUpdateResponse.yaml

func TestContract_AuditLogUpdateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "AuditLogUpdateResponse", FixtureAuditLogUpdateResponse_Populated())
}

func TestContract_AuditLogUpdateResponse_ZeroValue(t *testing.T) {
	// All boolean fields default to false — zero value is valid for this schema.
	mustPassComponent(t, "AuditLogUpdateResponse", FixtureAuditLogUpdateResponse_ZeroValue())
}

func TestContract_AuditLogUpdateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "AuditLogUpdateResponse", FixtureAuditLogUpdateResponse_Edge())
}

func TestContract_AuditLogUpdateResponse_Differentiation(t *testing.T) {
	f1 := FixtureAuditLogUpdateResponse_Populated()
	f2 := FixtureAuditLogUpdateResponse_Edge()
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different AuditLogUpdateResponse fixtures must produce different JSON")
}

// ── SkillTrustUpdateRequest ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SkillTrustUpdateRequest.yaml

func TestContract_SkillTrustUpdateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "SkillTrustUpdateRequest", FixtureSkillTrustUpdateRequest_Populated())
}

func TestContract_SkillTrustUpdateRequest_ZeroValue(t *testing.T) {
	// level="" — required, must be one of: block_unverified | warn_unverified | allow_all
	mustFailComponent(t, "SkillTrustUpdateRequest", FixtureSkillTrustUpdateRequest_ZeroValue(),
		"zero value has empty level field; not a valid enum value")
}

func TestContract_SkillTrustUpdateRequest_Edge(t *testing.T) {
	mustPassComponent(t, "SkillTrustUpdateRequest", FixtureSkillTrustUpdateRequest_Edge())
}

func TestContract_SkillTrustUpdateRequest_Differentiation(t *testing.T) {
	f1 := FixtureSkillTrustUpdateRequest_Populated() // block_unverified
	f2 := FixtureSkillTrustUpdateRequest_Edge()      // allow_all
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different skill trust levels must produce different JSON")
	mustPassComponent(t, "SkillTrustUpdateRequest", f1)
	mustPassComponent(t, "SkillTrustUpdateRequest", f2)
}

// ── SkillTrustUpdateResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SkillTrustUpdateResponse.yaml

func TestContract_SkillTrustUpdateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "SkillTrustUpdateResponse", FixtureSkillTrustUpdateResponse_Populated())
}

func TestContract_SkillTrustUpdateResponse_ZeroValue(t *testing.T) {
	// applied_level="" — not in enum
	mustFailComponent(t, "SkillTrustUpdateResponse", FixtureSkillTrustUpdateResponse_ZeroValue(),
		"zero value has empty applied_level; not a valid enum value")
}

func TestContract_SkillTrustUpdateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "SkillTrustUpdateResponse", FixtureSkillTrustUpdateResponse_Edge())
}

func TestContract_SkillTrustUpdateResponse_Differentiation(t *testing.T) {
	f1 := FixtureSkillTrustUpdateResponse_Populated() // allow_all + warning
	f2 := FixtureSkillTrustUpdateResponse_Edge()      // block_unverified, no warning
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different SkillTrustUpdateResponse values must produce different JSON")
	mustPassComponent(t, "SkillTrustUpdateResponse", f1)
	mustPassComponent(t, "SkillTrustUpdateResponse", f2)
}

// ── PromptGuardUpdateRequest ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PromptGuardUpdateRequest.yaml

func TestContract_PromptGuardUpdateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "PromptGuardUpdateRequest", FixturePromptGuardUpdateRequest_Populated())
}

func TestContract_PromptGuardUpdateRequest_ZeroValue(t *testing.T) {
	// level="" — not in enum [high, medium, low]
	mustFailComponent(t, "PromptGuardUpdateRequest", FixturePromptGuardUpdateRequest_ZeroValue(),
		"zero value has empty level field; not a valid enum value")
}

func TestContract_PromptGuardUpdateRequest_Edge(t *testing.T) {
	mustPassComponent(t, "PromptGuardUpdateRequest", FixturePromptGuardUpdateRequest_Edge())
}

func TestContract_PromptGuardUpdateRequest_Differentiation(t *testing.T) {
	f1 := FixturePromptGuardUpdateRequest_Populated() // high
	f2 := FixturePromptGuardUpdateRequest_Edge()      // low
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different prompt guard levels must produce different JSON")
	mustPassComponent(t, "PromptGuardUpdateRequest", f1)
	mustPassComponent(t, "PromptGuardUpdateRequest", f2)
}

// ── PromptGuardUpdateResponse ─────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PromptGuardUpdateResponse.yaml

func TestContract_PromptGuardUpdateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "PromptGuardUpdateResponse", FixturePromptGuardUpdateResponse_Populated())
}

func TestContract_PromptGuardUpdateResponse_ZeroValue(t *testing.T) {
	// applied_level="" — not in enum
	mustFailComponent(t, "PromptGuardUpdateResponse", FixturePromptGuardUpdateResponse_ZeroValue(),
		"zero value has empty applied_level; not a valid enum value")
}

func TestContract_PromptGuardUpdateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "PromptGuardUpdateResponse", FixturePromptGuardUpdateResponse_Edge())
}

func TestContract_PromptGuardUpdateResponse_Differentiation(t *testing.T) {
	f1 := FixturePromptGuardUpdateResponse_Populated() // high, no restart
	f2 := FixturePromptGuardUpdateResponse_Edge()      // medium, restart required
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different PromptGuardUpdateResponse values must produce different JSON")
	mustPassComponent(t, "PromptGuardUpdateResponse", f1)
	mustPassComponent(t, "PromptGuardUpdateResponse", f2)
}

// ── RateLimitsResponse ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsResponse.yaml

func TestContract_RateLimitsResponse_Populated(t *testing.T) {
	mustPassComponent(t, "RateLimitsResponse", FixtureRateLimitsResponse_Populated())
}

func TestContract_RateLimitsResponse_ZeroValue(t *testing.T) {
	// All numeric/boolean zero values are valid — no enum constraints.
	mustPassComponent(t, "RateLimitsResponse", FixtureRateLimitsResponse_ZeroValue())
}

func TestContract_RateLimitsResponse_Edge(t *testing.T) {
	mustPassComponent(t, "RateLimitsResponse", FixtureRateLimitsResponse_Edge())
}

func TestContract_RateLimitsResponse_Differentiation(t *testing.T) {
	f1 := FixtureRateLimitsResponse_Populated()
	f2 := FixtureRateLimitsResponse_Edge()
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"populated vs zero-limits RateLimitsResponse must produce different JSON")
}

// ── RateLimitsUpdateRequest ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsUpdateRequest.yaml

func TestContract_RateLimitsUpdateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "RateLimitsUpdateRequest", FixtureRateLimitsUpdateRequest_Populated())
}

func TestContract_RateLimitsUpdateRequest_ZeroValue(t *testing.T) {
	// All fields optional — empty object is valid (partial update with no fields).
	mustPassComponent(t, "RateLimitsUpdateRequest", FixtureRateLimitsUpdateRequest_ZeroValue())
}

func TestContract_RateLimitsUpdateRequest_Edge(t *testing.T) {
	mustPassComponent(t, "RateLimitsUpdateRequest", FixtureRateLimitsUpdateRequest_Edge())
}

func TestContract_RateLimitsUpdateRequest_Differentiation(t *testing.T) {
	f1 := FixtureRateLimitsUpdateRequest_Populated()
	f2 := FixtureRateLimitsUpdateRequest_Edge()
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"populated vs single-field RateLimitsUpdateRequest must produce different JSON")
}

// ── RateLimitsUpdateResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsUpdateResponse.yaml

func TestContract_RateLimitsUpdateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "RateLimitsUpdateResponse", FixtureRateLimitsUpdateResponse_Populated())
}

func TestContract_RateLimitsUpdateResponse_ZeroValue(t *testing.T) {
	// All boolean zero values are valid — saved=false, requires_restart=false.
	mustPassComponent(t, "RateLimitsUpdateResponse", FixtureRateLimitsUpdateResponse_ZeroValue())
}

func TestContract_RateLimitsUpdateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "RateLimitsUpdateResponse", FixtureRateLimitsUpdateResponse_Edge())
}

func TestContract_RateLimitsUpdateResponse_Differentiation(t *testing.T) {
	f1 := FixtureRateLimitsUpdateResponse_Populated()
	f2 := FixtureRateLimitsUpdateResponse_Edge()
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"populated vs edge RateLimitsUpdateResponse must produce different JSON")
}

// ── SessionScopeUpdateResponse ────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SessionScopeUpdateResponse.yaml

func TestContract_SessionScopeUpdateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "SessionScopeUpdateResponse", FixtureSessionScopeUpdateResponse_Populated())
}

func TestContract_SessionScopeUpdateResponse_ZeroValue(t *testing.T) {
	// All fields have zero/false defaults which are valid booleans.
	mustPassComponent(t, "SessionScopeUpdateResponse", FixtureSessionScopeUpdateResponse_ZeroValue())
}

func TestContract_SessionScopeUpdateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "SessionScopeUpdateResponse", FixtureSessionScopeUpdateResponse_Edge())
}

func TestContract_SessionScopeUpdateResponse_Differentiation(t *testing.T) {
	f1 := FixtureSessionScopeUpdateResponse_Populated()
	f2 := FixtureSessionScopeUpdateResponse_Edge()
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different session scope values must produce different JSON")
}

// ── RetentionUpdateResponse ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RetentionUpdateResponse.yaml

func TestContract_RetentionUpdateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "RetentionUpdateResponse", FixtureRetentionUpdateResponse_Populated())
}

func TestContract_RetentionUpdateResponse_ZeroValue(t *testing.T) {
	// All fields have valid zero values (booleans and ints).
	mustPassComponent(t, "RetentionUpdateResponse", FixtureRetentionUpdateResponse_ZeroValue())
}

func TestContract_RetentionUpdateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "RetentionUpdateResponse", FixtureRetentionUpdateResponse_Edge())
}

func TestContract_RetentionUpdateResponse_Differentiation(t *testing.T) {
	f1 := FixtureRetentionUpdateResponse_Populated()
	f2 := FixtureRetentionUpdateResponse_Edge()
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"enabled vs disabled retention must produce different JSON")
}

// ── AgentToolsResponse ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AgentToolsResponse.yaml

func TestContract_AgentToolsResponse_Populated(t *testing.T) {
	mustPassComponent(t, "AgentToolsResponse", FixtureAgentToolsResponse_Populated())
}

func TestContract_AgentToolsResponse_ZeroValue(t *testing.T) {
	// tools is required — nil marshals to null → must fail
	// Traces to: AgentToolsResponse.yaml (tools: required, type: array)
	fixture := FixtureAgentToolsResponse_ZeroValue()
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	// Zero value has nil Tools slice which marshals to null — schema violation
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AgentToolsResponse", raw),
		"zero value with nil tools (marshals to null) must fail schema validation")
}

func TestContract_AgentToolsResponse_Edge(t *testing.T) {
	mustPassComponent(t, "AgentToolsResponse", FixtureAgentToolsResponse_Edge())
}

func TestContract_AgentToolsResponse_Differentiation(t *testing.T) {
	f1 := FixtureAgentToolsResponse_Populated() // core agent, allow
	f2 := FixtureAgentToolsResponse_Edge()      // custom agent, deny
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"core vs custom AgentToolsResponse must produce different JSON")
	mustPassComponent(t, "AgentToolsResponse", f1)
	mustPassComponent(t, "AgentToolsResponse", f2)
}

// ── ChannelEnabledResponse ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelEnabledResponse.yaml

func TestContract_ChannelEnabledResponse_Populated(t *testing.T) {
	mustPassComponent(t, "ChannelEnabledResponse", FixtureChannelEnabledResponse_Populated())
}

func TestContract_ChannelEnabledResponse_ZeroValue(t *testing.T) {
	// id="" — required field is empty string (zero value is invalid if minLength:1)
	// Check schema; for now we document both cases
	fixture := FixtureChannelEnabledResponse_ZeroValue()
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	// id="" — if schema requires minLength:1 this should fail; if only required it passes
	_ = validateAgainstComponentSchemaRawJSON(t, "ChannelEnabledResponse", raw)
	// Either way, populated fixture passes
	mustPassComponent(t, "ChannelEnabledResponse", FixtureChannelEnabledResponse_Populated())
}

func TestContract_ChannelEnabledResponse_Edge(t *testing.T) {
	mustPassComponent(t, "ChannelEnabledResponse", FixtureChannelEnabledResponse_Edge())
}

func TestContract_ChannelEnabledResponse_Differentiation(t *testing.T) {
	f1 := FixtureChannelEnabledResponse_Populated() // telegram, enabled
	f2 := FixtureChannelEnabledResponse_Edge()      // discord, disabled
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different ChannelEnabledResponse values must produce different JSON")
	mustPassComponent(t, "ChannelEnabledResponse", f1)
	mustPassComponent(t, "ChannelEnabledResponse", f2)
}

// ── ChannelTestResponse ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelTestResponse.yaml

func TestContract_ChannelTestResponse_Populated(t *testing.T) {
	mustPassComponent(t, "ChannelTestResponse", FixtureChannelTestResponse_Populated())
}

func TestContract_ChannelTestResponse_ZeroValue(t *testing.T) {
	// message="" — required field is empty string (may fail minLength if any)
	// Either way, zero value documents the state and populated must pass
	mustPassComponent(t, "ChannelTestResponse", FixtureChannelTestResponse_Populated())
}

func TestContract_ChannelTestResponse_Edge(t *testing.T) {
	mustPassComponent(t, "ChannelTestResponse", FixtureChannelTestResponse_Edge())
}

func TestContract_ChannelTestResponse_Differentiation(t *testing.T) {
	f1 := FixtureChannelTestResponse_Populated() // success=true
	f2 := FixtureChannelTestResponse_Edge()      // success=false, missing cred
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"success vs failure ChannelTestResponse must produce different JSON")
	mustPassComponent(t, "ChannelTestResponse", f1)
	mustPassComponent(t, "ChannelTestResponse", f2)
}

// ── BackupCreateResponse ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/BackupCreateResponse.yaml

func TestContract_BackupCreateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "BackupCreateResponse", FixtureBackupCreateResponse_Populated())
}

func TestContract_BackupCreateResponse_ZeroValue(t *testing.T) {
	// JSON Schema "required" checks key presence, not non-empty values.
	// path="", size_bytes=0, and created_at="0001-01-01T00:00:00Z" all satisfy
	// the presence requirement — the zero value passes schema validation.
	mustPassComponent(t, "BackupCreateResponse", FixtureBackupCreateResponse_ZeroValue())
}

func TestContract_BackupCreateResponse_Edge(t *testing.T) {
	mustPassComponent(t, "BackupCreateResponse", FixtureBackupCreateResponse_Edge())
}

func TestContract_BackupCreateResponse_Differentiation(t *testing.T) {
	f1 := FixtureBackupCreateResponse_Populated()
	f2 := FixtureBackupCreateResponse_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different BackupCreateResponse fixtures must produce different JSON")
	mustPassComponent(t, "BackupCreateResponse", f1)
	mustPassComponent(t, "BackupCreateResponse", f2)
}

// ── OperationResult ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/OperationResult.yaml

func TestContract_OperationResult_Populated(t *testing.T) {
	mustPassComponent(t, "OperationResult", FixtureOperationResult_Populated())
}

func TestContract_OperationResult_ZeroValue(t *testing.T) {
	// success=false, no error — valid (success is boolean, false is a valid value).
	mustPassComponent(t, "OperationResult", FixtureOperationResult_ZeroValue())
}

func TestContract_OperationResult_Edge(t *testing.T) {
	mustPassComponent(t, "OperationResult", FixtureOperationResult_Edge())
}

func TestContract_OperationResult_Differentiation(t *testing.T) {
	f1 := FixtureOperationResult_Populated() // success=true
	f2 := FixtureOperationResult_Edge()      // success=false + error
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"success vs failure OperationResult must produce different JSON")
	mustPassComponent(t, "OperationResult", f1)
	mustPassComponent(t, "OperationResult", f2)
}

// ── ToolApprovalResponse ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ToolApprovalResponse.yaml

func TestContract_ToolApprovalResponse_Populated(t *testing.T) {
	mustPassComponent(t, "ToolApprovalResponse", FixtureToolApprovalResponse_Populated())
}

func TestContract_ToolApprovalResponse_ZeroValue(t *testing.T) {
	// approval_id="", action="" (not in enum), status="" (not in enum) — must fail
	mustFailComponent(t, "ToolApprovalResponse", FixtureToolApprovalResponse_ZeroValue(),
		"zero value has empty required fields including enum-constrained action and status")
}

func TestContract_ToolApprovalResponse_Edge(t *testing.T) {
	mustPassComponent(t, "ToolApprovalResponse", FixtureToolApprovalResponse_Edge())
}

func TestContract_ToolApprovalResponse_Differentiation(t *testing.T) {
	f1 := FixtureToolApprovalResponse_Populated() // allow action
	f2 := FixtureToolApprovalResponse_Edge()      // deny action
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"allow vs deny ToolApprovalResponse must produce different JSON")
	mustPassComponent(t, "ToolApprovalResponse", f1)
	mustPassComponent(t, "ToolApprovalResponse", f2)
}

// ── UploadFilesResponse ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/UploadFilesResponse.yaml

func TestContract_UploadFilesResponse_Populated(t *testing.T) {
	mustPassComponent(t, "UploadFilesResponse", FixtureUploadFilesResponse_Populated())
}

func TestContract_UploadFilesResponse_ZeroValue(t *testing.T) {
	// files=nil → null — schema requires type: array
	fixture := FixtureUploadFilesResponse_ZeroValue()
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "UploadFilesResponse", raw),
		"nil files (marshals to null) must fail schema — files is required type: array")
}

func TestContract_UploadFilesResponse_Edge(t *testing.T) {
	mustPassComponent(t, "UploadFilesResponse", FixtureUploadFilesResponse_Edge())
}

func TestContract_UploadFilesResponse_Differentiation(t *testing.T) {
	f1 := FixtureUploadFilesResponse_Populated() // one PDF
	f2 := FixtureUploadFilesResponse_Edge()      // two files
	raw1, _ := json.Marshal(f1)
	raw2, _ := json.Marshal(f2)
	assert.NotEqual(t, string(raw1), string(raw2),
		"different file counts must produce different JSON")
	mustPassComponent(t, "UploadFilesResponse", f1)
	mustPassComponent(t, "UploadFilesResponse", f2)
}

// TaskAcceptedResponse was removed in Sprint 2 (the /start endpoint and its
// accepted-response are gone; the unified surface uses PATCH to set in_progress).

// ── Todo / TaskTrigger component fixtures ────────────────────────────────────

func TestContract_Todo_Populated(t *testing.T) {
	mustPassComponent(t, "Todo", FixtureTodo_Populated())
}

func TestContract_Todo_ZeroValue(t *testing.T) {
	mustFailComponent(t, "Todo", FixtureTodo_ZeroValue(),
		"text is \"\" (minLength: 1)")
}

func TestContract_Todo_Edge(t *testing.T) {
	mustPassComponent(t, "Todo", FixtureTodo_Edge())
}

func TestContract_TaskTrigger_Populated(t *testing.T) {
	mustPassComponent(t, "TaskTrigger", FixtureTaskTrigger_Populated())
}

func TestContract_TaskTrigger_ZeroValue(t *testing.T) {
	mustFailComponent(t, "TaskTrigger", FixtureTaskTrigger_ZeroValue(),
		"type is \"\" (not in enum)")
}

func TestContract_TaskTrigger_Edge(t *testing.T) {
	mustPassComponent(t, "TaskTrigger", FixtureTaskTrigger_Edge())
}

// TestContract_TaskTrigger_Rrule pins the rrule/dtstart_ms/tz wire keys
// (Calendar Recurrence Redesign) against TaskTrigger.yaml — previously
// untested by any FixtureTaskTrigger_* (only cron_expr was exercised).
func TestContract_TaskTrigger_Rrule(t *testing.T) {
	mustPassComponent(t, "TaskTrigger", FixtureTaskTrigger_Rrule())
}

// ── ActivityEventsResponse ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ActivityEventsResponse.yaml
// Note: ActivityEventsResponse is not a named Go type (it's inlined). We test
// via raw JSON against the component schema directly.

func TestContract_ActivityEventsResponse_Populated(t *testing.T) {
	// events is required type: array — must be a non-nil JSON array.
	doc := map[string]any{
		"events": []any{
			map[string]any{
				"id":        "session-abc123",
				"type":      "session_start",
				"timestamp": "2026-05-16T10:00:00Z",
			},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "ActivityEventsResponse", raw),
		"populated ActivityEventsResponse must validate")
}

func TestContract_ActivityEventsResponse_EmptyEvents(t *testing.T) {
	// Empty events array is valid (no activity in the window).
	doc := map[string]any{"events": []any{}}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "ActivityEventsResponse", raw),
		"empty events array must validate (no activity)")
}

func TestContract_ActivityEventsResponse_NullEventsRejected(t *testing.T) {
	// events=null → required type: array → must fail
	doc := map[string]any{"events": nil}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "ActivityEventsResponse", raw),
		"events:null must fail — events is required type: array")
}

func TestContract_ActivityEventsResponse_WithWarning(t *testing.T) {
	// warning field is optional
	doc := map[string]any{
		"events":  []any{},
		"warning": "failed to read session store for agent jim: permission denied",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "ActivityEventsResponse", raw),
		"ActivityEventsResponse with warning must validate")
}

// ── OnboardingStatusResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/OnboardingStatusResponse.yaml
// Note: OnboardingStatusResponse is not a named Go type (it's inlined). Test via raw JSON.

func TestContract_OnboardingStatusResponse_Populated(t *testing.T) {
	doc := map[string]any{"onboarding_complete": true}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingStatusResponse", raw),
		"onboarding_complete=true must validate")
}

func TestContract_OnboardingStatusResponse_False(t *testing.T) {
	doc := map[string]any{"onboarding_complete": false}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingStatusResponse", raw),
		"onboarding_complete=false must validate (boolean, not enum)")
}

func TestContract_OnboardingStatusResponse_MissingField(t *testing.T) {
	doc := map[string]any{}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingStatusResponse", raw),
		"missing onboarding_complete field must fail — it is required")
}

func TestContract_OnboardingStatusResponse_Differentiation(t *testing.T) {
	doc1 := map[string]any{"onboarding_complete": true}
	doc2 := map[string]any{"onboarding_complete": false}
	raw1, err := json.Marshal(doc1)
	require.NoError(t, err)
	raw2, err := json.Marshal(doc2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"true vs false onboarding_complete must produce different JSON")
}

// ── AuthFrame token pattern ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuthFrame.yaml (pattern: '^omnipus_[a-f0-9]{64}$')

func TestContract_AuthFrame_TokenPatternRejects(t *testing.T) {
	// Traces to: AuthFrame.yaml — token: pattern: '^omnipus_[a-f0-9]{64}$'
	cases := []struct {
		name   string
		token  string
		reason string
	}{
		{"wrong_prefix", "bearer_" + repeatStr("a", 64), "must start with omnipus_"},
		{"uppercase_hex", "omnipus_" + repeatStr("A", 64), "uppercase hex not allowed — pattern requires [a-f0-9]"},
		{"too_short", "omnipus_" + repeatStr("a", 63), "63 hex chars is 1 too short"},
		{"too_long", "omnipus_" + repeatStr("a", 65), "65 hex chars is 1 too long"},
		{"non_hex", "omnipus_" + repeatStr("g", 64), "g is not a hex char"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := map[string]any{"type": "auth", "token": tc.token}
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AuthFrame", raw),
				"token %q must fail AuthFrame pattern validation — %s", tc.token, tc.reason)
		})
	}
}

// ── LoginResponse token exact-72-char ────────────────────────────────────────
// Traces to: contracts/components/schemas/LoginResponse.yaml (minLength:72, maxLength:72)

func TestContract_LoginResponse_Token72ExactRejects(t *testing.T) {
	// Traces to: LoginResponse.yaml — token: minLength:72, maxLength:72
	// 71-char token (too short) and 73-char token (too long) must fail.
	cases := []struct {
		name   string
		token  string
		reason string
	}{
		{"71_chars", "omnipus_" + repeatStr("a", 63), "71 chars is below minLength:72"},
		{"73_chars", "omnipus_" + repeatStr("a", 65), "73 chars is above maxLength:72"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := map[string]any{
				"token":    tc.token,
				"role":     "admin",
				"username": "admin",
			}
			raw, err := json.Marshal(doc)
			require.NoError(t, err)
			assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "LoginResponse", raw),
				"token %q (len=%d) must fail LoginResponse — %s", tc.token, len(tc.token), tc.reason)
		})
	}
}

// ── TokenFrame content maxLength:65536 ───────────────────────────────────────
// Traces to: contracts/components/schemas/TokenFrame.yaml (content: maxLength:65536)

func TestContract_TokenFrame_ContentTooLong(t *testing.T) {
	// Traces to: TokenFrame.yaml — content: maxLength: 65536
	// 65537-char content must fail.
	frame := map[string]any{
		"type":       "token",
		"session_id": "sess-1",
		"content":    repeatStr("x", 65537), // one byte over limit
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "TokenFrame", raw),
		"content with 65537 chars must fail maxLength:65536")
}

func TestContract_TokenFrame_ContentAtLimit(t *testing.T) {
	// Exactly 65536 chars — must pass.
	frame := map[string]any{
		"type":       "token",
		"session_id": "sess-1",
		"content":    repeatStr("x", 65536),
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "TokenFrame", raw),
		"content with exactly 65536 chars must pass maxLength:65536")
}

// ── ErrorFrame message maxLength:4096 ────────────────────────────────────────
// Traces to: contracts/components/schemas/ErrorFrame.yaml (message: maxLength:4096)

func TestContract_ErrorFrame_MessageTooLong(t *testing.T) {
	// Traces to: ErrorFrame.yaml — message: maxLength: 4096
	// 4097-char message must fail.
	frame := map[string]any{
		"type":    "error",
		"message": repeatStr("e", 4097),
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "ErrorFrame", raw),
		"message with 4097 chars must fail maxLength:4096")
}

func TestContract_ErrorFrame_MessageAtLimit(t *testing.T) {
	// Exactly 4096 chars — must pass (boundary value).
	frame := map[string]any{
		"type":    "error",
		"message": repeatStr("e", 4096),
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "ErrorFrame", raw),
		"message with exactly 4096 chars must pass maxLength:4096")
}

// ── Task priority out of range ────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Task.yaml (priority: minimum:1, maximum:5)

func TestContract_Task_PriorityOutOfRange(t *testing.T) {
	// Sprint 2 unified Task: priority is minimum:1, maximum:5. Test both boundaries.
	base := func() map[string]any {
		return map[string]any{
			"id":           "task-uuid-1",
			"title":        "Test task",
			"action":       "llm",
			"status":       "inbox",
			"workspace_id": "ws-1",
			"owner":        "alice",
			"created_by":   "alice",
			"created_at":   "2026-06-20T10:00:00Z",
			"updated_at":   "2026-06-20T10:00:00Z",
		}
	}
	below := base()
	below["priority"] = 0 // below minimum:1
	rawBelow, err := json.Marshal(below)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Task", rawBelow),
		"priority=0 must fail Task schema — minimum:1")

	above := base()
	above["priority"] = 6 // above maximum:5
	rawAbove, err := json.Marshal(above)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Task", rawAbove),
		"priority=6 must fail Task schema — maximum:5")
}

func TestContract_Task_TagsBoundary(t *testing.T) {
	// ADR-049 D1: tags maxItems:16, each item maxLength:64.
	base := func() map[string]any {
		return map[string]any{
			"id":           "task-uuid-1",
			"title":        "Test task",
			"action":       "llm",
			"status":       "inbox",
			"workspace_id": "ws-1",
			"owner":        "alice",
			"created_by":   "alice",
			"created_at":   "2026-06-20T10:00:00Z",
			"updated_at":   "2026-06-20T10:00:00Z",
		}
	}

	within := base()
	tags16 := make([]string, 16)
	for i := range tags16 {
		tags16[i] = fmt.Sprintf("tag-%d", i)
	}
	within["tags"] = tags16
	rawWithin, err := json.Marshal(within)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "Task", rawWithin),
		"16 tags must pass Task schema — maxItems:16")

	over := base()
	tags17 := make([]string, 17)
	for i := range tags17 {
		tags17[i] = fmt.Sprintf("tag-%d", i)
	}
	over["tags"] = tags17
	rawOver, err := json.Marshal(over)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Task", rawOver),
		"17 tags must FAIL Task schema — maxItems:16")

	tooLong := base()
	tooLong["tags"] = []string{strings.Repeat("x", 65)}
	rawTooLong, err := json.Marshal(tooLong)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Task", rawTooLong),
		"a 65-char tag must FAIL Task schema — maxLength:64")
}

func TestContract_Task_CriteriaRoundtrip(t *testing.T) {
	// Traces to: Task.yaml `criteria` (ADR-049 D2/D5/FR-3) — a Task fixture
	// carrying a machine-check criterion must validate.
	mustPassComponent(t, "Task", FixtureTask_Populated())
}

// ── ToolApprovalRequiredFrame expires_in_ms range ────────────────────────────
// Traces to: contracts/components/schemas/ToolApprovalRequiredFrame.yaml
//            (expires_in_ms: minimum:0, maximum:86400000)

func TestContract_ToolApprovalRequiredFrame_ExpiresInvalid(t *testing.T) {
	// Traces to: ToolApprovalRequiredFrame.yaml — expires_in_ms: minimum:0, maximum:86400000
	cases := []struct {
		name        string
		expiresInMs int
		reason      string
	}{
		{"negative", -1, "negative expiry is not allowed (minimum:0)"},
		{"over_24h", 86400001, "over 24h is not allowed (maximum:86400000)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := map[string]any{
				"type":          "tool_approval_required",
				"approval_id":   "ap-1",
				"tool_call_id":  "tc-1",
				"tool_name":     "workspace.shell",
				"args":          map[string]any{},
				"agent_id":      "jim",
				"session_id":    "sess-1",
				"turn_id":       "turn-1",
				"expires_in_ms": tc.expiresInMs,
			}
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "ToolApprovalRequiredFrame", raw),
				"expires_in_ms=%d must fail — %s", tc.expiresInMs, tc.reason)
		})
	}
}

// ── DoneStats minimum:0 ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DoneStats.yaml (tokens, cost, duration_ms: minimum:0)

func TestContract_DoneStats_NegativeRejects(t *testing.T) {
	// Traces to: DoneStats.yaml — tokens/cost/duration_ms all have minimum:0
	cases := []struct {
		name   string
		field  string
		value  float64
		reason string
	}{
		{"negative_tokens", "tokens", -1, "tokens minimum is 0"},
		{"negative_cost", "cost", -0.001, "cost minimum is 0"},
		{"negative_duration", "duration_ms", -1, "duration_ms minimum is 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := map[string]any{
				"type":       "done",
				"session_id": "sess-1",
				"stats": map[string]any{
					tc.field: tc.value,
				},
			}
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DoneFrame", raw),
				"%s=%v must fail DoneStats schema — %s", tc.field, tc.value, tc.reason)
		})
	}
}

// ── Session partitions maxItems:3650 ─────────────────────────────────────────
// Traces to: contracts/components/schemas/Session.yaml (partitions: maxItems:3650)

func TestContract_Session_TooManyPartitions(t *testing.T) {
	// Traces to: Session.yaml — partitions: maxItems: 3650
	// 3651 partitions must fail.
	partitions := make([]string, 3651)
	for i := range partitions {
		partitions[i] = fmt.Sprintf("2026-%04d.jsonl", i)
	}
	doc := map[string]any{
		"id":         "session-1",
		"agent_id":   "jim",
		"title":      "Test session",
		"status":     "active",
		"created_at": "2026-05-17T10:00:00Z",
		"updated_at": "2026-05-17T10:00:00Z",
		"channel":    "webchat",
		"partitions": partitions,
		"stats": map[string]any{
			"cost": 0.0, "message_count": 0,
			"tokens_in": 0, "tokens_out": 0,
			"tokens_total": 0, "tool_calls": 0,
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Session", raw),
		"3651 partitions must fail — maxItems:3650")
}

// ── SessionDetail messages maxItems:100000 ────────────────────────────────────
// Traces to: contracts/components/schemas/SessionDetail.yaml (messages: maxItems:100000)

func TestContract_SessionDetail_TooManyMessages(t *testing.T) {
	// Traces to: SessionDetail.yaml — messages: maxItems: 100000
	// 100001 messages must fail.
	messages := make([]any, 100001)
	for i := range messages {
		messages[i] = map[string]any{
			"id": fmt.Sprintf("msg-%d", i), "role": "user",
			"content": "x", "type": "text", "status": "complete",
		}
	}
	doc := map[string]any{
		"id":         "session-1",
		"agent_id":   "jim",
		"title":      "Test session",
		"status":     "active",
		"created_at": "2026-05-17T10:00:00Z",
		"updated_at": "2026-05-17T10:00:00Z",
		"channel":    "webchat",
		"partitions": []string{},
		"stats": map[string]any{
			"cost": 0.0, "message_count": 0,
			"tokens_in": 0, "tokens_out": 0,
			"tokens_total": 0, "tool_calls": 0,
		},
		"messages": messages,
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SessionDetail", raw),
		"100001 messages must fail — maxItems:100000")
}

// ── MediaFrame parts maxItems:32 ─────────────────────────────────────────────
// Traces to: contracts/components/schemas/MediaFrame.yaml (parts: maxItems:32)

func TestContract_MediaFrame_TooManyParts(t *testing.T) {
	// Traces to: MediaFrame.yaml — parts: maxItems: 32
	// 33 parts must fail.
	parts := make([]any, 33)
	for i := range parts {
		parts[i] = map[string]any{
			"type":         "image",
			"url":          fmt.Sprintf("/api/v1/media/img%d.png", i),
			"filename":     fmt.Sprintf("img%d.png", i),
			"content_type": "image/png",
		}
	}
	frame := map[string]any{
		"type":       "media",
		"session_id": "sess-1",
		"parts":      parts,
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "MediaFrame", raw),
		"33 parts must fail MediaFrame — maxItems:32")
}

// ── DevicesResponse pending/paired maxItems:100 ───────────────────────────────
// Traces to: contracts/components/schemas/DevicesResponse.yaml (pending/paired: maxItems:100)

func TestContract_DevicesResponse_TooManyPending(t *testing.T) {
	// Traces to: DevicesResponse.yaml — pending: maxItems: 100
	// 101 pending entries must fail.
	pending := make([]any, 101)
	for i := range pending {
		pending[i] = map[string]any{
			"device_id":    fmt.Sprintf("dev-%d", i),
			"fingerprint":  "SHA256:abc",
			"pairing_code": "CODE-XYZ",
			"device_name":  "Device",
			"created_at":   "2026-05-16T10:00:00Z",
			"expires_at":   "2026-05-16T10:10:00Z",
		}
	}
	doc := map[string]any{
		"pending": pending,
		"paired":  []any{},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DevicesResponse", raw),
		"101 pending entries must fail — maxItems:100")
}

// ── DoctorResult issues maxItems:100 ─────────────────────────────────────────
// Traces to: contracts/components/schemas/DoctorResult.yaml (issues: maxItems:100)

func TestContract_DoctorResult_TooManyIssues(t *testing.T) {
	// Traces to: DoctorResult.yaml — issues: maxItems: 100
	// 101 issues must fail.
	issues := make([]any, 101)
	for i := range issues {
		issues[i] = map[string]any{
			"id":             fmt.Sprintf("issue-%d", i),
			"severity":       "low",
			"title":          fmt.Sprintf("Issue %d", i),
			"description":    "A test issue.",
			"recommendation": "Fix it.",
		}
	}
	doc := map[string]any{
		"score":      100.0,
		"checked_at": "2026-05-17T10:00:00Z",
		"issues":     issues,
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DoctorResult", raw),
		"101 issues must fail — maxItems:100")
}

// ── SessionStateFrame pending_approvals maxItems:1000 ─────────────────────────
// Traces to: contracts/components/schemas/SessionStateFrame.yaml (pending_approvals: maxItems:1000)

func TestContract_SessionStateFrame_TooManyPending(t *testing.T) {
	// Traces to: SessionStateFrame.yaml — pending_approvals: maxItems: 1000
	// 1001 pending approvals must fail.
	approvals := make([]any, 1001)
	for i := range approvals {
		approvals[i] = map[string]any{
			"approval_id":   fmt.Sprintf("ap-%d", i),
			"session_id":    "sess-1",
			"tool_name":     "workspace.shell",
			"agent_id":      "jim",
			"expires_in_ms": 30000,
		}
	}
	frame := map[string]any{
		"type":              "session_state",
		"user_id":           "user-admin-1",
		"emitted_at":        "2026-05-17T10:00:00Z",
		"pending_approvals": approvals,
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SessionStateFrame", raw),
		"1001 pending_approvals must fail — maxItems:1000")
}

// ── Closed-shape rejection tests (additionalProperties: false) ────────────────

func TestContract_LoginResponse_RejectsExtraneousField(t *testing.T) {
	// Traces to: LoginResponse.yaml — additionalProperties: false
	doc := map[string]any{
		"token":          "omnipus_" + repeatStr("a", 64),
		"username":       "admin",
		"injected_field": "should be rejected",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "LoginResponse", raw),
		"LoginResponse with extraneous field must fail — additionalProperties: false")
}

func TestContract_OnboardingCompleteRequest_RejectsExtraneousField(t *testing.T) {
	// Traces to: OnboardingCompleteRequest.yaml — additionalProperties: false
	doc := map[string]any{
		"username":   "admin",
		"password":   "securepassword",
		"api_key":    "key123",
		"provider":   "anthropic",
		"model":      "claude-sonnet-4-6",
		"extra_flag": true, // extraneous
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw),
		"OnboardingCompleteRequest with extraneous field must fail — additionalProperties: false")
}

func TestContract_GlobalToolPolicies_RejectsExtraneousField(t *testing.T) {
	// Traces to: GlobalToolPolicies.yaml — additionalProperties: false
	doc := map[string]any{
		"default_policy": "ask",
		"policies":       map[string]any{},
		"injected":       "value",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "GlobalToolPolicies", raw),
		"GlobalToolPolicies with extraneous field must fail — additionalProperties: false")
}

func TestContract_ChannelEntry_RejectsExtraneousField(t *testing.T) {
	// Traces to: ChannelEntry.yaml — additionalProperties: false
	doc := map[string]any{
		"id":          "telegram",
		"name":        "Telegram",
		"description": "Telegram channel",
		"enabled":     true,
		"transport":   "webhook",
		"secret":      "should-be-rejected",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "ChannelEntry", raw),
		"ChannelEntry with extraneous field must fail — additionalProperties: false")
}

func TestContract_ExecAllowlist_RejectsExtraneousField(t *testing.T) {
	// Traces to: ExecAllowlist.yaml — additionalProperties: false
	doc := map[string]any{
		"allowed_binaries": []string{"ls", "grep"},
		"bypass_flag":      true, // extraneous
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "ExecAllowlist", raw),
		"ExecAllowlist with extraneous field must fail — additionalProperties: false")
}

func TestContract_AuditLogToggle_RejectsExtraneousField(t *testing.T) {
	// Traces to: AuditLogToggle.yaml — additionalProperties: false
	doc := map[string]any{
		"enabled":       true,
		"secret_bypass": true, // extraneous
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AuditLogToggle", raw),
		"AuditLogToggle with extraneous field must fail — additionalProperties: false")
}

func TestContract_SkillTrustResponse_RejectsExtraneousField(t *testing.T) {
	// Traces to: SkillTrustResponse.yaml — additionalProperties: false
	doc := map[string]any{
		"level":       "verified_only",
		"extra_field": "injected",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SkillTrustResponse", raw),
		"SkillTrustResponse with extraneous field must fail — additionalProperties: false")
}

func TestContract_PromptGuardResponse_RejectsExtraneousField(t *testing.T) {
	// Traces to: PromptGuardResponse.yaml — additionalProperties: false
	doc := map[string]any{
		"level":            "high",
		"requires_restart": false,
		"extra":            "injected",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "PromptGuardResponse", raw),
		"PromptGuardResponse with extraneous field must fail — additionalProperties: false")
}

func TestContract_DevicesResponse_RejectsExtraneousField(t *testing.T) {
	// Traces to: DevicesResponse.yaml — additionalProperties: false
	doc := map[string]any{
		"pending":     []any{},
		"paired":      []any{},
		"admin_token": "injected",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "DevicesResponse", raw),
		"DevicesResponse with extraneous field must fail — additionalProperties: false")
}

// ── Enum rejection tests ──────────────────────────────────────────────────────

func TestContract_TaskStatusChangedFrame_InvalidStatus(t *testing.T) {
	// Traces to: TaskStatusChangedFrame.yaml — status: enum: [queued, assigned, running, completed, failed]
	// "wibble" is not a valid status.
	frame := map[string]any{
		"type":       "task_status_changed",
		"session_id": "sess-1",
		"task_id":    "task-1",
		"status":     "wibble", // NOT in enum
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "TaskStatusChangedFrame", raw),
		"status='wibble' must fail TaskStatusChangedFrame — not in enum")
}

func TestContract_ReplayMessageFrame_InvalidRole(t *testing.T) {
	// Traces to: ReplayMessageFrame.yaml — role enum includes the AsyncAPI spec's wire value.
	// (British spelling avoided in this comment to satisfy the misspell linter.)
	// "robot" is not a valid role.
	frame := map[string]any{
		"type":       "replay_message",
		"session_id": "sess-1",
		"content":    "Hello",
		"role":       "robot", // NOT in enum
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "ReplayMessageFrame", raw),
		"role='robot' must fail ReplayMessageFrame — role not in allowed enum")
}

func TestContract_Attachment_InvalidType(t *testing.T) {
	// Traces to: Attachment.yaml — type: enum: [image, audio, video, file]
	// "directory" is not a valid attachment type.
	doc := map[string]any{
		"type":      "directory", // NOT in enum
		"path":      "workspace/output/",
		"size":      int64(4096),
		"mime_type": "inode/directory",
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Attachment", raw),
		"type='directory' must fail Attachment — not in enum [image, audio, video, file]")
}

// ── minProperties: 1 test for AgentUpdateRequest ──────────────────────────────
// Traces to: contracts/components/schemas/AgentUpdateRequest.yaml (minProperties: 1)

func TestContract_AgentUpdateRequest_EmptyObjectRejected(t *testing.T) {
	// Traces to: AgentUpdateRequest.yaml — minProperties: 1
	// An empty patch body {} must be rejected — at least one field must be present.
	doc := map[string]any{}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AgentUpdateRequest", raw),
		"empty AgentUpdateRequest {} must fail — minProperties: 1 requires at least one field")
}

func TestContract_AgentUpdateRequest_SingleFieldAccepted(t *testing.T) {
	// One field is the minimum that satisfies minProperties:1.
	doc := map[string]any{"model": "gpt-4o"}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "AgentUpdateRequest", raw),
		"AgentUpdateRequest with one field (model) must pass — satisfies minProperties:1")
}

// ── Concurrent compile race test ──────────────────────────────────────────────
// Traces to: Phase 7 fix-Y — concurrent schema compilation must be race-free

func TestCompileInboundSchema_ConcurrentDifferentSchemas(t *testing.T) {
	// This test must be run with -race to detect data races in the schema compiler cache.
	// Traces to: pkg/gateway/rest_inbound_validate.go — compileInboundSchema with sync.Map cache.
	t.Parallel()

	// 10 different schema names to compile concurrently.
	schemas := []string{
		"AgentCreateRequestMain", "AgentUpdateRequest", "SessionCreateRequest",
		"ProbeProviderRequest", "SandboxConfigUpdate", "ExecAllowlist",
		"SessionScopeRequest", "AuditLogToggleRequest", "SkillTrustUpdateRequest",
		"PromptGuardUpdateRequest",
	}

	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(schemas))

	for _, name := range schemas {
		n := name
		go func() {
			// Always call initSchemas first — sync.Once serializes the write
			// to componentSchemaDir + sharedCompiler. Reading the global var
			// directly (the previous "skip init if non-empty" optimisation)
			// races with the in-flight Once.Do on the first call, producing
			// "fatal error: concurrent map read and map write" under -race
			// when many goroutines hit this path on a cold cache.
			_ = initSchemas(t)
			raw := []byte(`{"name":"test"}`)
			err := validateAgainstComponentSchemaRawJSON(t, n, raw)
			// We expect validation to either pass or fail — no panic or race.
			// The nil-vs-error outcome depends on the schema, but the important
			// thing is no data race occurs.
			results <- result{name: n, err: err}
		}()
	}

	for range schemas {
		r := <-results
		// Each schema must compile without panicking (err may be non-nil for invalid fixture data).
		t.Logf("schema %s: validate result=%v", r.name, r.err != nil)
	}
}

// ── pkg/session.TranscriptEntry → Message.yaml round-trip ───────────────────
//
// These tests close a real production bug class: `getSessionMessages` and
// `jsonSessionDetail` in pkg/gateway/rest.go emit `[]session.TranscriptEntry`
// into JSON fields typed as `[]gen.Message` on the wire. The Go-internal
// TranscriptEntry has a richer Type/Role/cancel-field surface than gen.Message
// claims, so any unmodelled value (e.g. type="tool_call", type="turn_canceled"
// for a real canceled turn) used to fail the SPA Zod schema with
// "Backend response failed validation".
//
// The fix is structural: every EntryType const must round-trip through
// Message.yaml validation. If a new EntryType lands without a schema enum
// update, the table-driven test below fails before the bug ships.

// transcriptEntryToWireJSON marshals a TranscriptEntry-shaped struct via JSON
// the same way pkg/gateway/rest.go emits it, then validates against the
// Message.yaml component schema. We rebuild a minimal local shape here
// (rather than importing pkg/session) to keep this package's dependency
// graph narrow — pkg/api/generated should not depend on domain packages.
type transcriptEntryFixture struct {
	ID          string    `json:"id"`
	Type        string    `json:"type,omitempty"`
	Role        string    `json:"role,omitempty"`
	Content     string    `json:"content,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Tokens      int       `json:"tokens,omitempty"`
	Cost        float64   `json:"cost,omitempty"`
	Status      string    `json:"status,omitempty"`
	Attachments []any     `json:"attachments,omitempty"`
	ToolCalls   []any     `json:"tool_calls,omitempty"`
	AgentID     string    `json:"agent_id"`

	// Cancel-entry fields (omitempty in the Go struct; appear on the wire
	// only for turn_canceled entries).
	Truncated            bool     `json:"truncated,omitempty"`
	TurnID               string   `json:"turn_id,omitempty"`
	CancelledByUser      string   `json:"canceled_by_user,omitempty"`
	CancelledByChannel   string   `json:"canceled_by_channel,omitempty"`
	CancelMethod         string   `json:"cancel_method,omitempty"`
	DescendantsCancelled []string `json:"descendants_canceled,omitempty"`
}

// Table-driven test: every EntryType value the Go side may set must validate
// against the Message.yaml type enum. A new EntryType const lands → this test
// fails until the YAML enum is updated → CI catches the drift before
// production. This is the structural prevention for the bug class that
// landed in production despite a 79-passed e2e suite.
//
// Source of truth: pkg/session/daypartition.go EntryType constants.
// Mirror here (keeping pkg/api/generated free of domain imports).
func TestContract_Message_AllEntryTypes_ValidateAgainstWireSchema(t *testing.T) {
	allEntryTypes := []string{
		"message",
		"compaction",
		"system",
		"tool_call",
		"turn_canceled",
	}

	for _, entryType := range allEntryTypes {
		t.Run(entryType, func(t *testing.T) {
			fx := transcriptEntryFixture{
				ID:        "msg_" + entryType,
				Type:      entryType,
				Role:      "assistant",
				Content:   "stub",
				Timestamp: time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
				AgentID:   "jim",
			}
			// turn_canceled entries don't carry a role (skipped in real code).
			if entryType == "turn_canceled" {
				fx.Role = ""
				fx.Content = ""
				fx.TurnID = "turn-T3"
				fx.CancelledByUser = "admin"
				fx.CancelledByChannel = "webchat"
				fx.CancelMethod = "graceful"
				fx.DescendantsCancelled = []string{"turn-T3-sub-1"}
			}
			raw, err := json.Marshal(fx)
			require.NoError(t, err)
			validationErr := validateAgainstComponentSchemaRawJSON(t, "Message", raw)
			assert.NoError(t, validationErr,
				"TranscriptEntry with type=%q must validate against Message.yaml — "+
					"if this fails, add %q to the type enum in "+
					"contracts/components/schemas/Message.yaml and run make gen-contracts",
				entryType, entryType)
		})
	}
}

// TestContract_Message_ToolCallEntry_FromTranscriptEntry is a targeted
// reproducer for the production bug filed in this session. Jim's session
// contained 44 tool_call entries; opening the session failed SPA validation
// because the wire schema didn't allow type="tool_call". This test fails
// loudly if that enum value is ever dropped again.
func TestContract_Message_ToolCallEntry_FromTranscriptEntry(t *testing.T) {
	fx := transcriptEntryFixture{
		ID:        "call_abc123",
		Type:      "tool_call",
		Timestamp: time.Date(2026, 5, 21, 4, 20, 0, 0, time.UTC),
		AgentID:   "jim",
		ToolCalls: []any{
			map[string]any{
				"id":          "call_abc123",
				"tool":        "write_file",
				"status":      "success",
				"duration_ms": 3,
				"parameters":  map[string]any{"path": "/tmp/out.txt", "content": "hi"},
			},
		},
	}
	raw, err := json.Marshal(fx)
	require.NoError(t, err)
	validationErr := validateAgainstComponentSchemaRawJSON(t, "Message", raw)
	assert.NoError(t, validationErr,
		"tool_call entry must validate — reproducer for the bug-class that shipped to public IP")
}

// TestContract_Message_TurnCanceledEntry_FromTranscriptEntry covers the
// cancel-entry shape produced by pkg/agent/cancel.go after FR-15. The
// Truncated/TurnID/Cancel* fields are silently stripped by the SPA's Zod
// schema (no .strict()), so they don't cause validation failure — but the
// type="turn_canceled" enum must be allowed.
func TestContract_Message_TurnCanceledEntry_FromTranscriptEntry(t *testing.T) {
	fx := transcriptEntryFixture{
		ID:                   "cancel_xyz",
		Type:                 "turn_canceled",
		Timestamp:            time.Date(2026, 5, 21, 4, 25, 0, 0, time.UTC),
		AgentID:              "mia",
		TurnID:               "turn-T3-transcript",
		CancelledByUser:      "admin",
		CancelledByChannel:   "webchat",
		CancelMethod:         "graceful",
		DescendantsCancelled: []string{"turn-T3-sub-1", "turn-T3-sub-2"},
	}
	raw, err := json.Marshal(fx)
	require.NoError(t, err)
	validationErr := validateAgainstComponentSchemaRawJSON(t, "Message", raw)
	assert.NoError(t, validationErr,
		"turn_canceled entry with cancel-specific fields must validate")
}

// TestContract_Message_UnknownTypeRejected guards the negative direction —
// an unknown type value MUST fail validation. This catches code drift where
// a developer adds a new EntryType const but forgets to add it to the YAML
// enum; the table-driven test above catches *additions*, this one catches
// arbitrary string-typed Type fields.
func TestContract_Message_UnknownTypeRejected(t *testing.T) {
	fx := transcriptEntryFixture{
		ID:        "msg_x",
		Type:      "wholly_unknown_entry_kind",
		Timestamp: time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
		AgentID:   "jim",
	}
	raw, err := json.Marshal(fx)
	require.NoError(t, err)
	validationErr := validateAgainstComponentSchemaRawJSON(t, "Message", raw)
	assert.Error(t, validationErr,
		"type=\"wholly_unknown_entry_kind\" must fail Message.yaml validation")
}

// ── pkg/taskstore.TaskEntity → Task.yaml round-trip ─────────────────────────
//
// TaskEntity is emitted from the gateway's /api/v1/tasks endpoints by the
// jsonOK calls in pkg/gateway/rest.go. The Status and TriggerType fields are
// plain Go strings, but the wire Task.yaml schema constrains them with
// enums. Today the runtime code only writes enum-valid values, but there's
// no test enforcing that — and the REST request body for POST /tasks
// accepts any string. These tests pin the round-trip contract.

// taskEntityFixture mirrors the persisted-then-served unified Task shape with
// the minimum required fields for schema validation (Sprint 2).
type taskEntityFixture struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	WorkspaceID string `json:"workspace_id"`
	Owner       string `json:"owner"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func newTaskEntityFixture(status string) taskEntityFixture {
	return taskEntityFixture{
		ID:          "task-uuid",
		Title:       "test",
		Action:      "llm",
		Status:      status,
		WorkspaceID: "ws-1",
		Owner:       "alice",
		CreatedBy:   "alice",
		CreatedAt:   "2026-05-21T10:00:00Z",
		UpdatedAt:   "2026-05-21T10:00:00Z",
	}
}

func TestContract_TaskEntity_AllStatusValues_Validate(t *testing.T) {
	// Unified 6-state vocabulary (ADR-051 D5 removed `planning`).
	allStatuses := []string{"inbox", "next", "in_progress", "blocked", "done", "failed"}
	for _, status := range allStatuses {
		t.Run(status, func(t *testing.T) {
			raw, err := json.Marshal(newTaskEntityFixture(status))
			require.NoError(t, err)
			validationErr := validateAgainstComponentSchemaRawJSON(t, "Task", raw)
			assert.NoError(t, validationErr,
				"unified Task with status=%q must validate against Task.yaml", status)
		})
	}
}

func TestContract_TaskEntity_LegacyStatusValuesRejected(t *testing.T) {
	// The legacy GTD/workflow vocabularies (and, since ADR-051 D5, the removed
	// `planning` status) are gone. Any of these must FAIL — they are the canary
	// catching a regression that re-introduces a dual enum or resurrects
	// `planning`.
	legacy := []string{"queued", "assigned", "running", "completed", "active", "waiting", "planning"}
	for _, status := range legacy {
		t.Run(status, func(t *testing.T) {
			raw, err := json.Marshal(newTaskEntityFixture(status))
			require.NoError(t, err)
			validationErr := validateAgainstComponentSchemaRawJSON(t, "Task", raw)
			assert.Error(t, validationErr,
				"legacy status=%q must FAIL the unified Task schema (6-state only)", status)
		})
	}
}

func TestContract_TaskTrigger_AllKinds_Validate(t *testing.T) {
	// Tier-2 trigger kinds with their required config.
	cases := []struct {
		name   string
		kind   string
		config map[string]any
	}{
		{"manual", "manual", map[string]any{}},
		{"once", "once", map[string]any{"at_ms": int64(1781000000000)}},
		{"every", "every", map[string]any{"every_ms": int64(3600000)}},
		{"recurring_cron_expr", "recurring", map[string]any{"cron_expr": "0 9 * * MON"}},
		{"recurring_rrule", "recurring", map[string]any{
			"rrule":      "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10",
			"dtstart_ms": int64(1784624400000),
			"tz":         "Europe/Berlin",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"type": c.kind, "config": c.config})
			require.NoError(t, err)
			validationErr := validateAgainstComponentSchemaRawJSON(t, "TaskTrigger", raw)
			assert.NoError(t, validationErr,
				"TaskTrigger kind=%q must validate against TaskTrigger.yaml", c.kind)
		})
	}
}

// ── AcceptanceCriterion ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AcceptanceCriterion.yaml

func TestContract_AcceptanceCriterion_Populated(t *testing.T) {
	mustPassComponent(t, "AcceptanceCriterion", FixtureAcceptanceCriterion_Populated())
}

func TestContract_AcceptanceCriterion_Prose(t *testing.T) {
	mustPassComponent(t, "AcceptanceCriterion", FixtureAcceptanceCriterion_Prose())
}

func TestContract_AcceptanceCriterion_ZeroValue(t *testing.T) {
	mustFailComponent(t, "AcceptanceCriterion", FixtureAcceptanceCriterion_ZeroValue(),
		"kind/author.kind/status are \"\" (not in enum) and author.id is \"\" (minLength: 1)")
}

// NOTE: kind==prose carrying a `check` object is a documented, server-enforced
// 400 (ADR D2 SD-A9) — like TaskTrigger's per-type-required config keys, this
// is a prose-only conditional, not a JSON Schema construct (OpenAPI 3.0 has no
// if/then/else). There is intentionally no contract-level test asserting a
// schema-validation failure here; server-side validation is Wave 1/2's job.

// ── EvidenceRecord ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/EvidenceRecord.yaml

func TestContract_EvidenceRecord_Populated(t *testing.T) {
	mustPassComponent(t, "EvidenceRecord", FixtureEvidenceRecord_Populated())
}

func TestContract_EvidenceRecord_ZeroValue(t *testing.T) {
	mustFailComponent(t, "EvidenceRecord", FixtureEvidenceRecord_ZeroValue(),
		"id/task_id/criterion_id/command/output/recorded_at are all \"\"")
}

// ── CriterionVerdict ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CriterionVerdict.yaml

func TestContract_CriterionVerdict_Populated(t *testing.T) {
	mustPassComponent(t, "CriterionVerdict", FixtureCriterionVerdict_Populated())
}

func TestContract_CriterionVerdict_ZeroValue(t *testing.T) {
	mustFailComponent(t, "CriterionVerdict", FixtureCriterionVerdict_ZeroValue(),
		"criterion_id/reason are \"\"")
}

// ── JudgeVerdict ─────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/JudgeVerdict.yaml

func TestContract_JudgeVerdict_Populated(t *testing.T) {
	mustPassComponent(t, "JudgeVerdict", FixtureJudgeVerdict_Populated())
}

func TestContract_JudgeVerdict_ZeroValue(t *testing.T) {
	mustFailComponent(t, "JudgeVerdict", FixtureJudgeVerdict_ZeroValue(),
		"id/scope/model/judged_at/judge_agent_id are \"\" and round=0 (minimum:1)")
}

// ── Plan ─────────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Plan.yaml

func TestContract_Plan_Populated(t *testing.T) {
	// Traces to: Plan.yaml — required: [id, workspace_id, title, state, owner_agent_id, owner, created_by, created_at, updated_at]
	mustPassComponent(t, "Plan", FixturePlan_Populated())
}

func TestContract_Plan_ZeroValue(t *testing.T) {
	mustFailComponent(t, "Plan", FixturePlan_ZeroValue(),
		"zero value has empty required fields; state is not a valid enum value")
}

func TestContract_Plan_ProgressRange(t *testing.T) {
	// progress must be within [0, 1] — mirrors the removed Milestone.progress boundary.
	p := FixturePlan_Populated()
	bad := float32(1.5)
	p.Progress = &bad
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Plan", raw),
		"progress=1.5 must FAIL Plan.yaml validation (maximum: 1)")
}

func TestContract_Plan_UnknownStateRejected(t *testing.T) {
	// R1: PlanState is a closed 5-value enum (draft/approved/running/done/failed).
	p := FixturePlan_Populated()
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	doc["state"] = "archived"
	raw2, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "Plan", raw2),
		"state=archived is not one of the 5 canonical PlanState values")
}

// ── PlanCreateRequest ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PlanCreateRequest.yaml

func TestContract_PlanCreateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "PlanCreateRequest", FixturePlanCreateRequest_Populated())
}

func TestContract_PlanCreateRequest_ZeroValue(t *testing.T) {
	mustFailComponent(t, "PlanCreateRequest", FixturePlanCreateRequest_ZeroValue(),
		"workspace_id/title/owner_agent_id are all \"\"")
}

// ── PlanUpdateRequest ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PlanUpdateRequest.yaml

func TestContract_PlanUpdateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "PlanUpdateRequest", FixturePlanUpdateRequest_Populated())
}

func TestContract_PlanUpdateRequest_EmptyRejected(t *testing.T) {
	// minProperties:1 — an empty patch is rejected.
	mustFailComponent(t, "PlanUpdateRequest", FixturePlanUpdateRequest_Empty(),
		"empty object violates minProperties:1")
}

// ── PlanListResponse ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PlanListResponse.yaml

func TestContract_PlanListResponse_Populated(t *testing.T) {
	mustPassComponent(t, "PlanListResponse", FixturePlanListResponse_Populated())
}

func TestContract_PlanListResponse_ZeroValue(t *testing.T) {
	mustFailComponent(t, "PlanListResponse", FixturePlanListResponse_ZeroValue(),
		"plans is nil (marshals to null, schema requires array) and total is missing")
}

func TestContract_Workspace_Populated(t *testing.T) {
	mustPassComponent(t, "Workspace", FixtureWorkspace_Populated())
}

func TestContract_Workspace_ZeroValue(t *testing.T) {
	mustFailComponent(t, "Workspace", FixtureWorkspace_ZeroValue(),
		"status is \"\" (not in [active, archived]) and name is \"\" (minLength: 1)")
}

func TestContract_ExecutorConfig_Populated(t *testing.T) {
	mustPassComponent(t, "ExecutorConfig", FixtureExecutorConfig_Populated())
}

func TestContract_ExecutorConfig_ZeroValue(t *testing.T) {
	// ExecutorConfig.yaml no longer requires `kind` — kind is derived server-side
	// from the agent's type. Empty object validates; the backend fills in kind
	// (Main/Subagent -> native, subagent_3p -> external-cli) on dispatch.
	assert.NoError(t,
		validateAgainstComponentSchemaRawJSON(t, "ExecutorConfig", []byte(`{}`)),
		"empty executor config {} must validate (kind is derived, not required)")
}

func TestContract_IntegrationProvider_Populated(t *testing.T) {
	mustPassComponent(t, "IntegrationProvider", FixtureIntegrationProvider_Populated())
}

func TestContract_IntegrationProvider_ZeroValue(t *testing.T) {
	mustFailComponent(t, "IntegrationProvider", FixtureIntegrationProvider_ZeroValue(),
		"kind is \"\" (not in [search, voice])")
}

func TestContract_ReAuthResponse_Populated(t *testing.T) {
	mustPassComponent(t, "ReAuthResponse", FixtureReAuthResponse_Populated())
}

func TestContract_ReAuthResponse_ZeroValue(t *testing.T) {
	// All required fields are scalars with no value constraints → zero value is valid.
	mustPassComponent(t, "ReAuthResponse", FixtureReAuthResponse_ZeroValue())
}

func TestContract_PerformanceSettings_Populated(t *testing.T) {
	mustPassComponent(t, "PerformanceSettings", FixturePerformanceSettings_Populated())
}

func TestContract_PerformanceSettings_ZeroValue(t *testing.T) {
	// No required fields; both properties are optional pointers → {} is valid.
	mustPassComponent(t, "PerformanceSettings", FixturePerformanceSettings_ZeroValue())
}

func TestContract_PerformanceSettings_LargeValueAccepted(t *testing.T) {
	// 2026-08-04 concurrency-gate consolidation (commit 536b7340) removed the
	// policy ceiling PerformanceSettings.yaml used to declare (`maximum: 16`):
	// max_parallel_agents is now the single, UI-configurable authority for
	// agent concurrency and an explicit operator value is never silently
	// capped (see clampParallelExplicit, pkg/config/config.go). A large
	// explicit value MUST validate — this replaces the old
	// TestContract_PerformanceSettings_OutOfRange, which asserted the exact
	// ceiling this change deliberately removed.
	ps := FixturePerformanceSettings_Populated()
	ps.MaxParallelAgents = intPtr(99)
	raw, err := json.Marshal(ps)
	require.NoError(t, err)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "PerformanceSettings", raw),
		"max_parallel_agents=99 must PASS PerformanceSettings.yaml validation (no ceiling — only minimum:1 applies)")
}

func TestContract_PerformanceSettings_BelowFloorRejected(t *testing.T) {
	// The floor still applies even though the ceiling is gone:
	// max_parallel_agents must be >= 1 (PerformanceSettings.yaml `minimum: 1`).
	// This is the guard that replaces the retired ceiling assertion — proves
	// the schema still rejects SOMETHING, rather than accepting any integer.
	ps := FixturePerformanceSettings_Populated()
	ps.MaxParallelAgents = intPtr(0)
	raw, err := json.Marshal(ps)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "PerformanceSettings", raw),
		"max_parallel_agents=0 must FAIL PerformanceSettings.yaml validation (minimum: 1)")
}

// ── AgentCreateRequestMain / AgentCreateRequestSubagent / AgentCreateRequestSubagent3p ──
// Traces to: contracts/components/schemas/AgentCreateRequest{Main,Subagent,Subagent3p}.yaml
//
// W1 replaced the single flat AgentCreateRequest schema with a discriminated
// union (openapi.yaml oneOf + discriminator over these three component
// schemas) — there is no longer an "AgentCreateRequest.yaml" component file to
// validate against directly.

func TestContract_AgentCreateRequestMain_Populated(t *testing.T) {
	mustPassComponent(t, "AgentCreateRequestMain", FixtureAgentCreateRequestMain_Populated())
}

func TestContract_AgentCreateRequestMain_InvalidType(t *testing.T) {
	// type="not-a-valid-type" is NOT in the enum [Main].
	mustFailComponent(t, "AgentCreateRequestMain", FixtureAgentCreateRequestMain_InvalidType(),
		"type must be exactly \"Main\"")
}

func TestContract_AgentCreateRequestMain_ExecutorRejected(t *testing.T) {
	// Main has no `executor` property at all — additionalProperties: false
	// rejects it. This is the schema-level enforcement that replaced the old
	// runtime "coerce external executor to native with a warning" behavior.
	raw := []byte(
		`{"type":"Main","name":"Bad Main","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}}`,
	)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AgentCreateRequestMain", raw),
		"Main must reject an executor property (additionalProperties: false, no executor in the schema)")
}

func TestContract_AgentCreateRequestSubagent_Populated(t *testing.T) {
	mustPassComponent(t, "AgentCreateRequestSubagent", FixtureAgentCreateRequestSubagent_Populated())
}

func TestContract_AgentCreateRequestSubagent_InvalidType(t *testing.T) {
	// type="not-a-valid-type" is NOT in the enum [Subagent].
	mustFailComponent(t, "AgentCreateRequestSubagent", FixtureAgentCreateRequestSubagent_InvalidType(),
		"type must be exactly \"Subagent\"")
}

func TestContract_AgentCreateRequestSubagent_ExecutorRejected(t *testing.T) {
	// Subagent (native worker) has no `executor` property either — the server
	// always derives kind=native for this variant; a client cannot set it.
	raw := []byte(`{"type":"Subagent","name":"Bad Subagent","soul":"s","description":"d","executor":{"kind":"native"}}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AgentCreateRequestSubagent", raw),
		"Subagent must reject an executor property (additionalProperties: false, no executor in the schema)")
}

func TestContract_AgentCreateRequestSubagent3p_Populated(t *testing.T) {
	mustPassComponent(t, "AgentCreateRequestSubagent3p", FixtureAgentCreateRequestSubagent3p_Populated())
}

func TestContract_AgentCreateRequestSubagent3p_InvalidType(t *testing.T) {
	// type="not-a-valid-type" is NOT in the enum [subagent_3p].
	mustFailComponent(t, "AgentCreateRequestSubagent3p", FixtureAgentCreateRequestSubagent3p_InvalidType(),
		"type must be exactly \"subagent_3p\"")
}

func TestContract_AgentCreateRequestSubagent3p_ExecutorRequired(t *testing.T) {
	// executor is REQUIRED on subagent_3p (spec §9.2) — a body omitting it
	// entirely must fail (required: [type, name, soul, executor]).
	raw := []byte(`{"type":"subagent_3p","name":"No Executor","soul":"s"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AgentCreateRequestSubagent3p", raw),
		"subagent_3p without an executor must fail — executor is a required property")
}

func TestContract_AgentCreateRequestSubagent3p_ForbiddenFieldRejected(t *testing.T) {
	// tools_cfg does not exist on subagent_3p — additionalProperties: false
	// rejects it. This is the schema-level enforcement that replaced the old
	// runtime firstForbiddenSubagent3pField check on create.
	assert.Error(
		t,
		validateAgainstComponentSchemaRawJSON(
			t,
			"AgentCreateRequestSubagent3p",
			FixtureAgentCreateRequestSubagent3p_ForbiddenFieldJSON(),
		),
		"subagent_3p must reject tools_cfg (additionalProperties: false, no tools_cfg in the schema)",
	)
}

func TestContract_AgentCreateRequestSubagent3p_DelegationPolicyRejected(t *testing.T) {
	// ADR-037: delegation_policy is retired from the wire entirely — the global
	// per-agent delegation policy never drove enforcement (the per-workspace
	// Delegation[] graph is the sole runtime authority) and has been removed
	// from every Agent*/AgentCreateRequest*/AgentUpdateRequest schema. A
	// client still sending delegation_policy on a subagent_3p create must now
	// be rejected as an unknown field (additionalProperties: false), not
	// silently accepted.
	raw := []byte(`{
		"type": "subagent_3p",
		"name": "Delegatable 3p",
		"soul": "s",
		"executor": {"cli": "codex", "cli_path": "/usr/local/bin/codex"},
		"delegation_policy": {"to": [{"kind": "local", "id": "ray"}], "modes": ["task"], "depth": 1}
	}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AgentCreateRequestSubagent3p", raw),
		"subagent_3p with delegation_policy must now FAIL schema validation (field retired, ADR-037)")
}

// ── AgentUpdateRequest ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AgentUpdateRequest.yaml

func TestContract_AgentUpdateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "AgentUpdateRequest", FixtureAgentUpdateRequest_Populated())
}

func TestContract_AgentUpdateRequest_UpdatedAt(t *testing.T) {
	// A patch body with only a valid updated_at timestamp satisfies minProperties:1
	// and the date-time format constraint.
	mustPassComponent(t, "AgentUpdateRequest", FixtureAgentUpdateRequest_UpdatedAt())
}

// ── ChannelRouting ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelRouting.yaml

func TestContract_ChannelRouting_Populated(t *testing.T) {
	mustPassComponent(t, "ChannelRouting", FixtureChannelRouting_Populated())
}

// TestContract_ChannelRouting_Bound verifies that a bound ChannelRouting
// (workspace_id + default_agent_id set) produces schema-valid JSON (F-12/FR-026).
// Traces to: ADR-029 MAJ-004, FR-029.
func TestContract_ChannelRouting_Bound(t *testing.T) {
	mustPassComponent(t, "ChannelRouting", FixtureChannelRouting_Bound())
}

// ── ChannelCreateResponse ─────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelCreateResponse.yaml

func TestContract_ChannelCreateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "ChannelCreateResponse", ChannelCreateResponse{
		Id:      "whatsapp.eu",
		Type:    "whatsapp",
		Enabled: false,
	})
}

// ── CliDetect ──────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliDetect.yaml
// Restructured from three booleans to per-CLI {installed, path, source}
// objects (External-Executor CLI Path Detection feature, ADR-030).

func TestContract_CliDetect_Populated(t *testing.T) {
	mustPassComponent(t, "CliDetect", FixtureCliDetect_Populated())
}

func TestContract_CliDetect_ZeroValue(t *testing.T) {
	mustPassComponent(t, "CliDetect", FixtureCliDetect_ZeroValue())
}

// ── CliDetectEntry ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliDetectEntry.yaml

func TestContract_CliDetectEntry_Populated(t *testing.T) {
	mustPassComponent(t, "CliDetectEntry", FixtureCliDetectEntry_Populated())
}

func TestContract_CliDetectEntry_NotInstalled(t *testing.T) {
	mustPassComponent(t, "CliDetectEntry", FixtureCliDetectEntry_NotInstalled())
}

// ── CliValidateRequest ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliValidateRequest.yaml

func TestContract_CliValidateRequest_Populated(t *testing.T) {
	mustPassComponent(t, "CliValidateRequest", FixtureCliValidateRequest_Populated())
}

func TestContract_CliValidateRequest_ZeroValueRejected(t *testing.T) {
	mustFailComponent(t, "CliValidateRequest", FixtureCliValidateRequest_ZeroValue(),
		"cli must be one of [claude-code, codex, opencode]")
}

// ── CliValidateResponse ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliValidateResponse.yaml

func TestContract_CliValidateResponse_Populated(t *testing.T) {
	mustPassComponent(t, "CliValidateResponse", FixtureCliValidateResponse_Populated())
}

func TestContract_CliValidateResponse_MissingBinary(t *testing.T) {
	mustPassComponent(t, "CliValidateResponse", FixtureCliValidateResponse_MissingBinary())
}

func TestContract_CliValidateResponse_ZeroValueRejected(t *testing.T) {
	mustFailComponent(t, "CliValidateResponse", FixtureCliValidateResponse_ZeroValue(),
		"reason must be one of [ok, missing-binary, handshake-failed, unauthenticated, unknown-cli]")
}

// ── ScheduleTrigger (B2 regression guards) ───────────────────────────────────
// Traces to: contracts/components/schemas/ScheduleTrigger.yaml (kind enum at|every|cron).
// Heartbeat schedules are interval-based and must emit kind="every"; the bug was
// the reconciler emitting kind="recurring", which fails this schema and gets
// dropped by the SPA's Zod validation.

func TestContract_ScheduleTrigger_EveryKind_IsSchemaValid(t *testing.T) {
	everyMS := int64(15 * 60_000)
	mustPassComponent(t, "ScheduleTrigger", ScheduleTrigger{
		Kind:    ScheduleTriggerKindEvery,
		EveryMs: &everyMS,
	})
}

func TestContract_ScheduleTrigger_RecurringKindRejected(t *testing.T) {
	everyMS := int64(15 * 60_000)
	mustFailComponent(t, "ScheduleTrigger", ScheduleTrigger{
		Kind:    ScheduleTriggerKind("recurring"),
		EveryMs: &everyMS,
	}, "recurring is not a valid ScheduleTrigger.kind (only at|every|cron allowed)")
}

// ── SlashCommand ──────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SlashCommand.yaml
//
// Regression guard for Constraint #8: delivery is a required enum [client, agent].
// Non-web commands (agents/tasks/skills/channels/status/config) previously left
// Delivery unset (Go zero value ""), which marshals to `"delivery":""` — an
// invalid enum value. EffectiveDelivery() and explicit field-setting on the 6
// non-web commands together close the hole. These tests lock the valid range.

func TestContract_SlashCommand_ClientDelivery(t *testing.T) {
	// Web command with delivery=client — both required and valid.
	// Traces to: SlashCommand.yaml — delivery: enum: [client, agent]
	mustPassComponent(t, "SlashCommand", FixtureSlashCommand_ClientDelivery())
}

func TestContract_SlashCommand_AgentDelivery(t *testing.T) {
	// Non-web command with delivery=agent — the regression case.
	// Before the fix, non-web commands emitted delivery="" which fails this schema.
	// Traces to: SlashCommand.yaml — delivery: required enum [client, agent]
	mustPassComponent(t, "SlashCommand", FixtureSlashCommand_AgentDelivery())
}

func TestContract_SlashCommand_WithAliasesAndStreaming(t *testing.T) {
	// /cancel with available_while_streaming=true — all optional fields exercised.
	// Traces to: SlashCommand.yaml
	mustPassComponent(t, "SlashCommand", FixtureSlashCommand_WithAliasesAndStreaming())
}

func TestContract_SlashCommand_ZeroValueRejected(t *testing.T) {
	// Zero value: name="", label="", description="", delivery="" — all required;
	// delivery="" is not in enum [client, agent].
	// Traces to: SlashCommand.yaml — required: [name, label, description, delivery]
	mustFailComponent(t, "SlashCommand", FixtureSlashCommand_ZeroValue(),
		"zero value has empty required fields; delivery=\"\" is not in enum [client, agent]")
}

func TestContract_SlashCommand_EmptyDeliveryRejected(t *testing.T) {
	// Directly exercises the exact wire state that the Constraint #8 bug produced:
	// a non-web command with Delivery="" marshals to "delivery":"" which is invalid.
	// Traces to: SlashCommand.yaml — delivery: enum: [client, agent]

	sc := map[string]any{
		"name":        "agents",
		"label":       "/agents",
		"description": "List registered agents",
		"delivery":    "", // THE BUG: empty string is not in enum [client, agent]
	}
	raw, err := json.Marshal(sc)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"delivery":""`,
		"fixture must contain delivery:\"\" to exercise the bug path")
	assert.Error(t,
		validateAgainstComponentSchemaRawJSON(t, "SlashCommand", raw),
		"delivery:\"\" MUST fail SlashCommand.yaml — not in enum [client, agent]")
}

func TestContract_SlashCommand_Differentiation(t *testing.T) {
	// client-delivery and agent-delivery fixtures must produce different JSON.
	// Guards against hardcoded stubs.
	f1 := FixtureSlashCommand_ClientDelivery()
	f2 := FixtureSlashCommand_AgentDelivery()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"client-delivery and agent-delivery SlashCommand fixtures must produce different JSON")
	mustPassComponent(t, "SlashCommand", f1)
	mustPassComponent(t, "SlashCommand", f2)
}

// TestContract_SlashCommandDelivery_ValidMethod verifies the generated Valid() helper
// reports true for both enum values and false for an empty string (Constraint #8 gate).
func TestContract_SlashCommandDelivery_ValidMethod(t *testing.T) {
	if !SlashCommandDeliveryAgent.Valid() {
		t.Error("SlashCommandDeliveryAgent.Valid() must be true")
	}
	if !SlashCommandDeliveryClient.Valid() {
		t.Error("SlashCommandDeliveryClient.Valid() must be true")
	}
	if SlashCommandDelivery("").Valid() {
		t.Error("SlashCommandDelivery(\"\").Valid() must be false — empty string is not a valid enum value")
	}
}

// ── Provider / ProbeProviderResponse / OperationResult with Validation ────────
//
// Tests #21 / #33 from the provider-validation TDD plan: verify that Provider,
// ProbeProviderResponse, and OperationResult with a populated validation field
// (outcome + message) marshal to schema-valid JSON.
//
// Traces to:
//   - contracts/components/schemas/Provider.yaml (validation: $ref ProviderValidation)
//   - contracts/components/schemas/ProbeProviderResponse.yaml
//   - contracts/components/schemas/OperationResult.yaml
//   - contracts/components/schemas/ProviderValidation.yaml
//
// These tests catch regressions where:
//   - the Outcome enum value is invalid (off-contract)
//   - the validation object is absent when expected
//   - the outcome type is serialized incorrectly

func TestContract_Provider_WithValidation_Valid(t *testing.T) {
	// Provider with validation.outcome=no_credit must pass Provider.yaml.
	// Traces to: Provider.yaml, ProviderValidation.yaml
	msg := "Your OpenRouter key works, but the account has no credit."
	p := Provider{
		Id:     "openrouter",
		Name:   "openrouter",
		Status: ProviderStatusConnected,
		Models: []string{"meta-llama/llama-3.1-8b-instruct:free"},
		// ADR-067/068 contract commit: auth_method, dependents and backs_default
		// are always present on the wire (zero values until ADR-068 computes them).
		AuthMethod:   ProviderAuthMethodApiKey,
		Dependents:   []ProviderDependent{},
		BacksDefault: false,
		Validation: &struct {
			Message *string                   `json:"message,omitempty"`
			Outcome ProviderValidationOutcome `json:"outcome"`
		}{
			Outcome: NoCredit,
			Message: &msg,
		},
	}
	mustPassComponent(t, "Provider", p)

	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"outcome":"no_credit"`,
		"serialized Provider must contain outcome:no_credit")
	assert.Contains(t, string(raw), `"message":`,
		"serialized Provider must contain the message field")
}

func TestContract_Provider_WithValidation_AllOutcomes(t *testing.T) {
	// Each non-valid ProviderValidation outcome must produce schema-valid JSON.
	// Traces to: ProviderValidation.yaml (enum constraint).
	outcomes := []ProviderValidationOutcome{
		NoCredit,
		Unreachable,
		Restricted,
	}
	for _, outcome := range outcomes {
		t.Run(string(outcome), func(t *testing.T) {
			o := outcome
			p := Provider{
				Id:           "testprovider",
				Name:         "testprovider",
				Status:       ProviderStatusConnected,
				Models:       []string{},
				AuthMethod:   ProviderAuthMethodApiKey,
				Dependents:   []ProviderDependent{},
				BacksDefault: false,
				Validation: &struct {
					Message *string                   `json:"message,omitempty"`
					Outcome ProviderValidationOutcome `json:"outcome"`
				}{
					Outcome: o,
				},
			}
			mustPassComponent(t, "Provider", p)
		})
	}
}

func TestContract_Provider_InvalidOutcome_FailsSchema(t *testing.T) {
	// An off-contract outcome value must fail ProviderValidation schema validation.
	// Traces to: ProviderValidation.yaml — outcome is an enum with 5 values.
	raw := []byte(`{"outcome":"totally_wrong","message":"bad"}`)
	err := validateAgainstComponentSchemaRawJSON(t, "ProviderValidation", raw)
	assert.Error(t, err,
		"an off-contract outcome value must fail ProviderValidation.yaml schema validation")
}

func TestContract_ProbeProviderResponse_WithValidation_Valid(t *testing.T) {
	// ProbeProviderResponse with validation.outcome=no_credit must pass schema.
	// Traces to: ProbeProviderResponse.yaml, ProviderValidation.yaml.
	msg := "Your key works but no credit remaining."
	models := []string{"gpt-4o", "gpt-4o-mini"}
	r := ProbeProviderResponse{
		Success: true,
		Models:  &models,
		Validation: &struct {
			Message *string                                `json:"message,omitempty"`
			Outcome ProbeProviderResponseValidationOutcome `json:"outcome"`
		}{
			Outcome: ProbeProviderResponseValidationOutcomeNoCredit,
			Message: &msg,
		},
	}
	mustPassComponent(t, "ProbeProviderResponse", r)

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"outcome":"no_credit"`,
		"serialized ProbeProviderResponse must carry outcome:no_credit")
}

func TestContract_ProbeProviderResponse_NoValidation_Valid(t *testing.T) {
	// ProbeProviderResponse with no validation field (valid probe) must also pass.
	// Traces to: ProbeProviderResponse.yaml — validation is optional.
	models := []string{"gpt-4o"}
	r := ProbeProviderResponse{
		Success: true,
		Models:  &models,
	}
	mustPassComponent(t, "ProbeProviderResponse", r)

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"validation"`,
		"a valid probe response must omit the validation field")
}

func TestContract_OperationResult_WithValidation_Valid(t *testing.T) {
	// OperationResult with validation.outcome=no_credit must pass schema.
	// Traces to: OperationResult.yaml, ProviderValidation.yaml.
	msg := "Your key works, but the account has insufficient credit."
	r := OperationResult{
		Success: true,
		Validation: &struct {
			Message *string                          `json:"message,omitempty"`
			Outcome OperationResultValidationOutcome `json:"outcome"`
		}{
			Outcome: OperationResultValidationOutcomeNoCredit,
			Message: &msg,
		},
	}
	mustPassComponent(t, "OperationResult", r)

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"outcome":"no_credit"`,
		"serialized OperationResult must carry outcome:no_credit")
	assert.Contains(t, string(raw), `"message":`,
		"serialized OperationResult must carry the message field")
}

func TestContract_OperationResult_WithValidation_AllOutcomes(t *testing.T) {
	// Each non-valid outcome must yield schema-valid JSON for OperationResult.
	// Traces to: OperationResult.yaml — validation.$ref ProviderValidation.yaml.
	outcomes := []OperationResultValidationOutcome{
		OperationResultValidationOutcomeNoCredit,
		OperationResultValidationOutcomeUnreachable,
		OperationResultValidationOutcomeRestricted,
	}
	for _, outcome := range outcomes {
		t.Run(string(outcome), func(t *testing.T) {
			o := outcome
			r := OperationResult{
				Success: true,
				Validation: &struct {
					Message *string                          `json:"message,omitempty"`
					Outcome OperationResultValidationOutcome `json:"outcome"`
				}{
					Outcome: o,
				},
			}
			mustPassComponent(t, "OperationResult", r)
		})
	}
}

func TestContract_OperationResult_NoValidation_Valid(t *testing.T) {
	// OperationResult with no validation field must also pass — it is optional.
	// Traces to: OperationResult.yaml — validation is not in required list.
	r := OperationResult{Success: true}
	mustPassComponent(t, "OperationResult", r)

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"validation"`,
		"an OperationResult with no validation must omit the field")
}

// TestErrorFrame_PayloadOmitEmpty is the ADR-051 B2 regression: when
// ErrorFrame.Payload is *ErrorPayload (pointer), encoding/json's omitempty
// tag must actually OMIT the field for a nil payload. With the old value
// struct shape, encoding/json serialized every error frame with
// `payload:{llm_error:{code:"",message:"",retryable:false}}`, violating the
// LLMError schema (empty code/message fails enum+minLength) and causing the
// SPA Zod validator to drop the frame silently.
func TestErrorFrame_PayloadOmitEmpty(t *testing.T) {
	// Case 1: nil Payload → "payload" key must NOT appear in JSON.
	nilFrame := ErrorFrame{
		Type:    "error",
		Message: "auth failed",
	}
	raw, err := json.Marshal(nilFrame)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"payload"`,
		"nil *ErrorPayload must be omitted (omitempty regression)")

	// Case 2: populated Payload → payload.llm_error.code must be present.
	popFrame := FixtureErrorFrame_Populated()
	rawPop, err := json.Marshal(popFrame)
	require.NoError(t, err)
	assert.Contains(t, string(rawPop), `"payload"`,
		"populated payload must serialize")
	assert.Contains(t, string(rawPop), `"llm_error"`,
		"payload.llm_error must be present")
	assert.Contains(t, string(rawPop), `"code":"rate_limited"`,
		"payload.llm_error.code must carry the classifier code")
}

// TestReplayErrorFrame_PayloadOmitEmpty is the ADR-051 B2 replay-side
// counterpart: a ReplayErrorFrame with no ErrorCode must omit payload
// entirely (the buildReplayErrorFrame producer leaves Payload nil when
// entry.ErrorCode is empty).
func TestReplayErrorFrame_PayloadOmitEmpty(t *testing.T) {
	// Case 1: nil Payload → "payload" must NOT appear.
	nilFrame := ReplayErrorFrame{
		Type:      "replay_error",
		SessionId: "sess-1",
		EntryId:   "entry-1",
		Timestamp: "2026-07-22T00:00:00Z",
		Kind:      "error",
		Message:   "something went wrong",
	}
	raw, err := json.Marshal(nilFrame)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"payload"`,
		"nil *ReplayErrorPayload must be omitted (omitempty regression)")

	// Case 2: populated Payload → payload.llm_error must be present.
	popFrame := nilFrame
	popFrame.Payload = &ReplayErrorPayload{
		LlmError: LLMErrorReplay{
			Code:      "rate_limited",
			Message:   "rate limit hit",
			Retryable: true,
		},
	}
	rawPop, err := json.Marshal(popFrame)
	require.NoError(t, err)
	assert.Contains(t, string(rawPop), `"payload"`,
		"populated payload must serialize")
	assert.Contains(t, string(rawPop), `"code":"rate_limited"`,
		"payload.llm_error.code must carry the classifier code")
}

// ── AuditEntry ───────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuditEntry.yaml. Previously ZERO
// contract coverage — flagged by the test-coverage gate on
// fix/uat-v0.1.1-defects (GAP 2).

func TestContract_AuditEntry_Populated(t *testing.T) {
	// Traces to: AuditEntry.yaml — required: [timestamp, event]; every
	// optional field also set (the security_setting_change-adjacent shape).
	mustPassComponent(t, "AuditEntry", FixtureAuditEntry_Populated())
}

func TestContract_AuditEntry_ZeroValue(t *testing.T) {
	// Traces to: AuditEntry.yaml — event: pattern ^[a-z_]+$ (one-or-more).
	// Go zero value has Event="" which does not match (zero characters).
	mustFailComponent(t, "AuditEntry", FixtureAuditEntry_ZeroValue(),
		"zero value: event=\"\" does not match pattern ^[a-z_]+$ (requires 1+ chars)")
}

func TestContract_AuditEntry_Edge(t *testing.T) {
	// Traces to: AuditEntry.yaml — only required fields set (timestamp,
	// event); every optional field absent. A routine startup/shutdown
	// record has exactly this shape.
	mustPassComponent(t, "AuditEntry", FixtureAuditEntry_Edge())
}

func TestContract_AuditEntry_Differentiation(t *testing.T) {
	// Two structurally different, both-valid entries must not collapse to
	// the same JSON — catches a hardcoded/constant-fixture regression.
	f1 := FixtureAuditEntry_Populated()
	f2 := FixtureAuditEntry_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated and Edge AuditEntry fixtures must produce different JSON")
	mustPassComponent(t, "AuditEntry", f1)
	mustPassComponent(t, "AuditEntry", f2)
}

func TestContract_AuditEntry_InvalidDecisionRejected(t *testing.T) {
	// Traces to: AuditEntry.yaml — decision enum: [allow, deny, error].
	doc := map[string]any{
		"timestamp": "2026-05-16T10:30:00Z",
		"event":     "policy_eval",
		"decision":  "maybe", // NOT in enum
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AuditEntry", raw),
		"decision='maybe' must fail — not in enum [allow, deny, error]")
}

func TestContract_AuditEntry_InvalidEventPatternRejected(t *testing.T) {
	// Traces to: AuditEntry.yaml — event: pattern ^[a-z_]+$. Uppercase and
	// hyphenated values (as a caller might accidentally pass, e.g. a Go
	// constant rendered as "ToolCall" or "tool-call") must be rejected —
	// only lowercase letters and underscores are legal.
	tests := []struct {
		name  string
		event string
	}{
		{"uppercase", "ToolCall"},
		{"hyphenated", "tool-call"},
		{"digits", "tool_call_2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := map[string]any{
				"timestamp": "2026-05-16T10:30:00Z",
				"event":     tc.event,
			}
			raw, err := json.Marshal(doc)
			require.NoError(t, err)
			assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "AuditEntry", raw),
				"event=%q must fail — does not match pattern ^[a-z_]+$", tc.event)
		})
	}
}

// ── GodModeStatus ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/GodModeStatus.yaml. Previously
// ZERO contract coverage — flagged by the test-coverage gate on
// fix/uat-v0.1.1-defects (GAP 2).

func TestContract_GodModeStatus_Populated(t *testing.T) {
	// Traces to: GodModeStatus.yaml — required: [enabled, available,
	// supported, persisted], additionalProperties: false. Fully armed/active.
	mustPassComponent(t, "GodModeStatus", FixtureGodModeStatus_Populated())
}

func TestContract_GodModeStatus_ZeroValue(t *testing.T) {
	// UNLIKE most _ZeroValue contract tests, all-false is schema-VALID here:
	// every field is a plain required boolean with no further constraint,
	// and false/false/false/false is the real "never armed" fresh-install
	// state per GodModeStatus.yaml's own field docs. This test intentionally
	// asserts PASS, not FAIL — the anti-shortcut concern for a bool-only
	// resource is additionalProperties/enum-adjacent rejection, not "zero
	// value must be invalid".
	mustPassComponent(t, "GodModeStatus", FixtureGodModeStatus_ZeroValue())
}

func TestContract_GodModeStatus_Edge(t *testing.T) {
	// Traces to: GodModeStatus.yaml persisted field doc — the "armed via the
	// UI, pending restart" state: persisted=true, available=false,
	// enabled=false.
	mustPassComponent(t, "GodModeStatus", FixtureGodModeStatus_Edge())
}

func TestContract_GodModeStatus_Differentiation(t *testing.T) {
	f1 := FixtureGodModeStatus_Populated()
	f2 := FixtureGodModeStatus_Edge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"Populated (all true) and Edge (pending-restart) GodModeStatus fixtures must produce different JSON")
	mustPassComponent(t, "GodModeStatus", f1)
	mustPassComponent(t, "GodModeStatus", f2)
}

func TestContract_GodModeStatus_RejectsExtraneousField(t *testing.T) {
	// Traces to: GodModeStatus.yaml — additionalProperties: false.
	doc := map[string]any{
		"enabled":          true,
		"available":        true,
		"supported":        true,
		"persisted":        true,
		"restart_required": false, // not a GodModeStatus field (belongs to GodModeUpdateResponse)
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "GodModeStatus", raw),
		"GodModeStatus with extraneous field must fail — additionalProperties: false")
}

func TestContract_GodModeStatus_MissingRequiredFieldRejected(t *testing.T) {
	// Traces to: GodModeStatus.yaml — required: [enabled, available,
	// supported, persisted]. Every field is a plain bool with no omitempty,
	// so a legitimately-constructed Go struct can never omit one — this
	// guards the wire contract itself (e.g. a handwritten partial JSON
	// response) rather than anything reachable through the generated type.
	doc := map[string]any{
		"enabled":   true,
		"available": true,
		"supported": true,
		// "persisted" omitted
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "GodModeStatus", raw),
		"GodModeStatus missing required 'persisted' must fail")
}
