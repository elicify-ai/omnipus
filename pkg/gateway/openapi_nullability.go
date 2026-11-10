package gateway

// The embedded components use OpenAPI 3.0's nullable keyword. The JSON Schema
// compiler does not interpret that keyword: translate its type effect in memory,
// without changing canonical schemas, enum constraints or non-nullable fields.
// In OpenAPI 3.0 nullable takes effect only beside an explicit type.
func normalizeOpenAPINullability(schema any) {
	object, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if nullable, _ := object["nullable"].(bool); nullable {
		if kind, ok := object["type"].(string); ok && kind != "" && kind != "null" {
			object["type"] = []any{kind, "null"}
		}
	}
	// Only schema-valued keywords are traversed. Example/default/enum values
	// remain literal data, including objects with properties named type/nullable.
	for _, key := range []string{"items", "additionalItems", "additionalProperties", "unevaluatedProperties", "contains", "propertyNames", "not", "if", "then", "else"} {
		normalizeOpenAPINullability(object[key])
	}
	for _, key := range []string{"properties", "patternProperties", "definitions", "$defs", "dependentSchemas"} {
		if children, ok := object[key].(map[string]any); ok {
			for _, child := range children {
				normalizeOpenAPINullability(child)
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := object[key].([]any); ok {
			for _, child := range children {
				normalizeOpenAPINullability(child)
			}
		}
	}
}
