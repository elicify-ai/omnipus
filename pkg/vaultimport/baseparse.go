// Omnipus — reading a `.base` file's own YAML shape (importer HALF 2, read
// side). This is the ONLY place a `.base` file's bytes are read (FR-102):
// the import is one-shot and nothing downstream of this package ever opens
// a `.base` file again.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"sort"

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
//
// ---------------------------------------------------------------------------
// EVERYTHING A `.base` FILE DECLARES AT TOP LEVEL IS CARRIED HERE, AND THAT IS
// THE POINT OF THE STRUCT
//
// This type held exactly two fields — Filters and Views — and everything else
// the YAML decoder produced was discarded at the end of ParseBaseFile. Two of
// the discarded keys were not obscure:
//
//   - `formulas:` — the base's computed properties. Fourteen of the founder's
//     eighteen bases declare one, and the 57 `formula.*` references in their
//     filters, columns, sort keys and aggregates were all dropped, under a
//     loss line that said this importer did not yet carry a base's formulas
//     block so there was nothing for the reference to resolve against. That
//     line was accurate, and what it was accurate ABOUT was this struct: the
//     block was parsed by yaml.Unmarshal and thrown away one line later.
//     (The line itself is retired — a reference that still cannot be carried
//     is now refused with the real validator's own reason for it.)
//
//   - `properties:` — per-column display config (`displayName`). ViewDef has
//     had a field for it the whole time (`property_config`, whose schema says
//     in as many words that it is "the `properties` key of an Obsidian
//     base"). It was dropped SILENTLY: not translated, not refused, not
//     named in `untranslated`. A base whose only untranslatable content was
//     its column headings scored CLEAN.
//
// A dropped key that produces a named loss is a gap. A dropped key that
// produces nothing at all is the failure this whole importer is written
// against, so the rule for this struct is now simply: read every top-level key
// the format defines, and let the TRANSLATOR decide what it can carry.
// ---------------------------------------------------------------------------
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
	// Formulas is the base-level `formulas:` map — formula name to the
	// expression's SOURCE TEXT, exactly as the operator wrote it.
	//
	// A key whose value is not a scalar string is NOT here; it is in
	// FormulaNames only, so the translator refuses it by name rather than
	// this file inventing a source for it.
	Formulas map[string]string
	// FormulaNames is every key `formulas:` declared, sorted, INCLUDING the
	// ones with an unreadable value. It is what the translator iterates, so a
	// formula whose source could not be read is a NAMED refusal rather than a
	// name that quietly never existed.
	FormulaNames []string
	// PropertyConfig is the base-level `properties:` block: the property (or
	// `formula.<name>`) a column heading was configured for, and the config.
	PropertyConfig map[string]BasePropertyConfig
	// PropertyConfigNames is every key `properties:` declared, sorted —
	// including keys whose value was not a mapping.
	PropertyConfigNames []string
}

// BasePropertyConfig is one entry of a `.base` file's top-level `properties:`
// block.
//
// Obsidian's own key is `displayName` (camelCase); ours is `display_name`.
// The spelling is the format's, the meaning is shared, so the rename is a
// translation and not a loss. Every OTHER key under a property is carried in
// UnknownKeys and becomes a named loss — Obsidian has added display keys
// before (it is the format whose own schema description records breaking in
// five consecutive releases) and a key this importer does not recognise must
// be visible rather than absent.
type BasePropertyConfig struct {
	// DisplayName is the column heading Obsidian was asked to render. Empty
	// means the entry declared none.
	DisplayName string
	// UnknownKeys is every other key under this property, sorted. Each one
	// becomes a named loss.
	UnknownKeys []string
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
	pb.Formulas, pb.FormulaNames = parseBaseFormulas(top["formulas"])
	pb.PropertyConfig, pb.PropertyConfigNames = parseBasePropertyConfig(top["properties"])
	return pb, nil
}

// parseBaseFormulas reads the top-level `formulas:` mapping.
//
// A value that is not a scalar string is deliberately NOT coerced. YAML would
// happily give us `42` or a nested mapping, and rendering either as text would
// manufacture a formula source the operator never wrote — which then parses,
// or does not, under a name they would not recognise. The name is kept and the
// source is not, so the translator refuses it naming the key.
func parseBaseFormulas(raw any) (map[string]string, []string) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	names := make([]string, 0, len(m))
	for k, v := range m {
		names = append(names, k)
		if s, isString := v.(string); isString {
			out[k] = s
		}
	}
	sort.Strings(names)
	if len(out) == 0 {
		out = nil
	}
	return out, names
}

// parseBasePropertyConfig reads the top-level `properties:` mapping.
func parseBasePropertyConfig(raw any) (map[string]BasePropertyConfig, []string) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]BasePropertyConfig, len(m))
	names := make([]string, 0, len(m))
	for k, v := range m {
		names = append(names, k)
		entry, isMap := v.(map[string]any)
		if !isMap {
			// A `properties:` entry that is not a mapping (`status: true`)
			// declares nothing this importer can read. It is recorded as an
			// entry with no display name and no keys, so the translator names
			// it rather than the key vanishing between here and the report.
			out[k] = BasePropertyConfig{}
			continue
		}
		cfg := BasePropertyConfig{}
		for ek, ev := range entry {
			if ek == "displayName" {
				if s, isString := ev.(string); isString && s != "" {
					cfg.DisplayName = s
					continue
				}
			}
			cfg.UnknownKeys = append(cfg.UnknownKeys, ek)
		}
		sort.Strings(cfg.UnknownKeys)
		out[k] = cfg
	}
	sort.Strings(names)
	return out, names
}
