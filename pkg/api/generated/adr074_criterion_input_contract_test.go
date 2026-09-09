// Omnipus — ADR-074 D2 AcceptanceCriterionInput contract-parity guard
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package generated

// adr074_criterion_input_contract_test.go — judgment-first-criteria-spec test
// #6 (ADR-074 required-test #11 included), extended by ADR-080 D-TYPES:
// AcceptanceCriterionInput.yaml is DERIVED from the canonical
// AcceptanceCriterion.yaml — identical field set and shapes, with a
// `required` delta of exactly {kind, judgment} — and the generated
// TypeScript emits `kind`/`judgment` OPTIONAL on the Input type while the
// response type keeps them required (guarding the documented `default:`
// codegen trap, which would silently make the field non-optional again).

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// loadSchemaYAML parses one contracts/components/schemas/<name>.yaml file.
func loadSchemaYAML(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join(contractsDir(), "components", "schemas", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

// sortedKeys returns the sorted string keys of a schema node's map field.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stringSlice coerces a YAML []any of strings, sorted.
func stringSlice(t *testing.T, v any, what string) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a sequence", what, v)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("%s entry is %T, want string", what, e)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertSchemaShapeParity recursively compares the STRUCTURE of two schema
// nodes: property name sets, per-property `type`/`enum`, nested `required`
// lists, and nested properties — deliberately ignoring `description` and
// `example`, which legitimately differ between the canonical and the derived
// file. topLevel exempts the root `required` (its delta is asserted
// separately).
func assertSchemaShapeParity(t *testing.T, path string, canonical, derived map[string]any, topLevel bool) {
	t.Helper()

	if ct, dt := canonical["type"], derived["type"]; ct != dt {
		t.Errorf("%s: type differs: canonical %v vs derived %v", path, ct, dt)
	}
	if ce, ok := canonical["enum"]; ok {
		de, dok := derived["enum"]
		if !dok {
			t.Errorf("%s: enum missing from derived schema", path)
		} else if !equalStringSlices(stringSlice(t, ce, path+".enum"), stringSlice(t, de, path+".enum")) {
			t.Errorf("%s: enum differs: %v vs %v", path, ce, de)
		}
	}
	if !topLevel {
		cr, cok := canonical["required"]
		dr, dok := derived["required"]
		if cok != dok {
			t.Errorf("%s: required presence differs (canonical %v, derived %v)", path, cok, dok)
		} else if cok && !equalStringSlices(stringSlice(t, cr, path+".required"), stringSlice(t, dr, path+".required")) {
			t.Errorf("%s: required differs: %v vs %v", path, cr, dr)
		}
	}

	cp, cok := canonical["properties"].(map[string]any)
	dp, dok := derived["properties"].(map[string]any)
	if cok != dok {
		t.Errorf("%s: properties presence differs", path)
		return
	}
	if !cok {
		return
	}
	ck, dk := sortedKeys(cp), sortedKeys(dp)
	if !equalStringSlices(ck, dk) {
		t.Fatalf("%s: property sets differ: canonical %v vs derived %v — the Input schema is DERIVED "+
			"from AcceptanceCriterion.yaml and must mirror every field", path, ck, dk)
	}
	for _, k := range ck {
		cChild, cIsMap := cp[k].(map[string]any)
		dChild, dIsMap := dp[k].(map[string]any)
		if !cIsMap || !dIsMap {
			t.Errorf("%s.%s: property node is not a map (canonical %T, derived %T)", path, k, cp[k], dp[k])
			continue
		}
		assertSchemaShapeParity(t, path+"."+k, cChild, dChild, false)
	}
}

// TestAcceptanceCriterionInput_ParityWithCanonical asserts the derived
// authoring schema mirrors the canonical response schema field-for-field,
// with a root `required` delta of exactly {kind, judgment} — and that the
// Input's `kind`/`judgment` carry NO OpenAPI `default:` (the codegen trap:
// default + optional makes openapi-typescript emit the field non-optional).
func TestAcceptanceCriterionInput_ParityWithCanonical(t *testing.T) {
	t.Parallel()
	canonical := loadSchemaYAML(t, "AcceptanceCriterion.yaml")
	derived := loadSchemaYAML(t, "AcceptanceCriterionInput.yaml")

	assertSchemaShapeParity(t, "AcceptanceCriterion", canonical, derived, true)

	canonReq := stringSlice(t, canonical["required"], "canonical required")
	inputReq := stringSlice(t, derived["required"], "input required")
	wantCanon := []string{"author", "judgment", "kind", "status", "text"}
	wantInput := []string{"author", "status", "text"}
	if !equalStringSlices(canonReq, wantCanon) {
		t.Errorf("canonical required = %v, want %v", canonReq, wantCanon)
	}
	if !equalStringSlices(inputReq, wantInput) {
		t.Errorf("input required = %v, want %v — the delta must be exactly {kind, judgment}", inputReq, wantInput)
	}

	inputProps, ok := derived["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input properties is %T, want map", derived["properties"])
	}
	inputKind, ok := inputProps["kind"].(map[string]any)
	if !ok {
		t.Fatalf("input kind node is %T, want map", inputProps["kind"])
	}
	if _, has := inputKind["default"]; has {
		t.Error("AcceptanceCriterionInput.kind carries an OpenAPI `default:` — FORBIDDEN (ADR-074 D2 " +
			"codegen trap: default + absent-from-required makes openapi-typescript emit kind non-optional)")
	}
	inputJudgment, ok := inputProps["judgment"].(map[string]any)
	if !ok {
		t.Fatalf("input judgment node is %T, want map", inputProps["judgment"])
	}
	if _, has := inputJudgment["default"]; has {
		t.Error("AcceptanceCriterionInput.judgment carries an OpenAPI `default:` — FORBIDDEN (ADR-080 " +
			"D-TYPES codegen trap: default + absent-from-required makes openapi-typescript emit judgment non-optional)")
	}
}

// TestAcceptanceCriterionInput_GeneratedTSKindOptional is ADR-074 required-
// test #11, extended by ADR-080 D-TYPES: the generated TypeScript emits
// `kind`/`judgment` as OPTIONAL on the Input type and REQUIRED on the
// response type. Asserted against the committed generated artifact so a
// regeneration that regresses the trap fails here.
func TestAcceptanceCriterionInput_GeneratedTSKindOptional(t *testing.T) {
	t.Parallel()
	tsPath := filepath.Join(contractsDir(), "..", "src", "lib", "api", "generated", "openapi-types.ts")
	data, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("read generated TS %s: %v", tsPath, err)
	}
	src := string(data)

	// Match inside the named component blocks. The generated file nests each
	// schema under `AcceptanceCriterion: {` / `AcceptanceCriterionInput: {`.
	inputBlock := regexp.MustCompile(`(?s)AcceptanceCriterionInput: \{.*?\n {8}\};`).FindString(src)
	if inputBlock == "" {
		t.Fatal("AcceptanceCriterionInput block not found in generated openapi-types.ts")
	}
	if !regexp.MustCompile(`\bkind\?: `).MatchString(inputBlock) {
		t.Error("generated TS AcceptanceCriterionInput.kind is NOT optional (`kind?:`) — the ADR-074 D2 " +
			"relaxation was defeated (check for a stray OpenAPI `default:`)")
	}
	if !regexp.MustCompile(`\bjudgment\?: `).MatchString(inputBlock) {
		t.Error("generated TS AcceptanceCriterionInput.judgment is NOT optional (`judgment?:`) — the " +
			"ADR-080 D-TYPES relaxation was defeated (check for a stray OpenAPI `default:`)")
	}

	respBlock := regexp.MustCompile(`(?s)\n {8}AcceptanceCriterion: \{.*?\n {8}\};`).FindString(src)
	if respBlock == "" {
		t.Fatal("AcceptanceCriterion block not found in generated openapi-types.ts")
	}
	if !regexp.MustCompile(`\bkind: `).MatchString(respBlock) || regexp.MustCompile(`\bkind\?: `).MatchString(respBlock) {
		t.Error("generated TS AcceptanceCriterion.kind must stay REQUIRED on the response schema")
	}
	if !regexp.MustCompile(`\bjudgment: `).MatchString(respBlock) || regexp.MustCompile(`\bjudgment\?: `).MatchString(respBlock) {
		t.Error("generated TS AcceptanceCriterion.judgment must stay REQUIRED on the response schema")
	}
}
