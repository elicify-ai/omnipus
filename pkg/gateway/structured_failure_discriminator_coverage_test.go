//go:build !windows

// structured_failure_discriminator_coverage_test.go — issue #618 regression
// guard.
//
// Before this fix, TWO of the four discriminators tool_result_store.go's
// structuredFailureDiscriminators recognizes today — permission_denied and
// tool_assembly_duplicate — had real, reachable producers (pkg/agent's
// denialPayloadJSON and pkg/agent/loop.go's checkToolDedupInvariant guard)
// but NO contract schema, NO entry in the allow-list, and NO length budget:
// they were built by hand with fmt.Sprintf's %q verb, which is Go-string
// quoting, not JSON quoting, and silently produced invalid JSON for a path
// or tool name containing invalid UTF-8 or most C0/C1 control bytes.
//
// This test closes the gap for good, not just for the two members this fix
// adds: structuredFailureDiscriminators and discriminatorCoverageRegistry
// below must always have the same members. A future author who adds a
// fifth discriminator to the allow-list without a matching schema-validated,
// budget-bounded fixture here fails this test immediately, instead of
// shipping the same silent-corruption defect class a third time.
//
// Windows-excluded for the same reason pkg/api/generated/contract_test.go
// is: the yamlLoader below uses file:// URLs with POSIX paths.

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// discriminatorFixture pairs a contract schema name with a producer call
// that builds a real, adversarial-input payload for it.
type discriminatorFixture struct {
	schemaName string
	build      func() ([]byte, error)
}

// hugeEscapable is a long string built from characters JSON must escape
// (&, <, >, ", \) — the exact input class that overflowed the FIRST version
// of this project's per-field budgets (encoding/json HTML-escapes < > & to
// six runes each and doubles " and \, so arithmetic on ASCII input runes
// understates the encoded size). Reused across every fixture below so all
// four producers are exercised against the same adversarial shape.
//
// The HTML-escaping characters alone do not exercise #618's ENCODING defect
// (they are all legal under fmt.Sprintf's %q too) — only a raw C0/C1
// control byte outside \n\t\r or invalid UTF-8 does that (%q emits \xNN,
// not a legal JSON escape). The suffix below adds one of each: \x01 (a
// control byte %q cannot represent as valid JSON), \xff (a lone invalid
// UTF-8 byte, the exact shape a *os.PathError's Error() can contain
// byte-for-byte from the OS), and \xe2\x28 (a truncated 3-byte UTF-8
// sequence). pkg/tools/permission_denied_test.go's adversarialInputs table
// drives these classes individually and in isolation; this suffix makes
// sure the SAME size-stress fixtures this file's coverage test already
// runs also exercise the encoding class, not just the budget class.
var hugeEscapable = strings.Repeat(`R&D <agent> "name"\path/`, 300) + "\x01\x7f\xff\xe2\x28"

// discriminatorCoverageRegistry is the fixture for every discriminator
// structuredFailureDiscriminators (tool_result_store.go) currently
// recognizes. Kept deliberately independent of that map (a literal
// key-for-key duplicate, not a derived transform of it) so the completeness
// assertion in the test below actually catches drift instead of trivially
// agreeing with itself.
var discriminatorCoverageRegistry = map[string]discriminatorFixture{
	tools.DelegationDeniedCode: {
		schemaName: "DelegationFailure",
		build: func() ([]byte, error) {
			res := tools.DelegationDeniedResult("delegate", &tools.DelegationDenial{
				Reason:        "delegation to agent " + hugeEscapable + " is not permitted",
				Policy:        tools.DenyTrustSet,
				TargetAgentID: hugeEscapable,
			})
			return []byte(res.ForLLM), nil
		},
	},
	tools.FileExistsRefusalCode: {
		schemaName: "FileExistsRefusal",
		build: func() ([]byte, error) {
			longPath := "/workspace/" + hugeEscapable + "f.txt"
			res := tools.FileExistsRefusalResult("write_file", longPath,
				"file: "+longPath+" already exists. Set overwrite=true to replace.")
			return []byte(res.ForLLM), nil
		},
	},
	tools.PermissionDeniedCode: {
		schemaName: "PermissionDenied",
		build: func() ([]byte, error) {
			longPath := "/workspace/" + hugeEscapable + "f.txt"
			return tools.PermissionDeniedPayload(
				"write_file",
				"Access to this path is denied by filesystem policy.",
				"access denied: path is outside the effective filesystem scope: "+longPath,
				true,
			)
		},
	},
	tools.ToolAssemblyDuplicateCode: {
		schemaName: "ToolAssemblyDuplicate",
		build: func() ([]byte, error) {
			return tools.ToolAssemblyDuplicatePayload(
				"duplicate tool \"" + hugeEscapable + "\" in assembled tools[]",
			)
		},
	},
}

// ── Minimal self-contained AsyncAPI-fragment schema loader ────────────────
//
// Deliberately NOT shared with pkg/api/generated/contract_test.go — that
// package's helpers are unexported _test.go symbols in a different package
// and importing pkg/gateway from there (or vice versa via an external test
// package) is unnecessary complexity for four schema checks. This mirrors
// its approach at a much smaller scale: one compiler, no caching, four
// Compile calls in a single test.

type coverageYAMLLoader struct{}

func (coverageYAMLLoader) Load(rawURL string) (any, error) {
	path := strings.TrimPrefix(rawURL, "file://")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if uerr := yaml.Unmarshal(data, &doc); uerr != nil {
		return nil, uerr
	}
	return normalizeYAMLDoc(doc), nil
}

// normalizeYAMLDoc converts yaml.v3's map[string]interface{} (and nested
// map[interface{}]interface{} on older decode paths) into the
// map[string]any shape jsonschema/v6 requires. yaml.v3 already decodes into
// map[string]interface{} for string keys, so this is mostly a type-level
// walk that also normalizes int keys defensively.
func normalizeYAMLDoc(v any) any {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = normalizeYAMLDoc(vv)
		}
		return out
	case []interface{}:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = normalizeYAMLDoc(vv)
		}
		return out
	default:
		return v
	}
}

func asyncapiFilePathForCoverageTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed — cannot resolve contracts dir")
	return filepath.Join(filepath.Dir(file), "..", "..", "contracts", "asyncapi.yaml")
}

func validateFixtureAgainstAsyncAPISchema(t *testing.T, schemaName string, raw []byte) error {
	t.Helper()

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc), "fixture for schema %q is not valid JSON: %s", schemaName, raw)

	c := jsonschema.NewCompiler()
	c.UseLoader(coverageYAMLLoader{})

	asyncapiPath := asyncapiFilePathForCoverageTest(t)
	url := "file://" + filepath.ToSlash(asyncapiPath) + "#/components/schemas/" + schemaName

	sch, err := c.Compile(url)
	require.NoError(t, err, "could not compile asyncapi schema %q from %s", schemaName, asyncapiPath)

	return sch.Validate(doc)
}

// TestStructuredFailureDiscriminators_HaveSchemaAndBudgetBoundedProducer is
// the completeness + regression guard described in the file header.
func TestStructuredFailureDiscriminators_HaveSchemaAndBudgetBoundedProducer(t *testing.T) {
	require.Len(t, discriminatorCoverageRegistry, len(structuredFailureDiscriminators),
		"structuredFailureDiscriminators (tool_result_store.go) and this test's fixture registry "+
			"must have the same size — a discriminator added to the allow-list without a matching "+
			"schema-validated, budget-bounded fixture here is exactly the issue #618 gap")

	for code := range structuredFailureDiscriminators {
		fixture, ok := discriminatorCoverageRegistry[code]
		require.True(t, ok, "discriminator %q is in structuredFailureDiscriminators but has no "+
			"fixture/schema entry in discriminatorCoverageRegistry — add one", code)

		raw, err := fixture.build()
		require.NoError(t, err, "producer for discriminator %q returned an error", code)

		// Budget: must fit under the SAME 2000-rune cap the persisted
		// transcript (pkg/agent.maxFailClosedOutputChars) and the live
		// frame (pkg/gateway.maxLiveErrorChars) both apply downstream. A
		// payload over this cap is truncated into invalid JSON before a
		// reader ever sees it.
		runeLen := len([]rune(string(raw)))
		require.LessOrEqual(t, runeLen, maxLiveErrorChars,
			"producer for %q emitted %d runes — exceeds the %d-rune downstream truncation cap; a "+
				"payload this large is severed into unparseable JSON before it reaches a reader",
			code, runeLen, maxLiveErrorChars)

		// Self-consistency: the payload's own "error" field must equal the
		// map key it is registered under.
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed), "producer for %q emitted invalid JSON: %s", code, raw)
		require.Equal(t, code, parsed["error"], "producer for %q emitted a mismatched discriminator", code)

		// Schema: must validate against the named contract schema.
		verr := validateFixtureAgainstAsyncAPISchema(t, fixture.schemaName, raw)
		require.NoError(t, verr, "producer for %q emitted a payload that fails contracts/asyncapi.yaml "+
			"schema %q", code, fixture.schemaName)
	}
}
