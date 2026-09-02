// Omnipus — browser_snapshot (ADR-072 D2, capability spec Stream D).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// browser_snapshot renders the page's accessibility tree as an indented
// `role "name"` outline that an agent can read and then act on directly.
//
// THREE THINGS ABOUT THIS TOOL ARE DELIBERATE AND EASY TO "FIX" WRONGLY.
//
//  1. It is READ-ONLY (FR-038). It calls NEITHER controlledResult NOR the D1
//     write lease, so it is never {"deferred": true} — it returns the full
//     tree while a human is driving the tab and while another tool holds the
//     lease. Adding either gate would make the one tool that tells an agent
//     what is on the page unavailable exactly when the page is contested.
//     Its Execute therefore starts at mgr.Session, not at the gates.
//
//  2. Field VALUES are emitted unconditionally (FR-018, operator ruling).
//     There is no include_values parameter and no role-based omission. A
//     filled password field's value IS in the output. §2.3 of the spec records
//     the accepted risk; do not add a suppression here without reopening it.
//
//  3. The cap is a NODE-BOUNDARY cut, not capGetText's prefix cut (FR-017).
//     capGetText is `text[:n] + suffix`, which splits mid-node and can split a
//     UTF-8 rune mid-sequence. This builds the outline node by node and stops
//     BEFORE the first node that would carry the running byte total past the
//     cap, so the output is always valid UTF-8, always whole nodes, and always
//     retains the top of the tree — which is the part worth keeping.

package browser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// snapshotByteCap is the output ceiling, in BYTES.
//
// It MIRRORS the constant rather than restating a number: the alignment with
// the ADR-066 D4 builtin-success cap is the property, and a literal 64000 here
// would go stale silently the day that constant moves. Bytes, not characters —
// revision 1's "exactly 64,000 characters" is false for any accessible name
// carrying a non-ASCII glyph, which is most of the non-English web.
const snapshotByteCap = config.DefaultBuiltinSuccessCap

// snapshotIndent is one level of outline indentation.
const snapshotIndent = "  "

// sensitiveReplacer is FR-027's defence-in-depth substitution, set at agent
// registration time (pkg/agent/loop.go, alongside SetActionabilityGate) and
// read per call.
//
// WHY A PACKAGE-LEVEL SEAM rather than a tool field: browser.RegisterTools
// takes no *config.Config and the tools are re-registered on every hot reload,
// so a field would have to be threaded through a signature four other units
// also edit. SetActionabilityGate on the same call site is the precedent.
//
// It is DEFENCE IN DEPTH ONLY, and the honest limit belongs in the code rather
// than only in the spec: it substitutes registered credential plaintexts and
// does NOTHING for an arbitrary form value the page happens to hold. The tool
// is not made safe by this; it is made marginally less unsafe.
var sensitiveReplacer atomic.Pointer[strings.Replacer]

// SetSensitiveDataReplacer installs the credential replacer FR-027 runs the
// rendered snapshot through. A nil replacer is legitimate — filtering can be
// unavailable — and the render then passes through unchanged.
func SetSensitiveDataReplacer(r *strings.Replacer) { sensitiveReplacer.Store(r) }

// applySensitiveReplacer runs the current replacer over one rendered line.
func applySensitiveReplacer(s string) string {
	r := sensitiveReplacer.Load()
	if r == nil || s == "" {
		return s
	}
	return r.Replace(s)
}

type SnapshotTool struct {
	tools.BaseTool
	browserAudit
	res ManagerResolver
}

func (t *SnapshotTool) Name() string                 { return "browser_snapshot" }
func (t *SnapshotTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *SnapshotTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

func (t *SnapshotTool) Description() string {
	return "Read the page's structure as an outline of what is actually on it: every element's ARIA " +
		"role, its accessible name, and the value it currently holds. Use this INSTEAD of a screenshot " +
		"when you need to act on the page rather than look at it — it is text, so you can quote it, and " +
		"every line names an element you can pass straight back as `role` + `name` (plus `index` when " +
		"the line shows one) to browser_click, browser_type, browser_select_option, browser_hover or " +
		"browser_press_key. It takes no arguments and never changes the page, so it works while " +
		"someone else is driving the browser. Large pages are cut at 64,000 bytes on a whole-element " +
		"boundary, keeping the top of the page and saying how many elements were left out. INTERIM: " +
		"this reads the workspace browser, which is shared with the operator and carries their live " +
		"logins — values you read here may be theirs."
}

// Parameters is deliberately EMPTY. In particular there is no include_values:
// FR-018 makes values unconditional, and a parameter that exists but must
// always be true is a control an operator would reasonably believe in.
func (t *SnapshotTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// snapshotRender is the outcome of one render: the text plus the metadata the
// audit event carries. The counts are fields rather than derived afterwards so
// the event and the text can never disagree.
type snapshotRender struct {
	Text string
	// NodeCount is the number of nodes EMITTED (post-Ignored-filter,
	// post-truncation) — the number of lines the agent actually received. The
	// pre-filter total is not what an operator reading the trail is asking
	// about ("how much of this page did the agent see?").
	NodeCount int
	// OmittedCount is how many surviving nodes the cap dropped.
	OmittedCount int
	// ValueNodes is how many emitted nodes carried a value.
	ValueNodes int
	// Truncated reports whether the cap fired.
	Truncated bool
	// OutputBytes is len(Text), measured AFTER the replacer, because that is
	// the size actually returned. [FILTERED] is not the same length as the
	// plaintext it replaces, so a pre-replacer figure would not describe the
	// result.
	OutputBytes int
}

// renderSnapshot walks the AX tree and produces the outline.
//
// ORDERING IS THE WHOLE OF FR-016. The `index` handle rendered on a line is
// the 0-based occurrence of that (role, name) pair in DOCUMENT ORDER — NOT a
// global line number. That is the only reading under which a handle read from
// a snapshot resolves in the very next call, because the action tools' `index`
// disambiguates among the elements matching a given role+name
// (selectAXCandidate, target.go), not among all elements on the page. A global
// index would render a handle that looks usable and resolves to the wrong
// element or to nothing.
func renderSnapshot(nodes []*accessibility.Node) snapshotRender {
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(nodes))
	hasParent := make(map[accessibility.NodeID]bool, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		byID[n.NodeID] = n
	}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		for _, c := range n.ChildIDs {
			if _, ok := byID[c]; ok {
				hasParent[c] = true
			}
		}
	}

	var out strings.Builder
	res := snapshotRender{}
	occurrence := make(map[string]int)
	// surviving counts every non-ignored node the walk reaches, so the
	// omitted-node figure in the truncation marker is the number actually left
	// out rather than "all remaining nodes including ignored ones".
	surviving := 0
	capped := false

	var walk func(n *accessibility.Node, depth int)
	walk = func(n *accessibility.Node, depth int) {
		if n == nil {
			return
		}
		childDepth := depth
		if !n.Ignored {
			surviving++
			role := axValueString(n.Role)
			name := axValueString(n.Name)
			value := axValueString(n.Value)
			line := renderSnapshotLine(depth, role, name, value, occurrence)
			// FR-027: the replacer runs BEFORE the cap, per line, so the cap
			// measures what is actually returned and a substitution can never
			// push the output over it after the fact.
			line = applySensitiveReplacer(line)
			if !capped && out.Len()+len(line) > snapshotByteCap {
				capped = true
			}
			if !capped {
				out.WriteString(line)
				res.NodeCount++
				if value != "" {
					res.ValueNodes++
				}
			}
			childDepth = depth + 1
		}
		// An IGNORED node's children are walked at the SAME depth the ignored
		// node occupied, not one deeper. Chrome marks wrapper elements ignored
		// constantly; indenting under an invisible parent produces an outline
		// whose nesting describes nothing on the page.
		for _, cid := range n.ChildIDs {
			walk(byID[cid], childDepth)
		}
	}

	for _, n := range nodes {
		if n == nil || hasParent[n.NodeID] {
			continue
		}
		walk(n, 0)
	}

	res.Truncated = capped
	res.OmittedCount = surviving - res.NodeCount
	res.Text = out.String()
	if capped {
		// The marker names BOTH facts the spec fixes: the byte cap and the
		// omitted-node count. It is appended after the cap check on purpose —
		// a marker that had to fit inside the budget could itself be truncated,
		// which is the one line that must never be.
		res.Text += fmt.Sprintf("\n[truncated at %d bytes; %d further nodes omitted]",
			snapshotByteCap, res.OmittedCount)
	}
	res.OutputBytes = len(res.Text)
	return res
}

// renderSnapshotLine formats one node and advances its (role, name) occurrence
// counter.
func renderSnapshotLine(depth int, role, name, value string, occurrence map[string]int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(snapshotIndent, depth))
	if role == "" {
		role = "(unknown)"
	}
	b.WriteString(role)
	if name != "" {
		b.WriteString(fmt.Sprintf(" %q", name))
	}
	if value != "" {
		// value= is a separate labelled field, never concatenated into the
		// name: an agent must be able to tell "the button labelled Submit"
		// from "the field whose current contents are Submit".
		b.WriteString(fmt.Sprintf(" value=%q", value))
	}
	if role != "(unknown)" && name != "" {
		key := role + "\x00" + name
		idx := occurrence[key]
		occurrence[key] = idx + 1
		// index= is rendered on EVERY named node, including the first and
		// including unique ones. Rendering it only on duplicates would make
		// the agent's next call depend on a count it cannot see, and index=0
		// on a unique match resolves correctly (selectAXCandidate).
		b.WriteString(fmt.Sprintf(" index=%d", idx))
	}
	b.WriteString("\n")
	return b.String()
}

func (t *SnapshotTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	_ = args // takes no arguments — see Parameters.

	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// NO controlledResult and NO leaseWrite here (FR-038). This is not an
	// omission and the structural test in this package asserts their absence.

	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: %s", t.Name(), err))
	}
	tabCtx, cancelTimeout := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer cancelTimeout()

	var nodes []*accessibility.Node
	err = chromedp.Run(tabCtx, chromedp.ActionFunc(func(c context.Context) error {
		var ferr error
		nodes, ferr = accessibility.GetFullAXTree().Do(c)
		return ferr
	}))
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: could not read the page's accessibility tree: %s",
			t.Name(), err))
	}

	render := renderSnapshot(nodes)
	t.recordSnapshot(ctx, key, owner, hostOfActiveTab(mgr, sid), render)

	if render.NodeCount == 0 {
		return tools.ErrorResult(fmt.Sprintf(
			"%s: the page exposes no accessible elements. It is probably blank or still loading — "+
				"navigate first, or wait for content and retry.", t.Name()))
	}
	return tools.SilentResult(render.Text)
}

// recordSnapshot writes FR-028's metadata-only audit event.
//
// METADATA ONLY, and the omission is the requirement: the tool renders field
// values by operator ruling, so putting the rendered text in an audit row
// would copy every password and card number the page held into a file whose
// whole purpose is to be retained and read later. The event answers "a capture
// happened, of this shape, on this origin"; the chat thread answers "what was
// captured".
func (t *SnapshotTool) recordSnapshot(
	ctx context.Context, key BrowsingKey, owner TabOwner, pageOrigin string, r snapshotRender,
) {
	log := t.auditLogger()
	if log == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventBrowserSnapshot,
		Decision:  audit.DecisionAllow,
		AgentID:   tools.ToolAgentID(ctx),
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Tool:      t.Name(),
		Details: map[string]any{
			"workspace_id":        key.WorkspaceID(),
			"browsing_key":        key.String(),
			"tab_owner":           owner.String(),
			"page_origin":         pageOrigin,
			"node_count":          r.NodeCount,
			"output_bytes":        r.OutputBytes,
			"value_nodes_emitted": r.ValueNodes,
			"truncated":           r.Truncated,
		},
	}
	if err := log.Log(entry); err != nil {
		slog.Error("browser audit: snapshot log write failed",
			"error", err, "tool", "browser_snapshot", "workspace_id", key.WorkspaceID())
	}
}

var _ tools.Tool = (*SnapshotTool)(nil)
