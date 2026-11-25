package catalog

// BUILD-FAIL FILE — spec #1968 (read-file native PDF, FR-4). The tests here
// need symbols that do not exist on the base tree, so they live in their own
// file: while the symbols are missing, ONLY this file fails to build and the
// package's other tests still compile and run.
//
// Expected new symbols (spec §4.2; declared exactly as the tests use them):
//
//	// on Handle (resolve.go), per-field catalogue document limits:
//	func (h Handle) DocumentLimits() DocumentLimits
//
//	// the resolved per-field view. The tests read it BEHAVIOURALLY — every
//	// field is an accessor returning (value, stated), so the internal
//	// representation stays the implementer's (no *int64/*bool field pin):
//	//   stated == false → the field is not stated at any level (no
//	//                     client-side limit);
//	//   stated && value == 0 → explicitly no limit, cascade stopped;
//	//   stated && value > 0  → inclusive limit (≤ value allowed).
//	// The zero value of DocumentLimits must answer (0, false) everywhere.
//	type DocumentLimits struct { /* unexported state */ }
//	func (d DocumentLimits) InlinePDF() (bool, bool)
//	func (d DocumentLimits) MaxFileBytes() (int64, bool)
//	func (d DocumentLimits) MaxTotalFileBytes() (int64, bool)
//	func (d DocumentLimits) MaxRequestBytes() (int64, bool)
//	func (d DocumentLimits) MaxPagesPerFile() (int64, bool)
//	func (d DocumentLimits) MaxPagesPerRequest() (int64, bool)
//	func (d DocumentLimits) MaxDocumentsPerMessage() (int64, bool)
//	func (d DocumentLimits) MaxDocumentsPerRequest() (int64, bool)
//
//	// on the wire DTOs (parse.go), all optional:
//	//   documentDTO.DefaultDocumentLimits  json:"default_document_limits"
//	//   providerDTO.DocumentLimits         json:"document_limits"
//	//   modelDTO.DocumentLimits            json:"document_limits"
//
// Oracles: spec §4.2 (precedence and 0/absent semantics), §4.3 (seed values
// used as FIXTURE DATA only), §6 rows T4/T4b/T5d. No limit number may live
// in Go code (FR-4/§4.2); every number below is catalogue fixture data.

import (
	"encoding/json"
	"os"
	"testing"
)

// limitsDocJSON builds a 2.0.0 document with the given per-level
// document_limits members (nil member = omit the member entirely). The
// builder self-checks (F1) that every requested level actually landed in the
// marshalled document — a silently dropped level would turn the precedence
// assertions into vacuous passes.
func limitsDocJSON(t *testing.T, defaultLimits, providerLimits, modelLimits map[string]any) *Catalog {
	t.Helper()
	model := map[string]any{
		"id":               "fx/limits-doc-model",
		"name":             "Fixture Limits Model",
		"context_window":   128000,
		"input_modalities": []string{"text", "pdf"},
		"tool_call":        true,
		"status":           "active",
	}
	if modelLimits != nil {
		model["document_limits"] = modelLimits
	}
	provider := map[string]any{
		"id":           "fx/limits-doc-provider",
		"name":         "Fixture Limits Provider",
		"api":          "https://api.example.test/v1",
		"protocol":     "openai-compatible",
		"tier":         "standard",
		"auth_methods": []string{"api_key"},
		"models":       []map[string]any{model},
	}
	if providerLimits != nil {
		provider["document_limits"] = providerLimits
	}
	doc := map[string]any{
		"schema_version": "2.0.0",
		"version":        "v2026.11.22.1",
		"updated_at":     "2026-11-22T00:00:00Z",
		"source":         "test",
		"default_resize_limits": map[string]any{
			"long_edge_px": 7680, "max_bytes": 10485760,
		},
		"providers": []map[string]any{provider},
	}
	if defaultLimits != nil {
		doc["default_document_limits"] = defaultLimits
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal limits document: %v", err)
	}

	// F1 fixture self-check: re-parse what was marshalled and require each
	// requested level's document_limits member to match it canonically.
	var roundTrip struct {
		DefaultDocumentLimits map[string]any `json:"default_document_limits"`
		Providers             []struct {
			DocumentLimits map[string]any `json:"document_limits"`
			Models         []struct {
				ID             string         `json:"id"`
				DocumentLimits map[string]any `json:"document_limits"`
			} `json:"models"`
		} `json:"providers"`
	}
	if decodeErr := json.Unmarshal(data, &roundTrip); decodeErr != nil {
		t.Fatalf("re-parse limits fixture: %v", decodeErr)
	}
	canonical := func(m map[string]any) string {
		if m == nil {
			return "<absent>"
		}
		b, marshalErr := json.Marshal(m)
		if marshalErr != nil {
			t.Fatalf("canonicalise limits member: %v", marshalErr)
		}
		return string(b)
	}
	wantDefault, wantProvider, wantModel := canonical(defaultLimits), canonical(providerLimits), canonical(modelLimits)
	if len(roundTrip.Providers) != 1 || len(roundTrip.Providers[0].Models) != 1 {
		t.Fatalf("limits fixture lost its provider/model shape")
	}
	if got := canonical(roundTrip.DefaultDocumentLimits); got != wantDefault {
		t.Fatalf("FIXTURE BROKEN (F1 self-check): default_document_limits = %s, requested %s", got, wantDefault)
	}
	if got := canonical(roundTrip.Providers[0].DocumentLimits); got != wantProvider {
		t.Fatalf("FIXTURE BROKEN (F1 self-check): provider document_limits = %s, requested %s", got, wantProvider)
	}
	if got := canonical(roundTrip.Providers[0].Models[0].DocumentLimits); got != wantModel {
		t.Fatalf("FIXTURE BROKEN (F1 self-check): model %q document_limits = %s, requested %s",
			roundTrip.Providers[0].Models[0].ID, got, wantModel)
	}

	c, err := NewCatalog(data)
	if err != nil {
		t.Fatalf("NewCatalog(limits fixture): %v", err)
	}
	return c
}

// mustLimits resolves the fixture pair and returns its DocumentLimits.
func mustLimits(t *testing.T, c *Catalog) DocumentLimits {
	t.Helper()
	h := c.Resolve("fx/limits-doc-provider", "fx/limits-doc-model")
	if !h.Found() {
		t.Fatal("fixture pair must resolve")
	}
	return h.DocumentLimits()
}

// checkLimit asserts one accessor of the resolved view. want == nil (untyped)
// means "not stated at any level"; want int64 is the required stated value
// (F1: the check reads the accessor's two results, so a stated-value
// assertion can no longer degrade into a vacuous nil-check).
func checkLimit(t *testing.T, name string, get func() (int64, bool), want any) {
	t.Helper()
	w, required := want.(int64)
	got, stated := get()
	if !required {
		if stated {
			t.Fatalf("%s: stated as %d, want not stated at any level (a compiled default or an unrequested level leaked)", name, got)
		}
		return
	}
	if !stated {
		t.Fatalf("%s: not stated, want %d (first level that states the field wins)", name, w)
	}
	if got != w {
		t.Fatalf("%s: got %d, want %d (first level that states the field wins)", name, got, w)
	}
}

// checkInlinePDF asserts the InlinePDF accessor. want == nil (untyped) means
// absent at every level; want bool is the required stated value.
func checkInlinePDF(t *testing.T, name string, get func() (bool, bool), want any) {
	t.Helper()
	w, required := want.(bool)
	got, stated := get()
	if !required {
		if stated {
			t.Fatalf("%s: stated as %v, want absent everywhere", name, got)
		}
		return
	}
	if !stated {
		t.Fatalf("%s: absent, want %v (first level that states the field wins)", name, w)
	}
	if got != w {
		t.Fatalf("%s: got %v, want %v (first level that states the field wins)", name, got, w)
	}
}

// TestHandleDocumentLimits_Precedence (T4, B) — the per-field cascade of
// spec §4.2: model row → provider row → default row → nothing. Each case
// states limits at different levels and pins the resolved tri-state of every
// field. All numbers are §4.3-class fixture data.
func TestHandleDocumentLimits_Precedence(t *testing.T) {
	// T4 explicitly requires the SAME precedence/zero matrix for EVERY
	// numeric field, not just file bytes and a couple of absent fields.
	fields := []struct {
		name string
		get  func(DocumentLimits) (int64, bool)
	}{
		{"max_file_bytes", DocumentLimits.MaxFileBytes},
		{"max_total_file_bytes", DocumentLimits.MaxTotalFileBytes},
		{"max_request_bytes", DocumentLimits.MaxRequestBytes},
		{"max_pages_per_file", DocumentLimits.MaxPagesPerFile},
		{"max_pages_per_request", DocumentLimits.MaxPagesPerRequest},
		{"max_documents_per_message", DocumentLimits.MaxDocumentsPerMessage},
		{"max_documents_per_request", DocumentLimits.MaxDocumentsPerRequest},
	}
	for _, field := range fields {
		for _, tc := range []struct {
			name    string
			d, p, m any
			want    any
		}{
			{"model wins", 300, 200, 100, int64(100)},
			{"provider inherited", 300, 200, nil, int64(200)},
			{"default inherited", 300, nil, nil, int64(300)},
			{"model zero stops cascade", 300, 200, 0, int64(0)},
			{"provider zero stops cascade", 300, 0, nil, int64(0)},
			{"default zero", 0, nil, nil, int64(0)},
			{"nothing stated", nil, nil, nil, nil},
		} {
			t.Run(field.name+"/"+tc.name, func(t *testing.T) {
				row := func(value any) map[string]any {
					// A present row lacking this field must still inherit it.
					if value == nil {
						return map[string]any{"inline_pdf": true}
					}
					return map[string]any{field.name: value}
				}
				lim := mustLimits(t, limitsDocJSON(t, row(tc.d), row(tc.p), row(tc.m)))
				checkLimit(t, field.name, func() (int64, bool) { return field.get(lim) }, tc.want)
			})
		}
	}
	for _, tc := range []struct {
		name    string
		d, p, m any
		want    any
	}{
		{"model true", false, false, true, true}, {"model false", true, true, false, false},
		{"provider true", false, true, nil, true}, {"provider false", true, false, nil, false},
		{"default true", true, nil, nil, true}, {"default false", false, nil, nil, false},
		{"absent", nil, nil, nil, nil},
	} {
		t.Run("inline matrix/"+tc.name, func(t *testing.T) {
			row := func(v any) map[string]any {
				if v == nil {
					return map[string]any{}
				}
				return map[string]any{"inline_pdf": v}
			}
			lim := mustLimits(t, limitsDocJSON(t, row(tc.d), row(tc.p), row(tc.m)))
			checkInlinePDF(t, "InlinePDF", lim.InlinePDF, tc.want)
		})
	}
	t.Run("model row wins over provider and default", func(t *testing.T) {
		c := limitsDocJSON(t,
			map[string]any{"max_file_bytes": 300, "max_pages_per_file": 30},
			map[string]any{"max_file_bytes": 200},
			map[string]any{"max_file_bytes": 100},
		)
		lim := mustLimits(t, c)
		checkLimit(t, "MaxFileBytes", lim.MaxFileBytes, int64(100))
		checkLimit(t, "MaxPagesPerFile", lim.MaxPagesPerFile, int64(30))
		checkLimit(t, "MaxTotalFileBytes", lim.MaxTotalFileBytes, nil)
	})

	t.Run("row without a field inherits it from the next level", func(t *testing.T) {
		c := limitsDocJSON(t,
			map[string]any{"max_file_bytes": 300, "max_total_file_bytes": 300, "max_request_bytes": 300},
			map[string]any{"max_request_bytes": 250},
			map[string]any{"max_file_bytes": 100},
		)
		lim := mustLimits(t, c)
		checkLimit(t, "MaxFileBytes", lim.MaxFileBytes, int64(100))           // model row
		checkLimit(t, "MaxRequestBytes", lim.MaxRequestBytes, int64(250))     // provider row
		checkLimit(t, "MaxTotalFileBytes", lim.MaxTotalFileBytes, int64(300)) // default row
	})

	t.Run("explicit zero at the model row stops the cascade", func(t *testing.T) {
		c := limitsDocJSON(t,
			map[string]any{"max_pages_per_file": 100},
			nil,
			map[string]any{"max_pages_per_file": 0},
		)
		lim := mustLimits(t, c)
		checkLimit(t, "MaxPagesPerFile", lim.MaxPagesPerFile, int64(0))
	})

	t.Run("explicit zero at the provider row stops the cascade", func(t *testing.T) {
		c := limitsDocJSON(t,
			map[string]any{"max_documents_per_request": 5},
			map[string]any{"max_documents_per_request": 0},
			nil,
		)
		lim := mustLimits(t, c)
		checkLimit(t, "MaxDocumentsPerRequest", lim.MaxDocumentsPerRequest, int64(0))
	})

	t.Run("no own rows resolve the whole default row", func(t *testing.T) {
		c := limitsDocJSON(t,
			map[string]any{
				"max_file_bytes":            50000000,
				"max_total_file_bytes":      50000000,
				"max_pages_per_file":        100,
				"max_documents_per_request": 5,
			},
			nil, nil,
		)
		lim := mustLimits(t, c)
		checkLimit(t, "MaxFileBytes", lim.MaxFileBytes, int64(50000000))
		checkLimit(t, "MaxTotalFileBytes", lim.MaxTotalFileBytes, int64(50000000))
		checkLimit(t, "MaxPagesPerFile", lim.MaxPagesPerFile, int64(100))
		checkLimit(t, "MaxDocumentsPerRequest", lim.MaxDocumentsPerRequest, int64(5))
		checkLimit(t, "MaxRequestBytes", lim.MaxRequestBytes, nil)
		checkLimit(t, "MaxPagesPerRequest", lim.MaxPagesPerRequest, nil)
		checkLimit(t, "MaxDocumentsPerMessage", lim.MaxDocumentsPerMessage, nil)
	})

	t.Run("nothing anywhere is the zero value", func(t *testing.T) {
		c := limitsDocJSON(t, nil, nil, nil)
		lim := mustLimits(t, c)
		checkInlinePDF(t, "InlinePDF", lim.InlinePDF, nil)
		checkLimit(t, "MaxFileBytes", lim.MaxFileBytes, nil)
		checkLimit(t, "MaxTotalFileBytes", lim.MaxTotalFileBytes, nil)
		checkLimit(t, "MaxRequestBytes", lim.MaxRequestBytes, nil)
		checkLimit(t, "MaxPagesPerFile", lim.MaxPagesPerFile, nil)
		checkLimit(t, "MaxPagesPerRequest", lim.MaxPagesPerRequest, nil)
		checkLimit(t, "MaxDocumentsPerMessage", lim.MaxDocumentsPerMessage, nil)
		checkLimit(t, "MaxDocumentsPerRequest", lim.MaxDocumentsPerRequest, nil)
	})

	t.Run("inline_pdf follows the same cascade", func(t *testing.T) {
		// Stated at the model row wins over provider and default.
		c := limitsDocJSON(t,
			map[string]any{"inline_pdf": true},
			map[string]any{"inline_pdf": true},
			map[string]any{"inline_pdf": false},
		)
		lim := mustLimits(t, c)
		checkInlinePDF(t, "InlinePDF", lim.InlinePDF, false)

		// First level that states it wins: provider true beats default false.
		c = limitsDocJSON(t,
			map[string]any{"inline_pdf": false},
			map[string]any{"inline_pdf": true},
			nil,
		)
		lim = mustLimits(t, c)
		checkInlinePDF(t, "InlinePDF", lim.InlinePDF, true)

		// Absent everywhere: not stated (absent = true is the consumer's
		// rule, not a value this method invents).
		c = limitsDocJSON(t, nil, nil, nil)
		lim = mustLimits(t, c)
		checkInlinePDF(t, "InlinePDF", lim.InlinePDF, nil)
	})
}

// TestDocumentLimits_NoGoDefaults (T4b, B) — a document with no
// document_limits and no default row resolves every field to no limit for
// EVERY provider id in the embedded snapshot. The oracle is §4.2's "no limit
// value lives in Go code": if a compiled per-provider or global default ever
// leaks into the resolution, a real provider id hits it here.
func TestDocumentLimits_NoGoDefaults(t *testing.T) {
	embedded, err := ParseDocument(EmbeddedSnapshot)
	if err != nil {
		t.Fatalf("embedded snapshot must parse: %v", err)
	}
	if len(embedded.Providers) == 0 {
		t.Fatal("embedded snapshot carries no providers — fixture broken")
	}

	// Build a document that carries the snapshot's provider ids, each with
	// one model, and NO document_limits anywhere.
	providers := make([]map[string]any, 0, len(embedded.Providers))
	for _, p := range embedded.Providers {
		providers = append(providers, map[string]any{
			"id":           p.ID,
			"name":         p.ID,
			"api":          "https://api.example.test/v1",
			"protocol":     "openai-compatible",
			"tier":         "standard",
			"auth_methods": []string{"api_key"},
			"models": []map[string]any{
				{
					"id":               "fx/limits-probe-model",
					"name":             "Probe",
					"context_window":   128000,
					"input_modalities": []string{"text", "pdf"},
					"tool_call":        true,
					"status":           "active",
				},
			},
		})
	}
	data, err := json.Marshal(map[string]any{
		"schema_version": "2.0.0",
		"version":        "v2026.11.22.1",
		"updated_at":     "2026-11-22T00:00:00Z",
		"source":         "test",
		"default_resize_limits": map[string]any{
			"long_edge_px": 7680, "max_bytes": 10485760,
		},
		"providers": providers,
	})
	if err != nil {
		t.Fatalf("marshal no-limits document: %v", err)
	}
	c, err := NewCatalog(data)
	if err != nil {
		t.Fatalf("NewCatalog(no-limits document): %v", err)
	}

	for _, p := range embedded.Providers {
		h := c.Resolve(p.ID, "fx/limits-probe-model")
		if !h.Found() {
			t.Fatalf("probe pair for provider %q must resolve", p.ID)
		}
		lim := h.DocumentLimits()
		checkInlinePDF(t, p.ID+" InlinePDF", lim.InlinePDF, nil)
		checkLimit(t, p.ID+" MaxFileBytes", lim.MaxFileBytes, nil)
		checkLimit(t, p.ID+" MaxTotalFileBytes", lim.MaxTotalFileBytes, nil)
		checkLimit(t, p.ID+" MaxRequestBytes", lim.MaxRequestBytes, nil)
		checkLimit(t, p.ID+" MaxPagesPerFile", lim.MaxPagesPerFile, nil)
		checkLimit(t, p.ID+" MaxPagesPerRequest", lim.MaxPagesPerRequest, nil)
		checkLimit(t, p.ID+" MaxDocumentsPerMessage", lim.MaxDocumentsPerMessage, nil)
		checkLimit(t, p.ID+" MaxDocumentsPerRequest", lim.MaxDocumentsPerRequest, nil)
	}
}

// TestParse_NewParserOldFeed (T5d, B) — the NEW parser on an OLD feed: the
// package's own committed 2.0.0 fixture (published before document_limits
// existed) parses, and the resolution yields no limits for its pairs.
func TestParse_NewParserOldFeed(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read committed fixture: %v", err)
	}
	c, err := NewCatalog(raw)
	if err != nil {
		t.Fatalf("the committed 2.0.0 fixture must parse on the new parser: %v", err)
	}
	doc := c.Document()
	if doc == nil || len(doc.Providers) == 0 {
		t.Fatal("fixture document carries no providers")
	}
	p := doc.Providers[0]
	if len(p.Models) == 0 {
		t.Fatalf("fixture provider %q carries no models", p.ID)
	}
	h := c.Resolve(p.ID, p.Models[0].ID)
	if !h.Found() {
		t.Fatalf("fixture pair %s/%s must resolve", p.ID, p.Models[0].ID)
	}
	lim := h.DocumentLimits()
	checkInlinePDF(t, "InlinePDF", lim.InlinePDF, nil)
	checkLimit(t, "MaxFileBytes", lim.MaxFileBytes, nil)
	checkLimit(t, "MaxTotalFileBytes", lim.MaxTotalFileBytes, nil)
	checkLimit(t, "MaxRequestBytes", lim.MaxRequestBytes, nil)
	checkLimit(t, "MaxPagesPerFile", lim.MaxPagesPerFile, nil)
	checkLimit(t, "MaxPagesPerRequest", lim.MaxPagesPerRequest, nil)
	checkLimit(t, "MaxDocumentsPerMessage", lim.MaxDocumentsPerMessage, nil)
	checkLimit(t, "MaxDocumentsPerRequest", lim.MaxDocumentsPerRequest, nil)
}
