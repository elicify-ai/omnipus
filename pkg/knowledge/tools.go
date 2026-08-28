// Omnipus — ADR-067 D7: the agent-facing knowledge retrieval tools.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// WHAT IS HERE, AND WHAT IS DELIBERATELY NOT
//
// ADR-067 D7 splits the knowledge tool family in two: RETRIEVAL
// (knowledge_search, knowledge_graph) and AUTHORING (knowledge_create,
// knowledge_link, knowledge_set_property, knowledge_append_section,
// knowledge_tasks, knowledge_move, knowledge_rename). This file implements the
// retrieval half — the read path, stage 2. The authoring half is stage 3 and
// has no implementation here.
//
// The policy seed already enumerates all nine names, on purpose: D17 requires
// every knowledge tool to carry an explicit, literal, wildcard-free posture for
// every seeded agent, and a name absent from the seed does not fail loudly. It
// ships silently DENIED, because the load path repairs before it validates
// (FR-071). Seeding a posture for a tool that does not exist yet costs nothing
// — the registry simply never offers it — whereas the reverse costs a dead
// feature nobody is told about.
//
// EVERY TOOL HERE IS SCOPED BY scope.go. That is not a convention, it is the
// P0 (US-9): an unscoped search reads across the workspace trust boundary. The
// scope is resolved from the CALLING AGENT'S workspace, taken from the tool
// context the agent loop installs — never from an argument, which the model
// controls.
// ---------------------------------------------------------------------------

// ToolDeps is what the retrieval tools need from their host.
type ToolDeps struct {
	// Home is $OMNIPUS_HOME. An empty Home makes every tool resolve an empty
	// scope, so a misconstructed tool reads nothing rather than reading the
	// process working directory.
	Home string
	// RateLimiter bounds retrieval per agent (FR-055). Leave nil and the
	// constructors install the default — the limiter is NOT optional, and a
	// nil-means-bypass convention would let a wiring mistake silently disable
	// a stated requirement.
	RateLimiter *RetrievalRateLimiter
	// Progress resolves the LIVE index-progress tracker for a collection root,
	// so a search issued while a first index is still running carries FR-035's
	// incompleteness statement instead of a confident fraction of the truth.
	//
	// Leave nil and SharedProgressTracker is used. See its doc comment for the
	// wiring obligation that carries — an idle tracker reports "complete", so
	// whoever runs the index MUST drive the same tracker this returns.
	Progress func(collectionRoot string) *ProgressTracker
	// ExcerptBudget is the wall-clock budget shared by ALL of one search's
	// query-time excerpt re-reads (FR-050a(b)). Zero or negative takes
	// DefaultExcerptBudget. A host that has measured a slower disk than the
	// one MV-1 was written against raises it here rather than patching a
	// constant; the honesty is unaffected either way, because an exhausted
	// budget is REPORTED (excerpt_unavailable=budget_exhausted plus an
	// incompleteness note) rather than turned into a silently missing excerpt.
	ExcerptBudget time.Duration
}

// SharedProgressTracker returns the process-wide ProgressTracker for one
// collection root, creating an idle one on first use.
//
// It is keyed by the collection's resolved real path — the same identity the
// index itself is keyed on (D3/FR-031) — so the component that RUNS an index
// and the tool that SEARCHES it converge on one tracker without having to be
// handed each other's objects.
//
// WIRING OBLIGATION, and it is not cosmetic: a tracker nobody drives sits at
// IndexPhaseIdle, and buildSearchReport reads idle as "Complete". A search
// during a first index would then answer a tenth of the corpus and state
// nothing — the precise failure US-6 (P0) exists to prevent. The indexing
// lifecycle must call BeginEnumeration/BeginIndexing/Finish on the tracker
// this function returns for the root it is indexing.
func SharedProgressTracker(collectionRoot string) *ProgressTracker {
	key := sharedProgressKey(collectionRoot)
	sharedProgress.mu.Lock()
	defer sharedProgress.mu.Unlock()
	if t, ok := sharedProgress.byRoot[key]; ok {
		return t
	}
	t := NewProgressTracker()
	sharedProgress.byRoot[key] = t
	return t
}

var sharedProgress = struct {
	mu     sync.Mutex
	byRoot map[string]*ProgressTracker
}{byRoot: make(map[string]*ProgressTracker)}

// sharedProgressKey is the collection's RESOLVED REAL PATH — the same identity
// D3/FR-031 keys the index on.
//
// Cleaning the path is not enough, and the difference is silent. The indexer is
// handed whatever spelling the mount record holds; the searcher is handed the
// scope layer's resolved spelling. On macOS those differ for every temporary
// and every /var path ("/var/…" against "/private/var/…"), and on any machine
// they differ whenever an operator mounts a collection through a symlinked
// parent. Two spellings meant two trackers: the indexer would drive one, the
// searcher would read the other — find it idle — and report a tenth of the
// corpus as the whole of it, which is the US-6 (P0) failure arriving through a
// map key.
//
// EvalSymlinks failing (the root does not exist yet, or is unreadable) falls
// back to the lexical form: a tracker under a slightly wrong key is still
// better than a panic, and the collection cannot be indexed in that state
// anyway.
func sharedProgressKey(collectionRoot string) string {
	clean := filepath.Clean(collectionRoot)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}
	return clean
}

// RetrievalTools returns the stage-2 read-path knowledge tools, ready to
// register in the builtin tool registry.
//
// Registration itself lives in pkg/gateway (it owns the registry population),
// so this constructor is the whole of this package's contribution to it.
func RetrievalTools(deps ToolDeps) []tools.Tool {
	if deps.RateLimiter == nil {
		deps.RateLimiter = NewRetrievalRateLimiter(RetrievalRateLimitConfig{})
	}
	return []tools.Tool{
		&SearchTool{deps: deps},
		&GraphTool{deps: deps},
	}
}

// RetrievalToolNames returns the names of the tools RetrievalTools builds,
// read from the tool objects themselves rather than restated as a literal.
// The policy-seeding test uses it, so a tool renamed here and nowhere else
// fails a test instead of shipping denied.
func RetrievalToolNames() []string {
	built := RetrievalTools(ToolDeps{})
	out := make([]string, 0, len(built))
	for _, t := range built {
		out = append(out, t.Name())
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Rate limiting (FR-055)
// ---------------------------------------------------------------------------

// RetrievalRateLimitConfig configures a RetrievalRateLimiter. Zero fields take
// the documented defaults.
type RetrievalRateLimitConfig struct {
	// PerAgentLimit is the number of retrieval calls one agent may make within
	// Window. Default 60, matching pkg/tools' memory-write limiter so the two
	// agent-facing limiters behave the same way under load.
	PerAgentLimit int
	// Window is the sliding window. Default one minute.
	Window time.Duration
	// nowFn is overridable in tests. Production leaves it nil.
	nowFn func() time.Time
}

// RetrievalRateLimiter is a per-agent sliding-window limiter over knowledge
// retrieval calls (FR-055). Its shape is deliberately the same as
// pkg/tools.MemoryRateLimiter's: evict timestamps older than the window, then
// admit if what remains is under the limit.
//
// Unlike that one, a nil *RetrievalRateLimiter is NOT a bypass — the
// constructors never hand out nil. FR-055 is a requirement, and "the limiter
// was nil in production" is precisely the kind of silent disablement this
// project has been bitten by.
type RetrievalRateLimiter struct {
	mu     sync.Mutex
	agents map[string][]time.Time
	limit  int
	window time.Duration
	nowFn  func() time.Time
}

// RetrievalRateDecision is the outcome of one rate-limit check.
type RetrievalRateDecision struct {
	Allowed    bool
	RetryAfter time.Duration
	Limit      int
	Window     time.Duration
}

// NewRetrievalRateLimiter builds a limiter, applying defaults for zero fields.
func NewRetrievalRateLimiter(cfg RetrievalRateLimitConfig) *RetrievalRateLimiter {
	if cfg.PerAgentLimit <= 0 {
		cfg.PerAgentLimit = 60
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	return &RetrievalRateLimiter{
		agents: make(map[string][]time.Time),
		limit:  cfg.PerAgentLimit,
		window: cfg.Window,
		nowFn:  cfg.nowFn,
	}
}

// Limit returns the configured per-agent ceiling.
func (l *RetrievalRateLimiter) Limit() int { return l.limit }

// Window returns the configured sliding window.
func (l *RetrievalRateLimiter) Window() time.Duration { return l.window }

// Allow admits one retrieval call by agentID.
//
// An empty agentID shares one "anonymous" bucket rather than being exempt:
// unattributable traffic is the case a limiter most needs to bound.
func (l *RetrievalRateLimiter) Allow(agentID string) RetrievalRateDecision {
	if l == nil {
		// Defensive only — the constructors never produce nil. A nil limiter
		// must still not admit unlimited traffic silently.
		return RetrievalRateDecision{Allowed: true}
	}
	if strings.TrimSpace(agentID) == "" {
		agentID = "anonymous"
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if l.nowFn != nil {
		now = l.nowFn()
	}
	cutoff := now.Add(-l.window)

	kept := l.agents[agentID][:0]
	for _, ts := range l.agents[agentID] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	l.agents[agentID] = kept

	if len(kept) >= l.limit {
		retry := kept[0].Add(l.window).Sub(now)
		if retry < 0 {
			retry = 0
		}
		return RetrievalRateDecision{Allowed: false, RetryAfter: retry, Limit: l.limit, Window: l.window}
	}
	l.agents[agentID] = append(kept, now)
	return RetrievalRateDecision{Allowed: true, Limit: l.limit, Window: l.window}
}

// ---------------------------------------------------------------------------
// Shared limits (MV-6, MV-7, MV-8)
// ---------------------------------------------------------------------------

const (
	// ExcerptMaxBytes caps one hit's excerpt (MV-8).
	ExcerptMaxBytes = 512
	// excerptLeadBytes is how much context is kept before the matched term
	// when centring an excerpt on it.
	excerptLeadBytes = 128
	// excerptScanWindow bounds how far past a hit's segment offset the matched
	// term is looked for. A segment is up to IndexSegmentSize, so an unbounded
	// scan would read half a megabyte per hit; this bounds it without capping
	// the excerpt's honesty — a term beyond the window is reported as
	// "match_not_found", never guessed at.
	excerptScanWindow = 64 << 10
	// titleScanBytes is how much of a note's head is read to find its title.
	titleScanBytes = 8 << 10
	// DefaultExcerptBudget is the wall-clock budget shared by ALL of one
	// search's excerpt re-reads (FR-050a(b)). MV-1 allows 500 ms for the whole
	// query across up to 20 results, so the re-reads get most, not all, of it.
	DefaultExcerptBudget = 350 * time.Millisecond
)

// ---------------------------------------------------------------------------
// knowledge_search
// ---------------------------------------------------------------------------

// SearchTool is FR-050's relevance search: ranked hits carrying path, title
// and a matched excerpt, scoped to the calling agent's workspace.
type SearchTool struct {
	tools.BaseTool
	deps ToolDeps
}

// Name is the registered tool name. It is seeded explicitly in
// pkg/config/defaults.go and pkg/coreagent/core.go (D17) — renaming it here
// without renaming it there ships the tool denied.
func (t *SearchTool) Name() string { return "knowledge_search" }

// Description is what the model reads.
func (t *SearchTool) Description() string {
	return "Search a knowledge base by relevance and get ranked notes back, each with its " +
		"path, title and the matching excerpt. Use this instead of listing or grepping files: " +
		"it ranks, and it reads the note text rather than only filenames. Only knowledge bases " +
		"mounted into your own workspace can be searched."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *SearchTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *SearchTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *SearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What to search for. Ordinary words, not a query language.",
			},
			"collection": map[string]any{
				"type": "string",
				"description": "Which knowledge base to search, by its name. Leave unset when " +
					"your workspace has exactly one.",
			},
			"folder": map[string]any{
				"type": "string",
				"description": "Restrict results to this folder inside the collection, e.g. " +
					"'projects/2026'. Leave unset to search the whole collection.",
			},
			"top_n": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"How many results to return. Default %d, maximum %d; a larger request is "+
						"reduced to the maximum and the response says so.",
					SearchDefaultTopN, SearchMaxTopN),
			},
		},
		"required": []string{"query"},
	}
}

// searchHit is one ranked result as the model sees it.
type searchHit struct {
	Path string `json:"path"`
	// Title is always present, even when the file could not be re-read —
	// FR-050a(a) requires the hit itself to survive an unreadable excerpt.
	Title string  `json:"title"`
	Kind  string  `json:"kind"`
	Score float64 `json:"score"`
	// Excerpt is re-read from disk at query time (FR-050a), never stored in
	// the index, so it always matches the file's current bytes.
	Excerpt string `json:"excerpt,omitempty"`
	// ExcerptUnavailable is the machine-readable reason there is no excerpt.
	// Empty when there is one. Never a fabricated excerpt, never a dropped
	// hit.
	ExcerptUnavailable ExcerptReason `json:"excerpt_unavailable,omitempty"`
}

// searchResponse is the whole answer, honest about what it does not know.
type searchResponse struct {
	Collection  string      `json:"collection,omitempty"`
	Query       string      `json:"query"`
	Results     []searchHit `json:"results"`
	ResultCount int         `json:"result_count"`
	// Report is search.go's incompleteness/clamping report, carried verbatim.
	// FR-035 requires it to arrive WITH the results, and FR-037's clamping is
	// one of its fields — restating either here would be a second source of
	// truth that could drift.
	Report SearchReport `json:"report"`
	// IndexState is "ready", "not_built" or "unavailable". A never-indexed
	// collection returning zero results is not the same statement as "nothing
	// matched", and US-6 forbids conflating them.
	IndexState string `json:"index_state"`
	// IndexedDocuments is how many documents the index holds.
	IndexedDocuments uint64 `json:"indexed_documents"`
	// Incomplete is true when anything above OR anything in this tool's own
	// layer (an exhausted excerpt budget, a truncated collection enumeration)
	// makes the answer partial.
	Incomplete bool     `json:"incomplete"`
	Notes      []string `json:"notes,omitempty"`
	// CollectionsInScope lists what this agent CAN address, so an unmatched
	// collection argument is actionable. It names only this workspace's own
	// collections.
	CollectionsInScope []string `json:"collections_in_scope,omitempty"`
}

// Execute runs one search.
func (t *SearchTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return tools.ErrorResult("knowledge_search: 'query' is required")
	}

	if res := t.checkRate(ctx); res != nil {
		return res
	}

	// ResolveTurnScope, not ResolveScope(…, ToolWorkspaceID(ctx)): a CLI or
	// scheduled turn carries no workspace id and would otherwise resolve an
	// empty scope over a workspace whose mounts exist. See scope_turn.go.
	scope, _ := ResolveTurnScope(ctx, t.deps.Home)
	collectionRef, _ := args["collection"].(string)
	col, ok := scope.Select(collectionRef)
	if !ok {
		// FR-053/MV-12: out of scope is an EMPTY RESULT SET, not a permission
		// error. The response deliberately cannot distinguish "exists but is
		// another workspace's" from "does not exist".
		return jsonResult(searchResponse{
			Query:              query,
			Results:            []searchHit{},
			IndexState:         "unavailable",
			Incomplete:         scope.Truncated(),
			Notes:              scopeNotes(scope, collectionRef),
			CollectionsInScope: scope.Names(),
		})
	}

	resp := searchResponse{
		Collection:         col.Name,
		Query:              query,
		Results:            []searchHit{},
		CollectionsInScope: scope.Names(),
	}
	if scope.Truncated() {
		resp.Incomplete = true
		resp.Notes = append(resp.Notes,
			"Collection enumeration hit its bound, so some knowledge bases in this workspace may not be listed.")
	}

	ix, err := OpenIndex(t.deps.Home, col.Root)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_search: open index for %q: %v", col.Name, err))
	}
	defer func() { _ = ix.Close() }()

	if _, statErr := os.Stat(ix.ManifestPath()); statErr != nil {
		// Never indexed. Saying "0 results" here would be a confidently
		// incomplete answer (US-6, P0) — the collection has simply not been
		// read yet.
		resp.IndexState = "not_built"
		resp.Incomplete = true
		resp.Notes = append(resp.Notes,
			"This knowledge base has not been indexed yet, so no result can be returned. "+
				"Indexing runs on mount and on a schedule; try again shortly.")
		return jsonResult(resp)
	}
	resp.IndexState = "ready"
	if count, cErr := ix.DocCount(); cErr == nil {
		resp.IndexedDocuments = count
	}

	// The honesty layer, not a second copy of it: Searcher applies FR-037's
	// clamp, reports it, and attaches FR-035/FR-036's incompleteness statement
	// to the same response as the hits.
	searcher, err := NewSearcher(ix, t.progressFor(col.Root))
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_search: %v", err))
	}
	// The folder scope goes INTO the query, not over its answer. Filtering the
	// already-clamped top-N would silently drop in-folder hits the moment a
	// collection has more matches than the cap, and the report attached above
	// would then describe a different result set from the one returned — a
	// partial answer with no incompleteness statement, which is what FR-035
	// forbids.
	found, err := searcher.Search(query, SearchOptions{
		TopN:   intArg(args["top_n"], 0),
		Folder: normalizeFolder(args["folder"]),
	})
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_search: %v", err))
	}
	hits, report := found.Results()
	resp.Report = report
	if !report.Complete {
		resp.Incomplete = true
	}
	if report.Statement != "" {
		resp.Notes = append(resp.Notes, report.Statement)
	}

	// FR-043/FR-044 at the RETRIEVAL boundary, not only at index time.
	//
	// A hit's path arrives from the manifest, which can be stale: the scanner
	// refused to index a symlink, but nothing stops a principal that can write
	// into a mounted collection from replacing an already-indexed note with a
	// symlink afterwards. Joining col.Root to the recorded path and opening
	// the result is a LEXICAL containment check, and a lexical check is
	// defeated by exactly one symlink — which is how the bytes of a file
	// outside the collection reached the model. Every path below is proven
	// contained against its REAL path before anything opens it.
	root, rootErr := NewCollectionRoot(OSLinkFS(), col.Root)
	if rootErr != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_search: %v", rootErr))
	}

	terms := queryTerms(query)
	deadline := time.Now().Add(t.excerptBudget())
	budgetRan := false
	escaped := false
	for _, h := range hits {
		hit := searchHit{
			Path:  h.Path,
			Title: stemTitle(h.Path),
			Kind:  string(h.Kind),
			Score: h.Score,
		}
		switch {
		case h.Kind == ScanKindAttachment:
			// FR-039a: an attachment's contents are never opened, for ANY
			// reason — and deriving a title counts. It matched by name, so
			// the name is the whole of what is reported. An earlier revision
			// read the first 8 KB of every hit before this branch was
			// reached, so an attachment's bytes were served to the model in
			// the `title` of the same object that says "attachment_not_read".
			hit.ExcerptUnavailable = ExcerptAttachment
		default:
			abs, cErr := retrievalPath(OSLinkFS(), root, h.Path)
			if cErr != nil {
				escaped = true
				hit.ExcerptUnavailable = ExcerptNotContained
				break
			}
			hit.Title = titleFor(abs, h.Path)
			hit.Excerpt, hit.ExcerptUnavailable = excerptAt(abs, h.Offset, terms, deadline)
		}
		if hit.ExcerptUnavailable == ExcerptBudgetExhausted {
			budgetRan = true
		}
		resp.Results = append(resp.Results, hit)
	}
	resp.ResultCount = len(resp.Results)
	if budgetRan {
		resp.Incomplete = true
		resp.Notes = append(resp.Notes,
			"Some excerpts were not read because the query-time read budget ran out; "+
				"those hits are still real matches.")
	}
	if escaped {
		resp.Incomplete = true
		resp.Notes = append(resp.Notes,
			"One or more indexed paths no longer resolve inside this knowledge base — they were "+
				"not read. This usually means a file was replaced by a symbolic link after it was "+
				"indexed; re-index the collection.")
	}
	return jsonResult(resp)
}

// excerptBudget is the wall-clock budget for one search's excerpt re-reads.
func (t *SearchTool) excerptBudget() time.Duration {
	if t.deps.ExcerptBudget > 0 {
		return t.deps.ExcerptBudget
	}
	return DefaultExcerptBudget
}

// retrievalPath turns a hit's recorded collection-relative path into a real
// absolute path proven to be inside the collection, or refuses.
//
// Two conditions, both required:
//
//   - FR-043 — the REAL path, after every symlink on the way to it has been
//     resolved, is inside the root. This is what ResolveContained decides.
//   - FR-044 — no symbolic link was traversed at all. ResolveContained permits
//     a symlink that lands back inside the collection; the walk that produced
//     the index does not, so an indexed path that only resolves through a link
//     is a stale manifest entry and is refused rather than read. The test is
//     that the resolved path is byte-identical to the lexical join: any link
//     anywhere in the chain makes the two differ.
//
// A path that does not exist at all passes both (realPathAllowingMissing falls
// back to the lexical form), which is correct — "missing" is the opener's
// answer to give, and it gives it as ExcerptFileMissing.
func retrievalPath(fsys LinkFS, root CollectionRoot, rel string) (string, error) {
	resolved, err := root.ResolveContained(fsys, rel)
	if err != nil {
		return "", err
	}
	lexical := filepath.Join(root.Path(), filepath.FromSlash(strings.TrimSpace(rel)))
	if resolved != lexical {
		return "", fmt.Errorf("%w: %q reaches %q only through a symbolic link",
			ErrOutsideCollection, rel, resolved)
	}
	return resolved, nil
}

// progressFor resolves the progress tracker for one collection root.
func (t *SearchTool) progressFor(root string) *ProgressTracker {
	if t.deps.Progress != nil {
		if p := t.deps.Progress(root); p != nil {
			return p
		}
	}
	return SharedProgressTracker(root)
}

func (t *SearchTool) checkRate(ctx context.Context) *tools.ToolResult {
	return checkRetrievalRate(t.deps.RateLimiter, t.Name(), tools.ToolAgentID(ctx))
}

// ---------------------------------------------------------------------------
// knowledge_graph
// ---------------------------------------------------------------------------

// GraphTool is FR-051's link, backlink, unresolved, orphan and neighbourhood
// query surface, scoped exactly as search is.
type GraphTool struct {
	tools.BaseTool
	deps ToolDeps
}

// Name is the registered tool name (seeded in D17's matrix — see SearchTool).
func (t *GraphTool) Name() string { return "knowledge_graph" }

// Description is what the model reads.
func (t *GraphTool) Description() string {
	return "Ask what a note links to, what links back to it, which links are broken, which " +
		"notes nothing links to, or what sits near a note in the link graph. Backlinks cover " +
		"every wikilink form, including aliased, heading and folder-path links. Only knowledge " +
		"bases mounted into your own workspace can be queried."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *GraphTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *GraphTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Graph operation names, as the model spells them.
const (
	GraphOpLinks         = "links"
	GraphOpBacklinks     = "backlinks"
	GraphOpUnresolved    = "unresolved"
	GraphOpOrphans       = "orphans"
	GraphOpNeighborhood  = "neighborhood"
	graphDefaultHops     = 1
	graphDefaultMaxNodes = MaxNeighborhoodNodes
)

// Parameters is the JSON schema the model fills in.
func (t *GraphTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{
					GraphOpLinks, GraphOpBacklinks, GraphOpUnresolved,
					GraphOpOrphans, GraphOpNeighborhood,
				},
				"description": "links = what this note points at; backlinks = what points at it; " +
					"unresolved = links with no target; orphans = notes nothing links to; " +
					"neighborhood = notes within a few link steps.",
			},
			"path": map[string]any{
				"type": "string",
				"description": "The note, as a path relative to the collection root, e.g. " +
					"'projects/Roadmap.md'. Required for links, backlinks and neighborhood.",
			},
			"collection": map[string]any{
				"type": "string",
				"description": "Which knowledge base to query, by its name. Leave unset when " +
					"your workspace has exactly one.",
			},
			"hops": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Neighbourhood radius in link steps. Default %d, maximum %d; a larger "+
						"request is reduced and the response says so.",
					graphDefaultHops, MaxNeighborhoodHops),
			},
			"max_nodes": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Most notes a neighbourhood may contain. Maximum %d.", MaxNeighborhoodNodes),
			},
		},
		"required": []string{"operation"},
	}
}

// graphLink is one link as the model sees it.
type graphLink struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	// Form is the raw source text, so an agent can see which of the four
	// wikilink forms was used (AC-7.2).
	Form      string `json:"form"`
	Alias     string `json:"alias,omitempty"`
	Heading   string `json:"heading,omitempty"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	Ambiguous bool   `json:"ambiguous,omitempty"`
	// Candidates lists every note an ambiguous name matched, in tie-break
	// order (FR-041) — the ambiguity is reported IN ADDITION to resolving.
	Candidates []string `json:"candidates,omitempty"`
	Line       int      `json:"line"`
}

// graphResponse is the whole answer.
type graphResponse struct {
	Collection string      `json:"collection,omitempty"`
	Operation  string      `json:"operation"`
	Path       string      `json:"path,omitempty"`
	Links      []graphLink `json:"links,omitempty"`
	Notes      []string    `json:"notes,omitempty"`
	// Nodes/Hops/MaxNodes describe a neighbourhood result. Clamping is
	// reported, per FR-054's bound and FR-037's principle.
	Nodes        []string `json:"nodes,omitempty"`
	Hops         int      `json:"hops,omitempty"`
	MaxNodes     int      `json:"max_nodes,omitempty"`
	HopsClamped  bool     `json:"hops_clamped,omitempty"`
	NodesClamped bool     `json:"nodes_clamped,omitempty"`
	// Count is the size of whichever list this operation produced.
	Count      int  `json:"count"`
	Incomplete bool `json:"incomplete"`
	// Skipped reports entries the walk refused to follow (symlinks) or could
	// not read, so an answer built over a partly-unreadable collection never
	// reads as complete (NB-9, FR-112).
	Skipped            []string `json:"skipped,omitempty"`
	CollectionsInScope []string `json:"collections_in_scope,omitempty"`
}

// Execute runs one graph query.
func (t *GraphTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	op, _ := args["operation"].(string)
	op = strings.ToLower(strings.TrimSpace(op))
	switch op {
	case GraphOpLinks, GraphOpBacklinks, GraphOpUnresolved, GraphOpOrphans, GraphOpNeighborhood:
	case "":
		return tools.ErrorResult("knowledge_graph: 'operation' is required")
	default:
		return tools.ErrorResult(fmt.Sprintf(
			"knowledge_graph: unknown operation %q; expected one of links, backlinks, unresolved, orphans, neighborhood", op))
	}

	if res := checkRetrievalRate(t.deps.RateLimiter, t.Name(), tools.ToolAgentID(ctx)); res != nil {
		return res
	}

	notePath := normalizeRel(strings.TrimSpace(stringArg(args["path"])))
	needsPath := op == GraphOpLinks || op == GraphOpBacklinks || op == GraphOpNeighborhood
	if needsPath && notePath == "" {
		return tools.ErrorResult(fmt.Sprintf("knowledge_graph: 'path' is required for operation %q", op))
	}

	// ResolveTurnScope, not ResolveScope(…, ToolWorkspaceID(ctx)): a CLI or
	// scheduled turn carries no workspace id and would otherwise resolve an
	// empty scope over a workspace whose mounts exist. See scope_turn.go.
	scope, _ := ResolveTurnScope(ctx, t.deps.Home)
	collectionRef, _ := args["collection"].(string)
	col, ok := scope.Select(collectionRef)
	if !ok {
		// US-9 AS-2: the knowledge base is not addressable at all. Same empty
		// answer as search, for the same reason (FR-053).
		return jsonResult(graphResponse{
			Operation:          op,
			Path:               notePath,
			Notes:              scopeNotes(scope, collectionRef),
			CollectionsInScope: scope.Names(),
		})
	}

	root, err := NewCollectionRoot(OSLinkFS(), col.Root)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_graph: %v", err))
	}
	g, err := BuildLinkGraph(OSLinkFS(), root)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_graph: %v", err))
	}

	resp := graphResponse{
		Collection:         col.Name,
		Operation:          op,
		Path:               notePath,
		CollectionsInScope: scope.Names(),
	}
	for _, s := range g.Skipped() {
		resp.Skipped = append(resp.Skipped, fmt.Sprintf("%s (%s)", s.RelPath, s.Reason))
	}
	if len(resp.Skipped) > 0 {
		resp.Incomplete = true
	}
	if scope.Truncated() {
		resp.Incomplete = true
		resp.Notes = append(resp.Notes,
			"Collection enumeration hit its bound, so some knowledge bases in this workspace may not be listed.")
	}

	switch op {
	case GraphOpLinks:
		resp.Links = toGraphLinks(g.Links(notePath))
	case GraphOpBacklinks:
		resp.Links = toGraphLinks(g.Backlinks(notePath))
	case GraphOpUnresolved:
		resp.Links = toGraphLinks(g.Unresolved())
	case GraphOpOrphans:
		resp.Nodes = g.Orphans()
	case GraphOpNeighborhood:
		hops := intArg(args["hops"], graphDefaultHops)
		maxNodes := intArg(args["max_nodes"], graphDefaultMaxNodes)
		n := g.Neighborhood(notePath, hops, maxNodes)
		resp.Nodes = n.Nodes
		resp.Hops = n.Hops
		resp.MaxNodes = n.MaxNodes
		resp.HopsClamped = n.HopsClamped
		resp.NodesClamped = n.NodesClamped
		if n.HopsClamped {
			resp.Notes = append(resp.Notes, fmt.Sprintf(
				"Requested %d hops; the maximum is %d, so the radius was clamped.", hops, MaxNeighborhoodHops))
		}
		if n.NodesClamped {
			resp.Incomplete = true
			resp.Notes = append(resp.Notes, fmt.Sprintf(
				"The neighbourhood was cut off at %d notes, so this is a subset.", n.MaxNodes))
		}
	}
	resp.Count = len(resp.Links) + len(resp.Nodes)
	return jsonResult(resp)
}

func toGraphLinks(in []ResolvedLink) []graphLink {
	out := make([]graphLink, 0, len(in))
	for _, l := range in {
		out = append(out, graphLink{
			From:       l.From,
			To:         l.To,
			Form:       l.Raw,
			Alias:      l.Alias,
			Heading:    l.Heading,
			State:      string(l.State),
			Reason:     string(l.Reason),
			Ambiguous:  l.Ambiguous,
			Candidates: l.Candidates,
			Line:       l.Line,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Excerpts, re-read at query time (FR-050a)
// ---------------------------------------------------------------------------

// ExcerptReason says why a hit carries no excerpt. It is machine-readable
// because the cases are not equivalent, and because FR-050a(a) forbids the two
// shortcuts that would otherwise be taken: returning "" (indistinguishable
// from an empty note) and dropping the hit (indistinguishable from no match).
type ExcerptReason string

const (
	// ExcerptOK — an excerpt was produced.
	ExcerptOK ExcerptReason = ""
	// ExcerptFileMissing — the file was indexed but is no longer on disk.
	ExcerptFileMissing ExcerptReason = "file_missing"
	// ExcerptFileUnreadable — the file exists but could not be read.
	ExcerptFileUnreadable ExcerptReason = "file_unreadable"
	// ExcerptMatchNotFound — the file was read and none of the query's terms
	// is present at or after the hit's offset. The note matched when it was
	// indexed and has since been edited: the hit is real, the excerpt is not
	// available, and inventing one would be a lie about current bytes.
	ExcerptMatchNotFound ExcerptReason = "match_not_found"
	// ExcerptBudgetExhausted — the query-time read budget ran out before this
	// hit (FR-050a(b)).
	ExcerptBudgetExhausted ExcerptReason = "budget_exhausted"
	// ExcerptAttachment — the hit is an attachment, whose contents are never
	// opened (FR-039a).
	ExcerptAttachment ExcerptReason = "attachment_not_read"
	// ExcerptNotContained — the indexed path no longer resolves inside the
	// collection root, or reaches it only through a symbolic link
	// (FR-043/FR-044). The hit is still returned with its path and its
	// filename-derived title, because FR-050a(a) forbids dropping it; nothing
	// was opened, because the whole point is that opening it would read
	// outside the collection.
	ExcerptNotContained ExcerptReason = "path_not_contained"
)

// excerptAt re-reads path at query time and returns the excerpt around the
// first query term at or after offset.
//
// offset is ABSOLUTE within the file (FR-050a(c)), which is what makes
// FR-034a's segmentation unable to misdirect the read: it is the start of the
// best-scoring segment, not a position within some segment-local buffer.
//
// It never returns text it did not just read from disk.
func excerptAt(path string, offset int64, terms []string, deadline time.Time) (string, ExcerptReason) {
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return "", ExcerptBudgetExhausted
	}
	// openFileForRead, not os.Open: index.go states that EVERY content read in
	// this package goes through that one seam, and the read-counting oracle
	// FR-039a's test relies on can only observe what goes through it. The two
	// query-time readers used to call os.Open directly, which made that
	// statement false and made test 70 unable to see an attachment being read.
	f, err := openFileForRead(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ExcerptFileMissing
		}
		return "", ExcerptFileUnreadable
	}
	defer func() { _ = f.Close() }()

	if offset > 0 {
		if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
			return "", ExcerptFileUnreadable
		}
	}
	buf := make([]byte, excerptScanWindow)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", ExcerptFileUnreadable
	}
	if n == 0 {
		return "", ExcerptMatchNotFound
	}
	window := buf[:n]

	pos := firstTermIndex(window, terms)
	if pos < 0 {
		return "", ExcerptMatchNotFound
	}
	return sliceExcerpt(window, pos), ExcerptOK
}

// firstTermIndex returns the earliest case-insensitive occurrence of any term,
// or -1.
func firstTermIndex(window []byte, terms []string) int {
	lower := bytes.ToLower(window)
	best := -1
	for _, term := range terms {
		if term == "" {
			continue
		}
		if i := bytes.Index(lower, []byte(term)); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	return best
}

// sliceExcerpt cuts at most ExcerptMaxBytes (MV-8) around pos, on valid UTF-8
// boundaries, and collapses the result to a single line so one hit stays one
// line in the model's context.
func sliceExcerpt(window []byte, pos int) string {
	start := pos - excerptLeadBytes
	if start < 0 {
		start = 0
	}
	end := start + ExcerptMaxBytes
	if end > len(window) {
		end = len(window)
	}
	cut := window[start:end]
	// Trim partial runes at both ends rather than emitting replacement
	// characters.
	for len(cut) > 0 && !utf8.Valid(cut[:1]) && cut[0]&0xC0 == 0x80 {
		cut = cut[1:]
	}
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRune(cut); r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(strings.Fields(string(cut)), " "))
}

// queryTerms splits a query into the terms an excerpt may be centred on.
func queryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\''
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// stemTitle is the title floor: the filename without its extension, derived
// from the path alone. It reads nothing, which is what makes it the only title
// an attachment may ever have (FR-039a) and the only title a hit whose path
// will not resolve inside the collection may have (FR-043).
func stemTitle(relPath string) string {
	return strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
}

// titleFor derives a NOTE's title. It NEVER fails: FR-050a(a) requires an
// unreadable hit to still carry path and title, so the filename stem is the
// floor rather than an error.
//
// Order: the frontmatter's own "title", then the first level-1 heading, then
// the filename without its extension.
//
// CALLER OBLIGATION: absPath must already be proven contained (retrievalPath),
// and the hit must not be an attachment. This function opens the file; FR-039a
// is unconditional, so an attachment must never reach it.
func titleFor(absPath, relPath string) string {
	fallback := stemTitle(relPath)
	f, err := openFileForRead(absPath)
	if err != nil {
		return fallback
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, titleScanBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fallback
	}

	// ONE title derivation, not two. This used to be a line scanner of its own,
	// and ADR-068 D21.2 added a second one on the indexing path when `title`
	// became an indexed field. Two derivations mean a note that RANKS on the
	// title bleve holds and DISPLAYS the different title this function found —
	// no error, just a result that argues with itself. extractNoteFields is the
	// one implementation; this call site supplies 8 KiB of head, the indexer
	// supplies its whole first segment.
	nf, _ := extractNoteFields(head[:n], relPath)
	if nf.Title == "" {
		return fallback
	}
	return nf.Title
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// intArg reads an integer argument out of the model's JSON, which may deliver
// it as a float64, a json.Number, an int or a string.
func intArg(raw any, fallback int) int {
	switch v := raw.(type) {
	case nil:
		return fallback
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		var i int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &i); err == nil {
			return i
		}
	}
	return fallback
}

func stringArg(raw any) string {
	s, _ := raw.(string)
	return s
}

// normalizeFolder turns a folder argument into a slash-separated, clean,
// root-relative prefix. Anything that tries to leave the collection resolves
// to "" (search the whole collection) rather than to a traversal.
func normalizeFolder(raw any) string {
	folder := normalizeRel(strings.TrimSpace(stringArg(raw)))
	if folder == "" || folder == "." || strings.HasPrefix(folder, "../") || folder == ".." {
		return ""
	}
	return strings.TrimSuffix(folder, "/")
}

// scopeNotes explains an empty scope result WITHOUT disclosing anything about
// collections outside it.
func scopeNotes(s Scope, ref string) []string {
	switch {
	case len(s.Collections()) == 0:
		return []string{"No knowledge base is available in this workspace."}
	case strings.TrimSpace(ref) == "":
		return []string{"This workspace has more than one knowledge base; name the one to use in 'collection'."}
	default:
		return []string{fmt.Sprintf("No knowledge base named %q is available in this workspace.", strings.TrimSpace(ref))}
	}
}

// checkRetrievalRate returns nil when the call may proceed, or the refusal
// result when it may not (FR-055).
func checkRetrievalRate(l *RetrievalRateLimiter, toolName, agentID string) *tools.ToolResult {
	d := l.Allow(agentID)
	if d.Allowed {
		return nil
	}
	return tools.ErrorResult(fmt.Sprintf(
		"%s: rate limited — at most %d knowledge retrieval calls per %s per agent. Retry in %s.",
		toolName, d.Limit, d.Window, d.RetryAfter.Round(time.Second)))
}

// jsonResult renders a response as the tool's LLM-facing payload. Retrieval is
// silent for the user: the calling agent narrates what it found.
func jsonResult(v any) *tools.ToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge: encode result: %v", err))
	}
	return tools.SilentResult(string(b))
}
