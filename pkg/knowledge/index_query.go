// index_query.go: Query the index — lookups, search, and result assembly over the open bleve index.

package knowledge

import (
	"errors"
	"fmt"
	"strings"
	stdunicode "unicode"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// FoldSearchText applies to arbitrary text the SAME normalisation the
// prose analyzer applies to every indexed token and every query term:
// lower-casing, Unicode NFC, then ASCII folding (analyzer_folded.go's
// foldProseTerm). It exists for a consumer that must locate a match in the
// ORIGINAL bytes of a note — the gateway's search excerpt — and therefore
// has to fold the note text exactly the way the matcher folded the query,
// or a hit the index found by folding ("cafe" finding "Café") renders with
// no excerpt at all.
//
// It is stateless and safe to call per rune: a caller that needs to map a
// folded byte offset back to the original text folds one rune at a time and
// sums the folded lengths. A combining mark that survives the fold on its own
// (NFD text folded rune by rune never composes) is dropped here, so
// "e" + U+0301 folds to "e" exactly as the composed "é" does.
func FoldSearchText(text string) string {
	if text == "" {
		return ""
	}
	folded := foldProseTerm(strings.ToLower(text))
	if folded == "" {
		return ""
	}
	// Drop any combining mark the per-rune path could not compose away.
	var b strings.Builder
	for _, r := range folded {
		if stdunicode.Is(stdunicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// IndexHit is one search result: exactly one per NOTE (or attachment), never
// one per segment.
type IndexHit struct {
	// Path is the collection-relative, slash-separated path.
	Path string
	// Kind is note or attachment.
	Kind ScanKind
	// Score is the relevance score of the file's BEST segment. It is a BM25
	// score because buildIndexMapping sets the scoring model explicitly; bleve's
	// default is TF-IDF (ADR-068 D21.1). Scores are comparable only within one
	// result set — BM25 is not normalised across queries or across indexes.
	Score float64
	// Offset is the absolute byte offset, within the file, of the start of the
	// best-scoring segment. FR-050a's query-time excerpt re-read starts here;
	// it is absolute precisely so segmentation cannot misdirect it.
	Offset int64
	// Segment is the ordinal of the best-scoring segment (0 for any file that
	// produced a single document).
	Segment int
	// SourceHash is the hex SHA-256 of the note's contents AS THE TEXT INDEX
	// LAST READ THEM — ADR-068 D16.5's freshness token, arriving with the hit
	// rather than being looked up afterwards.
	//
	// It is what the properties index's own `source_hash` column is compared
	// against: equal means the two indexes have seen the same bytes; different,
	// missing or empty means they have not, and the record goes into `problems`
	// with "the two indexes disagree" and `complete: false`. The comparison
	// establishes DISAGREEMENT, not which side is behind — claiming the second
	// is a precision the mechanism does not have.
	//
	// It is EMPTY for an attachment, always and by construction: FR-039a
	// forbids opening one and hashing is opening. An empty hash is unknown
	// freshness, which is flagged, never assumed fresh.
	SourceHash string
	// FallbackMode is true when this hit was produced by KB-7a's OR-ranked
	// fallback tier (searchRaw) rather than the strict AND tier — i.e. the
	// query's terms do not all appear in this note, and the result set as a
	// whole is looser than an exact answer. It is a property of the QUERY
	// this hit came from, not of the individual hit, so every hit returned
	// by one search call carries the same value; it rides on IndexHit
	// (rather than a call-level report only) so it survives every existing
	// narrow caller — Index.Search included — without forcing a signature
	// change on code outside this package.
	FallbackMode bool
}

// Search runs a query and returns at most limit results, ONE PER FILE. Hits are
// scored with BM25, which is in force because buildIndexMapping asks for it by
// name and the index was built under that mapping — bleve's default is TF-IDF
// (ADR-068 D21.1), and the model is read from the mapping persisted in the
// index, not from the one the code holds.
//
// FR-034a's segments are an implementation detail of bounded memory and must
// never reach the caller: a term appearing in three segments of one note is one
// result, scored by its best segment, carrying that segment's absolute byte
// offset so FR-050a's query-time excerpt re-read lands in the right place. The
// naive implementation returns three rows for one note and ranks them as three
// notes.
//
// limit is honoured as given — this layer does not silently clamp. FR-037's cap
// belongs to the tool/API layer, which must clamp AND report the clamping.
//
// It discards the truncation signal SearchFiltered now reports (FIX F7). That
// is deliberate, not an oversight: every production caller of this method
// reaches it through Searcher.Search / SearchFiltered instead (see search.go),
// which is where the truncation is folded into SearchReport.Complete. Search
// itself has no report to carry it in, and it has no production caller of its
// own — see index_test.go for its (many) direct callers, which do not read a
// completeness signal today.
//
// It also discards the KB-7a AND/OR-fallback signal SearchFiltered now
// reports, for the same reason: this method has no report to carry it in.
// Every hit still carries its own IndexHit.FallbackMode, so a caller of this
// narrower method is never left with no way to know — it just has to read
// the flag off the hits themselves rather than off a call-level report.
func (ix *Index) Search(query string, limit int) ([]IndexHit, error) {
	hits, _, _, err := ix.SearchFiltered(query, limit, nil)
	return hits, err
}

// indexSearchMaxFetch bounds how many raw segment hits one Search may pull while
// collapsing segments back into files. Without a bound, a query matching every
// segment of a very large note could pull the whole index into memory.
//
// It is a var, not a const, ONLY so a test can lower it (save, override,
// restore) to exercise the boundary against a fixture of a few dozen documents
// instead of the 2000+ real ones it would otherwise take to cross it (FIX F7's
// TestSearchFilteredReportsTruncationAtTheFetchCap). Production code never
// assigns to it; the default below is what ships.
var indexSearchMaxFetch = 2048

// SearchFiltered is Search restricted to the paths keep returns true for. A nil
// keep is the whole collection and makes this identical to Search.
//
// The filter is applied to the RAW segment hits, inside the escalating-fetch
// loop and before the limit is applied — so "the best `limit` matches inside
// this folder" is what comes back, not "whichever of the best `limit` matches
// in the collection happen to be in this folder". Those two differ the moment
// the collection has more matches than the limit, and the second silently
// returns a subset. The loop keeps widening its fetch until it has `limit`
// surviving files or bleve reports there are no more matching segments to see,
// so a narrow folder in a large collection is answered fully rather than
// emptily.
//
// truncated (FIX F7) reports the one case the loop cannot resolve either way:
// it hit indexSearchMaxFetch — the safety ceiling on how many raw segments one
// call may examine — WITHOUT having exhausted the corpus (more raw hits exist
// past the fetched prefix) and WITHOUT having already collected `limit`
// surviving files. Before this fix the loop's THIRD exit condition
// (`fetch >= indexSearchMaxFetch`) was folded into the same branch as the two
// honest exits ("found enough", "saw everything") with no way to tell them
// apart, so a caller filtering a folder whose matches rank below the fetch
// ceiling got back a confident, silently partial answer — as few as one row
// out of thirty real matches, indistinguishable from "there is only one".
//
// It is FALSE whenever `keep` is nil or matches broadly, because in that case
// the raw hits are already in global score order and reaching `limit`
// surviving files (or exhausting the corpus) within the fetched prefix proves
// there is nothing higher-ranked left unseen — see the two conditions ORed in
// the loop's first check below, which return before truncated is ever
// considered.
//
// fellBack (KB-7a) reports whether searchRaw had to drop from the strict
// AND tier to the OR-ranked fallback tier to answer this query at all — see
// searchRaw's own doc comment. It is a property of the QUERY, not of the
// fetch size, so every iteration of the escalating-fetch loop below agrees
// on it for one call (searchRaw's own AND-then-OR decision does not depend
// on `size`); it is threaded out of the loop rather than hardcoded to the
// last iteration's value purely so a future change to that invariant fails
// loudly here instead of silently reporting the wrong tier.
func (ix *Index) SearchFiltered(query string, limit int, keep func(relPath string) bool) ([]IndexHit, bool, bool, error) {
	if limit <= 0 {
		limit = 20
	}

	fetch := limit * 4
	if fetch < 20 {
		fetch = 20
	}

	for {
		if fetch > indexSearchMaxFetch {
			fetch = indexSearchMaxFetch
		}
		hits, total, fellBack, err := ix.searchRaw(query, fetch)
		if err != nil {
			return nil, false, false, err
		}
		for i := range hits {
			hits[i].FallbackMode = fellBack
		}
		if keep != nil {
			kept := hits[:0]
			for _, h := range hits {
				if keep(h.Path) {
					kept = append(kept, h)
				}
			}
			hits = kept
		}
		collapsed := collapseSegmentHits(hits)
		// Stop HONESTLY when we have enough distinct files, or when we have
		// already seen every matching segment (uint64(fetch) >= total): both
		// prove nothing higher-ranked is left unseen, per the doc comment
		// above.
		if len(collapsed) >= limit || uint64(fetch) >= total {
			if len(collapsed) > limit {
				collapsed = collapsed[:limit]
			}
			return collapsed, false, fellBack, nil
		}
		// Stop at the fetch ceiling WITHOUT either of the above being true
		// (FIX F7): more raw hits exist beyond what was examined, and fewer
		// than `limit` surviving files were found. This is the exit that used
		// to share the branch above and report exactly like it — a silent
		// truncation. Reported now, never silent.
		if fetch >= indexSearchMaxFetch {
			if len(collapsed) > limit {
				collapsed = collapsed[:limit]
			}
			return collapsed, true, fellBack, nil
		}
		fetch *= 4
	}
}

// queryableFields is the closed set of fields SearchField will accept.
//
// It is an allow-list rather than a validation of the string, because the
// alternative — passing a caller's field name to bleve — turns a typo into a
// query that matches nothing and reports no error, which is the exact failure
// shape ADR-068 §1.3 catalogues. fieldSourceHash and fieldOffset are absent on
// purpose: they are stored, not indexed, so a query against either would return
// nothing however it were spelled.
var queryableFields = map[string]struct{}{
	fieldPath:      {},
	fieldName:      {},
	fieldKind:      {},
	fieldTitle:     {},
	fieldHeadings:  {},
	fieldPropKey:   {},
	fieldPropValue: {},
	fieldProp:      {},
	fieldBody:      {},
}

// ErrUnknownField means a field query named a field the index does not have.
//
// It is an ERROR rather than an empty result, and that is the entire point of
// the allow-list behind it. bleve answers a query against a field it has never
// heard of with zero hits and no error, which is indistinguishable from "no
// note matches" — the confidently-wrong-answer shape ADR-068 §1.3 catalogues.
var ErrUnknownField = errors.New("knowledge: unknown index field")

// SearchField runs an exact TERM query against one field and returns at most
// limit results, one per file.
//
// This is ADR-068 D21.2's exit criterion, and the criterion is that it is
// possible AT ALL: before fielded indexing there was no field to query. A
// property key lived in the body as a loose prose token, so "which notes
// declare a `status`?" could only be asked as a full-text search for the word
// "status", which also matches every note that merely uses the word in a
// sentence — and reported Complete: true while doing it.
//
// Two field names are worth naming here because they are the ones a caller
// actually wants:
//
//	SearchField(fieldPropKey, "status", 20)          // notes that HAVE a status
//	SearchField(fieldProp, "status=prospect", 20)    // notes whose status IS prospect
//
// Both are keyword-analysed and case-folded at index time by fields.go, so the
// term must be folded the same way — which foldFieldTerm does here rather than
// leaving each caller to remember.
//
// # IT IS A MATCH QUERY, AND A TERM QUERY WOULD BE THE OBVIOUS WRONG ANSWER
//
// A field query names an exact value, so a raw term query looks right. It is
// not: a term query performs NO analysis, while every prose field in this index
// was written through the `en` analyzer, which lowercases and Porter-stems. A
// term query for `renewal` against the headings field looks for the literal
// term "renewal" in a dictionary that only ever contains "renew", and finds
// nothing — zero hits, no error, the exact failure shape this method exists to
// remove. (Measured, not reasoned: the first version of this method was a term
// query and five of these tests failed on precisely that.)
//
// A match query analyses the caller's term with THE FIELD'S OWN analyzer, which
// is the only rule that is right for every field at once — stemming for the
// prose fields, and the whole string as one term for the keyword ones, with no
// per-field branch here that could disagree with the mapping.
//
// The operator is AND: a multi-word field query asks for a field containing all
// of those words, not any of them. OR is what Search is for.
//
// THIS IS NOT A TYPED FILTER. ADR-068 D16.2b as reversed is explicit: the
// properties index narrows candidates and our own tested comparator decides
// every typed comparison. This narrows text candidates. It does not compare
// dates, it does not compare numbers, and no caller may treat a hit from it as
// a comparison having been evaluated.
func (ix *Index) SearchField(field, term string, limit int) ([]IndexHit, error) {
	if _, ok := queryableFields[field]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownField, field)
	}
	if strings.TrimSpace(term) == "" {
		return nil, fmt.Errorf("knowledge: field query on %q has no term", field)
	}
	if limit <= 0 {
		limit = 20
	}

	tq := bleveQuery.NewMatchQuery(foldFieldTerm(field, term))
	tq.SetField(field)
	tq.SetOperator(bleveQuery.MatchQueryOperatorAnd)

	// Segments collapse to one hit per file exactly as they do for Search, and
	// for the same reason: a property declared once in a note that happens to
	// be five segments long is one note, not five.
	fetch := limit * 4
	if fetch < 20 {
		fetch = 20
	}
	if fetch > indexSearchMaxFetch {
		fetch = indexSearchMaxFetch
	}
	hits, _, err := ix.runSearch(tq, fetch, fmt.Sprintf("%s:%s", field, term))
	if err != nil {
		return nil, err
	}
	collapsed := collapseSegmentHits(hits)
	if len(collapsed) > limit {
		collapsed = collapsed[:limit]
	}
	return collapsed, nil
}

// foldFieldTerm applies to a query term the same transformation fields.go
// applied to the indexed one.
//
// Only the two fields that are written folded are folded here. Folding a `path`
// or a `kind` term would silently change what a caller asked for, and folding a
// prose field's term does nothing the analyzer has not already done.
func foldFieldTerm(field, term string) string {
	switch field {
	case fieldPropKey, fieldProp:
		return records.FoldKey(strings.TrimSpace(term))
	default:
		return term
	}
}

// searchRaw executes one bleve query and returns the raw per-SEGMENT hits.
// prefixSearchTokenizer tokenizes a query for the prefix pass EXACTLY as the
// prose ("en") field analyzer tokenizes text for indexing — the Unicode UAX#29
// word tokenizer — so a prefix token is compared against the term dictionary on
// the same word boundaries the dictionary was built with. It is stateless and
// safe to share.
var prefixSearchTokenizer = unicode.NewUnicodeTokenizer()

// prefixSearchTokens returns the lower-cased query tokens the prefix pass
// prefix-matches against the prose dictionaries.
//
// It MUST tokenize the way the prose analyzer does, NOT the way
// foldName/foldTokens does. foldName collapses EVERY non-alphanumeric rune —
// the underscore included — to a break, so it split "keyword_new" into
// "keyword" and "new". A PrefixQuery("keyword") then matched the unrelated
// indexed term "keyword_old", because the "en" analyzer keeps "keyword_old" as
// ONE term (Unicode word segmentation treats "_" as an ExtendNumLet connector).
// That over-match made an edited note findable by its NEW term BEFORE the edit
// and by its OLD term AFTER — the round-2 regression. Tokenizing with the same
// Unicode tokenizer keeps "keyword_new" whole, so its prefix matches only terms
// that actually begin "keyword_new"; "compos" still tokenizes to "compos" and
// still prefix-matches "composio".
//
// Case is folded with Unicode lower-casing, which is what the "en" analyzer's
// to_lower filter applied when building the dictionary — deliberately NOT
// records.FoldKey (the name-ranking fold), which folds more aggressively
// (ß→ss) than the dictionary was built and would therefore MISS a term rather
// than over-match it. Stemming is deliberately skipped for the reason above.
//
// Since D-129 the dictionary is also NFC-normalised and ASCII-folded
// (en_folded, analyzer_folded.go), so each token is passed through
// foldProseTerm after lower-casing — the same two steps in the same order
// the analyzer applied — or a prefix "caf" typed as "café" would never
// reach the dictionary's "cafe".
func prefixSearchTokens(query string) []string {
	tokens := prefixSearchTokenizer.Tokenize([]byte(query))
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if term := foldProseTerm(strings.ToLower(string(tok.Term))); term != "" {
			out = append(out, term)
		}
	}
	return out
}

// textSearchFields is every field a free-text query is matched against.
// Declared once so the AND tier (buildAndQuery) and the OR tier
// (buildOrQuery) can never drift apart on which fields a query reaches —
// see fusionFieldWeights' own header in rank.go for what happened the last
// time this project kept two field lists that were supposed to agree.
var textSearchFields = []string{
	fieldName, fieldPath, fieldBody,
	// D21.2's fields. A term in a note's title, one of its headings or
	// one of its property values is now a reason to return the note,
	// where before the title and the headings were only findable
	// because they happened to also be body text and the property
	// values were findable as prose that had lost its key.
	fieldTitle, fieldHeadings, fieldPropValue,
	// prop_key is keyword-analysed, so a match query against it asks
	// "is the whole query string the name of a property this note
	// declares?". A search for `status` therefore finds every note
	// that HAS a status, which is a question the index could not
	// answer at all before.
	fieldPropKey,
}

// textSearchProseFields is the subset of textSearchFields that carries real
// prose through the "en" analyzer, as opposed to a keyword-analysed whole-
// string field (path/prop_key/prop). Both the prefix pass and the fuzzy
// pass are prose-only for the same reason: prefixing or fuzz-matching a
// keyword field would silently change what an exact pair or key query
// means, because that field stores its whole value as one term.
var textSearchProseFields = []string{fieldName, fieldBody, fieldTitle, fieldHeadings, fieldPropValue}

// fuzzyMatchBoost is the boost applied to every fuzzy clause in the
// OR-fallback query (KB-7a). It is deliberately far below the exact
// clauses' default boost of 1.0 (Boost.Value() — see bleve's query/boost.go
// — treats an unset BoostVal as 1.0), so a document that matches ONLY on a
// fuzzy (edit-distance) term cannot outrank a document that matches any
// exact clause: BM25's per-clause contribution is always positive, so
// scaling the fuzzy side down by 20x leaves an enormous margin against a
// single weak exact match ever losing to a fuzzy one. This is what "fuzzy
// matches must rank below exact ones" (KB-6/KB-7's ratified design) means
// operationally — there is no separate sort key for it, only this boost
// gap.
const fuzzyMatchBoost = 0.05

// buildOrQuery is the pre-KB-7 production query: a disjunction of per-field
// match queries over the WHOLE query string (operator OR within each
// field's own analysis), plus a prefix pass on the prose fields (F2 /
// harness Issue 14). It is unchanged from the query searchRaw built before
// KB-7 — every existing caller that only ever reaches this tier (a
// single-term query that matches something) sees byte-identical results.
//
// When fuzzy is true, one additional MatchQuery per prose field is added
// with SetFuzziness(1) and fuzzyMatchBoost — KB-7a's typo tolerance. It is
// deliberately confined to THIS tier and never added to buildAndQuery's
// conjunction: a fuzzy clause inside an AND would let a single mistyped
// word silently loosen every OTHER term's match into an edit-distance
// search too, which is precision the AND tier exists to guarantee. Typo
// tolerance only ever earns a place in the looser, already-degraded
// fallback pass.
func buildOrQuery(query string, fuzzy bool) bleveQuery.Query {
	qs := make([]bleveQuery.Query, 0, len(textSearchFields)+2*len(textSearchProseFields))
	for _, field := range textSearchFields {
		mq := bleveQuery.NewMatchQuery(query)
		mq.SetField(field)
		qs = append(qs, mq)
	}
	// PREFIX MATCHING (F2 / harness Issue 14). The match queries above are
	// exact-term-after-analysis: they find a note for `composio` but not for
	// `compos`, because `compos` analyses to the term "compos" and the body
	// dictionary holds "composio" (or its stem), which is a different term.
	// A caller typing a partial word expects the fuller term to be found —
	// the same expectation NearMissVocabulary already serves when it offers
	// `compos → composio` as a suggestion, so a `words` search must actually
	// honour what the suggestion promises rather than only naming it.
	//
	// A bleve PrefixQuery matches the term DICTIONARY by raw byte prefix and
	// performs no analysis of its own, so the prefix must arrive tokenized
	// and folded the SAME way the prose analyzer built the dictionary —
	// prefixSearchTokens does that (Unicode word tokenizer + lower-case, no
	// stem). It deliberately does NOT stem: a stem would shorten the prefix
	// past the very characters the caller typed. It also must not use
	// foldTokens, which splits on the underscore the "en" analyzer keeps
	// inside a token — see prefixSearchTokens for the over-match that caused
	// (round-2 regression). These disjuncts only ever ADD matches to the
	// exact ones above; they never remove one, so a query that already
	// matched exactly is unaffected. Only the PROSE fields get a prefix pass
	// — a keyword field (path/prop_key/prop) stores each value as one whole
	// term where prefixing would silently change what an exact pair or key
	// query means.
	for _, token := range prefixSearchTokens(query) {
		// vocabularyPrefixMin guards against a 1–2 character prefix matching
		// a large fraction of the dictionary — the same floor the
		// vocabulary suggester uses for the same reason.
		if len([]rune(token)) < vocabularyPrefixMin {
			continue
		}
		for _, field := range textSearchProseFields {
			pq := bleveQuery.NewPrefixQuery(token)
			pq.SetField(field)
			qs = append(qs, pq)
		}
	}
	if fuzzy {
		for _, field := range textSearchProseFields {
			mq := bleveQuery.NewMatchQuery(query)
			mq.SetField(field)
			mq.SetFuzziness(1)
			mq.SetBoost(fuzzyMatchBoost)
			qs = append(qs, mq)
		}
	}
	return bleve.NewDisjunctionQuery(qs...)
}

// buildAndQuery is KB-7a's primary tier: a note is a candidate only when
// EVERY query term is present SOMEWHERE in the note, not merely when any one
// term is present anywhere in the collection. This is the "notes containing
// BOTH terms" test the founder's own measurement used (784-note vault,
// query "investment report": 0 notes contain both, 12 contain "investment",
// 212 contain "report", and the pre-fix OR search returned all 224).
//
// It is a document-level AND, not a per-field one: for each term, a
// DISJUNCTION across every textSearchField (plus the same prefix pass
// buildOrQuery uses, so "compos report" still partial-matches "composio"
// under AND) decides whether that term is present ANYWHERE in the note —
// term T in the title and term U only in the body both count. The per-term
// disjunctions are then wrapped in one CONJUNCTION, so a document must
// satisfy every term's own "present somewhere" test independently. A
// simpler per-FIELD AND (SearchField's own MatchQueryOperatorAnd pattern,
// requiring all terms in the SAME field) was considered and rejected: it
// would miss a real match split across fields (e.g. the note's TITLE names
// one term and its BODY discusses the other), which is not what "all query
// terms must appear" promises.
//
// A single-term query degenerates to one term's own disjunction, i.e. the
// same fields buildOrQuery(query, false) would search — so "a single word
// searches fine" (KB-7's own framing) is preserved unchanged; the AND/OR
// distinction only has teeth once there are two or more terms to relate.
func buildAndQuery(terms []string) bleveQuery.Query {
	perTerm := make([]bleveQuery.Query, 0, len(terms))
	for _, term := range terms {
		qs := make([]bleveQuery.Query, 0, len(textSearchFields)+len(textSearchProseFields))
		for _, field := range textSearchFields {
			mq := bleveQuery.NewMatchQuery(term)
			mq.SetField(field)
			qs = append(qs, mq)
		}
		if len([]rune(term)) >= vocabularyPrefixMin {
			for _, field := range textSearchProseFields {
				pq := bleveQuery.NewPrefixQuery(term)
				pq.SetField(field)
				qs = append(qs, pq)
			}
		}
		perTerm = append(perTerm, bleve.NewDisjunctionQuery(qs...))
	}
	if len(perTerm) == 1 {
		return perTerm[0]
	}
	return bleve.NewConjunctionQuery(perTerm...)
}

// searchRaw executes one free-text query and returns the raw per-SEGMENT
// hits, KB-7a's tier the fallback was used, and the total bleve reports.
//
// Two tiers, tried in order:
//
//  1. AND — buildAndQuery, every term required. This is the tier that
//     answers "investment report" with the 12 notes that actually mention
//     investment, instead of the 224 that mention either word.
//  2. OR, WITH FUZZINESS — buildOrQuery(query, true), tried ONLY when tier 1
//     found nothing, so a too-narrow query degrades to the pre-KB-7
//     production behaviour (plus typo tolerance) instead of dead-ending on
//     an empty answer. fellBack tells the caller which tier actually
//     answered, so the honesty layer (search.go's SearchReport) can say so
//     rather than presenting a loosened answer as if it were exact.
//
// An empty query keeps the pre-existing MatchAllQuery behaviour untouched —
// there are no terms to relate, so neither tier's distinction applies.
func (ix *Index) searchRaw(query string, size int) ([]IndexHit, uint64, bool, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		hits, total, err := ix.runSearch(bleve.NewMatchAllQuery(), size, query)
		return hits, total, false, err
	}

	// Tier 1 needs at least one term to build a query from; a query that is
	// non-empty after TrimSpace but tokenizes to nothing (e.g. pure
	// punctuation) has no AND tier to try and goes straight to tier 2 with
	// the ORIGINAL query text, exactly as searchRaw did before KB-7a.
	//
	// A SINGLE term also goes through buildAndQuery, not around it:
	// buildAndQuery(terms) with one term degenerates to that term's own
	// disjunction across fields — the same query buildOrQuery(query, false)
	// would build for a one-word query — so this is not a second, different
	// query for the single-word case. Routing it through tier 1 rather than
	// straight to tier 2 is what keeps a successful single-word search
	// reported as fellBack=false (an exact match, not a relaxed one) and
	// keeps fuzzy clauses OUT of it entirely unless it genuinely finds
	// nothing — "a single word searches fine" (KB-7's own framing) must not
	// regress into every one-word query being marked as a loosened answer.
	terms := prefixSearchTokens(query)
	if len(terms) >= 1 {
		hits, total, err := ix.runSearch(buildAndQuery(terms), size, query)
		if err != nil {
			return nil, 0, false, err
		}
		if total > 0 {
			return hits, total, false, nil
		}
	}
	hits, total, err := ix.runSearch(buildOrQuery(query, true), size, query)
	return hits, total, true, err
}

// runSearch executes one bleve query and decodes its hits.
//
// It is shared by the free-text path and the field-query path so that the
// retrieved stored fields and the tie-break are decided ONCE. Two copies of
// req.Fields is how a field gets added to one search path and not the other,
// and the symptom of that is a hit whose SourceHash is empty for no reason the
// caller can see — reported by D16.5 as "the two indexes disagree" about a note
// that is perfectly fresh.
//
// label appears in the error only; it is what the caller asked for, in whatever
// form makes the failure readable.
func (ix *Index) runSearch(q bleveQuery.Query, size int, label string) ([]IndexHit, uint64, error) {
	req := bleve.NewSearchRequestOptions(q, size, 0, false)
	req.Fields = []string{fieldPath, fieldKind, fieldOffset, fieldSourceHash}
	req.SortBy([]string{"-_score", "_id"}) // deterministic ties (FR-046)

	res, err := ix.idx.Search(req)
	if err != nil {
		return nil, 0, fmt.Errorf("knowledge: search %q: %w", label, err)
	}
	out := make([]IndexHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		relPath, ordinal := splitSegmentDocID(h.ID)
		hit := IndexHit{Path: relPath, Score: h.Score, Segment: ordinal, Kind: ScanKindNote}
		if v, ok := h.Fields[fieldPath].(string); ok && v != "" {
			hit.Path = v
		}
		if v, ok := h.Fields[fieldKind].(string); ok && v != "" {
			hit.Kind = ScanKind(v)
		}
		if v, ok := h.Fields[fieldOffset].(float64); ok {
			hit.Offset = int64(v)
		}
		// D16.5. A missing or empty value is left empty rather than defaulted:
		// unknown freshness must reach the caller as unknown, because the
		// caller's rule is to flag it, and a default would make it look known.
		if v, ok := h.Fields[fieldSourceHash].(string); ok {
			hit.SourceHash = v
		}
		out = append(out, hit)
	}
	return out, res.Total, nil
}

// collapseSegmentHits folds every segment of a file into ONE result, keeping the
// best-scoring segment's score and offset, and preserves descending score order.
func collapseSegmentHits(hits []IndexHit) []IndexHit {
	best := make(map[string]IndexHit, len(hits))
	order := make([]string, 0, len(hits))
	for _, h := range hits {
		prev, seen := best[h.Path]
		if !seen {
			best[h.Path] = h
			order = append(order, h.Path)
			continue
		}
		if h.Score > prev.Score || (h.Score == prev.Score && h.Segment < prev.Segment) {
			best[h.Path] = h
		}
	}
	out := make([]IndexHit, 0, len(order))
	for _, p := range order {
		out = append(out, best[p])
	}
	return out
}
