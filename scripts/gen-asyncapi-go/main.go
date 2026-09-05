// Command gen-asyncapi-go generates Go structs from AsyncAPI 3 component schemas.
//
// Usage:
//
//	go run . <asyncapi.yaml> <types-output.go> [<llm-error-messages-output.go>]
//
// The generator reads the `components.schemas` section of the given AsyncAPI YAML
// file and emits a Go source file containing one struct per schema. Given the
// optional third path it ALSO emits the LLMError user-facing copy catalogue from
// the x-user-messages block on components.schemas.LLMError — see usermessages.go.
// It applies the following mapping rules:
//
//   - required fields → non-pointer Go types (string, int, bool, map[string]any, []T)
//   - optional fields → pointer types (*string, *int, *bool) or `,omitempty` for maps/slices
//   - additionalProperties: true → map[string]any
//   - type: object with properties → named struct (or map[string]any for open objects)
//   - type: array with items → []T
//   - type: string → string (or *string if optional)
//   - type: integer / number → int or float64 (or pointer variants)
//   - type: boolean → bool (or *bool if optional)
//   - $ref → resolved type name
//   - Cross-schema $ref → resolved Go type name
//   - inline-payload short-circuit: a property whose inline shape is structurally
//     identical to a sibling named schema `<OwnerWithoutFrame><PascalCase(propName)>`
//     is emitted as a pointer to (or value of, when required) that named type — this
//     preserves the hand-adjusted `*ErrorPayload` / `*ReplayErrorPayload` shape that
//     the Zod TDZ workaround previously required (see contracts/asyncapi.yaml comments
//     at ErrorFrame.payload / ReplayErrorFrame.payload).
//
// Required maps and slices are never nil — they use map[string]any and []T directly
// (not pointers) so the nil-safety contract from the AsyncAPI spec is upheld in Go.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr,
			"usage: gen-asyncapi-go <asyncapi.yaml> <types-output.go> [<llm-error-messages-output.go>]\n")
		os.Exit(1)
	}
	inputPath := os.Args[1]
	outputPath := os.Args[2]
	// Optional third output: the LLMError user-facing copy catalogue emitted
	// from components.schemas.LLMError's x-user-messages block. See
	// usermessages.go. Omitted by callers that only want the struct types.
	messagesOutputPath := ""
	if len(os.Args) == 4 {
		messagesOutputPath = os.Args[3]
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	var doc map[string]any
	err = yaml.Unmarshal(raw, &doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	schemas, err := extractSchemas(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract schemas: %v\n", err)
		os.Exit(1)
	}

	src, err := generate(schemas)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}

	formatted, err := format.Source(src)
	if err != nil {
		// Write the unformatted source for debugging, then fail.
		_ = os.WriteFile(outputPath, src, 0o644)
		fmt.Fprintf(os.Stderr, "gofmt: %v\n(unformatted source written to %s for inspection)\n", err, outputPath)
		os.Exit(1)
	}

	if writeErr := os.WriteFile(outputPath, formatted, 0o644); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outputPath, writeErr)
		os.Exit(1)
	}

	if messagesOutputPath == "" {
		return
	}

	catalogue, err := extractUserMessageCatalogue(doc, "LLMError")
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract LLMError user-message catalogue: %v\n", err)
		os.Exit(1)
	}
	messagesSrc, err := generateUserMessages(catalogue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate LLMError user-message catalogue: %v\n", err)
		os.Exit(1)
	}
	if writeErr := os.WriteFile(messagesOutputPath, messagesSrc, 0o644); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", messagesOutputPath, writeErr)
		os.Exit(1)
	}
}

// schema represents a parsed AsyncAPI/JSON Schema entry.
type schema struct {
	name            string
	title           string
	description     string
	schemaType      string // "object", "string", "integer", "number", "boolean", "array", ""
	format          string // e.g. "int64", "date-time" — refines schemaType
	properties      map[string]*schema
	propertyOrder   []string // preserves YAML key order
	required        map[string]bool
	items           *schema  // for array type
	ref             string   // $ref value (already resolved to schema name)
	enum            []string // for enum types
	additionalProps bool     // additionalProperties: true
	constValue      string   // const: value
	isEnum          bool     // this schema IS an enum type definition
	anyOf           []*schema
	oneOf           []*schema
}

func extractSchemas(doc map[string]any) (map[string]*schema, error) {
	components, ok := doc["components"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing components section")
	}
	rawSchemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing components.schemas section")
	}

	schemas := make(map[string]*schema)
	// First pass: parse all schemas by name
	for name, raw := range rawSchemas {
		s, err := parseSchema(name, raw)
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		schemas[name] = s
	}
	return schemas, nil
}

func parseSchema(name string, raw any) (*schema, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map, got %T", raw)
	}

	s := &schema{
		name:       name,
		properties: make(map[string]*schema),
		required:   make(map[string]bool),
	}

	if v, ok := m["title"].(string); ok {
		s.title = v
	}
	if v, ok := m["description"].(string); ok {
		s.description = v
	}
	if v, ok := m["type"].(string); ok {
		s.schemaType = v
	}
	if v, ok := m["format"].(string); ok {
		s.format = v
	}
	if v, ok := m["const"].(string); ok {
		s.constValue = v
	}

	// additionalProperties
	if v, ok := m["additionalProperties"]; ok {
		if b, ok := v.(bool); ok && b {
			s.additionalProps = true
		}
	}

	// required array
	if reqRaw, ok := m["required"].([]any); ok {
		for _, r := range reqRaw {
			if rs, ok := r.(string); ok {
				s.required[rs] = true
			}
		}
	}

	// enum
	if enumRaw, ok := m["enum"].([]any); ok {
		s.isEnum = true
		for _, e := range enumRaw {
			if es, ok := e.(string); ok {
				s.enum = append(s.enum, es)
			}
		}
	}

	// properties
	if propsRaw, ok := m["properties"].(map[string]any); ok {
		// Sort for determinism
		keys := make([]string, 0, len(propsRaw))
		for k := range propsRaw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		s.propertyOrder = keys

		for _, propName := range keys {
			propRaw := propsRaw[propName]
			ps, err := parseSchema(propName, propRaw)
			if err != nil {
				return nil, fmt.Errorf("property %s: %w", propName, err)
			}
			s.properties[propName] = ps
		}
	}

	// items (for arrays)
	if itemsRaw, ok := m["items"]; ok {
		is, err := parseSchema("", itemsRaw)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		s.items = is
	}

	// $ref
	if ref, ok := m["$ref"].(string); ok {
		s.ref = resolveRef(ref)
	}

	// oneOf / anyOf — collect refs for ToolCallResultFrame.result
	if oneOfRaw, ok := m["oneOf"].([]any); ok {
		for _, o := range oneOfRaw {
			os, err := parseSchema("", o)
			if err == nil {
				s.oneOf = append(s.oneOf, os)
			}
		}
	}
	if anyOfRaw, ok := m["anyOf"].([]any); ok {
		for _, o := range anyOfRaw {
			os, err := parseSchema("", o)
			if err == nil {
				s.anyOf = append(s.anyOf, os)
			}
		}
	}

	return s, nil
}

// resolveRef strips the AsyncAPI local ref prefix and returns just the schema name.
func resolveRef(ref string) string {
	// Handle '#/components/schemas/Foo' and 'Foo.yaml' (from openapi component files)
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		return ref[idx+1:]
	}
	// strip .yaml suffix if present
	ref = strings.TrimSuffix(ref, ".yaml")
	return ref
}

// generate produces the Go source file bytes.
func generate(schemas map[string]*schema) ([]byte, error) {
	// Sort schema names for deterministic output
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer

	buf.WriteString("//go:build go1.22\n")
	buf.WriteString("\n")
	buf.WriteString("// Code generated by scripts/gen-asyncapi-go. DO NOT EDIT.\n")
	buf.WriteString("// Source: contracts/asyncapi.yaml\n")
	buf.WriteString("//\n")
	buf.WriteString("// Regenerate with:\n")
	buf.WriteString("//   cd scripts/gen-asyncapi-go &&\n")
	buf.WriteString("//   go run . ../../contracts/asyncapi.yaml ../../pkg/api/generated/asyncapi_types.gen.go\n")
	buf.WriteString("\n")
	buf.WriteString("package generated\n\n")

	// We may need time import for date-time fields
	buf.WriteString("import \"time\"\n\n")
	buf.WriteString("// Ensure time is used even if no date-time fields are present.\n")
	buf.WriteString("var _ = time.Time{}\n\n")

	for _, name := range names {
		s := schemas[name]
		if err := writeSchema(&buf, name, s, schemas); err != nil {
			return nil, fmt.Errorf("write schema %s: %w", name, err)
		}
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

func writeSchema(buf *bytes.Buffer, name string, s *schema, allSchemas map[string]*schema) error {
	goName := toPascalCase(name)

	// If the schema is just a string enum (type: string with enum), emit a Go type alias + const block.
	if s.schemaType == "string" && s.isEnum && len(s.enum) > 0 {
		writeComment(buf, goName, s.description)
		fmt.Fprintf(buf, "type %s string\n\n", goName)
		fmt.Fprintf(buf, "// Defines values for %s.\n", goName)
		fmt.Fprintf(buf, "const (\n")
		for _, e := range s.enum {
			constName := goName + toPascalCase(e)
			fmt.Fprintf(buf, "\t%s %s = %q\n", constName, goName, e)
		}
		fmt.Fprintf(buf, ")\n")
		return nil
	}

	// If the schema is a plain string type (no enum, no properties), emit a type alias.
	if s.schemaType == "string" && !s.isEnum {
		writeComment(buf, goName, s.description)
		fmt.Fprintf(buf, "type %s = string\n", goName)
		return nil
	}

	// If the schema is an object, emit a Go struct.
	if s.schemaType == "object" || len(s.properties) > 0 {
		return writeStruct(buf, goName, s, allSchemas)
	}

	// Schema has no type (e.g., bare {} or oneOf) — emit interface{} alias.
	if s.schemaType == "" && len(s.properties) == 0 && s.ref == "" {
		writeComment(buf, goName, s.description)
		fmt.Fprintf(buf, "type %s = any\n", goName)
		return nil
	}

	// Fallback: struct with no fields for unrecognized schemas.
	writeComment(buf, goName, s.description)
	fmt.Fprintf(buf, "type %s struct{}\n", goName)
	return nil
}

func writeStruct(buf *bytes.Buffer, goName string, s *schema, allSchemas map[string]*schema) error {
	writeComment(buf, goName, s.description)
	fmt.Fprintf(buf, "type %s struct {\n", goName)

	// First pass: resolve Go field names with collision detection.
	// Underscore-prefixed JSON keys (e.g. "_ref", "_truncated") have their
	// underscore stripped by toPascalCase, which can collide with a sibling
	// non-underscored field (e.g. "ref"). When that happens, rename the
	// underscore-prefixed field with an "Is" prefix to preserve both as
	// distinct Go fields (e.g. "_ref" -> "IsRef" when "ref" already exists).
	fieldNames := make(map[string]string, len(s.propertyOrder))
	usedGoNames := make(map[string]string, len(s.propertyOrder)) // goName -> propName
	for _, propName := range s.propertyOrder {
		name := toPascalCase(propName)
		if existingProp, collides := usedGoNames[name]; collides {
			// Decide which one to rename: the underscore-prefixed one (the
			// sentinel discriminator) gets the "Is" prefix. If neither is
			// underscore-prefixed, that's a real spec bug — error out.
			switch {
			case strings.HasPrefix(propName, "_"):
				renamed := "Is" + name
				// Check for a three-way collision: "Is" + name is also taken.
				if alreadyUsedBy, taken := usedGoNames[renamed]; taken {
					return fmt.Errorf(
						"struct %s: three-way Go field name collision — %q, %q, and %q all resolve to %q or %q",
						goName, alreadyUsedBy, existingProp, propName, name, renamed,
					)
				}
				name = renamed
			case strings.HasPrefix(existingProp, "_"):
				renamed := "Is" + name
				// Check for a three-way collision before applying the rename:
				// if `renamed` is already claimed, or if `existingProp` has already
				// been renamed once (its current fieldName already has an "Is" prefix),
				// the three properties form an irresolvable collision.
				if alreadyUsedBy, taken := usedGoNames[renamed]; taken {
					return fmt.Errorf(
						"struct %s: three-way Go field name collision — %q, %q, and %q all resolve to %q or %q",
						goName, alreadyUsedBy, existingProp, propName, name, renamed,
					)
				}
				if current := fieldNames[existingProp]; strings.HasPrefix(current, "Is") {
					return fmt.Errorf(
						"struct %s: three-way Go field name collision — %q was already "+
							"renamed to %q, and %q also resolves to %q",
						goName, existingProp, current, propName, renamed,
					)
				}
				fieldNames[existingProp] = renamed
				delete(usedGoNames, name) // existingProp now uses renamed name
				usedGoNames[renamed] = existingProp
			default:
				return fmt.Errorf(
					"struct %s: properties %q and %q both map to Go field %s",
					goName, existingProp, propName, name,
				)
			}
		}
		fieldNames[propName] = name
		usedGoNames[name] = propName
	}

	for _, propName := range s.propertyOrder {
		ps := s.properties[propName]
		isRequired := s.required[propName]
		fieldName := fieldNames[propName]
		goType, matched := matchingNamedInlineGoType(goName, propName, ps, isRequired, allSchemas)
		if !matched {
			var err error
			goType, err = resolveGoType(ps, propName, isRequired, allSchemas)
			if err != nil {
				return fmt.Errorf("field %s: %w", propName, err)
			}
		}
		tag := buildTag(propName, isRequired, ps)
		if ps.description != "" {
			desc := strings.ReplaceAll(strings.TrimSpace(ps.description), "\n", " ")
			// Collapse multiple spaces
			for strings.Contains(desc, "  ") {
				desc = strings.ReplaceAll(desc, "  ", " ")
			}
			fmt.Fprintf(buf, "\t// %s\n", desc)
		}
		fmt.Fprintf(buf, "\t%s %s %s\n", fieldName, goType, tag)
	}

	fmt.Fprintf(buf, "}\n")
	return nil
}

// matchingNamedInlineGoType returns a pointer to (or value of, when required) a
// sibling named schema when a property's inline shape is structurally identical
// to that named schema.
//
// The naming convention is `<OwnerGoName without "Frame" suffix><PascalCase(propName)>`
// — e.g. owner `ErrorFrame` + prop `payload` → candidate `ErrorPayload`. This matches
// the project's stable convention (grep `\b[A-Z][a-zA-Z]+Frame\b` against
// contracts/asyncapi.yaml to verify if it ever changes); a future rename of the
// suffix (e.g. to `Message`) would require updating this function.
//
// Returns ("", false) when:
//   - the property is a $ref (already resolves through resolveGoType's ref branch), or
//   - the property has no nested properties (primitives/arrays don't carry a mirror-able shape), or
//   - no sibling schema with the candidate name exists, or
//   - the shapes diverge (sameSchemaShape returns false — silent fallback to anonymous
//     struct is the safe default; see SFH-01 in the Wave 0 review for the cost of this
//     drift visibility gap).
//
// Returns ("*Name", true) for optional fields (with the matching pointer shape)
// and ("Name", true) for required fields (the Zod validator distinguishes the
// two via the field's required-status; see TestGenerate_RequiredMatchingPropertyReturnsValueType).
func matchingNamedInlineGoType(
	ownerGoName string,
	propName string,
	property *schema,
	isRequired bool,
	allSchemas map[string]*schema,
) (string, bool) {
	if property.ref != "" || len(property.properties) == 0 {
		return "", false
	}
	candidateName := strings.TrimSuffix(ownerGoName, "Frame") + toPascalCase(propName)
	candidate, ok := allSchemas[candidateName]
	if !ok || !sameSchemaShape(property, candidate) {
		return "", false
	}
	if isRequired {
		return candidateName, true
	}
	return "*" + candidateName, true
}

// sameSchemaShape reports whether two schemas are structurally identical for the
// purposes of Go-emit reuse (i.e. the inline mirror can be safely replaced by a
// pointer/value of the named type without changing the generated wire shape).
//
// Comparison is strictly structural — `description`, `title`, and `default` are
// intentionally NOT compared. Drift in those text fields is a maintenance hazard
// (see SFH-02) but is a CI-lint concern, not a generator-emit concern: two
// structurally-identical schemas that differ only in description text can be
// safely emitted through the same Go type because the generated Zod validator
// reads only the structural fields.
func sameSchemaShape(left, right *schema) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.schemaType != right.schemaType ||
		left.format != right.format ||
		left.ref != right.ref ||
		left.additionalProps != right.additionalProps ||
		left.constValue != right.constValue ||
		left.isEnum != right.isEnum ||
		!slices.Equal(left.enum, right.enum) ||
		!maps.Equal(left.required, right.required) ||
		len(left.properties) != len(right.properties) ||
		len(left.oneOf) != len(right.oneOf) ||
		len(left.anyOf) != len(right.anyOf) {
		return false
	}
	for name, leftProperty := range left.properties {
		rightProperty, ok := right.properties[name]
		if !ok || !sameSchemaShape(leftProperty, rightProperty) {
			return false
		}
	}
	if !sameSchemaShape(left.items, right.items) {
		return false
	}
	for index := range left.oneOf {
		if !sameSchemaShape(left.oneOf[index], right.oneOf[index]) {
			return false
		}
	}
	for index := range left.anyOf {
		if !sameSchemaShape(left.anyOf[index], right.anyOf[index]) {
			return false
		}
	}
	return true
}

// resolveGoType returns the Go type string for a property schema.
func resolveGoType(ps *schema, propName string, isRequired bool, allSchemas map[string]*schema) (string, error) {
	// Handle $ref first
	if ps.ref != "" {
		refName := toPascalCase(ps.ref)
		// Check if the referenced schema is an object or struct type
		if ref, ok := allSchemas[ps.ref]; ok {
			if ref.schemaType == "string" && ref.isEnum {
				// Enum type — use as value when required, pointer when optional
				if isRequired {
					return refName, nil
				}
				return "*" + refName, nil
			}
			if ref.schemaType == "object" || len(ref.properties) > 0 {
				if isRequired {
					return refName, nil
				}
				return "*" + refName, nil
			}
		}
		if isRequired {
			return refName, nil
		}
		return "*" + refName, nil
	}

	// oneOf / anyOf with no type — use any
	if (len(ps.oneOf) > 0 || len(ps.anyOf) > 0) && ps.schemaType == "" {
		return "any", nil
	}

	switch ps.schemaType {
	case "string":
		if isRequired {
			return "string", nil
		}
		return "*string", nil

	case "integer":
		// format: int64 must map to Go int64 — plain "int" is 32-bit on
		// mipsle/arm targets (shipped, see CLAUDE.md Tech Stack), so an
		// epoch-millisecond value wraps. Mirrors oapi-codegen's own
		// int64-format handling on the OpenAPI side (openapi_types.gen.go).
		if ps.format == "int64" {
			if isRequired {
				return "int64", nil
			}
			return "*int64", nil
		}
		if isRequired {
			return "int", nil
		}
		return "*int", nil

	case "number":
		if isRequired {
			return "float64", nil
		}
		return "*float64", nil

	case "boolean":
		if isRequired {
			return "bool", nil
		}
		return "*bool", nil

	case "array":
		itemType := "any"
		if ps.items != nil {
			var err error
			// items are always considered required (non-nil) within a slice
			itemType, err = resolveGoType(ps.items, "", true, allSchemas)
			if err != nil {
				return "", fmt.Errorf("array items: %w", err)
			}
		}
		// Arrays that are required must never be nil — use []T (not *[]T).
		// Arrays that are optional can be nil — but we still use []T with omitempty,
		// since a nil slice and an empty slice both marshal as absent with omitempty.
		return "[]" + itemType, nil

	case "object":
		// An object with additionalProperties: true and no fixed properties → map[string]any
		if ps.additionalProps && len(ps.properties) == 0 {
			return "map[string]any", nil
		}
		// An object with both additionalProperties and fixed properties → map[string]any
		// (open-ended map wins; the fixed properties are documented in the schema but
		// we can't express both in a single Go type without a custom marshaler)
		if ps.additionalProps {
			return "map[string]any", nil
		}
		// An object with only fixed properties is an anonymous inline struct.
		// We generate a named sub-struct for readability but inline it here for simplicity.
		if len(ps.properties) > 0 {
			// For inline objects, generate an anonymous struct representation
			var fields []string
			// sort for determinism
			subKeys := make([]string, 0, len(ps.properties))
			for k := range ps.properties {
				subKeys = append(subKeys, k)
			}
			sort.Strings(subKeys)
			for _, subKey := range subKeys {
				subProp := ps.properties[subKey]
				subRequired := ps.required[subKey]
				subType, err := resolveGoType(subProp, subKey, subRequired, allSchemas)
				if err != nil {
					return "", fmt.Errorf("inline object field %s: %w", subKey, err)
				}
				subTag := buildTag(subKey, subRequired, subProp)
				fields = append(fields, fmt.Sprintf("%s %s %s", toPascalCase(subKey), subType, subTag))
			}
			inline := "struct{ " + strings.Join(fields, "; ") + " }"
			// An OPTIONAL inline object must be a pointer: `omitempty` on a
			// struct VALUE is inert (encoding/json never treats a struct as
			// empty), so a value-typed optional object always marshals — as
			// an all-zero object that then fails the SPA's strict zod schema
			// for the frame (required sub-fields empty), dropping the whole
			// payload. Pointer + omitempty gives real absence. (Found by
			// ADR-074 D5.2's GoalStatusFrame.criteria items, whose optional
			// per-kind check/behavior payloads hit exactly this.)
			if !isRequired {
				return "*" + inline, nil
			}
			return inline, nil
		}
		// Empty object with no additionalProperties — use map[string]any as a safe fallback
		return "map[string]any", nil

	case "":
		// No type specified — could be a bare {} (any), or a schema with only $ref/description
		return "any", nil

	default:
		return "any", nil
	}
}

// buildTag constructs the json struct tag for a field.
// Required fields never use omitempty. Optional fields always use omitempty so
// absent values do not marshal as null.
func buildTag(propName string, isRequired bool, _ *schema) string {
	if isRequired {
		return fmt.Sprintf("`json:%q`", propName)
	}
	return fmt.Sprintf("`json:%q`", propName+",omitempty")
}

// toPascalCase converts snake_case or kebab-case to PascalCase.
func toPascalCase(s string) string {
	if s == "" {
		return ""
	}
	// Special cases
	switch s {
	case "ok":
		return "Ok"
	}

	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '+'
	})
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	result := b.String()
	if result == "" {
		// Fall back: capitalize first char
		runes := []rune(s)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
	return result
}

// writeComment writes a Go doc comment for a type.
func writeComment(buf *bytes.Buffer, name, description string) {
	if description == "" {
		fmt.Fprintf(buf, "// %s is an AsyncAPI schema type.\n", name)
		return
	}
	// Wrap description — emit first line as single-line comment to keep gofmt happy.
	desc := strings.TrimSpace(description)
	// Replace newlines with spaces for a single-line comment
	desc = strings.ReplaceAll(desc, "\n", " ")
	for strings.Contains(desc, "  ") {
		desc = strings.ReplaceAll(desc, "  ", " ")
	}
	fmt.Fprintf(buf, "// %s — %s\n", name, desc)
}
