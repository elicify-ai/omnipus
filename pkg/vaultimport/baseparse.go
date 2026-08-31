// Omnipus — reading a `.base` file's own YAML shape (importer HALF 2, read
// side). This is the ONLY place a `.base` file's bytes are read (FR-102):
// the import is one-shot and nothing downstream of this package ever opens
// a `.base` file again.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParsedBase is one `.base` file's generic structure. Obsidian's Base format
// is read here as plain `any` (map[string]any / []any / scalars) rather than
// into a typed struct: it is not this product's format, it is not versioned
// the way our own schema/view files are (the ViewDef schema's own description
// notes it "broke in five consecutive releases across eight weeks"), and a
// typed struct would have to guess at every field this importer does not
// care about. Reading generically means an unrecognised Base field is
// simply never looked at, rather than causing a decode failure.
type ParsedBase struct {
	// Filters is the base-level `filters:` tree (any of a leaf string, an
	// `and:`/`or:`/`not:` node, or nil when the file has none).
	Filters any
	// Views is each `views:` entry, still generic.
	Views []map[string]any
	// Limit is the base-level `limit:` value, applying to every view that does
	// not declare its own — the same composition `filters:` uses. Kept as
	// `any` for the same reason everything else here is: this is not our
	// format, and a value that is not a whole number becomes a NAMED loss
	// rather than a decode failure that takes the whole file down.
	Limit any
}

// ParseBaseFile reads one `.base` file's bytes into its generic structure.
func ParseBaseFile(data []byte) (*ParsedBase, error) {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("vaultimport: .base file is not valid YAML: %w", err)
	}
	pb := &ParsedBase{Filters: top["filters"], Limit: top["limit"]}
	rawViews, _ := top["views"].([]any)
	for _, rv := range rawViews {
		if m, ok := rv.(map[string]any); ok {
			pb.Views = append(pb.Views, m)
		}
	}
	return pb, nil
}
