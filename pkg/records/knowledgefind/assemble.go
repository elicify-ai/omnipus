// Omnipus — spec FR-020c, FR-063, FR-121..FR-127: turning survivors into the
// answer, with the verdict attached.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// renderProperties is the ordered set of properties a row renders and sorts by:
// `select` when given, the record type's own declaration order otherwise, so a
// report reads the way the operator wrote it.
//
// IT IS ALSO THE DECODE LIST, and that second job is why it resolves through
// the NAMESPACE rather than the schema. Whatever it returns is what
// materialise() decodes into the survivor's values map — and the sorter, the
// grouper and all fifteen summaries read that map and nothing else. A
// `file.mtime` sort key or a `formula.age` summary target missing from this
// list is not a missing column: it is a sort that orders by a value nobody
// read and a summary computed over an empty set, both rendering perfectly.
//
// THE DEFAULT IS UNCHANGED: with no `select`, the columns are the record type's
// declared properties in declaration order. The twelve `file.*` properties are
// resolvable but not defaulted — a view gets them because it asked, never
// because they exist. An UNTYPED query has no declaration order, so its columns
// are exactly what it named.
func (q *query) renderProperties() []*records.Property {
	ns := q.namespace()
	names := q.selectCols
	if len(names) == 0 && q.schema != nil {
		names = q.schema.PropertyOrder
	}
	seen := map[string]bool{}
	var out []*records.Property
	add := func(n string) {
		if seen[n] {
			return
		}
		if p, ok := ns.composite.Property(n); ok {
			seen[n] = true
			out = append(out, p)
		}
	}
	for _, n := range names {
		add(n)
	}
	// A sort or group key that is not a rendered column still has to be
	// DECODED, or the sort would order by a value nobody read.
	for _, s := range q.sort {
		add(s.property)
	}
	for _, g := range q.groupBy {
		add(g)
	}
	for _, a := range q.aggregates {
		if a.property != "" {
			add(a.property)
		}
	}
	return out
}

// echo renders the query AS EXECUTED (FR-122) — defaults filled in, clamps
// applied. It is the line that makes a clamp visible without a second call.
func (q *query) echo() string {
	var parts []string
	if q.recordType != "" {
		parts = append(parts, "type="+q.recordType)
	}
	if q.kind != KindNote {
		parts = append(parts, "kind="+q.kind)
	}
	if q.words != "" {
		parts = append(parts, `words="`+q.words+`"`)
	}
	if q.view != "" {
		parts = append(parts, "view="+q.view)
	}
	if q.filter != nil {
		// buildCombinator already parenthesises a multi-child node, so wrapping
		// unconditionally produced `filter=((a AND b))`. The echo is what a
		// reader checks their own query against; noise in it is noise in the
		// one line that is supposed to remove doubt.
		text := q.filter.text
		if !strings.HasPrefix(text, "(") {
			text = "(" + text + ")"
		}
		parts = append(parts, "filter="+text)
	}
	if q.near != "" {
		parts = append(parts, fmt.Sprintf("near=%s hops=%d", q.near, q.hops))
	}
	if len(q.join) > 0 {
		parts = append(parts, "join="+strings.Join(q.join, ","))
	}
	if len(q.groupBy) > 0 {
		parts = append(parts, "group_by="+strings.Join(q.groupBy, ","))
	}
	for _, s := range q.sort {
		dir := "asc"
		if s.desc {
			dir = "desc"
		}
		parts = append(parts, "sort="+s.property+" "+dir)
	}
	for _, a := range q.aggregates {
		parts = append(parts, "aggregate="+a.label())
	}
	// The limit is ALWAYS echoed, including when it is the default and
	// especially when it was clamped: a clamp the caller cannot see is the
	// silent truncation FR-063 exists to prevent.
	if q.clamped {
		parts = append(parts, fmt.Sprintf("limit=%d (clamped from %d)", q.limit, q.limitAsked))
	} else {
		parts = append(parts, fmt.Sprintf("limit=%d", q.limit))
	}
	if q.minimal {
		parts = append(parts, "detail=minimal")
	}
	return strings.Join(parts, "  ")
}

func (a aggregate) label() string {
	if a.property == "" {
		return a.op + "()"
	}
	return a.op + "(" + a.property + ")"
}

// ---------------------------------------------------------------------------
// SORTING — in Go, by the comparator (ruling R-A, ruling R-E)
// ---------------------------------------------------------------------------

// sortSurvivors orders the answer.
//
// Every comparison goes through records.Compare, which is a three-valued VIEW of
// the same oracle the filter used. That is not indirection for its own sake: an
// enum sorts on its FOLDED form so `Won`, `won` and `WON` sort together exactly
// as they group together, and no SQL collation in the linked build does that.
//
// The final tiebreak is the PATH, and it is not decoration. Without a total
// order two runs of the same query can return the same rows in different orders,
// which makes a cursor meaningless and a diff-based test flap forever.
func sortSurvivors(rows []survivor, q *query) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, key := range q.sort {
			c, ok := compareByProperty(rows[i], rows[j], key.property)
			if !ok || c == 0 {
				continue
			}
			if key.desc {
				return c > 0
			}
			return c < 0
		}
		if len(q.sort) == 0 && rows[i].score != rows[j].score {
			// No explicit sort: relevance, highest first.
			return rows[i].score > rows[j].score
		}
		return rows[i].cand.Path < rows[j].cand.Path
	})
}

// compareByProperty orders two survivors on one property.
//
// ABSENCE SORTS LAST in both directions, and that is deliberate rather than
// incidental. A record with no value has not got a small value; putting it at
// the top of a descending sort would put "nobody recorded this" where the reader
// is looking for the largest.
func compareByProperty(a, b survivor, name string) (int, bool) {
	av, aok := firstValue(a, name)
	bv, bok := firstValue(b, name)
	switch {
	case !aok && !bok:
		return 0, false
	case !aok:
		return 1, true
	case !bok:
		return -1, true
	}
	return records.Compare(av, bv)
}

func firstValue(s survivor, name string) (records.TypedValue, bool) {
	pv, ok := s.values[name]
	if !ok || pv.State != records.StatePresent || len(pv.Values) == 0 {
		return records.TypedValue{}, false
	}
	return pv.Values[0], true
}

// ---------------------------------------------------------------------------
// ASSEMBLY
// ---------------------------------------------------------------------------

// assemble turns survivors into the response, in the order the rules require:
// sort, then aggregate over the FULL evaluated set, then page, then render.
//
// The order matters and is the subject of FR-125a. Aggregating after paging
// would produce a page-scoped number wearing the word "total", which is the
// exact defect an earlier revision of the worked example shipped.
func (e *evaluation) assemble(ctx context.Context, d Deps, echo string) generated.VaultFindResponse {
	q := e.q
	sortSurvivors(e.survivors, q)

	evaluated := len(e.survivors)

	// TOTALS FIRST, over every evaluated row — never over the page.
	totals := computeTotals(q, e.survivors)

	// GROUPS, before e.problems is read into the response below: a relation
	// value groupKeys could not resolve is reported into e.problems by
	// buildGroups itself (D5 — "reported, never silently rendered as a
	// distinct group of one"), and that report has to land before the
	// Problems field is set or it would silently miss the response it
	// belongs to.
	var groups []generated.VaultFindGroup
	if len(q.groupBy) > 0 {
		groups = buildGroups(q, e)
	}

	// FRESHNESS, per returned record (FR-020c). It is computed over the rows
	// this response RETURNS, which is what FR-020c1 scopes it to.
	page := e.survivors
	offset := cursorOffset(q.cursor)
	if offset > 0 && offset < len(page) {
		page = page[offset:]
	} else if offset >= len(page) && offset > 0 {
		page = nil
	}
	if len(page) > q.limit {
		page = page[:q.limit]
	}

	rows := make([]generated.VaultFindRow, 0, len(page))
	agreeing := 0
	for _, s := range page {
		row := renderRow(q, s)

		// The text hash comes from the word-search hit when there was one, and
		// is LOOKED UP otherwise. A typed query returns rows whose two indexes
		// can disagree exactly as easily as a word query's, and checking only
		// the word path would leave the commonest query shape unchecked.
		textHash := s.textHash
		if !s.hasText {
			if h, ok, err := d.Text.SourceHash(ctx, s.cand.Path); err == nil && ok {
				textHash = h
			}
		}
		fresh := propindex.CompareFreshness(s.cand.SourceHash, textHash)
		if fresh == propindex.FreshnessAgree {
			agreeing++
		} else {
			t := true
			row.Stale = &t
			p := problem(generated.StaleRecord, s.cand.Path+": "+fresh.Reason(),
				"re-run to confirm; run knowledge_describe check_integrity if it persists",
				identityOf(s.cand))
			p.Paths = &[]string{s.cand.Path}
			e.problems = append(e.problems, p)
		}
		rows = append(rows, row)
	}

	resp := generated.VaultFindResponse{
		Complete:  true,
		Refused:   false,
		QueryEcho: echo,
		Counts: generated.VaultFindCounts{
			// SELECTED is the records the query SELECTED — the survivors plus
			// the ones it could not read. It is NOT the narrowed candidate
			// population.
			//
			// It was the population, and that was a verdict that lied: every
			// record that simply did not MATCH the filter was then counted as
			// "could not be evaluated", so a clean 3-record corpus with a
			// 2-record answer rendered "COMPLETE: no — 1 of 3 selected records
			// could not be evaluated". A false caveat is worse than none: it
			// trains a reader to stop believing the header, which is the one
			// line this whole response format depends on.
			Selected:  evaluated + e.unevaluable,
			Evaluated: evaluated,
			Shown:     len(rows),
		},
		Rows:     rows,
		Totals:   totals,
		Problems: e.problems,
		Next:     []generated.VaultFindAction{},
		Index: &generated.VaultIndexState{
			Returned: len(rows),
			Agreeing: agreeing,
			Epoch:    &d.Epoch,
		},
	}
	if len(q.groupBy) > 0 {
		resp.Groups = &groups
	}
	if q.clamped {
		t := true
		resp.LimitClamped = &t
		asked := q.limitAsked
		resp.LimitRequested = &asked
	}
	applied := q.limit
	resp.LimitApplied = &applied

	// RESERVE THE CURSOR LINE'S AND THE NEXT BLOCK'S BYTES before
	// trimToBudget measures anything. The final cursor's PRESENCE can only be
	// known for certain after trimming — trimming a page that was otherwise
	// complete (limit never bound it) makes it incomplete, and an incomplete
	// page needs a cursor (see below), which also makes nextActions add a
	// "page" line, and a "narrow" line as soon as Shown drops below
	// Evaluated — but trimToBudget's own budget measurement has to include
	// all of those bytes on every pass, or the trim decision it makes is
	// measured against a response that grows again the moment the real
	// cursor and NEXT block are added afterward. Reserving whenever trimming
	// is even POSSIBLE (len(rows) is still above the floor trimToBudget will
	// not cut past) or the page was already limit-capped short of the full
	// evaluated set is a safe overestimate: worst case it trims one row it
	// did not strictly need to, never that it exceeds the budget or drops a
	// row with no cursor to reach it. The exact offset encoded here, and the
	// exact Shown this pessimistically forces nextActions to see, do not
	// matter yet — both are corrected below, once trimming has actually run.
	if len(rows) > minRenderedRows || offset+len(rows) < evaluated {
		c := encodeCursor(offset+len(rows), d.Epoch)
		resp.NextCursor = &c
		realShown := resp.Counts.Shown
		resp.Counts.Shown = 0
		resp.Next = nextActions(q, &resp)
		resp.Counts.Shown = realShown
	} else {
		resp.Next = nextActions(q, &resp)
	}

	// THE BUDGET IS APPLIED BEFORE THE VERDICT, and the order is the point: a
	// row dropped by the byte budget is a row the answer does not show, so the
	// header has to count it. Trimming afterwards would leave the verdict
	// describing a response that no longer exists.
	trimToBudget(&resp)
	finishVerdict(&resp, q)

	// THE CURSOR MUST BE DERIVED FROM WHAT WAS ACTUALLY RETURNED (F8), which
	// means it is computed AFTER trimToBudget, not before. Deriving it from
	// len(rows) — the page assemble() built before the byte budget dropped
	// rows off the end of it — encoded a NextCursor that started past every
	// row the budget trimmed: with 200 survivors and limit=50, a page of 50
	// trimmed to 20 by the budget still stamped `consumed = 0 + 50`, so the
	// next page picked up at row 50 and rows 20-49 were never returned by any
	// page at all. resp.Counts.Shown is trimToBudget's own count of what it
	// left in resp.Rows, so it is the one number guaranteed to match what this
	// response actually sent.
	if consumed := offset + resp.Counts.Shown; consumed < evaluated {
		c := encodeCursor(consumed, d.Epoch)
		resp.NextCursor = &c
	} else {
		resp.NextCursor = nil
	}
	resp.Next = nextActions(q, &resp)
	return resp
}

func identityOf(c propindex.Candidate) string {
	if c.RecordID != "" {
		return c.RecordID
	}
	return c.Path
}

// finishVerdict decides `complete` and writes the reason.
//
// The invariant it enforces is AC-P1's: a response whose verdict is `no` and
// whose problem list is EMPTY is a defect — either the reason is named or the
// verdict is wrong. Here the verdict is DERIVED from the problem list and the
// counts, so the two cannot disagree by construction.
func finishVerdict(resp *generated.VaultFindResponse, q *query) {
	var reasons []string

	unevaluable := resp.Counts.Selected - resp.Counts.Evaluated
	if unevaluable > 0 {
		reasons = append(reasons, fmt.Sprintf("%d of %d selected records could not be evaluated",
			unevaluable, resp.Counts.Selected))
	}
	if resp.Counts.Shown < resp.Counts.Evaluated {
		reasons = append(reasons, fmt.Sprintf("%d of %d shown",
			resp.Counts.Shown, resp.Counts.Evaluated))
	}
	if len(resp.Problems) > 0 && unevaluable == 0 {
		reasons = append(reasons, fmt.Sprintf("%d problem(s) reported", len(resp.Problems)))
	}
	if q.clamped {
		reasons = append(reasons, fmt.Sprintf("page size clamped to %d", q.limit))
		resp.Problems = append(resp.Problems, problem(generated.PageSizeClamped,
			fmt.Sprintf("limit=%d exceeds the cap of %d and was reduced to %d",
				q.limitAsked, MaxLimit, q.limit),
			fmt.Sprintf("ask for %d or fewer, and page with the cursor", MaxLimit)))
	}

	if len(reasons) == 0 {
		resp.Complete = true
		// A COMPLETE, ZERO-ROW UNTYPED ANSWER SAYS WHAT IT RESOLVED.
		//
		// An untyped query cannot refuse a misspelled property: FR-018b makes
		// every name legal there, and one no type declares resolves in the text
		// domain. So `stauts = "open"` is a valid query that correctly matches
		// nothing — and "0 records matched, complete" is precisely the shape
		// this surface exists to make trustworthy. The name cannot be refused;
		// the answer can carry the fact that nothing in scope declares it, which
		// is what turns a confident zero into a checkable one.
		resp.CompleteReason = undeclaredNameNote(q, resp.Counts.Evaluated)
		return
	}
	resp.Complete = false
	resp.CompleteReason = str(strings.Join(reasons, "; "))
}

// undeclaredNameNote returns the header line for a zero-row answer that named a
// property no in-scope record type declares, and nil for every other answer —
// including a zero-row one whose names all resolved to a declaration, where "0
// records matched" is already the whole truth.
//
// There is no `q.schema != nil` guard here and there must not be one: a TYPED
// query never reaches resolveUntyped, so its undeclared list is empty by
// construction and the guard would be a branch no test could distinguish from a
// working one.
func undeclaredNameNote(q *query, evaluated int) *string {
	if evaluated > 0 {
		// The name matched real rows, so the vault plainly does carry it. There
		// is nothing to caveat, and a note here would be the noise that trains a
		// reader to skip the header line.
		return nil
	}
	names := q.namespace().undeclared
	if len(names) == 0 {
		return nil
	}
	return str(fmt.Sprintf("0 records matched; no record type in scope declares %s, so %s compared as text "+
		"over the raw frontmatter — check the spelling with knowledge_describe",
		strings.Join(quoteAll(names), ", "),
		map[bool]string{true: "it was", false: "they were"}[len(names) == 1]))
}

// nextActions is FR-126: every response ends with calls the caller can issue.
func nextActions(q *query, resp *generated.VaultFindResponse) []generated.VaultFindAction {
	out := []generated.VaultFindAction{}
	if resp.NextCursor != nil {
		out = append(out, generated.VaultFindAction{
			Label: "page", Call: `knowledge_find cursor="` + *resp.NextCursor + `"`,
		})
	}
	if resp.Counts.Evaluated > resp.Counts.Shown && q.schema != nil {
		out = append(out, generated.VaultFindAction{
			Label: "narrow",
			Call:  "knowledge_find type=" + q.schema.Type + " filter=<tighten a leaf> limit=" + strconv.Itoa(q.limit),
		})
	}
	// The FIX action names a REAL record — the first one that failed — because
	// an instruction the caller has to fill in is an instruction the caller has
	// to guess at.
	for _, p := range resp.Problems {
		if p.Paths != nil && len(*p.Paths) > 0 {
			out = append(out, generated.VaultFindAction{
				Label: "fix", Call: `knowledge_read path="` + (*p.Paths)[0] + `"`,
			})
			break
		}
	}
	if len(resp.Rows) > 0 {
		out = append(out, generated.VaultFindAction{
			Label: "read", Call: `knowledge_read path="` + resp.Rows[0].Path + `"`,
		})
	}
	if len(out) == 0 {
		out = append(out, generated.VaultFindAction{
			Label: "describe", Call: "knowledge_describe",
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// CURSORS
// ---------------------------------------------------------------------------

// encodeCursor stamps the offset WITH the epoch it was issued against, which is
// what makes a stale cursor detectable at all. A bare offset would silently
// address a different row after the corpus changed.
func encodeCursor(offset int, epoch int64) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%d.%d", offset, epoch)))
}

func decodeCursor(s string) (offset int, epoch int64, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, 0, false
	}
	parts := strings.SplitN(string(raw), ".", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	o, err1 := strconv.Atoi(parts[0])
	e, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || o < 0 {
		return 0, 0, false
	}
	return o, e, true
}

func cursorOffset(s string) int {
	off, _, ok := decodeCursor(s)
	if !ok {
		return 0
	}
	return off
}

// rawEcho renders the query AS RECEIVED, for a response that was refused before
// the query could be validated into an executable form.
//
// It exists because FR-122's echo was EMPTY on every early refusal — the shape
// where a caller most needs to see what arrived. "unknown property conditon"
// with no echo leaves them unable to tell whether the tool saw the argument they
// think they sent, which is precisely the doubt the echo exists to remove.
//
// It reads only the wire request and reports what is PRESENT, never a default:
// a default shown on a query that never ran would be a claim about execution
// that did not happen.
func rawEcho(req generated.VaultFindRequest) string {
	var parts []string
	if req.Type != nil {
		parts = append(parts, "type="+*req.Type)
	}
	if req.Kind != nil {
		parts = append(parts, "kind="+string(*req.Kind))
	}
	if req.Words != nil {
		parts = append(parts, `words="`+*req.Words+`"`)
	}
	if req.View != nil {
		parts = append(parts, "view="+*req.View)
	}
	if req.Filter != nil {
		parts = append(parts, "filter="+describeNode(*req.Filter))
	}
	if req.Near != nil {
		parts = append(parts, "near="+*req.Near)
	}
	if req.Hops != nil {
		parts = append(parts, fmt.Sprintf("hops=%d", *req.Hops))
	}
	if req.Join != nil {
		parts = append(parts, "join="+strings.Join(*req.Join, ","))
	}
	if req.GroupBy != nil {
		parts = append(parts, "group_by="+strings.Join(*req.GroupBy, ","))
	}
	if req.Select != nil {
		parts = append(parts, "select="+strings.Join(*req.Select, ","))
	}
	if req.Sort != nil {
		for _, k := range *req.Sort {
			dir := "asc"
			if k.Direction != nil {
				dir = string(*k.Direction)
			}
			parts = append(parts, "sort="+k.Property+" "+dir)
		}
	}
	if req.Aggregate != nil {
		for _, a := range *req.Aggregate {
			label := string(a.Op) + "()"
			if a.Property != nil {
				label = string(a.Op) + "(" + *a.Property + ")"
			}
			parts = append(parts, "aggregate="+label)
		}
	}
	if req.Explain != nil && *req.Explain {
		parts = append(parts, "explain=true")
	}
	if req.Limit != nil {
		parts = append(parts, fmt.Sprintf("limit=%d", *req.Limit))
	}
	if req.Cursor != nil {
		parts = append(parts, `cursor="`+*req.Cursor+`"`)
	}
	if req.Detail != nil {
		parts = append(parts, "detail="+string(*req.Detail))
	}
	if len(parts) == 0 {
		return "(no arguments)"
	}
	return strings.Join(parts, "  ")
}

// describeNode renders an UNVALIDATED filter node. It cannot use node.text —
// that is built during validation, which is the step that failed — so it walks
// the wire shape directly and quotes the caller's own spelling, mistakes and all.
func describeNode(n generated.VaultFilterNode) string {
	switch {
	case n.All != nil:
		return "(" + joinNodes(*n.All, " AND ") + ")"
	case n.Any != nil:
		return "(" + joinNodes(*n.Any, " OR ") + ")"
	case n.Not != nil:
		return "NOT " + describeNode(*n.Not)
	}
	property, op := "?", "?"
	if n.Property != nil {
		property = *n.Property
	}
	if n.Op != nil {
		op = string(*n.Op)
	}
	switch {
	case n.Values != nil:
		return property + " " + op + " (" + strings.Join(*n.Values, ", ") + ")"
	case n.Value != nil:
		return property + " " + op + " '" + *n.Value + "'"
	}
	return property + " " + op
}

func joinNodes(in []generated.VaultFilterNode, sep string) string {
	parts := make([]string, 0, len(in))
	for i := range in {
		parts = append(parts, describeNode(in[i]))
	}
	return strings.Join(parts, sep)
}
