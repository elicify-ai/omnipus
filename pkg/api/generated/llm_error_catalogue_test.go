//go:build !windows

// llm_error_catalogue_test.go — four-way contract agreement for the LLMError
// user-facing copy catalogue.
//
// The LLMError code enum lives in FOUR hand-maintained places, because
// contracts/asyncapi.yaml does not $ref the component files and both code
// generators read only asyncapi.yaml:
//
//	contracts/components/schemas/LLMError.yaml
//	contracts/components/schemas/LLMErrorReplay.yaml
//	contracts/asyncapi.yaml  → components.schemas.LLMError
//	contracts/asyncapi.yaml  → components.schemas.LLMErrorReplay
//
// Adding the x-user-messages copy catalogue multiplies that duplication by the
// length of the copy deck. This file is what makes the duplication safe: the
// four copies are compared field-for-field, so a message edited in one place
// and forgotten in the other three fails here instead of shipping as a
// generated artifact that disagrees with the schema it was generated from.
//
// The companion file llm_error_codes_test.go closes the OTHER half of the
// contract — that the Go classifier constants, the JSON-Schema enums, and the
// generated Zod enum all name the same set of codes.
//
// Build constraint: !windows — repoRoot() lives in llm_error_codes_test.go,
// which is !windows-gated for its schema loader.

package generated

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// llmErrorCatalogue is the comparable slice of an LLMError-shaped schema: the
// parts that must be identical across all four copies. Everything else (title,
// description, the live-only `detail` property) legitimately differs.
type llmErrorCatalogue struct {
	codes        []string
	attributions []string
	messages     map[string]map[string]string // code → {message, attribution}
}

// loadCatalogueFrom extracts the comparable slice out of one parsed schema map.
func loadCatalogueFrom(t *testing.T, label string, schema map[string]any) llmErrorCatalogue {
	t.Helper()

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "%s: missing properties", label)
	codeProp, ok := properties["code"].(map[string]any)
	require.True(t, ok, "%s: missing properties.code", label)

	out := llmErrorCatalogue{messages: map[string]map[string]string{}}

	rawCodes, ok := codeProp["enum"].([]any)
	require.True(t, ok, "%s: properties.code.enum must be a list", label)
	for _, c := range rawCodes {
		s, ok := c.(string)
		require.True(t, ok, "%s: enum values must be strings, got %T", label, c)
		out.codes = append(out.codes, s)
	}

	rawAttributions, ok := schema["x-user-message-attributions"].([]any)
	require.True(t, ok, "%s: missing x-user-message-attributions", label)
	for _, a := range rawAttributions {
		s, ok := a.(string)
		require.True(t, ok, "%s: attributions must be strings, got %T", label, a)
		out.attributions = append(out.attributions, s)
	}

	rawMessages, ok := schema["x-user-messages"].(map[string]any)
	require.True(t, ok, "%s: missing x-user-messages", label)
	for code, rawEntry := range rawMessages {
		entry, ok := rawEntry.(map[string]any)
		require.True(t, ok, "%s: x-user-messages.%s must be a mapping", label, code)
		message, _ := entry["message"].(string)
		attribution, _ := entry["attribution"].(string)
		out.messages[code] = map[string]string{"message": message, "attribution": attribution}
	}

	return out
}

// loadComponentCatalogue reads contracts/components/schemas/<name>.yaml.
func loadComponentCatalogue(t *testing.T, name string) llmErrorCatalogue {
	t.Helper()
	path := filepath.Join(repoRoot(), "contracts", "components", "schemas", name+".yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "could not read %s", path)
	var schema map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &schema), "could not parse %s", path)
	return loadCatalogueFrom(t, "components/schemas/"+name+".yaml", schema)
}

// loadAsyncAPICatalogue reads components.schemas.<name> from asyncapi.yaml.
func loadAsyncAPICatalogue(t *testing.T, name string) llmErrorCatalogue {
	t.Helper()
	path := filepath.Join(repoRoot(), "contracts", "asyncapi.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "could not read %s", path)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc), "could not parse %s", path)

	components, ok := doc["components"].(map[string]any)
	require.True(t, ok, "asyncapi.yaml: missing components")
	schemas, ok := components["schemas"].(map[string]any)
	require.True(t, ok, "asyncapi.yaml: missing components.schemas")
	schema, ok := schemas[name].(map[string]any)
	require.True(t, ok, "asyncapi.yaml: missing components.schemas.%s", name)
	return loadCatalogueFrom(t, "asyncapi.yaml#/components/schemas/"+name, schema)
}

// TestContract_LLMErrorCatalogue_AllFourCopiesAgree is the anti-drift guard for
// the four hand-maintained copies of the LLMError code enum and its copy deck.
//
// asyncapi.yaml is the copy both generators actually read; the component files
// are what the runtime JSON-Schema validators (and pkg/gateway/inboundschemas)
// compile. Nothing enforces that they say the same thing — this test does.
// Without it, a message fixed in LLMError.yaml but not in asyncapi.yaml
// regenerates green while shipping the OLD copy to users.
func TestContract_LLMErrorCatalogue_AllFourCopiesAgree(t *testing.T) {
	reference := loadAsyncAPICatalogue(t, "LLMError")
	require.NotEmpty(t, reference.codes, "the reference catalogue must not be empty")
	require.NotEmpty(t, reference.messages, "the reference catalogue must carry copy")

	others := map[string]llmErrorCatalogue{
		"asyncapi.yaml#/components/schemas/LLMErrorReplay": loadAsyncAPICatalogue(t, "LLMErrorReplay"),
		"components/schemas/LLMError.yaml":                 loadComponentCatalogue(t, "LLMError"),
		"components/schemas/LLMErrorReplay.yaml":           loadComponentCatalogue(t, "LLMErrorReplay"),
	}

	for label, other := range others {
		t.Run(label, func(t *testing.T) {
			assert.Equal(t, reference.codes, other.codes,
				"%s: code enum differs from asyncapi.yaml#/components/schemas/LLMError — "+
					"all four copies must be updated together", label)
			assert.Equal(t, reference.attributions, other.attributions,
				"%s: x-user-message-attributions differs from the reference copy", label)
			assert.Equal(t, reference.messages, other.messages,
				"%s: x-user-messages differs from the reference copy — a message edited in one "+
					"copy and not the others ships the stale text from whichever copy codegen reads", label)
		})
	}
}

// TestContract_LLMErrorCatalogue_MatchesGeneratedGo asserts the committed Go
// catalogue is what the current contract would produce. A stale
// llm_error_messages.gen.go — someone edited the YAML and did not run
// `make gen-contracts` — fails here rather than silently serving old copy from
// the binary while the contract claims otherwise.
//
// This is the same property `make verify-contracts` enforces via git diff, but
// as a test it also fires for anyone running the suite without the codegen
// toolchain installed.
func TestContract_LLMErrorCatalogue_MatchesGeneratedGo(t *testing.T) {
	contract := loadAsyncAPICatalogue(t, "LLMError")

	assert.Equal(t, contract.codes, LLMErrorCodes,
		"LLMErrorCodes is stale — re-run `make gen-contracts`")

	wantAttributions := make([]LLMErrorAttribution, 0, len(contract.attributions))
	for _, a := range contract.attributions {
		wantAttributions = append(wantAttributions, LLMErrorAttribution(a))
	}
	assert.Equal(t, wantAttributions, LLMErrorAttributionValues,
		"LLMErrorAttributionValues is stale — re-run `make gen-contracts`")

	assert.Len(t, LLMErrorUserMessages, len(contract.codes),
		"the generated message catalogue must have exactly one entry per code")
	assert.Len(t, LLMErrorUserAttributions, len(contract.codes),
		"the generated attribution catalogue must have exactly one entry per code")

	for _, code := range contract.codes {
		entry := contract.messages[code]
		require.NotNil(t, entry, "contract has no x-user-messages entry for %q", code)
		assert.Equal(t, entry["message"], LLMErrorUserMessages[code],
			"generated message for %q does not match the contract — re-run `make gen-contracts`", code)
		assert.Equal(t, LLMErrorAttribution(entry["attribution"]), LLMErrorUserAttributions[code],
			"generated attribution for %q does not match the contract — re-run `make gen-contracts`", code)
	}
}

// TestContract_LLMErrorCatalogue_GoAndTypeScriptAgree checks the SPA catalogue
// carries the same sentences as the Go one.
//
// Both are generated from a single contract block, so they cannot drift by
// hand — but they are generated by two INDEPENDENT programs
// (scripts/gen-asyncapi-go/usermessages.go and scripts/_gen-asyncapi-types.mjs).
// A divergence in either emitter — a stale committed artifact, an escaping bug
// that mangles the typographic apostrophes on one side only — would otherwise
// show up as the chat bubble and the persisted transcript quietly disagreeing,
// which is the exact failure the generated catalogue exists to prevent.
func TestContract_LLMErrorCatalogue_GoAndTypeScriptAgree(t *testing.T) {
	const target = "src/lib/api/generated/llm-error-messages.ts"
	raw, err := os.ReadFile(filepath.Join(repoRoot(), target))
	require.NoError(t, err, "could not read %s", target)
	ts := string(raw)

	for _, code := range LLMErrorCodes {
		t.Run(code, func(t *testing.T) {
			// The emitter writes `  <code>: "<message>",` via JSON.stringify,
			// so an exact substring match is a faithful comparison of both the
			// key and the fully-escaped value.
			message, err := jsonStringLiteral(LLMErrorUserMessages[code])
			require.NoError(t, err)
			assert.Contains(t, ts, "  "+code+": "+message+",",
				"%s is missing the Go catalogue's message for %q — one of the two emitters is stale",
				target, code)

			attribution, err := jsonStringLiteral(string(LLMErrorUserAttributions[code]))
			require.NoError(t, err)
			assert.Contains(t, ts, "  "+code+": "+attribution+",",
				"%s is missing the Go catalogue's attribution for %q", target, code)
		})
	}
}

// jsonStringLiteral renders s the way JavaScript's JSON.stringify would, so the
// Go-side expectation matches the TS emitter's output byte for byte.
//
// SetEscapeHTML(false) is required: Go's default Marshal escapes <, > and & as
// </>/&, which JSON.stringify does not — leaving it on would
// make this test fail the first time a message contains an ampersand, for a
// reason that has nothing to do with drift.
func jsonStringLiteral(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	// Encode appends a trailing newline.
	return strings.TrimRight(buf.String(), "\n"), nil
}
