// Omnipus — tests for the closed key sets of a schema declaration (ADR-068 D2/D3).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// yaml.v3 drops a key it has no field for, in silence. `label` and a `scale`
// were both defined by contracts/components/schemas/PropertyDef.yaml and both
// went nowhere: an author who wrote `label: Status` got no label, no rejection
// and no warning. `scale`'s wire `maximum: 12` even matched a Go constant of
// the same value, which made it look VERIFIED while nothing read it — a number
// agreeing with a number is not an enforcement. (`scale` and the type it
// belonged to are both deleted now; the lesson is why this file exists.)
//
// One fix would have been to parse those two keys. That leaves the CLASS open —
// the third contract key added would land in the same silence. So the parser
// now holds a closed set per declaration (propertyDeclKeys, schemaFileKeys,
// identityDeclKeys, enumValueDeclKeys) in which every key is PARSED or REFUSED,
// and anything else is rejected BY NAME.
//
// TestSchema_EveryContractPropertyKeyIsHandled is the part that has to keep
// working after everyone here has forgotten this: it reads PropertyDef.yaml off
// disk and proves, per key, that the parser does something observable with it.
// ---------------------------------------------------------------------------

// propertyDefContractPath is the contract this parser answers to.
func propertyDefContractPath(t *testing.T) string {
	t.Helper()
	// The test runs in its package directory; the contract lives at the repo
	// root. Resolved rather than assumed so a failure says which path was tried.
	path, err := filepath.Abs(filepath.Join("..", "..", "contracts", "components", "schemas", "PropertyDef.yaml"))
	if err != nil {
		t.Fatalf("resolving the contract path: %v", err)
	}
	return path
}

// contractPropertyKeys reads PropertyDef.yaml and returns the keys it defines,
// in the order the contract declares them.
//
// It deliberately does NOT skip on a missing file. A guard that quietly turns
// itself off when it cannot find its oracle is the same silent third state this
// whole file exists to close.
func contractPropertyKeys(t *testing.T) []string {
	t.Helper()
	path := propertyDefContractPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("this guard reads the contract itself; it cannot run without it. Reading %s: %v", path, err)
	}
	var doc struct {
		Properties yaml.Node `yaml:"properties"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if doc.Properties.Kind != yaml.MappingNode {
		t.Fatalf("%s declares no `properties` mapping; the contract's shape changed and this guard needs updating", path)
	}
	out := make([]string, 0, len(doc.Properties.Content)/2)
	for i := 0; i+1 < len(doc.Properties.Content); i += 2 {
		out = append(out, doc.Properties.Content[i].Value)
	}
	if len(out) == 0 {
		t.Fatalf("%s defines no property keys; that cannot be right", path)
	}
	return out
}

// parseFixtureProperty runs one property declaration through the REAL loading
// path — ParseSchema, not parseProperty — and returns either the parsed
// property or the rejection's reason.
func parseFixtureProperty(t *testing.T, decl string) (*Property, string) {
	t.Helper()
	src := "schema_version: 1\ntype: fixture\nproperties:\n  p: {" + decl + "}\n"
	sc, rej := ParseSchema("fixture.yaml", []byte(src))
	if rej != nil {
		if rej.Reason == "" {
			t.Fatalf("a rejection with an empty reason tells the operator nothing: %+v", rej)
		}
		return nil, rej.Reason
	}
	p, ok := sc.Property("p")
	if !ok {
		t.Fatalf("declaration %q parsed but produced no property", decl)
	}
	return p, ""
}

// contractKeyProbe is one key's evidence: the declaration WITHOUT it, and the
// `key: value` pair that adds it. Parsing both and comparing is what proves the
// parser does something with the key rather than dropping it.
type contractKeyProbe struct {
	base  string
	extra string
}

// contractKeyProbes covers every key PropertyDef.yaml defines except those in
// contractKeySkips. It is hand-written — a probe needs a plausible VALUE, and
// the contract's `example:` is not always usable (a `many: false` example is
// indistinguishable from the key being absent).
//
// Being hand-written does not make it a hand-maintained list that CLAIMS to be
// complete: the test fails on any contract key that has neither a probe nor a
// documented skip, so a new key cannot slip through by being forgotten here.
var contractKeyProbes = map[string]contractKeyProbe{
	"many":     {base: "type: text", extra: "many: true"},
	"required": {base: "type: text", extra: "required: true"},
	"label":    {base: "type: text", extra: "label: Status"},
	"values":   {base: "type: enum", extra: "values: [draft, shipped]"},
	"to":       {base: "type: relation", extra: "to: company"},
	"inverse":  {base: "type: relation, to: company", extra: "inverse: deals"},
	"unit":     {base: "type: integer", extra: "unit: minutes"},

	// `formula` IS A REFUSED KEY, and this probe lands in the `withErr != ""`
	// arm below: the refusal has to NAME it. That is the whole point of it
	// being published-and-refused rather than absent — a schema property
	// cannot carry a formula (nothing evaluates one; see propertyDeclKeys'
	// entry), but the contract defines the key, so an author who wrote it gets
	// told where a formula belongs instead of "unknown key".
	//
	// The base stays a valid declaration on purpose. If it were malformed the
	// row would go red for the wrong reason, and a later edit that quietly
	// stopped refusing `formula` would still look refused.
	"formula": {base: "type: decimal", extra: "formula: 1 + 1"},
}

// contractKeySkips are the keys a behavioural probe cannot express, each with
// the reason. A skip is a claim that has to survive reading, not a way to make
// a red test green.
var contractKeySkips = map[string]string{
	"name": "in a schema FILE the property name is the map key the declaration hangs off, " +
		"so it never appears inside the declaration; PropertyDef is that same declaration flattened for the wire",
	"type": "the declaration's own base — every probe below carries one, and a declaration without a type is rejected outright",
}

// TestSchema_EveryContractPropertyKeyIsHandled is the class guard: it reads
// PropertyDef.yaml and proves, key by key, that the parser either READS the key
// or REFUSES it by name. A key that is silently ignored fails here.
func TestSchema_EveryContractPropertyKeyIsHandled(t *testing.T) {
	keys := contractPropertyKeys(t)

	t.Run("every contract key is in the parser's closed set", func(t *testing.T) {
		for _, k := range keys {
			if _, skip := contractKeySkips[k]; skip {
				continue
			}
			if _, ok := propertyDeclKeys[k]; !ok {
				t.Errorf("PropertyDef.yaml defines %q but propertyDeclKeys does not mention it, so the parser drops it in silence. "+
					"Add it as declKeyParsed (and a field on propertyDecl) or as declKeyRefused with a reason — there is no third option", k)
			}
		}
	})

	t.Run("the parser mentions no key the contract does not define", func(t *testing.T) {
		defined := map[string]bool{}
		for _, k := range keys {
			defined[k] = true
		}
		for k := range propertyDeclKeys {
			if !defined[k] {
				t.Errorf("the parser entitles %q but PropertyDef.yaml does not define it; either the contract is behind or the key is dead", k)
			}
		}
	})

	t.Run("every contract key has a probe or a documented skip", func(t *testing.T) {
		for _, k := range keys {
			_, probed := contractKeyProbes[k]
			reason, skipped := contractKeySkips[k]
			if probed && skipped {
				t.Errorf("%q is both probed and skipped; pick one", k)
			}
			if !probed && !skipped {
				t.Errorf("PropertyDef.yaml defines %q and this guard has no probe for it. "+
					"Add one to contractKeyProbes, or a reason to contractKeySkips — a key with neither is exactly the state this test exists to prevent", k)
			}
			if skipped && strings.TrimSpace(reason) == "" {
				t.Errorf("%q is skipped with no reason given", k)
			}
		}
	})

	t.Run("no contract key is silently ignored", func(t *testing.T) {
		for _, k := range keys {
			probe, ok := contractKeyProbes[k]
			if !ok {
				continue // reported by the subtest above
			}
			withProp, withErr := parseFixtureProperty(t, probe.base+", "+probe.extra)
			withoutProp, withoutErr := parseFixtureProperty(t, probe.base)

			switch {
			case withErr != "":
				// Refused. The refusal has to name the key, or the operator is
				// told the declaration is wrong without being told where.
				if !strings.Contains(withErr, k) {
					t.Errorf("%q is refused, but the refusal does not name it: %q", k, withErr)
				}
			case withoutErr != "":
				// Adding the key turned a rejection into an acceptance, so it
				// is unambiguously read.
			case reflect.DeepEqual(withProp, withoutProp):
				t.Errorf("%q is SILENTLY IGNORED: %q and %q produce an identical property (%+v). "+
					"The author wrote something meaningful and was told nothing when it was thrown away",
					k, probe.base+", "+probe.extra, probe.base, withProp)
			}
		}
	})
}

// TestSchema_RefusedKeysCarryAReason holds the invariant the whole scheme rests
// on: a refused key is refused in words the operator can act on, and a parsed
// key does not carry a stray reason nobody will ever see.
func TestSchema_RefusedKeysCarryAReason(t *testing.T) {
	sets := map[string]map[string]declKey{
		"propertyDeclKeys":  propertyDeclKeys,
		"schemaFileKeys":    schemaFileKeys,
		"identityDeclKeys":  identityDeclKeys,
		"enumValueDeclKeys": enumValueDeclKeys,
	}
	for setName, set := range sets {
		for key, entry := range set {
			switch entry.kind {
			case declKeyRefused:
				if strings.TrimSpace(entry.reason) == "" {
					t.Errorf("%s[%q] is refused with no reason; the rejection would say `%s` and nothing else", setName, key, key)
				}
			case declKeyParsed:
				if entry.reason != "" {
					t.Errorf("%s[%q] is parsed but carries a refusal reason %q that is never shown", setName, key, entry.reason)
				}
			default:
				t.Errorf("%s[%q] has kind %d, which is neither parsed nor refused", setName, key, entry.kind)
			}
		}
	}
}

// TestSchema_PropertyLabelIsParsed covers the first of the two dropped keys.
func TestSchema_PropertyLabelIsParsed(t *testing.T) {
	t.Run("a property label reaches the Property", func(t *testing.T) {
		p, reason := parseFixtureProperty(t, "type: text, label: Status")
		if reason != "" {
			t.Fatalf("a labelled property must load; rejected: %s", reason)
		}
		if p.Label != "Status" {
			t.Fatalf("PropertyDef.label must be read; Label = %q", p.Label)
		}
	})

	t.Run("an absent label leaves Label empty, so a consumer can fall back to Name", func(t *testing.T) {
		p, reason := parseFixtureProperty(t, "type: text")
		if reason != "" {
			t.Fatalf("unexpected rejection: %s", reason)
		}
		if p.Label != "" {
			t.Fatalf("absent means render `name`; Label = %q", p.Label)
		}
	})

	t.Run("a label is legal on every one of the seven property types", func(t *testing.T) {
		// The contract puts no type restriction on `label`, so neither may we.
		for _, pt := range PropertyTypes {
			decl := "type: " + string(pt) + ", label: Shown"
			switch pt {
			case TypeEnum:
				decl += ", values: [draft]"
			case TypeRelation:
				decl += ", to: company"
			}
			p, reason := parseFixtureProperty(t, decl)
			if reason != "" {
				t.Errorf("%s: a label must be accepted; rejected: %s", pt, reason)
				continue
			}
			if p.Label != "Shown" {
				t.Errorf("%s: Label = %q, want %q", pt, p.Label, "Shown")
			}
		}
	})

	t.Run("NewProperty trims a label the same way the loader does", func(t *testing.T) {
		p, err := NewProperty(Property{Name: "status", Type: TypeText, Label: "  Status  ", RecordType: "fixture"})
		if err != nil {
			t.Fatalf("NewProperty: %v", err)
		}
		if p.Label != "Status" {
			t.Fatalf("the two construction paths must agree on a label; got %q", p.Label)
		}
	})
}

// TestSchema_PropertyScaleIsRejected covers the second dropped key.
//
// THE DISPOSITION CHANGED WITH THE TYPE, and the change is worth stating rather
// than quietly editing. `scale` used to be a declKeyRefused entry with its own
// reason, because it was a property-level declaration for the `money` type and
// nothing enforced it. ADR-068 D3 deletes that type, so `scale` is now simply
// an UNKNOWN key: there is no remedy to point the author at, because there is
// no per-value form to send them to any more.
//
// What survives unchanged is the property this whole file exists for — a key
// the parser does not read is REJECTED BY NAME, never dropped in silence.
func TestSchema_PropertyScaleIsRejected(t *testing.T) {
	t.Run("a property declaring a scale is rejected, naming the key", func(t *testing.T) {
		_, reason := parseFixtureProperty(t, "type: decimal, scale: 2")
		if reason == "" {
			t.Fatalf("`scale` must not be accepted while nothing reads it")
		}
		if !strings.Contains(reason, "scale") {
			t.Fatalf("the rejection must name the key the operator has to change; got %q", reason)
		}
		// A rejection that does not say what IS allowed makes the operator go
		// and read our source. The unknown-key path lists the permitted set.
		for _, k := range []string{"type", "many", "required", "unit"} {
			if !strings.Contains(reason, k) {
				t.Fatalf("the rejection must list what a property declaration DOES carry; %q is missing from %q", k, reason)
			}
		}
	})

	t.Run("the rejection does not depend on the property type", func(t *testing.T) {
		// It must not be the case that only numeric declarations are checked.
		_, reason := parseFixtureProperty(t, "type: text, scale: 2")
		if reason == "" || !strings.Contains(reason, "scale") {
			t.Fatalf("`scale` on a text property must be rejected naming the key; got %q", reason)
		}
	})

	t.Run("a decimal with no scale still loads, and its bound is the parser's own", func(t *testing.T) {
		p, reason := parseFixtureProperty(t, "type: decimal")
		if reason != "" {
			t.Fatalf("rejecting `scale` must not make decimal undeclarable; rejected: %s", reason)
		}
		if p.Type != TypeDecimal {
			t.Fatalf("Type = %q, want decimal", p.Type)
		}
		// FR-013: the bound is maxDecimalScale, and it is enforced per VALUE.
		// The retired 12-place, currency-shaped bound must NOT have been
		// inherited — 20 decimal places is past 12 and well inside 100.
		node := Node{Kind: KindScalar, Text: "0.12345678901234567890"}
		if _, verr := ParseValue(p, node); verr != nil {
			t.Fatalf("FR-013: a decimal must carry far more than twelve places; %q was refused: %v", node.Text, verr)
		}
	})
}

// TestSchema_UnknownKeysAreRefused is the class fix itself, at each of the four
// declarations that decode a mapping.
func TestSchema_UnknownKeysAreRefused(t *testing.T) {
	t.Run("a property declaration", func(t *testing.T) {
		_, rej := ParseSchema("fixture.yaml", []byte("schema_version: 1\ntype: fixture\nproperties:\n  p: {type: text, labl: Status}\n"))
		if rej == nil {
			t.Fatalf("`labl` must be refused; a one-letter typo silently produced an unlabelled property")
		}
		if rej.Code != RejectBadProperty {
			t.Fatalf("code = %q, want %q", rej.Code, RejectBadProperty)
		}
		for _, want := range []string{"labl", "p"} {
			if !strings.Contains(rej.Reason, want) {
				t.Fatalf("the rejection must name %q; got %q", want, rej.Reason)
			}
		}
		if !strings.Contains(rej.Reason, "`label`") {
			t.Fatalf("the rejection must list what IS permitted, or the operator has to read our source; got %q", rej.Reason)
		}
	})

	t.Run("a schema file's top level", func(t *testing.T) {
		_, rej := ParseSchema("fixture.yaml", []byte("schema_version: 1\ntype: fixture\nlabl: Fixture\nproperties:\n  p: {type: text}\n"))
		if rej == nil {
			t.Fatalf("an unknown top-level key must be refused")
		}
		if rej.Code != RejectUnknownKey {
			t.Fatalf("code = %q, want %q", rej.Code, RejectUnknownKey)
		}
		if !strings.Contains(rej.Reason, "labl") {
			t.Fatalf("the rejection must name the key; got %q", rej.Reason)
		}
		if rej.Type != "fixture" {
			t.Fatalf("the rejection must carry the declared type so a report can group by it; got %q", rej.Type)
		}
	})

	t.Run("an identity block", func(t *testing.T) {
		_, rej := ParseSchema("fixture.yaml", []byte("schema_version: 1\ntype: fixture\nidentity:\n  prefx: FI\nproperties:\n  p: {type: text}\n"))
		if rej == nil {
			t.Fatalf("`prefx` must be refused; the identity prefix silently went missing (D7)")
		}
		if rej.Code != RejectUnknownKey {
			t.Fatalf("code = %q, want %q", rej.Code, RejectUnknownKey)
		}
		if !strings.Contains(rej.Reason, "prefx") {
			t.Fatalf("the rejection must name the key; got %q", rej.Reason)
		}
	})

	t.Run("an enum value's long form", func(t *testing.T) {
		_, rej := ParseSchema("fixture.yaml", []byte("schema_version: 1\ntype: fixture\nproperties:\n  p: {type: enum, values: [{name: draft, grop: open}]}\n"))
		if rej == nil {
			t.Fatalf("`grop` must be refused; the value's lifecycle group silently went missing (D4)")
		}
		if !strings.Contains(rej.Reason, "grop") {
			t.Fatalf("the rejection must name the key; got %q", rej.Reason)
		}
		if !strings.Contains(rej.Reason, "position 0") {
			t.Fatalf("the rejection must locate which value it is; got %q", rej.Reason)
		}
	})

	t.Run("a declaration reached through an alias is checked through the alias", func(t *testing.T) {
		// The anchor is on the `identity` block, so the aliased mapping is NOT
		// itself a property declaration and its keys are seen for the first
		// time here. Stop following the alias and the check reads a node with
		// no Content, finds nothing to object to, and the operator is told the
		// declaration has no `type` — true, but not the fault.
		src := "schema_version: 1\ntype: fixture\nidentity: &shared\n  prefix: FI\nproperties:\n  p: *shared\n"
		_, rej := ParseSchema("fixture.yaml", []byte(src))
		if rej == nil {
			t.Fatalf("an alias to a non-declaration must be refused")
		}
		if !strings.Contains(rej.Reason, "prefix") {
			t.Fatalf("the refusal must name the key the alias actually contributed, not merely the missing `type`; got %q", rej.Reason)
		}
	})

	t.Run("an unsupported schema_version is reported as a version, not as unknown keys", func(t *testing.T) {
		// A version this release does not understand may legitimately carry
		// keys it does not know. Reporting those keys would send the operator
		// to fix the wrong thing.
		_, rej := ParseSchema("fixture.yaml", []byte("schema_version: 2\ntype: fixture\nfuture_key: x\nproperties:\n  p: {type: text}\n"))
		if rej == nil {
			t.Fatalf("schema_version 2 must be rejected")
		}
		if rej.Code != RejectUnsupportedVersion {
			t.Fatalf("code = %q, want %q — the version is the operator's actual problem", rej.Code, RejectUnsupportedVersion)
		}
	})
}

// TestSchema_EnumValueLabelIsParsed covers EnumValueDef.label, which the long
// form dropped in exactly the same silence as the property label.
func TestSchema_EnumValueLabelIsParsed(t *testing.T) {
	sc, rej := ParseSchema("fixture.yaml", []byte(
		"schema_version: 1\ntype: fixture\nproperties:\n  p:\n    type: enum\n    values:\n      - {name: draft, label: Draft, group: open}\n      - shipped\n"))
	if rej != nil {
		t.Fatalf("unexpected rejection: %s", rej.Reason)
	}
	p, ok := sc.Property("p")
	if !ok {
		t.Fatalf("property did not load")
	}
	if len(p.Values) != 2 {
		t.Fatalf("expected 2 enum values, got %d", len(p.Values))
	}
	if p.Values[0].Label != "Draft" {
		t.Fatalf("EnumValueDef.label must be read; Label = %q", p.Values[0].Label)
	}
	if p.Values[0].Group != "open" {
		t.Fatalf("adding `label` must not disturb `group`; Group = %q", p.Values[0].Group)
	}
	if p.Values[1].Label != "" || p.Values[1].Name != "shipped" {
		t.Fatalf("the short form must still work: %+v", p.Values[1])
	}
	// Membership is ResolveEnum's job, and a label is not part of it.
	v, ok := p.ResolveEnum("shipped")
	if !ok || v.Name != "shipped" {
		t.Fatalf("ResolveEnum(shipped) = %+v, %v; want the declared value, true", v, ok)
	}
}

// TestSchema_SharedDeclarationsStillLoad guards the CAUTION on the class fix:
// the key check must not refuse a declaration a real vault legitimately writes.
// YAML anchors, aliases and merge keys all decode today, so they must all still
// decode — while the keys they contribute stay checked.
func TestSchema_SharedDeclarationsStillLoad(t *testing.T) {
	t.Run("an aliased declaration loads and is checked", func(t *testing.T) {
		sc, rej := ParseSchema("fixture.yaml", []byte(
			"schema_version: 1\ntype: fixture\nproperties:\n  a: &shared {type: text, required: true}\n  b: *shared\n"))
		if rej != nil {
			t.Fatalf("an aliased declaration must still load; rejected: %s", rej.Reason)
		}
		b, ok := sc.Property("b")
		if !ok {
			t.Fatalf("the aliased property did not load")
		}
		if b.Type != TypeText || !b.Required {
			t.Fatalf("the alias must carry the anchored declaration; got %+v", b)
		}
	})

	t.Run("a merge key loads and the merged keys are checked", func(t *testing.T) {
		src := "schema_version: 1\ntype: fixture\nproperties:\n  a: &base {type: text, required: true}\n  b:\n    <<: *base\n    many: true\n"
		sc, rej := ParseSchema("fixture.yaml", []byte(src))
		if rej != nil {
			t.Fatalf("`<<` decodes in yaml.v3, so a shared-defaults schema must still load; rejected: %s", rej.Reason)
		}
		b, ok := sc.Property("b")
		if !ok {
			t.Fatalf("the merged property did not load")
		}
		if b.Type != TypeText || !b.Required || !b.Many {
			t.Fatalf("the merge must contribute the base keys and the local one; got %+v", b)
		}
	})

	t.Run("a typo alongside a merge key is still refused", func(t *testing.T) {
		src := "schema_version: 1\ntype: fixture\nproperties:\n  a: &base {type: text}\n  b:\n    <<: *base\n    labl: X\n"
		_, rej := ParseSchema("fixture.yaml", []byte(src))
		if rej == nil {
			t.Fatalf("a merge key must not become a hole the check does not look through")
		}
		if !strings.Contains(rej.Reason, "labl") {
			t.Fatalf("the rejection must name the key; got %q", rej.Reason)
		}
	})
}
