package catalog

// #1968 config-ownership revision R1/R5: catalogue PDF limits are unknown
// members. Old and new feeds load identically; only the pdf modality matters.
// Retain the original compatibility fixture and its known-structure control.

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// limitsFeedFixture is a minimal 2.0.0 document carrying the new
// document_limits members in every place §4 puts them (document default row,
// provider row, model row). Values are §4.3 seed data used as fixture data —
// the test asserts nothing about them, it only requires the feed to parse.
const limitsFeedFixture = `{
  "schema_version": "2.0.0",
  "version": "v2026.11.22.1",
  "updated_at": "2026-11-22T00:00:00Z",
  "source": "test",
  "default_resize_limits": {"long_edge_px": 7680, "max_bytes": 10485760},
  "default_document_limits": {
    "max_file_bytes": 50000000,
    "max_total_file_bytes": 50000000,
    "max_pages_per_file": 100,
    "max_documents_per_request": 5
  },
  "providers": [
    {
      "id": "fx/limits-provider",
      "name": "Fixture Limits Provider",
      "api": "https://api.example.test/v1",
      "protocol": "openai-compatible",
      "tier": "standard",
      "auth_methods": ["api_key"],
      "document_limits": {
        "max_request_bytes": 128000000,
        "max_pages_per_request": 100
      },
      "models": [
        {
          "id": "fx/limits-model",
          "name": "Fixture Limits Model",
          "context_window": 128000,
          "input_modalities": ["text", "pdf"],
          "tool_call": true,
          "status": "active",
          "document_limits": {
            "max_file_bytes": 4500000
          }
        }
      ]
    }
  ]
}`

// TestParse_OldParserIgnoresDocumentLimits (T5c, G) — the parser in this
// build accepts a feed that already carries document_limits members, and the
// rest of the document still resolves normally. An unknown JSON member is
// ignored, never a rejection: the publisher's new feed must not break old
// builds (spec §4.5 feeding order; FR-001's contract that only structural
// defects reject).
func TestParse_OldParserIgnoresDocumentLimits(t *testing.T) {
	doc, err := ParseDocument([]byte(limitsFeedFixture))
	if err != nil {
		t.Fatalf("a feed carrying document_limits members must parse on this build's parser, got: %v", err)
	}
	if len(doc.Providers) != 1 {
		t.Fatalf("providers parsed = %d, want 1", len(doc.Providers))
	}
	p := doc.Providers[0]
	if p.ID != "fx/limits-provider" {
		t.Fatalf("provider id = %q, want fx/limits-provider", p.ID)
	}
	if len(p.Models) != 1 || p.Models[0].ID != "fx/limits-model" {
		t.Fatalf("model rows did not survive the parse: %+v", p.Models)
	}

	// And the same feed through the whole Catalog constructor: a document
	// with the new members must load and resolve the pair.
	c, err := NewCatalog([]byte(limitsFeedFixture))
	if err != nil {
		t.Fatalf("NewCatalog on a document_limits-carrying feed must succeed, got: %v", err)
	}
	h := c.Resolve("fx/limits-provider", "fx/limits-model")
	if !h.Found() {
		t.Fatal("the fixture pair must resolve after a parse that ignored the new members")
	}
	if !h.Supports(ModalityPDF) {
		t.Fatal("the fixture model's pdf modality must survive the parse")
	}
}

// TestParse_RejectsBrokenStructure_UnderNewMembers (G, companion) — the
// unknown-member tolerance must not swallow STRUCTURAL defects: a feed whose
// document_limits member carries a wrong-shaped value where a known member
// sits (here: a non-array providers field, and a broken version) still
// rejects. This keeps the guard honest — it proves the parse path is really
// exercised, not silently short-circuited.
func TestParse_RejectsBrokenStructure_UnderNewMembers(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(limitsFeedFixture), &raw); err != nil {
		t.Fatalf("fixture must be valid JSON: %v", err)
	}
	raw["providers"] = "not an array"
	broken, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal broken fixture: %v", err)
	}
	if _, err := ParseDocument(broken); err == nil {
		t.Fatal("a structurally broken feed under the new members must still be rejected")
	} else if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a structural rejection must carry ErrInvalid, got: %v", err)
	}
}

// R1: even a stale field's wrong type/negative/unknown member is ignored.
// Compare the entire real parsed document AND served projection with a clean
// feed, not just successful decoding (which would miss retained limit data).
func TestParse_LegacyPDFLimitsIgnored(t *testing.T) {
	for _, value := range []string{`{"max_file_bytes":1,"inline_pdf":false}`, `{"max_file_bytes":-1,"typo":2}`, `"not an object"`, `[]`, `null`} {
		t.Run(value, func(t *testing.T) {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(limitsFeedFixture), &raw); err != nil {
				t.Fatal(err)
			}
			var providers []map[string]json.RawMessage
			if err := json.Unmarshal(raw["providers"], &providers); err != nil {
				t.Fatal(err)
			}
			var models []map[string]json.RawMessage
			if err := json.Unmarshal(providers[0]["models"], &models); err != nil {
				t.Fatal(err)
			}
			build := func(withLimits bool) []byte {
				if withLimits {
					raw["default_document_limits"] = json.RawMessage(value)
					providers[0]["document_limits"] = json.RawMessage(value)
					models[0]["document_limits"] = json.RawMessage(value)
				} else {
					delete(raw, "default_document_limits")
					delete(providers[0], "document_limits")
					delete(models[0], "document_limits")
				}
				var err error
				providers[0]["models"], err = json.Marshal(models)
				if err != nil {
					t.Fatal(err)
				}
				raw["providers"], err = json.Marshal(providers)
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			stale, err := NewCatalog(build(true))
			if err != nil {
				t.Fatalf("unknown PDF limits must be ignored: %v", err)
			}
			clean, err := NewCatalog(build(false))
			if err != nil {
				t.Fatalf("clean control must load: %v", err)
			}
			if !reflect.DeepEqual(clean.Document(), stale.Document()) {
				t.Fatal("unknown limits survived catalogue parsing")
			}
			got, ok := stale.Served()
			if !ok {
				t.Fatal("loaded feed has no served projection")
			}
			want, ok := clean.Served()
			if !ok {
				t.Fatal("control has no served projection")
			}
			if string(got.Body) != string(want.Body) || got.ETag != want.ETag {
				t.Fatal("ignored limits changed served catalogue bytes/ETag")
			}
			if !stale.Resolve("fx/limits-provider", "fx/limits-model").Supports(ModalityPDF) {
				t.Fatal("pdf modality was lost")
			}
			if stale.Resolve("fx/limits-provider", "missing").Supports(ModalityPDF) {
				t.Fatal("catalogue miss must not support PDF")
			}
		})
	}
}
