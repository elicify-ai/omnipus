// Omnipus — the prose analyzer the text index writes and reads with.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// UAT 2026-09-13, D-129 (and the text-index half of D-99). The prose fields
// (body, name, title, headings, prop_value) were analysed with bleve's stock
// "en" analyzer: Unicode word tokenizer, possessive strip, lower-case,
// English stop words, Porter stem. Nothing in that pipeline folds an accent
// or normalises Unicode, so:
//
//   - `cafe` did not find "Café" and `resume` did not find "résumé", while
//     `zurich` DID find "Zürich" — not by folding but by the OR tier's
//     fuzzy fallback (edit distance 1), which reaches a six-letter word and
//     not a four-letter one. A user was being taught that accents fold when
//     they fold only sometimes.
//   - "café.png" written in NFC (U+00E9) and "café (1).png" written in NFD
//     ("e" + U+0301, what macOS names files) are indistinguishable on screen
//     and answered to DIFFERENT queries, because the tokenizer keeps the
//     combining mark inside the token and nothing recomposed it.
//
// This file defines "en_folded": the same pipeline with two extra token
// filters between lower-casing and stop-word removal — Unicode NFC (so the
// two spellings above become one term) and then ASCII folding (so "café"
// and "cafe" become one term, and "Zürich" and "zurich" without relying on
// fuzziness). Every place that builds a term OUTSIDE the analyzer — the
// prefix pass, the vocabulary suggester, the per-term counts — folds with
// foldProseTerm, which is the same NFC + ASCII fold, so a dictionary lookup
// compares like with like.
//
// The change is to the PERSISTED mapping, so an index written under the
// old analyzer is rebuilt once on open: G1 (indexFormatVersion 4) fires
// before the open, and G2 (mappingDrift) would catch the analyzer name on
// every field anyway.

package knowledge

import (
	"strings"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/char/asciifolding"
	"github.com/blevesearch/bleve/v2/analysis/lang/en"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/token/porter"
	"github.com/blevesearch/bleve/v2/analysis/token/unicodenorm"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/registry"
	"golang.org/x/text/unicode/norm"
)

// proseAnalyzerName is the analyzer every prose field is mapped to.
const proseAnalyzerName = "en_folded"

// asciiFoldTokenFilterName is the registry name of the token filter below.
// bleve ships ASCII folding only as a CHAR filter (it runs before the
// tokenizer, i.e. before NFC could recompose a combining mark), so it is
// wrapped here as a token filter that runs after normalize_unicode.
const asciiFoldTokenFilterName = "omnipus_ascii_fold"

// nfcTokenFilterName is the mapping-local name of the normalize_unicode
// filter configured for NFC.
const nfcTokenFilterName = "omnipus_nfc"

// asciiFoldTokenFilter applies bleve's ASCII folding table to each token's
// term in place.
type asciiFoldTokenFilter struct {
	fold *asciifolding.AsciiFoldingFilter
}

func (f *asciiFoldTokenFilter) Filter(in analysis.TokenStream) analysis.TokenStream {
	for _, tok := range in {
		tok.Term = f.fold.Filter(tok.Term)
	}
	return in
}

func asciiFoldTokenFilterConstructor(map[string]any, *registry.Cache) (analysis.TokenFilter, error) {
	return &asciiFoldTokenFilter{fold: asciifolding.New()}, nil
}

func init() {
	if err := registry.RegisterTokenFilter(asciiFoldTokenFilterName, asciiFoldTokenFilterConstructor); err != nil {
		panic(err)
	}
}

// registerProseAnalyzer adds en_folded (and the NFC filter it uses) to a
// mapping. It is called from buildIndexMapping, so every index this package
// creates persists the definition alongside the fields that reference it.
func registerProseAnalyzer(m *bleveMapping.IndexMappingImpl) error {
	if err := m.AddCustomTokenFilter(nfcTokenFilterName, map[string]any{
		"type": unicodenorm.Name,
		"form": unicodenorm.NFC,
	}); err != nil {
		return err
	}
	return m.AddCustomAnalyzer(proseAnalyzerName, map[string]any{
		"type":      custom.Name,
		"tokenizer": unicode.Name,
		"token_filters": []any{
			en.PossessiveName,
			lowercase.Name,
			nfcTokenFilterName,
			asciiFoldTokenFilterName,
			en.StopName,
			porter.Name,
		},
	})
}

// proseTermFolder is the query-side twin of the analyzer's NFC + ASCII fold
// step, shared by every caller of foldProseTerm. Stateless and safe to share.
var proseTermFolder = asciifolding.New()

// foldProseTerm applies to an already lower-cased query token the SAME
// normalisation and folding the en_folded analyzer applied to the dictionary
// term it will be compared against: Unicode NFC, then ASCII folding. It does
// not stem and does not lower-case (callers do that where the analyzer
// would). A prefix pass, a vocabulary lookup or a per-term count that
// skipped this step would compare "café" against a dictionary that only
// holds "cafe" and miss.
func foldProseTerm(term string) string {
	if term == "" {
		return ""
	}
	nfc := norm.NFC.String(term)
	folded := proseTermFolder.Filter([]byte(nfc))
	return strings.TrimSpace(string(folded))
}

// foldProseTerms is foldProseTerm over a token list, dropping tokens that
// fold to nothing.
func foldProseTerms(terms []string) []string {
	out := terms[:0:0]
	for _, t := range terms {
		if f := foldProseTerm(t); f != "" {
			out = append(out, f)
		}
	}
	return out
}
