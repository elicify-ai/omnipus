// llm_error_neutral_catalogue_1857_test.go — #1857 frontend round, fix round 1
// (CHECK F2): neutral-twin parity.
//
// Oracle: design note /workspace/logs/1857/arch/design-note-frontend-round.md,
// D-B B1: "`neutral` and `neutral_variants` ... identical in all 4 copies",
// "Go emits `LLMErrorNeutralMessages` and `LLMErrorNeutralVariants`", and
// "Codes whose single sentence has no first person ... need no twin and render
// the same on both" (the no-twin fallback is the chat sentence).
//
// Two layers:
//  1. The four-copy comparison itself lives in llm_error_catalogue_test.go
//     (TestContract_LLMErrorCatalogue_AllFourCopiesAgree, extended to compare
//     neutral and neutral_variants). TestLLMErrorNeutral_FourCopyInstrument...
//     below proves the instrument now sees a neutral edit made in one copy only.
//  2. Source to generated: the generated Go maps are compared with the contract
//     source: keys, variant keys, values and the no-twin fallback.
//
// Assumption (stated for the report): the generated symbols do not exist yet,
// so this file must compile today. It therefore reads them from the generated
// source text with go/parser instead of referencing them by name. Today the
// check fails for the right reason: the two maps are not emitted. Once GREEN
// emits them, the same test compares their contents. A generator that
// consistently drops neutral data (which a clean regeneration cannot detect)
// fails here because the source twin has no generated counterpart.

package generated

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseGeneratedStringMaps reads the top-level `var <name> = map[...]` literals
// of a generated file. flat is set for map[string]string, nested for
// map[string]map[string]string. found reports whether the variable exists.
func parseGeneratedStringMaps(t *testing.T, path, name string) (flat map[string]string, nested map[string]map[string]string, found bool) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "SETUP: parsing %s", path)

	unquote := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(lit.Value)
		return s, err == nil
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != name || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.CompositeLit)
			require.True(t, ok, "%s must be initialised with a map literal", name)
			found = true
			flat = map[string]string{}
			nested = map[string]map[string]string{}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				require.True(t, ok, "%s: entries must be key: value", name)
				key, ok := unquote(kv.Key)
				require.True(t, ok, "%s: keys must be string literals", name)
				if value, ok := unquote(kv.Value); ok {
					flat[key] = value
					continue
				}
				inner, ok := kv.Value.(*ast.CompositeLit)
				require.True(t, ok, "%s[%q]: value must be a string or a map literal", name, key)
				variants := map[string]string{}
				for _, ie := range inner.Elts {
					ikv, ok := ie.(*ast.KeyValueExpr)
					require.True(t, ok, "%s[%q]: entries must be key: value", name, key)
					ik, ok := unquote(ikv.Key)
					require.True(t, ok, "%s[%q]: variant keys must be string literals", name, key)
					iv, ok := unquote(ikv.Value)
					require.True(t, ok, "%s[%q][%q]: value must be a string literal", name, key, ik)
					variants[ik] = iv
				}
				nested[key] = variants
			}
			return flat, nested, true
		}
	}
	return nil, nil, false
}

func neutral1857Keys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestLLMErrorNeutral_FourCopyInstrumentSeesNeutralEdit1857 is the control for
// F2's surviving mutant: a neutral sentence present in only one copy must make
// the comparable slice differ, which is what the four-copy test asserts on.
func TestLLMErrorNeutral_FourCopyInstrumentSeesNeutralEdit1857(t *testing.T) {
	schema := func(extra map[string]any) map[string]any {
		entry := map[string]any{"message": "base", "attribution": "config"}
		for k, v := range extra {
			entry[k] = v
		}
		return map[string]any{
			"properties":                  map[string]any{"code": map[string]any{"enum": []any{"alpha"}}},
			"x-user-message-attributions": []any{"config"},
			"x-user-messages":             map[string]any{"alpha": entry},
		}
	}
	bare := loadCatalogueFrom(t, "bare", schema(nil))
	withTwin := loadCatalogueFrom(t, "twin", schema(map[string]any{"neutral": "twin"}))
	withVariantTwin := loadCatalogueFrom(t, "variant-twin", schema(map[string]any{
		"neutral_variants": map[string]any{"v1": "twin"},
	}))
	assert.NotEqual(t, bare.neutral, withTwin.neutral, "D-B: a neutral twin in one copy must be visible to the parity comparison")
	assert.Equal(t, "twin", withTwin.neutral["alpha"])
	assert.NotEqual(t, bare.neutralVariants, withVariantTwin.neutralVariants,
		"D-B: a neutral_variants entry in one copy must be visible to the parity comparison")
	assert.Equal(t, map[string]string{"v1": "twin"}, withVariantTwin.neutralVariants["alpha"])
}

// TestContract_LLMErrorNeutral_MatchesGeneratedGo1857 compares the contract's
// neutral data with the generated Go maps: every source twin is emitted with
// the same value and variant keys, nothing is emitted that the source lacks,
// and the no-twin fallback is the chat sentence.
func TestContract_LLMErrorNeutral_MatchesGeneratedGo1857(t *testing.T) {
	source := loadAsyncAPICatalogue(t, "LLMError")
	genPath := filepath.Join(repoRoot(), "pkg", "api", "generated", "llm_error_messages.gen.go")

	flat, _, foundFlat := parseGeneratedStringMaps(t, genPath, "LLMErrorNeutralMessages")
	_, nested, foundNested := parseGeneratedStringMaps(t, genPath, "LLMErrorNeutralVariants")
	if !assert.True(t, foundFlat, "D-B B1 generated map: LLMErrorNeutralMessages is not emitted by the Go generator") ||
		!assert.True(t, foundNested, "D-B B1 generated map: LLMErrorNeutralVariants is not emitted by the Go generator") {
		return
	}

	known := map[string]bool{}
	for _, code := range source.codes {
		known[code] = true
	}

	// Keys: nothing generated that the contract does not define.
	for code := range flat {
		assert.True(t, known[code], "D-B generated map: LLMErrorNeutralMessages has a key %q that is not a contract code", code)
	}
	for code := range nested {
		assert.True(t, known[code], "D-B generated map: LLMErrorNeutralVariants has a key %q that is not a contract code", code)
	}

	for _, code := range source.codes {
		// Chat sentence: the generated catalogue is the fallback for a code with no twin.
		chat := source.messages[code]["message"]
		effective := chat
		if twin, ok := source.neutral[code]; ok {
			effective = twin
		}
		generatedEffective := LLMErrorUserMessages[code]
		if twin, ok := flat[code]; ok {
			generatedEffective = twin
		}
		assert.Equal(t, effective, generatedEffective,
			"D-B generated map: the effective neutral sentence for %q differs from the contract (value or no-twin fallback)", code)
		if _, ok := source.neutral[code]; ok {
			assert.Contains(t, flat, code, "D-B generated map: contract twin for %q is missing from LLMErrorNeutralMessages", code)
		}

		// Variants: keys and values.
		sourceVariants := source.neutralVariants[code]
		generatedVariants := nested[code]
		assert.Equal(t, neutral1857Keys(sourceVariants), neutral1857Keys(generatedVariants),
			"D-B generated map: neutral variant keys for %q differ from the contract", code)
		for key, sentence := range sourceVariants {
			assert.Equal(t, sentence, generatedVariants[key],
				"D-B generated map: neutral variant %q of %q differs from the contract", key, code)
		}
		// Variant keys must be exactly the chat variant keys (design note D-B B1).
		if len(sourceVariants) > 0 {
			assert.Equal(t, neutral1857Keys(LLMErrorMessageVariants[code]), neutral1857Keys(sourceVariants),
				"D-B B1: neutral_variants keys for %q must equal the chat variants keys", code)
		}
	}
}
