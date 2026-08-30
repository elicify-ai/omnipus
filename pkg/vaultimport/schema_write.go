// Omnipus — writing an inferred record type as a records.ParseSchema-shaped
// YAML file.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// RenderSchemaYAML builds one <type>.yaml schema file's bytes from inferred
// properties, in the exact shape records.ParseSchema decodes: a bare
// `schema_version: 1` int, `type:`, and a `properties:` mapping in
// alphabetical property-name order (this package's own generation order —
// InferSchema already sorts, so re-sorting here is a formality, not a second
// decision).
//
// It is round-tripped against records.ParseSchema in
// schema_write_roundtrip_test.go — this file is the only place that builds
// this shape, and pkg/records is the only place that reads it back.
func RenderSchemaYAML(recordType string, props []InferredProperty) ([]byte, error) {
	propPairs := make([]ordPair, 0, len(props))
	for _, p := range props {
		propPairs = append(propPairs, ordPair{Key: p.Name, Value: renderPropertyDecl(p)})
	}
	top := orderedMap(
		ordPair{Key: "schema_version", Value: records.SupportedSchemaVersion},
		ordPair{Key: "type", Value: recordType},
		ordPair{Key: "properties", Value: orderedMap(propPairs...)},
	)
	return marshalDoc(top)
}

func renderPropertyDecl(p InferredProperty) *yaml.Node {
	pairs := []ordPair{{Key: "type", Value: string(p.Type)}}
	if p.Many {
		pairs = append(pairs, ordPair{Key: "many", Value: true})
	}
	if p.Required {
		pairs = append(pairs, ordPair{Key: "required", Value: true})
	}
	if p.Type == records.TypeEnum && len(p.EnumValues) > 0 {
		pairs = append(pairs, ordPair{Key: "values", Value: p.EnumValues})
	}
	if (p.Type == records.TypeRelation || p.Type == records.TypePerson) && p.To != "" {
		pairs = append(pairs, ordPair{Key: "to", Value: p.To})
	}
	return orderedMap(pairs...)
}
