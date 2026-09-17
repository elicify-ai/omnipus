package browser

// target.go — the SINGLE element-resolution seam every browser tool that
// names an element goes through (ADR-075 D2, spec §3 "Shared interface
// contract"). It is deliberately the FIRST thing landed in this wave: four
// existing tools and every new interaction verb code against `Locator`,
// `ErrLocatorConflict` and `resolveTarget`, so their shapes are fixed here
// before anything calls them.
//
// This is NOT a new package. resolveActionSelector, resolvePseudoOnlySelector,
// wrapTextMatch, removeTextMarker, textMarkerAttr, textResolvePollInterval and
// nextTextSelectorToken all stay unexported in package browser and are reused
// verbatim — the CSS and visible-text branches keep running the exact code the
// existing tests already exercise, so this change is additive to them.
//
// The one genuinely new branch is ARIA role + accessible name, built on CDP
// `Accessibility.queryAXTree`. It ends where the text branch ends: by stamping
// the same `data-omnipus-tsel` marker attribute on the winning node and
// returning the same `[data-omnipus-tsel="<token>"]` marker selector. That is
// what makes it additive downstream — no chromedp action anywhere changes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Locator is the closed set of ways an agent may name an element.
//
// EXACTLY ONE locator KIND may be populated: CSS/text, or role+name. Supplying
// both kinds is an ErrLocatorConflict that NAMES both populated fields — never
// a silent precedence rule ("honoured in that documented order" is a weaker
// contract than "exactly one", and an agent cannot tell which of its two
// locators actually ran).
//
// Selector and Text together are ONE kind, not two. That composite — "match
// this visible text, but only among elements inside this CSS scope" — is the
// pre-existing, shipped and tested contract of resolveActionSelector, echoed
// in browser_click's and browser_get_text's own Descriptions. Treating it as a
// conflict would break every caller that uses it and would fail the FR-008
// regression set outright, so the conflict rule is drawn between the CSS/text
// kind and the role+name kind. See TestLocator_ConflictNamesBothFields.
type Locator struct {
	// Selector is a CSS selector, optionally with a trailing
	// :has-text()/:text-is()/:text() pseudo.
	Selector string
	// Text is a visible-text substring. NOT valid on browser_type or
	// browser_press_key, whose own `text`/`key` argument is the VALUE they
	// send, not a way to name an element.
	Text string
	// Role is an ARIA/computed role, e.g. "button", "combobox", "link".
	Role string
	// Name is the computed accessible name, e.g. "Submit".
	Name string
	// Index is a 0-based disambiguator used when the locator matches more
	// than one element; nil means "the match must be unique".
	//
	// A pointer, not an (int, bool) pair: the two-field form permits an
	// Index:3 / HasIndex:false state that nothing validates and that reads
	// as a deliberate choice in a debugger.
	Index *int
}

// locatorKind is the closed set of resolution branches. It is unexported and
// derived, never supplied by a caller.
type locatorKind int

const (
	locatorKindNone locatorKind = iota
	// locatorKindCSS covers Selector, Text, and the composite Selector+Text.
	locatorKindCSS
	locatorKindRoleName
)

// kind reports which locator kind this Locator populates, and the field names
// that populated it (for error messages).
func (l Locator) kind() (locatorKind, []string) {
	var css, role []string
	if l.Selector != "" {
		css = append(css, "selector")
	}
	if l.Text != "" {
		css = append(css, "text")
	}
	if l.Role != "" {
		role = append(role, "role")
	}
	if l.Name != "" {
		role = append(role, "name")
	}
	switch {
	case len(css) > 0 && len(role) > 0:
		return locatorKindNone, append(css, role...)
	case len(role) > 0:
		return locatorKindRoleName, role
	case len(css) > 0:
		return locatorKindCSS, css
	default:
		return locatorKindNone, nil
	}
}

// ErrLocatorConflict is returned when more than one locator KIND is populated,
// or when a tool is handed a locator kind it does not accept. It names the
// offending fields and the tool; it NEVER picks a winner.
//
// It keeps the Err prefix for the same reason ErrNotActionable does: the name
// is fixed by the shared D2 interface contract the other browser streams code
// against. Keep the suppression below to ONE line — gofmt moves a //nolint
// directive to the end of its comment block, so a multi-line rationale ends up
// severed from its own first clause.
//
//nolint:errname // Name fixed by the shared D2 interface contract; see above.
type ErrLocatorConflict struct {
	// Fields are the populated locator argument names, agent-facing (the
	// JSON parameter names, not the Go field names).
	Fields []string
	// Tool is the tool that received them, e.g. "browser_click".
	Tool string
	// Reason, when non-empty, explains why this combination is not accepted
	// by this particular tool (the per-tool matrix, spec §3).
	Reason string
}

func (e *ErrLocatorConflict) Error() string {
	fields := strings.Join(e.Fields, " and ")
	if e.Reason != "" {
		return fmt.Sprintf("%s: %s cannot be used together here: %s. Supply exactly one way to name the element.",
			e.Tool, fields, e.Reason)
	}
	return fmt.Sprintf("%s: %s name the element in two different ways; supply exactly one of {selector/text, role+name}.",
		e.Tool, fields)
}

// locatorAcceptance is the per-tool locator matrix from spec §3, expressed as
// data so it can be asserted row by row rather than inferred from behaviour
// (TestLocator_PerToolMatrix_Table).
//
// A tool absent from this map accepts every kind — the matrix constrains, it
// does not enumerate. Tools that take NO locator at all (browser_snapshot,
// browser_handle_dialog) never call resolveTarget, so they are not rows here.
type locatorAcceptance struct {
	css      bool
	text     bool
	roleName bool
	// textReason explains a text rejection in the tool's own terms.
	textReason string
}

var locatorMatrix = map[string]locatorAcceptance{
	"browser_click":         {css: true, text: true, roleName: true},
	"browser_get_text":      {css: true, text: true, roleName: true},
	"browser_wait":          {css: true, text: true, roleName: true},
	"browser_hover":         {css: true, text: true, roleName: true},
	"browser_select_option": {css: true, text: true, roleName: true},
	"browser_upload_file":   {css: true, text: true, roleName: true},
	"browser_type": {
		css: true, text: false, roleName: true,
		textReason: "`text` on browser_type is the VALUE typed into the element, not a way to find it",
	},
	"browser_press_key": {
		css: true, text: false, roleName: true,
		textReason: "`key` is the value dispatched and `text` is not a locator on this tool",
	},
}

// ErrEmptyAXTree is returned when the role+name branch cannot query an
// accessibility tree at all — a tab that has not committed a document. Named
// so an agent can tell it apart from "the element is not there yet"; a
// nil-deref or a silent zero-match would read as the latter.
var ErrEmptyAXTree = errors.New("the page has no committed document, so it has no accessibility tree to search")

// errIndexNotApplicable is the validation error for `index` on a CSS or text
// locator. Silently ignoring it would be worse: the agent would believe it had
// disambiguated a multi-match when it had not.
const errIndexNotApplicableFmt = "%s: `index` disambiguates a role+name match and is not applicable to a %s locator; drop it, or name the element by role+name"

// validateLocator applies the per-tool matrix, the one-kind rule and the
// `index` rules. It runs BEFORE any CDP call is issued, so an invalid locator
// costs zero round trips (spec §10 dataset: "no CDP call issued").
func validateLocator(toolName string, loc Locator) (locatorKind, error) {
	kind, fields := loc.kind()

	if kind == locatorKindNone {
		if len(fields) > 0 {
			return locatorKindNone, &ErrLocatorConflict{Fields: fields, Tool: toolName}
		}
		return locatorKindNone, fmt.Errorf("%s: no locator given; supply `selector`, `text`, or `role`+`name`", toolName)
	}

	if acc, ok := locatorMatrix[toolName]; ok {
		if loc.Text != "" && !acc.text {
			return locatorKindNone, &ErrLocatorConflict{
				Fields: []string{"text"},
				Tool:   toolName,
				Reason: acc.textReason,
			}
		}
		if kind == locatorKindRoleName && !acc.roleName {
			return locatorKindNone, &ErrLocatorConflict{Fields: fields, Tool: toolName, Reason: "this tool does not accept a role+name locator"}
		}
		if kind == locatorKindCSS && !acc.css {
			return locatorKindNone, &ErrLocatorConflict{Fields: fields, Tool: toolName, Reason: "this tool does not accept a CSS locator"}
		}
	}

	if loc.Index != nil {
		if *loc.Index < 0 {
			return locatorKindNone, fmt.Errorf("%s: `index` must be 0 or greater, got %d", toolName, *loc.Index)
		}
		if kind != locatorKindRoleName {
			return locatorKindNone, fmt.Errorf(errIndexNotApplicableFmt, toolName, "CSS/text")
		}
	}

	return kind, nil
}

// resolveTarget is the SINGLE seam every element-naming browser tool resolves
// through. It supersedes resolveActionSelector and resolvePseudoOnlySelector
// as an ENTRY POINT; both survive as internal branches, unchanged, so the
// existing CSS and visible-text tests keep exercising the same code.
//
// It returns a CSS string the caller's existing chromedp ByQuery action uses
// unchanged. For the role+name branch that string is the SAME
// data-omnipus-tsel marker selector the text branch already produces, stamped
// on the winning accessibility node's backing DOM node — which is what keeps
// this change additive: no downstream chromedp action anywhere changes.
//
// cleanup MUST be deferred immediately by the caller. It is always safe to
// call, including on the error path and when no marker was ever set.
func resolveTarget(
	tabCtx context.Context,
	toolName string,
	loc Locator,
	timeout time.Duration,
) (target string, cleanup func(), err error) {
	kind, verr := validateLocator(toolName, loc)
	if verr != nil {
		return "", func() {}, verr
	}

	switch kind {
	case locatorKindRoleName:
		return resolveRoleNameTarget(tabCtx, toolName, loc, timeout)
	default:
		// The pre-existing branches, entered exactly as before.
		return resolveActionSelector(tabCtx, toolName, loc.Selector, loc.Text, timeout)
	}
}

// ---------------------------------------------------------------------------
// The ARIA role + accessible-name branch
// ---------------------------------------------------------------------------

// axCandidate is one surviving accessibility node, kept with the fields the
// ordering, error messages and stamping all need.
type axCandidate struct {
	backendID cdp.BackendNodeID
	name      string
	role      string
}

// resolveRoleNameTarget resolves an element by ARIA role and computed
// accessible name, and returns the marker selector for it.
//
// It polls at textResolvePollInterval — the SAME interval the visible-text
// matcher uses, reused rather than re-declared — until the element appears or
// the caller's timeout expires, so a role+name locator behaves like a text one
// on a page that is still rendering.
func resolveRoleNameTarget(
	tabCtx context.Context,
	toolName string,
	loc Locator,
	timeout time.Duration,
) (string, func(), error) {
	deadlineCtx, cancel := context.WithTimeout(tabCtx, timeout)
	defer cancel()

	var lastErr error
	// sawCleanNoMatch records that at least one poll completed and found
	// nothing — the state the "why did it find nothing" diagnosis explains.
	// It is kept SEPARATELY from lastErr because the diagnosis is built once,
	// after the loop, and must survive a final poll that was cut short by our
	// own deadline rather than by anything about the page.
	sawCleanNoMatch := false
	ignoredFromPrimary := 0
	for {
		cands, ignoredCount, childFrameCount, qerr := queryAXCandidates(deadlineCtx, loc)
		switch {
		case qerr != nil:
			// A query that failed because OUR OWN deadline expired mid-flight
			// says nothing about the page, so it must not overwrite what an
			// earlier, complete poll already established. Without this guard
			// the last ~20 ms of every resolution is a window in which a
			// clean "found, but hidden" answer is replaced by a transport
			// error — see the note on noMatchErr.
			if deadlineCtx.Err() == nil {
				lastErr = qerr
			}
		case len(cands) > 0:
			winner, serr := selectAXCandidate(toolName, loc, cands)
			if serr != nil {
				// A multi-match or an out-of-range index is DEFINITIVE — the
				// page is not going to become less ambiguous by waiting, and
				// polling on it would turn a clear error into a timeout.
				return "", func() {}, serr
			}
			marker, merr := stampAXWinner(deadlineCtx, winner)
			if merr != nil {
				lastErr = merr
				break
			}
			token := tokenFromMarkerSelector(marker)
			return marker, func() { removeTextMarker(tabCtx, token) }, nil
		case childFrameCount > 0:
			// Definitive too: frame targeting is out of scope, and waiting
			// cannot move the element into the top document.
			return "", func() {}, fmt.Errorf(
				"%s: %s matched in a child frame; frame targeting is out of scope (ADR-075 D2.6) — use a CSS locator inside that frame",
				toolName, displayRoleName(loc))
		default:
			// Nothing survived. Record that, but do NOT ask WHY yet: the
			// "why" costs two extra round trips and only the last poll's
			// answer is ever used, so it is built once, below, after the
			// deadline — and on a budget of its own.
			sawCleanNoMatch = true
			if ignoredCount > ignoredFromPrimary {
				ignoredFromPrimary = ignoredCount
			}
			lastErr = nil
		}

		select {
		case <-deadlineCtx.Done():
			if sawCleanNoMatch && lastErr == nil {
				return "", func() {}, noMatchErr(tabCtx, toolName, loc, ignoredFromPrimary)
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("%s: no element matching %s", toolName, displayRoleName(loc))
			}
			return "", func() {}, lastErr
		case <-time.After(textResolvePollInterval):
		}
	}
}

// axDiagnosisBudget bounds the two extra round trips noMatchErr spends to tell
// "absent" from "hidden". It is deliberately NOT the caller's resolve timeout:
// by the time this runs that timeout is exhausted by construction, and probing
// on an expired context returns nothing — which silently degrades the answer
// to the plain "not found" the whole diagnosis exists to avoid.
//
// Measured on real Chrome, each probe is a single ~15 ms queryAXTree round
// trip, so a second is roughly 30x headroom for a loaded machine. It is spent
// only on the failure path, and only once per resolution.
const axDiagnosisBudget = time.Second

// noMatchErr builds the "nothing matched" error, distinguishing the two cases
// an agent has to act on differently: the element is genuinely absent, versus
// the element is there but hidden from assistive technology.
//
// The second case has to be a SEPARATE sentence or the agent retries the same
// locator forever. It cannot tell the two apart from "not found".
//
// ctx MUST be a LIVE context (the tab's), never the expired resolve deadline.
// This was the defect that survived e213064d1: the probes ran inside the poll
// loop on the resolve deadline, so whenever that deadline happened to expire
// during the ~30 ms the two probes take — about one resolution in six, and
// more often on a slower machine — both probes returned nothing, the count
// came back 0, and the error silently fell back to the "no element matching"
// wording. The fix passed on macOS and failed in Linux CI for that reason
// alone: it was never a platform difference, it was a race whose window is a
// larger share of the poll cycle the slower the machine is.
func noMatchErr(ctx context.Context, toolName string, loc Locator, ignoredFromPrimary int) error {
	ignored := ignoredFromPrimary
	if ignored == 0 {
		probeCtx, cancel := context.WithTimeout(ctx, axDiagnosisBudget)
		defer cancel()
		ignored = countIgnoredCandidates(probeCtx, loc)
	}
	// The iframe sentence rides on BOTH branches, not just the plain
	// not-found one. A page can have a hidden candidate AND the element the
	// agent wants inside a frame at the same time, and attributing the miss
	// to the hidden one would send it down the wrong path. Both limitations
	// are always true of this locator, so both are always stated.
	const scopeNote = " role+name searches the TOP document only, so an element inside an iframe " +
		"cannot be found this way; a CSS `selector` reaches both cases."

	if ignored > 0 {
		return fmt.Errorf(
			"%s: no visible match for %s — %d element(s) on the page are hidden from assistive "+
				"technology (aria-hidden, display:none, or a presentational container) and cannot be "+
				"reached this way. Also,%s",
			toolName, displayRoleName(loc), ignored, scopeNote)
	}
	return fmt.Errorf("%s: no element matching %s.%s", toolName, displayRoleName(loc), scopeNote)
}

// countIgnoredCandidates asks the SECOND question, on the failure path only:
// "is something with this role, or this name, actually there but ignored?"
//
// It needs its own queries because of a Chrome behaviour that is not obvious
// and was measured rather than assumed. queryAXTree does return nodes that are
// ignored for accessibility — but it does NOT compute a name for them. An
// `<button aria-hidden="true">Ghost</button>` comes back from a role-only
// query as {ignored: true, role: "button", name: ""}, so a combined
// role+name query filters it out before the Ignored flag is ever read, and
// the primary query's ignored count is 0.
//
// (Its TEXT survives separately: a name-only query for "Ghost" returns an
// ignored StaticText node. Both probes are run and the larger count wins,
// because either one alone misses a real case.)
func countIgnoredCandidates(ctx context.Context, loc Locator) int {
	probe := func(q *accessibility.QueryAXTreeParams) int {
		var n int
		_ = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
			docID, release, err := documentObjectID(c)
			if err != nil {
				return nil
			}
			defer release()
			nodes, err := q.WithObjectID(docID).Do(c)
			if err != nil {
				return nil
			}
			for _, node := range nodes {
				if node != nil && node.Ignored {
					n++
				}
			}
			return nil
		}))
		return n
	}

	best := 0
	if loc.Role != "" {
		if n := probe(accessibility.QueryAXTree().WithRole(loc.Role)); n > best {
			best = n
		}
	}
	if loc.Name != "" {
		if n := probe(accessibility.QueryAXTree().WithAccessibleName(loc.Name)); n > best {
			best = n
		}
	}
	return best
}

// queryAXCandidates runs ONE Accessibility.queryAXTree round trip (plus the
// DOM.getDocument the command requires — it errors without a root node) and
// returns the surviving candidates in document order, along with the counts
// the two "no match, but here is why" errors need.
func queryAXCandidates(ctx context.Context, loc Locator) (cands []axCandidate, ignored, childFrame int, err error) {
	err = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		// queryAXTree REQUIRES a root: "If no DOM node is specified, or the
		// DOM node does not exist, the command returns an error." The root is
		// obtained per resolution — never cached, since a cached id goes
		// stale on every navigation and a stale root fails as "element not
		// found", the most misleading failure this seam could produce.
		docID, release, derr := documentObjectID(c)
		if derr != nil {
			// Only a document that genuinely is not there is ErrEmptyAXTree.
			// A handle we could not fetch because the caller's deadline
			// expired mid-flight is a TIMEOUT, and reporting it as "the page
			// has no committed document" is an accurate-sounding claim about
			// a page that is fine — the most misleading shape of error this
			// seam can emit.
			if cerr := c.Err(); cerr != nil {
				return cerr
			}
			return ErrEmptyAXTree
		}
		defer release()

		q := accessibility.QueryAXTree().WithObjectID(docID)
		if loc.Role != "" {
			q = q.WithRole(loc.Role)
		}
		if loc.Name != "" {
			q = q.WithAccessibleName(loc.Name)
		}
		nodes, qerr := q.Do(c)
		if qerr != nil {
			return qerr
		}

		// The frame every surviving node must belong to. Sourced from the
		// nodes themselves rather than from a DOM.getDocument call — see
		// documentObjectID for why that call is not made here.
		var topFrameID cdp.FrameID
		for _, n := range nodes {
			if n != nil && !n.Ignored && n.FrameID != "" {
				topFrameID = n.FrameID
				break
			}
		}

		for _, n := range nodes {
			if n == nil || n.BackendDOMNodeID == 0 {
				continue
			}
			// queryAXTree's own doc: it returns matches "including nodes
			// that are ignored for accessibility". Filtering these out is
			// not an optimisation — without it a hidden node wins.
			if n.Ignored {
				ignored++
				continue
			}
			// The marker stamp and the downstream chromedp ByQuery are both
			// document-scoped, so a match owned by a child frame would
			// resolve an attribute the query can never find.
			//
			// DEFENSIVE, and measured to be so: rooted at the top document,
			// queryAXTree does not descend into a child frame at all on
			// Chrome 152 — a framed element simply does not come back, so
			// this branch never fires today. It stays because if that ever
			// changes the alternative is a marker stamped on a node the
			// click can never find: a silent timeout with no explanation.
			// What actually reaches the agent for the frame case is the
			// sentence in noMatchErr.
			if n.FrameID != "" && topFrameID != "" && n.FrameID != topFrameID {
				childFrame++
				continue
			}
			cands = append(cands, axCandidate{
				backendID: n.BackendDOMNodeID,
				name:      axValueString(n.Name),
				role:      axValueString(n.Role),
			})
		}
		return nil
	}))
	if err != nil {
		return nil, 0, 0, err
	}

	// DETERMINISTIC ORDERING, asserted directly by
	// TestResolveTarget_IndexSelectsDocumentOrder rather than inferred from a
	// passing click. queryAXTree walks the subtree, so its return order is
	// already document order; sorting on BackendNodeID would NOT be document
	// order (backend ids are allocation order, which is not the same thing on
	// a page that has mutated). A stable sort on the position queryAXTree
	// returned therefore preserves document order. Nothing re-sorts here on
	// purpose: any sort key available to us (BackendNodeID) would be WRONG.
	return cands, ignored, childFrame, nil
}

// documentObjectID returns a Runtime remote-object handle on the page's
// `document`, plus a release func the caller MUST defer.
//
// It exists instead of the obvious `dom.GetDocument()` because of a defect
// that was reproduced, not theorised. DOM.getDocument RESETS the DevTools DOM
// agent's node-id map, and chromedp keeps its own cache of node ids populated
// from that map. An out-of-band getDocument from our code therefore
// invalidates every node id chromedp is holding for that tab — so the very
// next `chromedp.Click(sel, ByQuery)` or `WaitVisible` polls a node id the
// browser no longer recognises and times out.
//
// The symptom was ugly and would have been near-impossible to diagnose in the
// field: resolving an element by role+name SUCCEEDED, the actionability gate
// PASSED (it uses Runtime.evaluate, which is unaffected), and then the click
// failed with "the element passed the actionability gate and then stopped
// being visible" — an accurate-sounding message about a page that had not
// changed at all. Caught by TestResolveTarget_AllActionToolsShareSeam, which
// is the only test that drives three real tools over one tab in sequence.
//
// Runtime.evaluate touches no node-id state, so this costs the same one round
// trip and poisons nothing.
func documentObjectID(ctx context.Context) (runtime.RemoteObjectID, func(), error) {
	noop := func() {}
	obj, exc, err := runtime.Evaluate("document").Do(ctx)
	if err != nil {
		return "", noop, err
	}
	if exc != nil {
		return "", noop, fmt.Errorf("%s", exc.Text)
	}
	if obj == nil || obj.ObjectID == "" {
		return "", noop, ErrEmptyAXTree
	}
	id := obj.ObjectID
	return id, func() { _ = runtime.ReleaseObject(id).Do(ctx) }, nil
}

// axValueString flattens an accessibility.Value's string payload, tolerating
// the nil and non-string cases CDP allows.
func axValueString(v *accessibility.Value) string {
	if v == nil || len(v.Value) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Value, &s); err == nil {
		return s
	}
	return strings.Trim(string(v.Value), `"`)
}

// selectAXCandidate applies the index / uniqueness rule.
func selectAXCandidate(toolName string, loc Locator, cands []axCandidate) (axCandidate, error) {
	if loc.Index != nil {
		i := *loc.Index
		if i >= len(cands) {
			return axCandidate{}, fmt.Errorf("%s: `index` %d is out of range for %s — %d element(s) match",
				toolName, i, displayRoleName(loc), len(cands))
		}
		return cands[i], nil
	}
	if len(cands) > 1 {
		return axCandidate{}, fmt.Errorf("%s: %s is ambiguous — %d elements match (%s); add `index` to pick one, or use a CSS `selector`",
			toolName, displayRoleName(loc), len(cands), describeFirstCandidates(cands, 3))
	}
	return cands[0], nil
}

// describeFirstCandidates renders at most n candidates for an ambiguity error,
// mirroring the shape resolvePendingErr uses for the text matcher.
func describeFirstCandidates(cands []axCandidate, n int) string {
	if n > len(cands) {
		n = len(cands)
	}
	parts := make([]string, 0, n)
	for _, c := range cands[:n] {
		label := c.name
		if label == "" {
			label = "(unnamed)"
		}
		parts = append(parts, fmt.Sprintf("%s %q", c.role, label))
	}
	out := strings.Join(parts, ", ")
	if len(cands) > n {
		out += ", …"
	}
	return out
}

// stampAXWinner sets the shared data-omnipus-tsel marker attribute on the
// winning node and returns the marker selector — the SAME shape the visible-
// text branch returns, which is what lets every downstream chromedp action
// stay exactly as it was.
//
// DOM.setAttributeValue takes a cdp.NodeID and queryAXTree hands back a
// BackendNodeID, and this cdproto revision exposes no
// DOM.pushNodesByBackendIdsToFrontend, so the winner is resolved to a remote
// object and the attribute is set through Runtime.callFunctionOn on it.
func stampAXWinner(ctx context.Context, winner axCandidate) (string, error) {
	token := nextTextSelectorToken()
	attrJSON, err := json.Marshal(textMarkerAttr)
	if err != nil {
		return "", err
	}
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return "", err
	}

	fn := fmt.Sprintf("function(){ this.setAttribute(%s, %s); return true; }", attrJSON, tokenJSON)

	err = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		obj, rerr := dom.ResolveNode().WithBackendNodeID(winner.backendID).Do(c)
		if rerr != nil {
			return rerr
		}
		if obj == nil || obj.ObjectID == "" {
			return fmt.Errorf("the matched element could not be resolved in the page")
		}
		defer func() { _ = runtime.ReleaseObject(obj.ObjectID).Do(c) }()

		_, exc, cerr := runtime.CallFunctionOn(fn).
			WithObjectID(obj.ObjectID).
			WithReturnByValue(true).
			Do(c)
		if cerr != nil {
			return cerr
		}
		if exc != nil {
			return fmt.Errorf("marking the matched element failed: %s", exc.Text)
		}
		return nil
	}))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`[%s="%s"]`, textMarkerAttr, token), nil
}

// displayRoleName renders a role+name locator the way an agent wrote it, for
// error messages: role=button name="Submit".
func displayRoleName(loc Locator) string {
	var parts []string
	if loc.Role != "" {
		parts = append(parts, "role="+loc.Role)
	}
	if loc.Name != "" {
		parts = append(parts, fmt.Sprintf("name=%q", loc.Name))
	}
	if loc.Index != nil {
		parts = append(parts, fmt.Sprintf("index=%d", *loc.Index))
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

// withRoleNameParams adds the agent-facing JSON schema fragment for the
// role/name/index locator to a tool's existing property map, so the three
// parameters are described identically everywhere. (Tools whose whole schema
// is the locator build it with locatorParamSchema in tools_interact.go
// instead; this variant is for the four shipped tools that already had their
// own selector/text wording worth keeping.)
func withRoleNameParams(into map[string]any) map[string]any {
	into["role"] = map[string]any{
		"type":        "string",
		"description": "ARIA role of the element, e.g. \"button\", \"link\", \"combobox\". Use with `name`. Survives CSS class names that change on every deploy.",
	}
	into["name"] = map[string]any{
		"type":        "string",
		"description": "Accessible name of the element, e.g. \"Submit\" — the label a screen reader would read out. Use with `role`.",
	}
	into["index"] = map[string]any{
		"type":        "integer",
		"description": "0-based disambiguator when `role`+`name` match more than one element, in document order. Omit to require a unique match.",
	}
	return into
}

// parseLocatorArgs reads the locator fields off a tool's raw argument map.
//
// textIsLocator is false for browser_type and browser_press_key: their own
// `text` / `key` argument is the VALUE they send, not a way to find an
// element, so it must never be read as one.
func parseLocatorArgs(toolName string, args map[string]any, textIsLocator bool) (Locator, error) {
	loc := Locator{}
	loc.Selector, _ = args["selector"].(string)
	if textIsLocator {
		loc.Text, _ = args["text"].(string)
	}
	loc.Role, _ = args["role"].(string)
	loc.Name, _ = args["name"].(string)

	if raw, ok := args["index"]; ok && raw != nil {
		f, ok := raw.(float64)
		if !ok {
			if i, iok := raw.(int); iok {
				f = float64(i)
			} else {
				return Locator{}, fmt.Errorf("%s: `index` must be a whole number", toolName)
			}
		}
		if f != float64(int(f)) {
			return Locator{}, fmt.Errorf("%s: `index` must be a whole number, got %v", toolName, f)
		}
		i := int(f)
		loc.Index = &i
	}
	return loc, nil
}

// empty reports whether nothing at all was supplied to find an element by.
func (l Locator) empty() bool {
	return l.Selector == "" && l.Text == "" && l.Role == "" && l.Name == ""
}

// roleNameLocatorHelp is the one sentence every locator-accepting tool's
// Description carries about the role+name locator, written once so the eight
// tools cannot drift into describing it eight different ways.
const roleNameLocatorHelp = "You can also name the element the way a person would — by its ARIA `role` " +
	"and its accessible `name` (role=\"button\", name=\"Submit\"). Prefer that on a site whose CSS class " +
	"names look generated: they change on every deploy, a role and a name do not. Add `index` (0-based, " +
	"document order) when more than one element matches. Give exactly ONE of {selector/text} or {role+name}. "
