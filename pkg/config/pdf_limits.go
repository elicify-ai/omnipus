package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PDFLimitsRow states inline PDF settings at one level. Pointers distinguish
// absence (inherit) from explicit zero (unlimited) or false (text route).
// These are config.json values, not catalogue or gateway wire types.
type PDFLimitsRow struct {
	InlinePDF              *bool  `json:"inline_pdf,omitempty" yaml:"inline_pdf,omitempty"`
	MaxFileBytes           *int64 `json:"max_file_bytes,omitempty" yaml:"max_file_bytes,omitempty"`
	MaxTotalFileBytes      *int64 `json:"max_total_file_bytes,omitempty" yaml:"max_total_file_bytes,omitempty"`
	MaxRequestBytes        *int64 `json:"max_request_bytes,omitempty" yaml:"max_request_bytes,omitempty"`
	MaxPagesPerFile        *int64 `json:"max_pages_per_file,omitempty" yaml:"max_pages_per_file,omitempty"`
	MaxPagesPerRequest     *int64 `json:"max_pages_per_request,omitempty" yaml:"max_pages_per_request,omitempty"`
	MaxDocumentsPerMessage *int64 `json:"max_documents_per_message,omitempty" yaml:"max_documents_per_message,omitempty"`
	MaxDocumentsPerRequest *int64 `json:"max_documents_per_request,omitempty" yaml:"max_documents_per_request,omitempty"`
}

// PDFLimitsOverride addresses a provider, optionally narrowed to an exact model
// id or a non-empty prefix followed by one trailing '*'. IDs are case-sensitive.
type PDFLimitsOverride struct {
	Provider     string `json:"provider" yaml:"provider"`
	Model        string `json:"model,omitempty" yaml:"model,omitempty"`
	PDFLimitsRow `yaml:",inline"`
}

// PDFLimitsConfig is the sparse operator overlay on the embedded table. Keeping
// it sparse on disk lets future shipped corrections reach fields not overridden.
type PDFLimitsConfig struct {
	Default   PDFLimitsRow        `json:"default,omitempty" yaml:"default"`
	Overrides []PDFLimitsOverride `json:"overrides,omitempty" yaml:"overrides"`
}

//go:embed data/pdf_limits.yaml
var shippedPDFLimitsYAML []byte

var shippedPDFLimits = loadShippedPDFLimits()

func loadShippedPDFLimits() PDFLimitsConfig {
	var table PDFLimitsConfig
	decoder := yaml.NewDecoder(bytes.NewReader(shippedPDFLimitsYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&table); err != nil {
		panic(fmt.Sprintf("embedded pdf_limits: %v", err))
	}
	if err := table.Validate(); err != nil {
		panic(fmt.Sprintf("embedded pdf_limits: %v", err))
	}
	return table
}

// UnmarshalJSON confines strict field/type checks to this section, leaving the
// config loader's forward-compatible handling of other sections unchanged.
func (c *PDFLimitsConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		Default   json.RawMessage   `json:"default"`
		Overrides []json.RawMessage `json:"overrides"`
	}
	if err := decodePDFLimitsJSON(data, &raw, "pdf_limits"); err != nil {
		return err
	}
	var next PDFLimitsConfig
	if len(raw.Default) != 0 {
		if err := decodePDFLimitsJSON(raw.Default, &next.Default, "pdf_limits.default"); err != nil {
			return err
		}
	}
	for i, row := range raw.Overrides {
		var override PDFLimitsOverride
		if err := decodePDFLimitsJSON(row, &override, fmt.Sprintf("pdf_limits.overrides[%d]", i)); err != nil {
			return err
		}
		next.Overrides = append(next.Overrides, override)
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

func decodePDFLimitsJSON(data []byte, into any, path string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Validate rejects bad rows with their index and identity, never dropping a row.
func (c PDFLimitsConfig) Validate() error {
	if err := c.Default.validate("pdf_limits.default"); err != nil {
		return err
	}
	type key struct{ provider, model string }
	seen := make(map[key]int, len(c.Overrides))
	for i, row := range c.Overrides {
		path := fmt.Sprintf("pdf_limits.overrides[%d] (provider=%q, model=%q)", i, row.Provider, row.Model)
		if strings.TrimSpace(row.Provider) == "" {
			return fmt.Errorf("%s: provider must be non-empty", path)
		}
		if row.Provider != strings.TrimSpace(row.Provider) || strings.ContainsAny(row.Provider, "*?[]{}") {
			return fmt.Errorf("%s: provider must be an exact id without surrounding whitespace or wildcards", path)
		}
		if row.Model != strings.TrimSpace(row.Model) || strings.ContainsAny(row.Model, "?[]{}") ||
			(strings.Contains(row.Model, "*") && (strings.Count(row.Model, "*") != 1 || !strings.HasSuffix(row.Model, "*") || row.Model == "*")) {
			return fmt.Errorf("%s: model must be an exact id or a non-empty prefix with one trailing *", path)
		}
		k := key{row.Provider, row.Model}
		if previous, ok := seen[k]; ok {
			return fmt.Errorf("%s: duplicate of pdf_limits.overrides[%d]", path, previous)
		}
		seen[k] = i
		if err := row.validate(path); err != nil {
			return err
		}
	}
	return nil
}

func (r PDFLimitsRow) validate(path string) error {
	for _, field := range r.integerFields() {
		if field.value != nil && *field.value < 0 {
			return fmt.Errorf("%s.%s: must be >= 0 (0 = no limit), got %d", path, field.name, *field.value)
		}
	}
	return nil
}

type pdfIntegerField struct {
	name  string
	value *int64
}

// Order matches PDFLimits' accessors. A single list drives validation/resolution.
func (r PDFLimitsRow) integerFields() [7]pdfIntegerField {
	return [7]pdfIntegerField{
		{"max_file_bytes", r.MaxFileBytes},
		{"max_total_file_bytes", r.MaxTotalFileBytes},
		{"max_request_bytes", r.MaxRequestBytes},
		{"max_pages_per_file", r.MaxPagesPerFile},
		{"max_pages_per_request", r.MaxPagesPerRequest},
		{"max_documents_per_message", r.MaxDocumentsPerMessage},
		{"max_documents_per_request", r.MaxDocumentsPerRequest},
	}
}

// ResolvePDFLimits resolves only engine configuration, never catalogue fields.
// Per field: exact model > longest matching prefix (then shorter prefixes) >
// provider > default. At the same key, a stated operator field beats shipped
// data. Explicit zero/false are stated. A nil Config uses the shipped table.
func (c *Config) ResolvePDFLimits(provider, model string) PDFLimits {
	var operator PDFLimitsConfig
	if c != nil && c.PDFLimits != nil {
		operator = *c.PDFLimits
	}
	return resolvePDFLimits(shippedPDFLimits, operator, provider, model)
}

func resolvePDFLimits(shipped, operator PDFLimitsConfig, provider, model string) PDFLimits {
	var out PDFLimits
	var ranks [8]int
	apply := func(row PDFLimitsRow, rank int) {
		if row.InlinePDF != nil && rank >= ranks[0] {
			out.inlinePDF = pdfBoolField{*row.InlinePDF, true}
			ranks[0] = rank
		}
		for i, field := range row.integerFields() {
			if field.value != nil && rank >= ranks[i+1] {
				out.integers[i] = pdfLimitField{*field.value, true}
				ranks[i+1] = rank
			}
		}
	}
	apply(shipped.Default, 0)
	apply(operator.Default, 0)
	// Operator rows are visited last: a tie is the same (provider, model) key.
	for _, rows := range [][]PDFLimitsOverride{shipped.Overrides, operator.Overrides} {
		for _, row := range rows {
			if row.Provider == provider {
				if rank := pdfModelRank(row.Model, model); rank > 0 {
					apply(row.PDFLimitsRow, rank)
				}
			}
		}
	}
	return out
}

func pdfModelRank(pattern, model string) int {
	if pattern == "" {
		return 1 // provider row
	}
	if pattern == model {
		return len(model) + 2 // exact beats even a prefix as long as the id
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok && strings.HasPrefix(model, prefix) {
		return len(prefix) + 1
	}
	return 0
}
