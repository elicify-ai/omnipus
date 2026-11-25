package catalog

// Document-limits parse guards for spec #1968 (read-file native PDF, FR-4).
//
// T5c — TestParse_OldParserIgnoresDocumentLimits (label G: passes on base and
// must stay green after the implementation lands). The catalogue feed gains
// `document_limits` on provider/model rows and `default_document_limits` on
// the document (spec §4). This build's parser must accept a feed that
// already carries those members today — the publisher ships the new feed
// before every client build understands it, and an unknown member is
// ignored, never a parse rejection. The oracle is spec §4.5 (feeding order)
// read together with the parser contract of FR-001: structural defects
// reject; unknown members do not.
//
// These are parse-level guards only: they assert the OLD parser tolerates
// the NEW feed shape. What the new fields MEAN is T4/T4b/T5d's business
// (document_limits_buildfail_test.go).

import (
	"encoding/json"
	"errors"
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
