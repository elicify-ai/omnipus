// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

// Office-document preview — the three-way model rule is catalog-only
// (office-preview spec AD-11, FR-018; TDD row T-43, BDD B-09).
//
// Oracles from the spec (AD-11), never from the code:
//   - "Catalog only: pkg/agent/media_present.go::modelSupportsPDF /
//     ::modelSupportsImage. pkg/agent/loop_media.go::pdfCapableModel is
//     deleted; its Haiku-via-Bedrock exclusion becomes the catalog entry
//     for that provider + model, in the authoritative catalog source."
//   - §9 prohibitions: "keep ... the PDF model list" is forbidden.
//
// RED reasons on the pre-change code:
//  1. loop_media.go still defines and calls pdfCapableModel (the deleted
//     hard-coded list) — the source scan below fails.
//  2. The embedded catalog still lists the "pdf" modality for the
//     Haiku-via-Bedrock entries the exclusion must move into (verified
//     before writing: amazon-bedrock anthropic.claude-haiku-4-5-20251001-v1:0
//     carries ['text','image','pdf']).

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

// TestOfficePreview_CatalogHaikuViaBedrockWithoutPDF (AD-11): the
// Haiku-via-Bedrock exclusion lives in the authoritative catalog source —
// every amazon-bedrock Haiku entry exists and lists NO pdf modality.
func TestOfficePreview_CatalogHaikuViaBedrockWithoutPDF(t *testing.T) {
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	if err != nil {
		t.Fatalf("loading embedded catalog snapshot failed: %v", err)
	}
	doc := cat.Document()

	entries := 0
	for _, provider := range doc.Providers {
		if provider.ID != "amazon-bedrock" {
			continue
		}
		for _, model := range provider.Models {
			if !strings.Contains(strings.ToLower(model.ID), "haiku") {
				continue
			}
			entries++
			for _, modality := range model.InputModalities {
				if modality == catalog.ModalityPDF {
					t.Fatalf("BLOCKED: catalog entry amazon-bedrock/%s lists the pdf modality — AD-11 moves the Haiku-via-Bedrock exclusion INTO this entry (T-43, B-09): the exclusion is missing", model.ID)
				}
			}
		}
	}
	if entries == 0 {
		t.Fatalf("no amazon-bedrock Haiku entry found in the embedded catalog — the AD-11 exclusion must exist as a real catalog entry, not as silence")
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
