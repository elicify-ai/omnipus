// Omnipus — the committed ranking-evaluation fixture (ADR-068 FR-113).
//
// This file builds the corpus and the query set that decide whether the D21.3
// fusion ships. It is therefore the file most worth attacking: every way an
// evaluation can be rigged is a way of making a ranking change look good, and a
// rigged eval is worse than no eval because it manufactures the confidence the
// eval existed to withhold.
//
// # What the eval is
//
// FR-113 asks for a human-authored query set with graded 0/1/2 judgements. None
// exists, and authorship cannot be manufactured. What is buildable is a
// SELF-GENERATED KNOWN-ITEM evaluation:
//
//	take a sentence out of a note -> reduce it to the query a person would type
//	-> the ground truth is the note the sentence came from.
//
// The judge is unbiased in the way that matters: relevance is established by
// PROVENANCE, not by lexical overlap with any ranker's notion of similarity. No
// ranking under test had any hand in deciding what the right answer is.
//
// # What the eval is NOT, stated before any number is reported
//
// It is binary, not graded, and it is a known-item task rather than a graded
// relevance task. Those two facts bound what it can conclude:
//
//   - it CAN detect regression — a fusion that makes the source note harder to
//     find is worse, full stop;
//   - it CANNOT establish improvement for the query-independent priors, because
//     the corpus is built so that recency and backlink degree are STATISTICALLY
//     INDEPENDENT of which note is ground truth.
//
// That independence is the anti-rigging property and it is asserted, not
// claimed: TestRankEval_GroundTruthIsNotPrivileged fails if the sampled ground
// truth is more linked, or more recent, than the corpus average. Without it the
// obvious mistake is to seed the corpus so the answer is always a hub, measure
// the backlink signal, and discover it works.
//
// # How difficulty is manufactured, and why it has to be
//
// A corpus of 520 notes about 520 unrelated subjects makes every known-item
// query trivial: one note contains the words, BM25 puts it first, every ranking
// scores ~1.0, and the measurement has no headroom to detect anything. Real
// vaults are not like that, and neither is this one:
//
//   - notes cluster on a small topic vocabulary, so many notes share most of any
//     query's terms;
//   - entity names OVERLAP by construction ("Northwind Ledger", "Harbour
//     Ledger", "Northwind Atlas"), so a name match is a real discrimination
//     problem rather than a lookup;
//   - every project's meetings and decisions repeat the project's name and
//     topic, so the strongest distractors for a meeting note are its own
//     project and its sibling meetings.
//
// # Independence of the random streams
//
// Four separate RNG streams, each with its own fixed seed. Link structure,
// timestamps, prose and query sampling never share a stream. Sharing one would
// couple "which note is ground truth" to "which note is a hub" through nothing
// more visible than call order, and that coupling is exactly the rigging the
// guard above exists to catch — it should not be reachable in the first place.
//
// # Committed, not generated per run
//
// FR-113 requires the corpus and queries to be committed. They are: the
// artifacts under testdata/rankeval/ are the fixture, and the generator here
// runs only under -rankeval.generate. The materialiser writes the committed
// bytes into a temp dir; it never regenerates them. A test run that silently
// regenerated its own fixture would be measuring whatever it had just decided
// to measure.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// regenerateFixture gates the generator. Without it the fixture is read, never
// written.
var regenerateFixture = flag.Bool("rankeval.generate", false,
	"regenerate the committed pkg/knowledge/testdata/rankeval fixture")

const (
	rankEvalDir        = "testdata/rankeval"
	rankEvalCorpusFile = "corpus.jsonl"
	rankEvalQueryFile  = "queries.json"

	// rankEvalQueriesPerCondition is FR-113's stated query-set size.
	rankEvalQueriesPerCondition = 30
)

// evalNote is one note of the committed corpus. Body is stored whole so the
// materialised bytes are exactly the committed bytes.
type evalNote struct {
	Path string `json:"path"`
	// Type is the record type in the note's frontmatter. FR-113 requires at
	// least four across the corpus.
	Type string `json:"type"`
	// UpdatedUnix is the mtime the materialiser stamps on the file, so the
	// recency signal reads the fixture's intent rather than the checkout time.
	UpdatedUnix int64 `json:"updated_unix"`
	// Body is the complete file content, frontmatter included.
	Body string `json:"body"`
}

// evalQuery is one query and its ground truth.
type evalQuery struct {
	// ID is stable across regenerations so a per-query table can be diffed.
	ID string `json:"id"`
	// Condition is "uniform" (the verdict basis) or "popular" (a secondary,
	// clearly-labelled user-behaviour assumption — see rank_eval_test.go).
	Condition string `json:"condition"`
	// Question is the human-readable phrasing, for a reader of this file. It is
	// NOT what is executed.
	Question string `json:"question"`
	// Query is the executed query: the sentence's content terms.
	Query string `json:"query"`
	// Relevant maps a note path to its graded relevance. Binary in practice —
	// the source note scores 2 and nothing else scores at all — and the map
	// shape is graded so the metric code is the real one rather than a
	// single-document special case.
	Relevant map[string]int `json:"relevant"`
	// SourceSentence is the sentence the query was reduced from, kept so a
	// reviewer can check the reduction by eye.
	SourceSentence string `json:"source_sentence"`
}

// evalFixture is the whole committed artifact.
type evalFixture struct {
	Notes   []evalNote  `json:"-"`
	Queries []evalQuery `json:"queries"`
}

// ---------------------------------------------------------------------------
// Vocabulary. Deliberately small and overlapping.
// ---------------------------------------------------------------------------

var (
	evalTopics = []string{
		"pricing", "onboarding", "migration", "retention", "compliance",
		"latency", "billing", "forecasting", "staffing", "security",
		"procurement", "localisation",
	}
	evalAspects = []string{
		"the rollout plan", "the discount tier", "the audit trail",
		"the support rota", "the data model", "the renewal date",
		"the escalation path", "the sandbox policy", "the retention window",
		"the pilot cohort", "the invoicing cadence", "the handover checklist",
	}
	evalVerbs = []string{
		"revisited", "approved", "postponed", "narrowed", "documented",
		"escalated", "rejected", "rescoped", "ratified", "deferred",
	}
	evalQuarters = []string{
		"2026 Q1", "2026 Q2", "2026 Q3", "2026 Q4",
		"2026 Q1", "2026 Q2", "2026 Q3", "2026 Q4",
	}
	// Entity names are built from these two banks, so names COLLIDE on one half
	// far more often than chance. That is the point: "Northwind Ledger" and
	// "Harbour Ledger" must be genuinely hard to tell apart from a query.
	evalEntityFirst = []string{
		"Northwind", "Harbour", "Blackstone", "Meridian", "Kestrel",
		"Thornbury", "Lowfield", "Ashgrove", "Pinehurst", "Redmoor",
	}
	evalEntitySecond = []string{
		"Ledger", "Atlas", "Compass", "Beacon", "Foundry", "Quarry",
	}
	evalGivenNames = []string{
		"Alina", "Bertrand", "Corinne", "Dmitri", "Eleni", "Farrah",
		"Gideon", "Halima", "Ivor", "Jolanta", "Kwame", "Lucia",
		"Mikael", "Noor", "Osian", "Petra", "Quentin", "Rasmus",
		"Sunniva", "Tomasz",
	}
	evalFamilyNames = []string{
		"Achebe", "Bergstrom", "Calloway", "Duarte", "Ellery",
		"Fontaine", "Grimsby", "Halvorsen", "Ionescu", "Jarrah",
	}
)

// evalStopwords is the reduction filter. It is intentionally a plain English
// stopword list and NOT bleve's analyzer: the query a person types is not the
// output of an analyzer, and reducing with the same analyzer the index uses
// would hand the retriever a query pre-shaped to its own vocabulary.
var evalStopwords = map[string]bool{
	"a": true, "about": true, "after": true, "all": true, "an": true,
	"and": true, "are": true, "as": true, "at": true, "be": true,
	"been": true, "before": true, "but": true, "by": true, "for": true,
	"from": true, "had": true, "has": true, "have": true, "in": true,
	"is": true, "it": true, "its": true, "of": true, "on": true,
	"or": true, "should": true, "that": true, "the": true, "their": true,
	"then": true, "there": true, "this": true, "to": true, "was": true,
	"were": true, "which": true, "will": true, "with": true, "would": true,
}

// ---------------------------------------------------------------------------
// Generation.
// ---------------------------------------------------------------------------

// buildEvalCorpus generates the note corpus deterministically.
//
// Three independent streams: prose, links, times. See the file header for why
// they must not be one.
func buildEvalCorpus() []evalNote {
	prose := rand.New(rand.NewSource(0x5EED_0001)) //nolint:gosec // deterministic fixture, not security
	links := rand.New(rand.NewSource(0x5EED_0002)) //nolint:gosec // deterministic fixture, not security
	times := rand.New(rand.NewSource(0x5EED_0003)) //nolint:gosec // deterministic fixture, not security

	type entity struct {
		name  string
		topic string
	}

	// Companies and projects share the overlapping name banks.
	companies := make([]entity, 0, 30)
	for i := 0; i < 30; i++ {
		// (i%10, i/10) is injective for i < 60, so 30 companies get 30 distinct
		// names. Colliding names silently OVERWRITE each other at materialise
		// time, which shrinks the corpus without any error — the exact failure
		// this construction is written to make impossible.
		name := evalEntityFirst[i%len(evalEntityFirst)] + " " + evalEntitySecond[i/len(evalEntityFirst)]
		companies = append(companies, entity{name: name, topic: evalTopics[i%len(evalTopics)]})
	}
	projects := make([]entity, 0, 60)
	for i := 0; i < 60; i++ {
		// Same injective pairing, and it deliberately spans the SAME name space
		// as the companies: "Northwind Ledger" the company and "Northwind Ledger
		// Programme" the project are distinct notes that a query cannot easily
		// tell apart, which is the discrimination problem a real vault poses.
		name := evalEntityFirst[i%len(evalEntityFirst)] + " " + evalEntitySecond[i/len(evalEntityFirst)]
		projects = append(projects, entity{name: name, topic: evalTopics[(i*7)%len(evalTopics)]})
	}
	people := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		people = append(people, evalGivenNames[i%len(evalGivenNames)]+" "+evalFamilyNames[i/len(evalGivenNames)])
	}

	// Time window: three years back from a FIXED epoch. A wall-clock "now"
	// would make the committed fixture's recency ordering depend on when the
	// test ran, which is the same class of defect as regenerating the corpus.
	epoch := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	stamp := func() int64 {
		return epoch.Add(-time.Duration(times.Intn(3*365*24)) * time.Hour).Unix()
	}

	// pick draws one element using the prose stream.
	pick := func(ss []string) string { return ss[prose.Intn(len(ss))] }

	// distinctive builds a sentence that carries a specific entity and a
	// specific aspect, so a query reduced from it is answerable in principle.
	distinctive := func(subject, topic string) string {
		return fmt.Sprintf("The %s review for %s %s %s ahead of %s.",
			topic, subject, pick(evalVerbs), pick(evalAspects), pick(evalQuarters))
	}
	// generic builds a sentence from the shared bank only. These are the
	// distractor mass: many notes carry near-identical ones.
	generic := func(topic string) string {
		return fmt.Sprintf("Ongoing %s work continues to affect %s across the portfolio.",
			topic, pick(evalAspects))
	}

	var notes []evalNote
	add := func(path, typ, title string, body []string, linksTo []string) {
		var b strings.Builder
		b.WriteString("---\n")
		fmt.Fprintf(&b, "type: %s\n", typ)
		fmt.Fprintf(&b, "title: %s\n", title)
		b.WriteString("---\n\n")
		fmt.Fprintf(&b, "# %s\n\n", title)
		for _, line := range body {
			b.WriteString(line)
			b.WriteString("\n\n")
		}
		if len(linksTo) > 0 {
			b.WriteString("## Related\n\n")
			for _, l := range linksTo {
				fmt.Fprintf(&b, "- [[%s]]\n", l)
			}
		}
		notes = append(notes, evalNote{Path: path, Type: typ, UpdatedUnix: stamp(), Body: b.String()})
	}

	for _, c := range companies {
		add("companies/"+c.name+".md", "company", c.name,
			[]string{distinctive(c.name, c.topic), generic(c.topic), distinctive(c.name, pick(evalTopics))},
			nil)
	}
	for i, p := range projects {
		owner := companies[links.Intn(len(companies))]
		add("projects/"+p.name+" Programme.md", "project", p.name+" Programme",
			[]string{distinctive(p.name, p.topic), generic(p.topic), distinctive(p.name+" Programme", pick(evalTopics))},
			[]string{owner.name, people[(i*11)%len(people)]})
	}
	for i, person := range people {
		home := projects[links.Intn(len(projects))]
		add("people/"+person+".md", "person", person,
			[]string{distinctive(person, evalTopics[i%len(evalTopics)]), generic(home.topic)},
			[]string{home.name + " Programme"})
	}
	for i := 0; i < 250; i++ {
		p := projects[links.Intn(len(projects))]
		attendee := people[links.Intn(len(people))]
		title := fmt.Sprintf("%s Sync %03d", p.name, i)
		add("meetings/"+title+".md", "meeting", title,
			[]string{distinctive(p.name, p.topic), generic(pick(evalTopics)), distinctive(attendee, p.topic)},
			[]string{p.name + " Programme", attendee})
	}
	for i := 0; i < 100; i++ {
		p := projects[links.Intn(len(projects))]
		topic := evalTopics[i%len(evalTopics)]
		title := fmt.Sprintf("Decision %03d %s", i, topic)
		add("decisions/"+title+".md", "decision", title,
			[]string{distinctive(p.name, topic), generic(topic)},
			[]string{p.name + " Programme"})
	}

	// BRIEFS — the notes that make the field weighting MEASURABLE.
	//
	// Without these the corpus cannot detect BM25F at all, and the reason is
	// subtle enough that the first version of this fixture shipped blind to it:
	// every note above repeats its own title inside its own body, so title and
	// body AGREE on every document. Re-weighting two fields that agree cannot
	// reorder anything — the BM25F row came back byte-identical to the baseline
	// on all 60 queries, which reads exactly like "field weighting does not
	// help" and actually meant "this corpus cannot tell".
	//
	// A brief is titled for topic A and written about topic B, mentioning A
	// once. So for a query naming the entity and topic A, the brief is the
	// TITLE match while some meeting that discusses A repeatedly is the BODY
	// match. Which one wins is exactly what a field weight decides, and it is a
	// real vault shape — a document named for its subject whose text is mostly
	// about something else.
	for i := 0; i < 40; i++ {
		p := projects[links.Intn(len(projects))]
		topicA := evalTopics[i%len(evalTopics)]
		topicB := evalTopics[(i+5)%len(evalTopics)]
		title := fmt.Sprintf("%s %s Brief", p.name, topicA)
		add("briefs/"+title+".md", "brief", title,
			[]string{
				generic(topicB),
				fmt.Sprintf("Most of this note concerns %s rather than %s.", topicB, topicA),
				distinctive(p.name, topicB),
			},
			[]string{p.name + " Programme"})
	}
	return notes
}

// reduceToQuery turns a sentence into the query a person would plausibly type:
// content words only, original order, at most six terms.
//
// The truncation keeps the FIRST six rather than a random six, deliberately. A
// random subset drawn from a stream would introduce a fourth coupling between
// query difficulty and the RNG's call order; keeping a prefix is boring, and
// boring is the property an eval wants.
func reduceToQuery(sentence string) string {
	fields := strings.FieldsFunc(strings.ToLower(sentence), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	})
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 || evalStopwords[f] {
			continue
		}
		terms = append(terms, f)
		if len(terms) == 6 {
			break
		}
	}
	return strings.Join(terms, " ")
}

// sentencesOf returns a note's body sentences, excluding frontmatter, headings
// and link list items.
func sentencesOf(n evalNote) []string {
	var out []string
	for _, line := range strings.Split(n.Body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "-") || strings.HasPrefix(line, "---") ||
			strings.Contains(line, ": ") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// buildEvalQueries samples the two conditions.
//
// CONDITION "uniform" is the verdict basis: ground truth drawn uniformly over
// every note, so recency and backlink degree carry no information about it.
//
// CONDITION "popular" is a SECONDARY, EXPLICITLY ASSUMED model of user
// behaviour: ground truth drawn with probability proportional to inbound link
// count, on the guess that people search more often for the notes their vault
// points at. It is reported and it is NOT the verdict basis, because the
// assumption is ours and unvalidated — measuring against an assumption we
// invented and then citing the result as evidence for a decision is the
// circularity this whole exercise exists to avoid.
func buildEvalQueries(notes []evalNote, degree map[string]int) []evalQuery {
	sample := rand.New(rand.NewSource(0x5EED_0004)) //nolint:gosec // deterministic fixture, not security

	usable := make([]evalNote, 0, len(notes))
	for _, n := range notes {
		if len(sentencesOf(n)) > 0 {
			usable = append(usable, n)
		}
	}

	var out []evalQuery
	emit := func(cond string, n evalNote, i int) {
		ss := sentencesOf(n)
		s := ss[sample.Intn(len(ss))]
		q := reduceToQuery(s)
		if q == "" {
			return
		}
		out = append(out, evalQuery{
			ID:             fmt.Sprintf("%s-%02d", cond, i),
			Condition:      cond,
			Question:       "What do the notes say about " + q + "?",
			Query:          q,
			Relevant:       map[string]int{n.Path: 2},
			SourceSentence: s,
		})
	}

	for i := 0; len(out) < rankEvalQueriesPerCondition; i++ {
		emit("uniform", usable[sample.Intn(len(usable))], i)
	}

	// Popularity-weighted draw over the same usable set.
	weighted := make([]evalNote, 0, len(usable))
	for _, n := range usable {
		for d := 0; d < degree[n.Path]; d++ {
			weighted = append(weighted, n)
		}
	}
	target := rankEvalQueriesPerCondition * 2
	for i := 0; len(out) < target && len(weighted) > 0; i++ {
		emit("popular", weighted[sample.Intn(len(weighted))], i)
	}
	return out
}

// ---------------------------------------------------------------------------
// Materialisation: committed bytes -> a real collection on disk.
// ---------------------------------------------------------------------------

// loadEvalFixture reads the committed corpus and query set. It never generates.
func loadEvalFixture(t *testing.T) evalFixture {
	t.Helper()
	corpusBytes, err := os.ReadFile(filepath.Join(rankEvalDir, rankEvalCorpusFile))
	if err != nil {
		t.Fatalf("read committed corpus: %v (regenerate with -rankeval.generate)", err)
	}
	var f evalFixture
	for _, line := range strings.Split(strings.TrimSpace(string(corpusBytes)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var n evalNote
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			t.Fatalf("corpus line: %v", err)
		}
		f.Notes = append(f.Notes, n)
	}
	queryBytes, err := os.ReadFile(filepath.Join(rankEvalDir, rankEvalQueryFile))
	if err != nil {
		t.Fatalf("read committed query set: %v", err)
	}
	if err := json.Unmarshal(queryBytes, &f); err != nil {
		t.Fatalf("query set: %v", err)
	}
	return f
}

// materializeEvalCorpus writes the fixture into dir, stamping each file with
// the committed mtime so the recency signal reads the fixture's intent rather
// than the checkout's timestamps.
func materializeEvalCorpus(t *testing.T, dir string, notes []evalNote) {
	t.Helper()
	for _, n := range notes {
		full := filepath.Join(dir, filepath.FromSlash(n.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(n.Body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
		at := time.Unix(n.UpdatedUnix, 0)
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatalf("chtimes %s: %v", full, err)
		}
	}
}

// TestGenerateRankEvalFixture writes the committed artifacts. It is a no-op
// without -rankeval.generate, so an ordinary run cannot rewrite the fixture it
// is about to be judged by.
func TestGenerateRankEvalFixture(t *testing.T) {
	if !*regenerateFixture {
		t.Skip("pass -rankeval.generate to rewrite the committed fixture")
	}
	notes := buildEvalCorpus()

	// The link graph has to exist before the queries do, because the "popular"
	// condition samples by inbound degree. It is built from the generated notes
	// by the real graph builder, not by counting the generator's own intent —
	// a link the generator wrote but the resolver cannot resolve must not count.
	dir := t.TempDir()
	materializeEvalCorpus(t, dir, notes)
	root, err := NewCollectionRoot(OSLinkFS(), dir)
	if err != nil {
		t.Fatalf("collection root: %v", err)
	}
	g, err := BuildLinkGraph(OSLinkFS(), root)
	if err != nil {
		t.Fatalf("link graph: %v", err)
	}
	degree := make(map[string]int, len(notes))
	for _, n := range notes {
		degree[n.Path] = len(g.Backlinks(n.Path))
	}

	// A colliding path is not a cosmetic defect: the second write OVERWRITES the
	// first, the corpus silently shrinks, and every metric below is computed
	// over a corpus nobody described. It cost 90 notes the first time this
	// generator ran, and it produced no error of any kind.
	seenPath := make(map[string]bool, len(notes))
	for _, n := range notes {
		if seenPath[n.Path] {
			t.Fatalf("duplicate note path %q: the corpus would silently lose a note", n.Path)
		}
		seenPath[n.Path] = true
	}

	queries := buildEvalQueries(notes, degree)

	if err := os.MkdirAll(rankEvalDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	var corpus strings.Builder
	for _, n := range notes {
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("marshal note: %v", err)
		}
		corpus.Write(b)
		corpus.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(rankEvalDir, rankEvalCorpusFile), []byte(corpus.String()), 0o600); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	qb, err := json.MarshalIndent(evalFixture{Queries: queries}, "", "  ")
	if err != nil {
		t.Fatalf("marshal queries: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rankEvalDir, rankEvalQueryFile), append(qb, '\n'), 0o600); err != nil {
		t.Fatalf("write queries: %v", err)
	}
	t.Logf("wrote %d notes and %d queries to %s", len(notes), len(queries), rankEvalDir)
}

// TestRankEval_FixtureMeetsFR113Shape asserts the committed fixture satisfies
// FR-113's stated corpus and query-set requirements.
//
// It exists because those requirements are the only thing standing between "we
// measured it" and "we measured something". A 40-note corpus with 5 queries
// would produce a table that looks exactly like a real one.
func TestRankEval_FixtureMeetsFR113Shape(t *testing.T) {
	f := loadEvalFixture(t)

	if len(f.Notes) < 500 {
		t.Errorf("FR-113 requires at least 500 notes, fixture has %d", len(f.Notes))
	}
	types := make(map[string]int)
	for _, n := range f.Notes {
		types[n.Type]++
	}
	if len(types) < 4 {
		t.Errorf("FR-113 requires at least 4 record types, fixture has %d: %v", len(types), types)
	}
	uniform := 0
	for _, q := range f.Queries {
		if q.Condition == "uniform" {
			uniform++
		}
		if strings.TrimSpace(q.Query) == "" {
			t.Errorf("query %s is empty", q.ID)
		}
		if len(q.Relevant) == 0 {
			t.Errorf("query %s has no ground truth", q.ID)
		}
	}
	if uniform != rankEvalQueriesPerCondition {
		t.Errorf("FR-113 requires %d queries in the verdict condition, fixture has %d",
			rankEvalQueriesPerCondition, uniform)
	}

	// Every ground-truth path must exist in the corpus. A judgement pointing at
	// a note nobody indexed scores zero for every ranking and silently drags
	// every mean down by the same amount, which looks like a hard query set
	// rather than a broken one.
	paths := make(map[string]bool, len(f.Notes))
	for _, n := range f.Notes {
		paths[n.Path] = true
	}
	for _, q := range f.Queries {
		for p := range q.Relevant {
			if !paths[p] {
				t.Errorf("query %s judges %q, which is not in the corpus", q.ID, p)
			}
		}
	}
}

// TestRankEval_GroundTruthIsNotPrivileged is the anti-rigging guard.
//
// The verdict condition's ground truth must be statistically ORDINARY: no more
// linked and no more recent than the corpus at large. If it were privileged,
// the recency and backlink signals would be measuring the fixture's
// construction rather than their own worth, and the ablation table would be a
// self-portrait.
//
// The bound is deliberately loose (within 1.75x of the corpus mean, and a
// two-sided check so an under-representation is caught too). 30 samples from a
// heavy-tailed degree distribution genuinely wander; a tight bound here would
// be a guard that cries wolf, and a guard that cries wolf gets disabled.
func TestRankEval_GroundTruthIsNotPrivileged(t *testing.T) {
	f := loadEvalFixture(t)

	dir := t.TempDir()
	materializeEvalCorpus(t, dir, f.Notes)
	root, err := NewCollectionRoot(OSLinkFS(), dir)
	if err != nil {
		t.Fatalf("collection root: %v", err)
	}
	g, err := BuildLinkGraph(OSLinkFS(), root)
	if err != nil {
		t.Fatalf("link graph: %v", err)
	}

	var corpusDegree, corpusAge float64
	ages := make(map[string]float64, len(f.Notes))
	for _, n := range f.Notes {
		corpusDegree += float64(len(g.Backlinks(n.Path)))
		age := float64(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Unix() - n.UpdatedUnix)
		ages[n.Path] = age
		corpusAge += age
	}
	corpusDegree /= float64(len(f.Notes))
	corpusAge /= float64(len(f.Notes))

	var gtDegree, gtAge float64
	var n int
	for _, q := range f.Queries {
		if q.Condition != "uniform" {
			continue
		}
		for p := range q.Relevant {
			gtDegree += float64(len(g.Backlinks(p)))
			gtAge += ages[p]
			n++
		}
	}
	if n == 0 {
		t.Fatal("no uniform-condition ground truth to check")
	}
	gtDegree /= float64(n)
	gtAge /= float64(n)

	const tol = 1.75
	if gtDegree > corpusDegree*tol {
		t.Errorf("ground truth is MORE linked than the corpus (%.2f vs %.2f backlinks): "+
			"the backlink signal would be measuring the fixture, not itself", gtDegree, corpusDegree)
	}
	if corpusDegree > 0 && gtDegree < corpusDegree/tol {
		t.Errorf("ground truth is LESS linked than the corpus (%.2f vs %.2f backlinks): "+
			"the backlink signal is being measured against a corpus that penalises it", gtDegree, corpusDegree)
	}
	if gtAge < corpusAge/tol {
		t.Errorf("ground truth is NEWER than the corpus (mean age %.0fs vs %.0fs): "+
			"the recency signal would be measuring the fixture, not itself", gtAge, corpusAge)
	}
	if gtAge > corpusAge*tol {
		t.Errorf("ground truth is OLDER than the corpus (mean age %.0fs vs %.0fs): "+
			"the recency signal is being measured against a corpus that penalises it", gtAge, corpusAge)
	}
	t.Logf("independence check: ground-truth mean backlinks %.2f (corpus %.2f), mean age %.0f days (corpus %.0f days)",
		gtDegree, corpusDegree, gtAge/86400, corpusAge/86400)
}

// sortedTypeCounts renders the corpus's type histogram for the report.
func sortedTypeCounts(notes []evalNote) []string {
	counts := make(map[string]int)
	for _, n := range notes {
		counts[n.Type]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return out
}
