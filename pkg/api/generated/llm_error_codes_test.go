//go:build !windows

// llm_error_codes_test.go — Exhaustive classifier-code contract test.
//
// Companion to contract_test.go; closes the SFH-W1-01 / TD-C1 regression:
// when a new LLMErrorCode constant is added to pkg/agent/translate_error.go,
// EVERY code MUST also be present in the canonical AsyncAPI enums for both
// the live LLMError schema and the LLMErrorReplay schema. This test
// enumerates the runtime constants via the Go AST, marshals an LLMError
// and LLMErrorReplay for each code, and validates the marshaled bytes
// against both canonical schemas. The Zod assertion reads the generated
// enum from _asyncapi-zod-schemas.generated.ts so the contract is closed
// at every layer the SPA consumes (Go wire shape + JSON-Schema canonical
// + Zod runtime parser).
//
// Build constraint: !windows — same YAML loader restriction as
// contract_test.go.

package generated

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot returns the absolute path to the repository root, computed
// from this test file's location.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed - cannot resolve repo root")
	}
	// file is /path/to/pkg/api/generated/llm_error_codes_test.go
	// repo root is three dirs up
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// classifierCodesFromAgent parses pkg/agent/translate_error.go and
// returns every LLMErrorCode constant value declared in that file. This
// is the source-of-truth enumeration: if a new code is added here, every
// contract test must cover it.
func classifierCodesFromAgent(t *testing.T) []string {
	t.Helper()

	const target = "pkg/agent/translate_error.go"
	path := filepath.Join(repoRoot(), target)

	src, err := os.ReadFile(path)
	require.NoError(t, err, "could not read %s", target)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, target, src, parser.ParseComments)
	require.NoError(t, err, "could not parse %s", target)

	var codes []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			isLLMErrorCode := false
			if vs.Type != nil {
				if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "LLMErrorCode" {
					isLLMErrorCode = true
				}
			}
			if !isLLMErrorCode {
				continue
			}
			for _, v := range vs.Values {
				bl, ok := v.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					continue
				}
				val := strings.Trim(bl.Value, "`\"")
				if val != "" {
					codes = append(codes, val)
				}
			}
		}
	}

	require.NotEmpty(t, codes,
		"parsed zero classifier codes from %s - parser or source changed; "+
			"if LLMErrorCode constants moved, update classifierCodesFromAgent", target)

	sort.Strings(codes)
	return codes
}

// liveCodesFromZodSchemas parses the generated _asyncapi-zod-schemas for
// the LLMError / LLMErrorReplay code enum and returns the values
// declared. This is the third layer of the contract (Go wire, JSON
// schema, Zod runtime); all three must agree.
func liveCodesFromZodSchemas(t *testing.T) []string {
	t.Helper()

	const target = "src/lib/api/generated/_asyncapi-zod-schemas.generated.ts"
	path := filepath.Join(repoRoot(), target)

	src, err := os.ReadFile(path)
	require.NoError(t, err, "could not read %s", target)

	text := string(src)
	out := map[string]struct{}{}
	for _, marker := range []string{"LLMError =", "LLMErrorReplay ="} {
		idx := strings.Index(text, marker)
		require.GreaterOrEqual(t, idx, 0,
			"could not locate %q in generated Zod schemas", marker)
		enumStart := strings.Index(text[idx:], "z.enum([")
		require.GreaterOrEqual(t, enumStart, 0,
			"could not locate z.enum([ after %q", marker)
		enumOpen := idx + enumStart + len("z.enum([")
		enumClose := strings.Index(text[enumOpen:], "])")
		require.GreaterOrEqual(t, enumClose, 0,
			"could not locate closing '])' after z.enum([")
		enumBody := text[enumOpen : enumOpen+enumClose]

		i := 0
		for i < len(enumBody) {
			if enumBody[i] != '"' {
				i++
				continue
			}
			j := i + 1
			for j < len(enumBody) && enumBody[j] != '"' {
				if enumBody[j] == '\\' && j+1 < len(enumBody) {
					j += 2
					continue
				}
				j++
			}
			if j >= len(enumBody) {
				break
			}
			out[enumBody[i+1:j]] = struct{}{}
			i = j + 1
		}
	}

	require.NotEmpty(t, out,
		"parsed zero codes from generated Zod schemas - generator output changed")
	codes := make([]string, 0, len(out))
	for k := range out {
		codes = append(codes, k)
	}
	sort.Strings(codes)
	return codes
}

// assertCodeInComponentSchema validates a fixture against a component
// schema file with the given code, returning no error if the schema
// accepts the code (or an error if the enum rejects it).
func assertCodeInComponentSchema(t *testing.T, schemaName string, code string) {
	t.Helper()
	live := LLMError{
		Code:      code,
		Message:   "user-facing copy for " + code,
		Retryable: false,
	}
	raw, err := json.Marshal(live)
	require.NoError(t, err, "json.Marshal failed for LLMError code=%q", code)

	require.NoError(t, validateAgainstComponentSchemaRawJSON(t, schemaName, raw),
		"classifier code %q fails %s schema validation - "+
			"this is the SFH-W1-01 / TD-C1 regression; "+
			"add the code to the schema enum and regenerate contracts",
		code, schemaName)
}

// TestContract_LLMError_AllClassifierCodesRoundTrip is the
// exhaustive-code regression guard for SFH-W1-01 / TD-C1. It:
//  1. enumerates every LLMErrorCode constant declared in
//     pkg/agent/translate_error.go (the runtime truth),
//  2. marshals an LLMError and LLMErrorReplay for each code and
//     validates the JSON against the JSON-Schema canonical,
//  3. asserts every runtime code is present in the generated Zod enum.
//
// A regression in any one of those layers (Go runtime / live JSON
// schema / replay JSON schema / generated Zod) fails this test. The
// corollary is the desired property: adding a new constant without also
// updating all four layers breaks the build, with a precise error
// pointing at the missing code.
func TestContract_LLMError_AllClassifierCodesRoundTrip(t *testing.T) {
	codes := classifierCodesFromAgent(t)
	zodCodes := liveCodesFromZodSchemas(t)

	t.Logf("discovered %d LLMErrorCode constants in pkg/agent: %v", len(codes), codes)
	t.Logf("discovered %d unique codes in generated Zod schemas: %v", len(zodCodes), zodCodes)

	zodSet := make(map[string]struct{}, len(zodCodes))
	for _, c := range zodCodes {
		zodSet[c] = struct{}{}
	}

	for _, code := range codes {
		t.Run("zod_enum/"+code, func(t *testing.T) {
			_, ok := zodSet[code]
			assert.True(t, ok,
				"classifier code %q is in pkg/agent but missing from generated Zod enums in "+
					"_asyncapi-zod-schemas.generated.ts - this is the SFH-W1-01 / TD-C1 regression; "+
					"regenerate contracts after updating LLMError.yaml / LLMErrorReplay.yaml / asyncapi.yaml",
				code)
		})
		t.Run("live_schema/"+code, func(t *testing.T) {
			assertCodeInComponentSchema(t, "LLMError", code)
		})
		t.Run("replay_schema/"+code, func(t *testing.T) {
			replay := LLMErrorReplay{
				Code:      code,
				Message:   "user-facing copy for " + code,
				Retryable: false,
			}
			raw, err := json.Marshal(replay)
			require.NoError(t, err, "json.Marshal failed for LLMErrorReplay code=%q", code)
			require.NoError(t, validateAgainstComponentSchemaRawJSON(t, "LLMErrorReplay", raw),
				"classifier code %q fails LLMErrorReplay schema validation - "+
					"add the code to LLMErrorReplay.yaml's enum and regenerate contracts",
				code)
		})
	}
}
