// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

// Office-document preview — the three-way model rule is catalog-only
// (office-preview spec AD-11, FR-018; TDD row T-43, BDD B-09).
//
// Oracles: spec AD-11, FR-018 and §4.1; team-lead option A (2026-10-30)
// corrects the stale Haiku-via-Bedrock exclusion premise. Model capabilities
// and upstream limits belong to the authoritative catalog, not these tests.
// The rule is independent of model names: only the fixture's modalities
// decide; the hard-coded PDF list must be deleted (§9, AD-11).
//
// Original RED: loop_media.go still defined and called pdfCapableModel.
// The fixture-based rule below replaces the stale embedded-snapshot test.

import (
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// TestOfficePreview_HardcodedPDFModelListDeleted (AD-11, FR-018): the
// pdfCapableModel allow-list is deleted outright — no definition and no
// call site may survive (greenfield, no shim; migrations delete the old
// implementation outright).
func TestOfficePreview_HardcodedPDFModelListDeleted(t *testing.T) {
	data, err := os.ReadFile("loop_media.go")
	if err != nil {
		t.Fatalf("reading loop_media.go failed: %v", err)
	}
	if strings.Contains(string(data), "pdfCapableModel") {
		t.Fatalf("BLOCKED: loop_media.go still references pdfCapableModel — AD-11 deletes the hard-coded PDF model list outright (T-43, FR-018); the catalog alone decides")
	}
}

// TestOfficePreview_PDFModality_CatalogAloneDecides pins the rule on fixtures,
// not model facts: the same provider/model key changes capability when only
// its catalog modalities change. Model facts and Bedrock limits stay upstream.
func TestOfficePreview_PDFModality_CatalogAloneDecides(t *testing.T) {
	const provider, model = "fixture-provider", "fixture-model"
	cases := []struct {
		name       string
		modalities []string
		wantPDF    bool
		wantImage  bool
	}{
		{"native_pdf", []string{"text", "image", "pdf"}, true, true},
		{"page_images_without_pdf", []string{"text", "image"}, false, true},
		{"text_only_without_pdf", []string{"text"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, err := catalog.NewCatalog(catalogDocJSON(t, provider, model, tc.modalities...))
			if err != nil {
				t.Fatalf("loading test catalog fixture failed: %v", err)
			}
			if got := modelSupportsPDF(cat, provider, model); got != tc.wantPDF {
				t.Errorf("modelSupportsPDF(%s/%s) = %v, want %v for catalog modalities %v (AD-11)", provider, model, got, tc.wantPDF, tc.modalities)
			}
			if got := modelSupportsImage(cat, provider, model); got != tc.wantImage {
				t.Errorf("modelSupportsImage(%s/%s) = %v, want %v for catalog modalities %v (§4.1)", provider, model, got, tc.wantImage, tc.modalities)
			}
		})
	}
}

// TestOfficePreview_CatalogOnlyGateStillDecides (regression guard, green by
// contract): modelSupportsPDF reads only the catalog — nil catalog stays
// optimistic, a miss stays pessimistic (FR-004 posture the spec keeps).
func TestOfficePreview_CatalogOnlyGateStillDecides(t *testing.T) {
	if !modelSupportsPDF(nil, "openrouter", "anything/model") {
		t.Fatalf("nil catalog must stay optimistic for the PDF gate (media_present.go contract kept by AD-11)")
	}
}
