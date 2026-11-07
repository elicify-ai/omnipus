package catalog

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// R4: run the actual generated SPA validator, not a hand-copied minimum check.
// Node 24 and node_modules are provisioned by the existing Go CI job. This
// child is a schema evaluator, not a second Go/vitest test process.
func issue1435R2GeneratedCatalogValidation(t *testing.T, body []byte) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("BLOCKED: Node runtime for the generated validator is unavailable — required by #1435 Round 2 R4")
	}
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	schema := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "src", "lib", "api", "generated", "schemas.ts"))
	const program = `
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
const { ProvidersCatalog } = await import(pathToFileURL(process.argv[1]).href);
const result = ProvidersCatalog.safeParse(JSON.parse(readFileSync(0, 'utf8')));
console.log(JSON.stringify({ success: result.success, issues: result.success ? [] : result.error.issues }));
`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", program, schema)
	cmd.Stdin = bytes.NewReader(body)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "generated-validator instrument could not execute: %s", output)
	var result map[string]any
	require.NoError(t, json.Unmarshal(output, &result), "validator must emit its actual verdict: %s", output)
	return result
}

func TestIssue1435R2_CatalogOptionalPatchLimitValidatesAfterServing(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  int
	}{
		{"positive_limit_control", 30_000},
		{"minimum_positive_control", 1},
		{"omitted_limit", 0}, // 0 here selects omission in the INPUT, never a valid wire value.
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := fixtureMap(t)
			limits, ok := input["default_resize_limits"].(map[string]any)
			require.True(t, ok)
			delete(limits, "max_patches")
			if tc.cap > 0 {
				limits["max_patches"] = tc.cap
			}
			for i := range providers(t, input) {
				row := provider(t, input, i)
				if raw, present := row["resize_limits"]; present {
					providerLimits, isMap := raw.(map[string]any)
					require.True(t, isMap)
					delete(providerLimits, "max_patches")
					if tc.cap > 0 {
						providerLimits["max_patches"] = tc.cap
					}
				}
			}
			cat, err := NewCatalog(encode(t, input))
			require.NoError(t, err, "the optional-field input is a valid catalog")
			served, ok := cat.Served()
			require.True(t, ok, "validate actual pre-serialized response bytes")
			verdict := issue1435R2GeneratedCatalogValidation(t, served.Body)
			require.Equal(t, true, verdict["success"],
				"#1435 R4: a valid catalog must stay valid across parser -> served response -> generated client validator; issues=%v", verdict["issues"])
			if tc.cap > 0 {
				// Negative control: prove this very generated instrument rejects
				// the invalid zero, rather than merely accepting every envelope.
				var invalid map[string]any
				require.NoError(t, json.Unmarshal(served.Body, &invalid))
				invalidLimits, ok := invalid["default_resize_limits"].(map[string]any)
				require.True(t, ok)
				invalidLimits["max_patches"] = 0
				mutant, err := json.Marshal(invalid)
				require.NoError(t, err)
				rejected := issue1435R2GeneratedCatalogValidation(t, mutant)
				require.Equal(t, false, rejected["success"], "instrument must detect the contract's minimum=1")
			}
		})
	}
}
