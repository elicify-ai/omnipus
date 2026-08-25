//go:build !windows

// Must match contract_test.go's constraint: the mustPassComponent /
// mustFailComponent / validateAgainstComponentSchemaRawJSON helpers this file
// uses are defined there, and that file is !windows. Without the same tag this
// file compiles on Windows WITHOUT its helpers and fails as "undefined".

package generated

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ── Vault records — ADR-068 D13/D19, spec FR-025, FR-090, FR-091 ─────────────
// Traces to: contracts/components/schemas/RecordQueryResponse.yaml
//
// The guarantee under test is structural, not behavioural: a caller must not be
// able to receive records without also receiving the verdict on them. ADR-068
// D19 states it as "RecordQueryResponse.complete and .problems are REQUIRED
// fields, not optional", and the reason it has to be structural rather than a
// convention is that a convention is exactly what fails silently — a server
// under load omits the fields, a client stops reading them, and nobody finds
// out until a total that excluded half the corpus is acted on.
//
// "Required" has to hold in two places at once, and this file checks both,
// because either one alone is defeatable:
//
//   1. THE SCHEMA — `required:` in the YAML. Checked by reading the contract
//      itself, so loosening the schema fails here IMMEDIATELY, without waiting
//      for anyone to regenerate.
//   2. THE GENERATED GO TYPE — a non-pointer field with no `omitempty`. This
//      is what makes the guarantee physical: `bool` and `[]RecordProblem`
//      always marshal a key, whereas `*bool` with `omitempty` can vanish from
//      the JSON entirely while the producing code still compiles.
//
// Checking only the schema would miss a generator change that emits pointers
// anyway; checking only the Go type would miss a schema loosening that nobody
// has regenerated yet. The pair is the test.

// requiredJSONField finds a field of typ by its json tag name and asserts it is
// present, non-pointer and NOT omitempty — i.e. that marshaling the zero value
// of typ still emits the key.
func requiredJSONField(t *testing.T, typ reflect.Type, jsonName string) reflect.StructField {
	t.Helper()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		parts := strings.Split(tag, ",")
		if parts[0] != jsonName {
			continue
		}

		assert.NotEqual(t, reflect.Ptr, f.Type.Kind(),
			"%s.%s (json:%q) is a POINTER. A pointer field is absent-able: it "+
				"marshals to null or, with omitempty, disappears — which is the "+
				"optional field ADR-068 D19 forbids.", typ.Name(), f.Name, jsonName)

		assert.NotContains(t, parts[1:], "omitempty",
			"%s.%s (json:%q) carries omitempty. The key can then be missing from "+
				"the wire payload entirely, so a caller can hold records without "+
				"holding the verdict on them.", typ.Name(), f.Name, jsonName)

		return f
	}

	t.Fatalf("%s has no field with json tag %q at all — the field the whole "+
		"design rests on is gone from the generated type", typ.Name(), jsonName)
	return reflect.StructField{}
}

// componentSchemaRequired reads a component schema file straight from
// contracts/ and returns its top-level `required` list. Deliberately reads the
// CONTRACT rather than the generated artifact: this is the half of the test
// that fires the moment someone loosens the YAML, regeneration or not.
func componentSchemaRequired(t *testing.T, schemaName string) []string {
	t.Helper()

	path := filepath.Join(contractsDir(), "components", "schemas", schemaName+".yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "could not read component schema %s", path)

	var doc struct {
		Required []string `yaml:"required"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc), "could not parse %s", path)
	return doc.Required
}

// TestContract_CompletenessFieldsAreRequired is the test named in the vault
// records spec's traceability table for FR-090/FR-091.
func TestContract_CompletenessFieldsAreRequired(t *testing.T) {
	// ── 1. The contract itself ────────────────────────────────────────────
	required := componentSchemaRequired(t, "RecordQueryResponse")
	assert.Contains(t, required, "complete",
		"RecordQueryResponse.complete must be listed under `required:` in "+
			"contracts/components/schemas/RecordQueryResponse.yaml (ADR-068 D19, FR-091)")
	assert.Contains(t, required, "problems",
		"RecordQueryResponse.problems must be listed under `required:` in "+
			"contracts/components/schemas/RecordQueryResponse.yaml (ADR-068 D19, FR-091)")
	assert.Contains(t, required, "records",
		"records must be required too — otherwise a response can carry a verdict "+
			"with no result set and mean nothing at all")

	// ── 2. The generated Go type ──────────────────────────────────────────
	typ := reflect.TypeOf(RecordQueryResponse{})

	complete := requiredJSONField(t, typ, "complete")
	assert.Equal(t, reflect.Bool, complete.Type.Kind(),
		"RecordQueryResponse.Complete must be a plain bool — a *bool has three "+
			"states (true / false / absent) where the design permits two")

	problems := requiredJSONField(t, typ, "problems")
	require.Equal(t, reflect.Slice, problems.Type.Kind(),
		"RecordQueryResponse.Problems must be a plain slice, not a pointer-to-slice")
	assert.Equal(t, "RecordProblem", problems.Type.Elem().Name(),
		"Problems must carry the NAMED contract type RecordProblem. An anonymous "+
			"inline struct is a JSON-shape-coincident copy no handler can name, and "+
			"it stops matching the schema the day a field is added (Constraint #8)")

	records := requiredJSONField(t, typ, "records")
	require.Equal(t, reflect.Slice, records.Type.Kind())
	assert.Equal(t, "VaultRecord", records.Type.Elem().Name())

	// `refused` is the third leg of D15.1b: "here is none of it, narrow and
	// re-ask" must be distinguishable from "here is some of it" without
	// parsing prose, so it cannot be an absent-able field either.
	assert.Contains(t, required, "refused")
	refused := requiredJSONField(t, typ, "refused")
	assert.Equal(t, reflect.Bool, refused.Type.Kind())
}

// TestContract_ValidationReport_CompletenessFieldsAreRequired applies the same
// rule to record_validate's report. A validation run that could not cover
// everything and does not say so certifies a corpus nobody checked.
func TestContract_ValidationReport_CompletenessFieldsAreRequired(t *testing.T) {
	required := componentSchemaRequired(t, "ValidationReport")
	assert.Contains(t, required, "complete")
	assert.Contains(t, required, "problems")

	typ := reflect.TypeOf(ValidationReport{})
	assert.Equal(t, reflect.Bool, requiredJSONField(t, typ, "complete").Type.Kind())
	assert.Equal(t, reflect.Slice, requiredJSONField(t, typ, "problems").Type.Kind())
}

// ── The same guarantee, exercised through the schema validator ───────────────

// fixtures are built by DECODING JSON rather than by writing a Go struct
// literal, deliberately. A struct literal would stop compiling the moment
// `complete` became a *bool — which reads as a failure, but it is a BUILD
// break that happens before a single assertion runs, so it proves nothing
// about whether the assertions above can actually detect the change. Decoding
// keeps every test in this file compiling under any field shape, which leaves
// the reflection and schema assertions as the sole judges of that shape.
func decodeRecordQueryResponse(t *testing.T, raw string) RecordQueryResponse {
	t.Helper()
	var r RecordQueryResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &r), "fixture JSON must decode")
	return r
}

const validRecordQueryResponseJSON = `{
  "records": [],
  "complete": false,
  "refused": false,
  "limit_applied": 50,
  "limit_clamped": false,
  "problems": [{
    "code": "type_mismatch",
    "reason": "company is text, not a relation — cannot be resolved",
    "records": ["CO-0052"],
    "expected": "expected a link to a company record"
  }]
}`

// refusalRecordQueryResponseJSON is FR-064/FR-066's shape: the candidate set
// exceeded the materialisation bound, so there is no answer at all — an empty
// record list, no aggregate, and a narrowing instruction.
const refusalRecordQueryResponseJSON = `{
  "records": [],
  "complete": false,
  "refused": true,
  "limit_applied": 50,
  "limit_clamped": false,
  "problems": [{
    "code": "candidate_cap_exceeded",
    "reason": "candidate set of 41,208 records exceeds the 10,000-record bound",
    "records": [],
    "fix": "add a status filter, or narrow the date range"
  }]
}`

func TestContract_RecordQueryResponse_Populated(t *testing.T) {
	mustPassComponent(t, "RecordQueryResponse",
		decodeRecordQueryResponse(t, validRecordQueryResponseJSON))
}

func TestContract_RecordQueryResponse_ZeroValueRejected(t *testing.T) {
	// The zero value marshals problems and records as null (nil slices) and
	// limit_applied as 0, which the minimum:1 constraint rejects. This is the
	// shape a producer yields if it returns a bare struct on an early exit —
	// the exact path by which "no problems" would otherwise be indistinguishable
	// from "the server never looked".
	mustFailComponent(t, "RecordQueryResponse", RecordQueryResponse{},
		"nil slices marshal to null, which is not an array; limit_applied 0 is below minimum")
	_ = json.Unmarshal // keep encoding/json referenced regardless of field shapes
}

func TestContract_RecordQueryResponse_NilProblemsRejected(t *testing.T) {
	// A producer that returns early without populating problems. The nil slice
	// marshals to "problems":null, and null is not an array — so "nothing to
	// report" has to be spelled [], which is a claim, rather than null, which
	// is silence wearing a field name.
	raw := []byte(`{"records":[],"complete":true,"refused":false,` +
		`"limit_applied":50,"limit_clamped":false,"problems":null}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordQueryResponse", raw)
	assert.Error(t, err, `"problems":null must be rejected; the empty case is []`)
}

func TestContract_RecordQueryResponse_MissingCompleteRejected(t *testing.T) {
	// Hand-built JSON, because the generated Go type cannot express this shape
	// at all — which is the point of the reflection test above. This is what a
	// non-Go producer, or a hand-rolled response, would put on the wire.
	raw := []byte(`{"records":[],"problems":[],"refused":false,` +
		`"limit_applied":50,"limit_clamped":false}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordQueryResponse", raw)
	assert.Error(t, err,
		"a response omitting `complete` must be schema-INVALID: records without a "+
			"completeness verdict is the exact payload ADR-068 D13 exists to forbid")
}

func TestContract_RecordQueryResponse_MissingProblemsRejected(t *testing.T) {
	raw := []byte(`{"records":[],"complete":true,"refused":false,` +
		`"limit_applied":50,"limit_clamped":false}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordQueryResponse", raw)
	assert.Error(t, err,
		"a response omitting `problems` must be schema-INVALID: `complete:true` with "+
			"no problem list is an unfalsifiable claim")
}

// ── Money can never become a float ───────────────────────────────────────────
// Traces to: contracts/components/schemas/RecordMoney.yaml, FR-012, FR-013, FR-020b.

func TestContract_RecordMoney_AmountIsAStringNotANumber(t *testing.T) {
	typ := reflect.TypeOf(RecordMoney{})

	amount := requiredJSONField(t, typ, "amount")
	assert.Equal(t, reflect.String, amount.Type.Kind(),
		"RecordMoney.Amount must be a string. `type: number` in the contract "+
			"generates a Go float32 — float64 only with format: double — and a "+
			"JavaScript number, and binary floating "+
			"point cannot represent 0.1 exactly — FR-020b forbids a binary float "+
			"anywhere in the path, and the wire is part of the path")

	currency := requiredJSONField(t, typ, "currency")
	assert.Equal(t, reflect.String, currency.Type.Kind())

	// Scale is an integer and required: it is what makes the decimal string
	// convertible to integer minor units without a rounding decision.
	scale := requiredJSONField(t, typ, "scale")
	assert.Equal(t, reflect.Int, scale.Type.Kind())

	required := componentSchemaRequired(t, "RecordMoney")
	assert.ElementsMatch(t, []string{"amount", "currency", "scale"}, required,
		"all three travel together or the value means nothing (FR-012)")
}

func TestContract_RecordMoney_Populated(t *testing.T) {
	// Decoded rather than written as a struct literal, for the same reason the
	// query-response fixtures are: if `amount` ever became a JSON number, a
	// literal would stop COMPILING and the assertions above would never run.
	// Decoding turns that same change into a visible test failure instead.
	var m RecordMoney
	require.NoError(t, json.Unmarshal(
		[]byte(`{"amount":"1250000.00","currency":"USD","scale":2}`), &m),
		"a decimal-string amount must decode into the generated type; if this "+
			"fails, RecordMoney.Amount is no longer a string")
	mustPassComponent(t, "RecordMoney", m)
}

func TestContract_RecordMoney_NumericAmountRejected(t *testing.T) {
	// The shape a careless producer emits. It must not validate, or the
	// float-free guarantee is decorative.
	raw := []byte(`{"amount":1250000.00,"currency":"USD","scale":2}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordMoney", raw)
	assert.Error(t, err, "a JSON number amount must be rejected; amount is a decimal string")
}

func TestContract_RecordMoney_MissingCurrencyRejected(t *testing.T) {
	raw := []byte(`{"amount":"1250000.00","scale":2}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordMoney", raw)
	assert.Error(t, err, "a money value missing currency must be rejected (FR-012)")
}

func TestContract_RecordMoney_LowercaseCurrencyRejected(t *testing.T) {
	raw := []byte(`{"amount":"1250000.00","currency":"usd","scale":2}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordMoney", raw)
	assert.Error(t, err, "ISO-4217 codes are upper case; accepting both spellings forks the currency")
}

func TestContract_RecordMoney_ExponentAmountRejected(t *testing.T) {
	// "1.25e6" is what a float that has been through a JSON round trip looks
	// like when someone stringifies it. Accepting it would readmit the float.
	raw := []byte(`{"amount":"1.25e6","currency":"USD","scale":2}`)
	err := validateAgainstComponentSchemaRawJSON(t, "RecordMoney", raw)
	assert.Error(t, err, "exponent notation is a stringified float, not a decimal amount")
}

// ── The seven property types are closed ──────────────────────────────────────
// Traces to: FR-004. An eighth type added anywhere would mean a value the
// validator, the index and the query evaluator do not all agree on.

func TestContract_PropertyDef_SevenTypesExactly(t *testing.T) {
	path := filepath.Join(contractsDir(), "components", "schemas", "PropertyDef.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		Properties struct {
			Type struct {
				Enum []string `yaml:"enum"`
			} `yaml:"type"`
		} `yaml:"properties"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	assert.ElementsMatch(t,
		[]string{"text", "enum", "relation", "date", "number", "money", "person"},
		doc.Properties.Type.Enum,
		"FR-004: exactly these seven property types, no more and no fewer")
}

// TestContract_PropertyDef_ArityAndPresenceAreRequired guards D3.1. `many`
// carrying no value on the wire is the ambiguity that lets a scalar property
// silently become a list — after which every query written against it returns
// nothing, with no error.
func TestContract_PropertyDef_ArityAndPresenceAreRequired(t *testing.T) {
	required := componentSchemaRequired(t, "PropertyDef")
	assert.Contains(t, required, "many", "arity must be stated, never inferred (D3.1)")
	assert.Contains(t, required, "required", "presence must be stated, never inferred")

	typ := reflect.TypeOf(PropertyDef{})
	assert.Equal(t, reflect.Bool, requiredJSONField(t, typ, "many").Type.Kind())
	assert.Equal(t, reflect.Bool, requiredJSONField(t, typ, "required").Type.Kind())
}

// ── ADR-068 D0: the product ships no record types of its own ─────────────────
// The prohibition is on SHIPPED TYPES, and a contract is where one would most
// plausibly be smuggled in — as an enum of "known" record types, or a schema
// named after a business object. This asserts the record contracts carry no
// such enum: `type` is an open string everywhere, populated from the vault.

func TestContract_D0_NoBuiltInRecordTypesInTheContract(t *testing.T) {
	for _, schemaName := range []string{"RecordType", "VaultRecord", "RecordQueryRequest", "ViewDef"} {
		path := filepath.Join(contractsDir(), "components", "schemas", schemaName+".yaml")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var doc struct {
			Properties map[string]struct {
				Type string   `yaml:"type"`
				Enum []string `yaml:"enum"`
			} `yaml:"properties"`
		}
		require.NoError(t, yaml.Unmarshal(raw, &doc))

		typeProp, ok := doc.Properties["type"]
		require.True(t, ok, "%s must carry a `type` property", schemaName)
		assert.Equal(t, "string", typeProp.Type)
		assert.Empty(t, typeProp.Enum,
			"%s.type must be an OPEN string. An enum here would ship a closed set "+
				"of record types the product decided on — exactly what ADR-068 D0 "+
				"forbids, since a shipped default becomes the de-facto standard and "+
				"stops being questioned", schemaName)
	}
}

// ── A worked example: the response a caller must not be able to misread ──────

func TestContract_RecordQueryResponse_RefusalCarriesNoPartialAnswer(t *testing.T) {
	// FR-064/FR-066: a candidate set over the materialisation bound is refused
	// with a narrowing instruction, and NO aggregate is returned. The shape is
	// a normal 200 response, so `refused` and `complete` are the only things
	// standing between the caller and a total over nothing.
	r := decodeRecordQueryResponse(t, refusalRecordQueryResponseJSON)
	mustPassComponent(t, "RecordQueryResponse", r)

	// And the same payload marshaled: complete and problems are both present
	// as keys, unconditionally, because neither field can be omitted.
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"complete":false`)
	assert.Contains(t, string(raw), `"problems":[`)
	assert.Contains(t, string(raw), `"refused":true`)
}
