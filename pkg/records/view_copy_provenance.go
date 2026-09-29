// License: MIT
// Copyright (c) 2026 Omnipus contributors
package records

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// StripCopiedViewProvenance removes the top-level derived_from field from a
// copied view without re-serializing its other fields, comments, or formatting.
// It refuses a shape it cannot edit safely rather than returning a copy that
// still claims to be managed by a .base.
func StripCopiedViewProvenance(data []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("records: parse copied view provenance: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("records: copied view is not a YAML mapping")
	}
	mapping := doc.Content[0]
	var marker *yaml.Node
	var value *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "derived_from" {
			if marker != nil {
				return nil, fmt.Errorf("records: copied view has duplicate derived_from fields")
			}
			marker, value = mapping.Content[i], mapping.Content[i+1]
		}
	}
	if marker == nil {
		return data, nil
	}
	if marker.Column != 1 || mapping.Style&yaml.FlowStyle != 0 || value.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("records: cannot safely strip copied view derived_from field")
	}

	// Node.Line counts physical lines, including a possible document header;
	// keep the original line terminators and all unrelated bytes intact.
	lines := strings.SplitAfter(string(data), "\n")
	start := marker.Line - 1
	if start < 0 || start >= len(lines) {
		return nil, fmt.Errorf("records: copied view provenance has invalid position")
	}
	end := start + 1
	if value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		for end < len(lines) {
			line := strings.TrimSuffix(lines[end], "\r\n")
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				end++
				continue
			}
			break
		}
	}
	return []byte(strings.Join(lines[:start], "") + strings.Join(lines[end:], "")), nil
}
