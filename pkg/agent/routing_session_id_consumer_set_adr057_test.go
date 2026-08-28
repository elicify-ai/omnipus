// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// routing_session_id_consumer_set_adr057_test.go — ADR-057 test #29
// (TestRoutingSessionID_ConsumerSetIsClosed, BDD-17/BDD-97, FR-014).
//
// Tagged U3 in the TDD plan, U3 explicitly deferred it (see the note at the
// top of turn_adr057_test.go): its K lower bound needed counts from
// steering.go's role-B predicates (U8, Wave E), subturn.go's pre-arm/payload
// sites (U7/U15, Wave F) and the WS-stamping audit (U9, Wave E) — none of
// which existed when U3 landed in Wave D. U19 is the last behaviour unit
// (Wave G), so K is now final. Per binding Rule 5 this is a NEW file; per
// binding Rule 6 its unexported helpers are prefixed u19.
//
// FR-014: "routingSessionID MUST NOT be read outside the closed consumer set
// (WS payload stamping, the seven role-B predicates, pre-arm keys), and a
// test MUST fail the build on any read outside it."
//
// "Read" is defined normatively in the spec's "Three reads, five sites"
// section: an AST SelectorExpr whose selector identifier is the field name
// ("routingSessionID"), appearing in non-test Go source, EXCLUDING the
// field's own declaration (not a SelectorExpr at all — struct field decls
// are *ast.Field, never *ast.SelectorExpr, so no special-case is needed) and
// EXCLUDING the left-hand side of an assignment to it. Comments, string
// literals, and calls that merely take the value are not reads — an AST
// parse already excludes comments/strings from producing SelectorExpr nodes
// at all, so this file's enumerator only needs to implement the
// assignment-LHS exclusion explicitly.
//
// --- K, derived by direct verification against this tree (2026-08) ---
//
// The spec's own worked total (K=10 = 7 role-B + 3 pre-arm, before the WS
// arm) assumed one AST read per cited role-B "predicate" (FR-015's 7
// citations). Verified against the CURRENT tree, that assumption is false
// for exactly one function: pkg/agent/turn.go's resolveSessionIDByChannelChat
// (:655) contains TWO distinct SelectorExpr reads of routingSessionID — one
// inside its activeTurnStates.Range callback (`sid := ts.routingSessionID`)
// and one in its return statement (`return string(rootTS.routingSessionID)`)
// — both load-bearing (the Range callback captures the field per-candidate
// turn under RLock; the return re-reads it off whichever *turnState the scan
// selected, which is a DIFFERENT turnState value than the one captured
// per-iteration). So the seven role-B PREDICATES (functions) resolve to
// EIGHT role-B READS (AST occurrences) in the current tree — a discrepancy
// this file measures rather than assumes, exactly per binding Rule 4's
// generative spirit ("prove the search is live" applies as much to a count
// that looks settled in prose as to a zero-assertion).
//
// Post-merge addition (2026-08, commit 7f4eab0b): RequestCancel's descendant-
// cancellation cascade gained a fallback, turn.go's claimAnyTurnForSession
// (:819), for the case where the single hook GetActiveTurnHookForSession
// resolved could not be claimed (already fired from an earlier, unrelated
// cancel) while a different, live, never-canceled descendant still shares
// the session — see claimAnyTurnForSession's own doc comment. That fallback
// predicate was originally written pre-identity-split matching
// transcriptSessionID; rebased in the same commit onto routingSessionID
// (post-D1 a delegated child's transcriptSessionID is its own id, so the old
// match could never find the live background/Critical delegate this
// fallback exists for) — "the same rebase every other role-B cancel
// predicate received," per that function's comment. This is the EIGHTH
// distinct role-B predicate FUNCTION (steering.go's 4 + turn.go's original
// 3 + this one), contributing the NINTH read — read count already led
// predicate count by one before this addition, because
// resolveSessionIDByChannelChat alone contributes 2 reads from 1 predicate
// (see above). Adding one more single-read predicate here keeps that same
// one-read lead (8 predicates, 9 reads), making this an intentional,
// reviewed addition to the FR-014 reader set rather than a violation of it.
//
// The four buckets that partition the closed set, each verified by file:line
// below (u19ExpectedRoutingSessionIDReads is the single source of truth a
// reviewer can diff against a future re-derivation):
//
//   - role-B predicates (9): steering.go's 4 post-W13-collapse predicates
//     (collectDescendantTurnIDs, resolveInterruptAnchors,
//     sessionTurnsStillAlive, hasLiveCriticalDelegate — one read each) +
//     turn.go's 4 predicates (GetActiveTurnHookForSession — 1 read;
//     resolveSessionIDByChannelChat — 2 reads; getActiveRootTurnStateForSession
//     — 1 read; claimAnyTurnForSession — 1 read, added 2026-08 per commit
//     7f4eab0b, see above).
//   - pre-arm keys (3): cancel_prearm.go's 2 direct reads + subturn.go's 1
//     (pendingSpawnKeysForThisCall) — FR-016's "three reads across five
//     sites" (the other two sites are transitive: a parameter and a call
//     taking the whole turn state, neither a SelectorExpr on the field).
//   - WS payload stamping (4): loop.go's u9ToolExecSessionIDs (feeds
//     tool_call_start/tool_call_result) + its "done"/TurnEndPayload site,
//     plus subturn.go's SubTurnSpawnPayload.SessionID and
//     SubTurnEndPayload.SessionID sites (FR-017) — every one of these
//     populates a wire-bound payload's SessionID/ProducingSessionID from
//     routingSessionID, the literal act FR-012/FR-017 require. Cross-checked
//     against the W5 audit artefact (pkg/gateway/websocket.go, FR-089) below:
//     that artefact's own text lists exactly the frame types these four
//     sites feed (tool_call_start, tool_call_result, done, subagent_start,
//     subagent_end) among its class-(a)/class-(b) rows — read LIVE from the
//     artefact's source text, never hardcoded, satisfying "#29 MUST read
//     that artefact rather than hardcoding a number."
//   - the FR-011 parent->child inheritance copy (1): subturn.go's
//     `childTS.routingSessionID = parentTS.routingSessionID` — the RHS reads
//     the PARENT's field to inherit it onto the child, which is D2's
//     defining "inherited verbatim" mechanism itself, not a "consumer" in
//     the sense the other three buckets are. FR-014's parenthetical
//     (WS stamping / role-B / pre-arm) does not name this bucket
//     explicitly; it is added here as a fourth, clearly-labelled category
//     because the normative AST-read definition offers no exemption for it
//     (unlike turn.go:406's root-turn establishment, which reads
//     transcriptSessionID, a DIFFERENT field, and so is not a
//     routingSessionID read at all). Flagged in this unit's final report as
//     a judgment call, mirroring how U11 recorded system_overload's
//     "COULD NOT DETERMINE" rather than silently guessing.
//
// K = 9 + 3 + 4 + 1 = 17.
package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// u19RoutingSessionIDRead is one AST-verified read of turnState.routingSessionID.
type u19RoutingSessionIDRead struct {
	file     string
	funcName string
	line     int
}

func (r u19RoutingSessionIDRead) String() string {
	return r.file + ":" + r.funcName + ":" + strconv.Itoa(r.line)
}

// u19FindRoutingSessionIDReads parses filePath and returns every AST
// SelectorExpr read of the routingSessionID field, per the spec's normative
// definition: a SelectorExpr whose selector identifier is exactly
// "routingSessionID", excluding the left-hand side of an assignment TO that
// field. Scans every top-level func/method declaration in the file.
func u19FindRoutingSessionIDReads(t *testing.T, fset *token.FileSet, filePath string) []u19RoutingSessionIDRead {
	t.Helper()
	src, err := os.ReadFile(filePath)
	require.NoErrorf(t, err, "read %s", filePath)
	astFile, err := parser.ParseFile(fset, filePath, src, 0)
	require.NoErrorf(t, err, "parse %s", filePath)

	base := filepath.Base(filePath)
	var reads []u19RoutingSessionIDRead

	for _, decl := range astFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Pass 1: mark the token.Pos of every SelectorExpr that is the
		// assignment TARGET (an Lhs element) of an *ast.AssignStmt — those
		// are writes, not reads, per the normative definition's explicit
		// exclusion.
		excluded := map[token.Pos]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "routingSessionID" {
					excluded[sel.Pos()] = true
				}
			}
			return true
		})

		// Pass 2: collect every remaining SelectorExpr on the field.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "routingSessionID" {
				return true
			}
			if excluded[sel.Pos()] {
				return true
			}
			pos := fset.Position(sel.Pos())
			reads = append(reads, u19RoutingSessionIDRead{file: base, funcName: fn.Name.Name, line: pos.Line})
			return true
		})
	}
	return reads
}

// u19RoutingSessionIDScanFiles is the EXHAUSTIVE non-test file list: every
// non-test .go file anywhere in the module referencing "routingSessionID" as
// of this verification (2026-08), found via
//
//	grep -rl "routingSessionID" --include="*.go" pkg/ | grep -v _test.go
//
// which returns exactly these nine files — all under pkg/agent (the field is
// unexported and never crosses a package boundary). Four of the nine
// (orphan_watch.go, cancel.go, events.go, session_messaging_wire.go) are
// verified below to contribute ZERO actual reads — they only mention the
// identifier in prose — which is itself part of the closure proof: scanning
// them and finding nothing is what rules out a read hiding in a file this
// spec's own "Eight sites"/"Three reads" sections never named.
var u19RoutingSessionIDScanFiles = []string{
	"steering.go",
	"turn.go",
	"cancel_prearm.go",
	"subturn.go",
	"loop.go",
	"orphan_watch.go",
	"cancel.go",
	"events.go",
	"session_messaging_wire.go",
}

// u19RoutingSessionIDBucket classifies one read into its FR-014 (or, for the
// fourth, FR-011) category.
type u19RoutingSessionIDBucket string

const (
	u19BucketRoleB       u19RoutingSessionIDBucket = "role-B predicate"
	u19BucketPreArm      u19RoutingSessionIDBucket = "pre-arm key"
	u19BucketWSStamping  u19RoutingSessionIDBucket = "WS payload stamping"
	u19BucketInheritance u19RoutingSessionIDBucket = "FR-011 inheritance copy"
)

// u19ClassifyRoutingSessionIDRead assigns r to its bucket by (file,
// funcName) — the same predicate/site identity the spec's "Eight sites,
// seven predicates" and "Three reads, five sites" sections use — and fails
// the test immediately if r matches none of them (a read outside the closed
// set is exactly what FR-014 forbids).
func u19ClassifyRoutingSessionIDRead(t *testing.T, r u19RoutingSessionIDRead) u19RoutingSessionIDBucket {
	t.Helper()
	switch r.file {
	case "steering.go":
		switch r.funcName {
		case "collectDescendantTurnIDs", "resolveInterruptAnchors", "sessionTurnsStillAlive", "hasLiveCriticalDelegate":
			return u19BucketRoleB
		}
	case "turn.go":
		switch r.funcName {
		case "GetActiveTurnHookForSession", "resolveSessionIDByChannelChat", "getActiveRootTurnStateForSession":
			return u19BucketRoleB
		case "claimAnyTurnForSession":
			// Added 2026-08 (commit 7f4eab0b): RequestCancel's descendant-
			// cancel fallback, rebased from transcriptSessionID onto
			// routingSessionID — the same role-B cancel-reachability
			// predicate class as the other three turn.go entries above. See
			// the file header's "Post-merge addition" note and
			// claimAnyTurnForSession's own doc comment (turn.go:~776) for
			// the full justification.
			return u19BucketRoleB
		}
	case "cancel_prearm.go":
		// FR-016's two direct citations both live in the same pre-arm
		// key-building function.
		return u19BucketPreArm
	case "loop.go":
		// loop.go's two sites are u9ToolExecSessionIDs (feeds
		// tool_call_start/tool_call_result) and TurnEndPayload's SessionID
		// stamp (inside runTurn's deferred EventKindTurnEnd emission — an
		// anonymous func literal, whose ast.FuncDecl-based funcName here is
		// "runTurn", the enclosing named declaration). Every routingSessionID
		// read this test finds in loop.go is a WS-payload-stamping site by
		// construction — there is no pre-arm/role-B site in this file — so
		// no funcName disambiguation is needed.
		return u19BucketWSStamping
	case "subturn.go":
		switch r.funcName {
		case "spawnSubTurn":
			// spawnSubTurn hosts all four of subturn.go's sites (pre-arm, the
			// FR-011 inheritance copy, and both WS-payload stamps).
			//
			// Disambiguated by an INTENT MARKER on the source line, not by
			// line number. The line-number form broke twice on a single branch
			// (once when a ~48-line transcript-persistence block landed ahead
			// of three sites, once when a 5-line store fix moved the pre-arm
			// read), and each repair was "append the new number" — which
			// confirms the number, never the invariant the test exists to
			// protect. A marker moves with the line it annotates, so a refactor
			// that RELOCATES a read stays green while one that ADDS a read
			// still fails closed, which is FR-014's actual requirement.
			marker := u19MarkerFor(t, r)
			switch marker {
			case "pre-arm":
				return u19BucketPreArm
			case "inheritance":
				return u19BucketInheritance
			case "ws-stamping":
				return u19BucketWSStamping
			}
		}
	}
	t.Fatalf("routingSessionID read %s falls OUTSIDE the FR-014 closed consumer set. "+
		"If this is a NEW consumer, FR-014 forbids it — justify it against ADR-057 and add it to a "+
		"bucket deliberately. If it is an EXISTING read you relocated or reworded, give the line its "+
		"intent marker (`// u19:pre-arm`, `// u19:inheritance`, `// u19:ws-stamping`) so it classifies "+
		"by intent rather than position", r.String())
	return ""
}

// u19MarkerFor returns the `// u19:<intent>` marker annotating the source line
// of read r, or fails the test if the line carries none.
//
// Why a marker and not a line number: the reads inside spawnSubTurn cannot be
// told apart by file or enclosing function — all four share both — so
// SOMETHING per-site is required. A line number is the one choice that changes
// every time anything above it changes, which is why the previous form needed
// re-pinning twice on a single branch. The marker is attached to the statement
// itself, so it survives relocation and only needs touching when a read is
// genuinely added, moved between buckets, or removed.
func u19MarkerFor(t *testing.T, r u19RoutingSessionIDRead) string {
	t.Helper()
	path := filepath.Join(".", r.file)
	src, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read %s to resolve the intent marker for %s", path, r.String())
	lines := strings.Split(string(src), "\n")
	require.Greaterf(t, len(lines), r.line-1,
		"%s reports line %d but the file has only %d lines", r.file, r.line, len(lines))

	line := lines[r.line-1]
	idx := strings.Index(line, "// u19:")
	require.GreaterOrEqualf(t, idx, 0,
		"routingSessionID read %s carries no `// u19:<intent>` marker. Every read inside "+
			"spawnSubTurn must declare which FR-014 bucket it belongs to on the line itself:\n  %s",
		r.String(), strings.TrimSpace(line))

	marker := strings.TrimSpace(line[idx+len("// u19:"):])
	// Tolerate a trailing comment continuation, e.g. `// u19:pre-arm (see …)`.
	if sp := strings.IndexAny(marker, " \t"); sp >= 0 {
		marker = marker[:sp]
	}
	return marker
}

// u19CountClassAInWS5Artefact reads pkg/gateway/websocket.go's own FR-089 W5
// audit classification comment (anchored at "ADR-057 FR-089 — W5 audit
// classification artefact") and counts its "class (a)" occurrences — a LIVE
// read of the committed artefact, never a hardcoded number, per "#29 MUST
// read that artefact rather than hardcoding a number." Used only as a
// cross-check that the WS-stamping bucket's size is grounded in a real,
// committed classification and not an arbitrary constant this test invented.
func u19CountClassAInWS5Artefact(t *testing.T) int {
	t.Helper()
	path := filepath.Join("..", "gateway", "websocket.go")
	src, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read %s (W5 audit artefact host file)", path)
	text := string(src)

	anchor := "ADR-057 FR-089 — W5 audit classification artefact"
	anchorIdx := strings.Index(text, anchor)
	require.Greaterf(t, anchorIdx, -1, "did not locate the W5 audit artefact anchor %q in %s — "+
		"the artefact this test reads may have moved or been renamed", anchor, path)

	// The artefact is a single contiguous comment block; bound the scan at
	// the first non-comment (code) line after the anchor so a later,
	// unrelated "class (a)" mention elsewhere in the file is never counted.
	rest := text[anchorIdx:]
	endIdx := strings.Index(rest, "\nfunc ")
	require.Greaterf(t, endIdx, -1, "could not find the end of the W5 audit artefact comment block in %s", path)
	block := rest[:endIdx]

	return strings.Count(block, "class (a)")
}

// TestRoutingSessionID_ConsumerSetIsClosed is test #29 (BDD-17, BDD-97,
// FR-014). It enumerates every AST read of routingSessionID across the
// EXHAUSTIVE non-test file list above, asserts a positive lower bound
// (binding Rule 4 — proving the search is live) BEFORE asserting closure,
// then asserts every read classifies into one of the four named buckets
// with the exact expected per-bucket count, and finally asserts the grand
// total is exactly K=17 (9 role-B + 3 pre-arm + 4 WS-stamping + 1
// inheritance-copy) — none outside the set, none silently missing.
func TestRoutingSessionID_ConsumerSetIsClosed(t *testing.T) {
	fset := token.NewFileSet()
	agentDir := "."

	perFile := make([][]u19RoutingSessionIDRead, 0, len(u19RoutingSessionIDScanFiles))
	total := 0
	for _, f := range u19RoutingSessionIDScanFiles {
		reads := u19FindRoutingSessionIDReads(t, fset, filepath.Join(agentDir, f))
		perFile = append(perFile, reads)
		total += len(reads)
	}
	all := make([]u19RoutingSessionIDRead, 0, total)
	for _, reads := range perFile {
		all = append(all, reads...)
	}

	// --- Rule 4: prove the search is live BEFORE the closure assertion. ---
	// The spec's own pre-artifact worked bound (7 role-B + 3 pre-arm = 10)
	// is the floor a stale/broken enumerator would fail to clear.
	require.GreaterOrEqualf(t, len(all), 10,
		"enumerated only %d routingSessionID reads across %v — must be >= 10 (7 role-B + 3 pre-arm, "+
			"the spec's own pre-artifact worked bound); fewer than that means the AST search itself is "+
			"broken (wrong field name, wrong file paths, or a parser returning no nodes), not that the "+
			"field genuinely has fewer consumers", len(all), u19RoutingSessionIDScanFiles)

	// --- Classify every read; a read outside the set fails immediately
	// inside u19ClassifyRoutingSessionIDRead. ---
	counts := map[u19RoutingSessionIDBucket]int{}
	byBucket := map[u19RoutingSessionIDBucket][]string{}
	for _, r := range all {
		b := u19ClassifyRoutingSessionIDRead(t, r)
		counts[b]++
		byBucket[b] = append(byBucket[b], r.String())
	}
	for b := range byBucket {
		sort.Strings(byBucket[b])
	}
	t.Logf("routingSessionID reads by bucket: role-B=%v pre-arm=%v WS-stamping=%v FR-011-inheritance=%v",
		byBucket[u19BucketRoleB], byBucket[u19BucketPreArm], byBucket[u19BucketWSStamping], byBucket[u19BucketInheritance])

	// --- Per-bucket exact counts, each independently a positive lower bound
	// (and here also an upper bound, since the set is closed). ---
	if got := counts[u19BucketRoleB]; got != 9 {
		t.Errorf("role-B predicate reads = %d, want 9 (steering.go's 4 post-W13 predicates + "+
			"turn.go's 4 predicates, one of which — resolveSessionIDByChannelChat — contains 2 reads, "+
			"not 1; plus claimAnyTurnForSession, added 2026-08 per commit 7f4eab0b's cancel-fallback "+
			"rebase; see this file's header comment for the verified discrepancy against the spec's "+
			"pre-verification worked total of 7)", got)
	}
	if got := counts[u19BucketPreArm]; got != 3 {
		t.Errorf("pre-arm key reads = %d, want 3 (FR-016's three direct sites: cancel_prearm.go x2, subturn.go x1)", got)
	}
	if got := counts[u19BucketWSStamping]; got != 19 {
		t.Errorf("WS-payload-stamping reads = %d, want 19 (loop.go x17, subturn.go x2: SubTurnSpawnPayload + "+
			"SubTurnEndPayload). loop.go grew from 2 to 13 in the 2026-08 UAT remediation, then to 14 when "+
			"ADR-066 D7 (T066-11) added typedTurnExit's ErrorPayload stamp for the typed turn exits, then to 15 "+
			"when ADR-066 D3 (T066-09) added runTurn's context_window_unknown pre-turn refusal (the same "+
			"ErrorPayload shape as the workspace refusal right above it), then to 16 when ADR-067 FR-016 "+
			"(T067-09) added runTurn's needs_provider pre-turn refusal — the FIRST of the three pre-turn "+
			"gates — then to 17 when ADR-068 FR-015 (T068-12) added runTurn's model_unassigned "+
			"pre-turn refusal, the SECOND of those three, completing the ladder "+
			"(needs_provider → model_unassigned → context_window_unknown). Each emits that same "+
			"ErrorPayload shape: every live "+
			"ErrorPayload and RateLimitPayload emit site now stamps SessionID with routingSessionID, "+
			"because ServeHTTP mints a fresh webchat: uuid per connection — an error carrying only the "+
			"ChatID was dropped by matchesEvent for a second tab or a reload, which is how a provider 429 "+
			"and a workspace refusal both reached the user as silence. These are WS payload stamping by "+
			"definition (the same bucket as TurnEndPayload), so the consumer set is still CLOSED — it is "+
			"wider. A read that is NOT a payload stamp still fails, in the classifier above", got)
	}
	if got := counts[u19BucketInheritance]; got != 1 {
		t.Errorf("FR-011 inheritance-copy reads = %d, want 1 (subturn.go's spawnSubTurn, the "+
			"childTS.routingSessionID = parentTS.routingSessionID assignment)", got)
	}

	// 9 role-B + 3 pre-arm + 19 WS-stamping + 1 inheritance. Was 17 before the
	// 2026-08 UAT remediation widened the WS-stamping bucket (see above), 28
	// before ADR-066 D7's typedTurnExit stamp, 29 before ADR-066 D3's
	// context_window_unknown refusal stamp (T066-09), 30 before ADR-067
	// FR-016's needs_provider refusal stamp (T067-09), and 31 before ADR-068
	// FR-015's model_unassigned refusal stamp (T068-12).
	const wantTotal = 32
	if len(all) != wantTotal {
		t.Fatalf("total routingSessionID reads = %d, want exactly %d (the closed consumer set) — "+
			"either a new read was added outside the four named buckets, or one of the buckets "+
			"undercounted; see the per-bucket breakdown above", len(all), wantTotal)
	}

	// --- Cross-check the WS-stamping bucket against the LIVE W5 audit
	// artefact text (FR-089) rather than a hardcoded number, per "#29 MUST
	// read that artefact rather than hardcoding a number." The artefact's
	// own prose classifies at least the 4 frame types these 4 code sites
	// feed (tool_call_start, tool_call_result, done, subagent_start/end) as
	// class (a) or (b); this only asserts the artefact is present, live,
	// and reports at least as many class-(a) rows as this bucket has
	// distinct payload-construction sites feeding class-(a) frames
	// (tool_call_start/tool_call_result share one site, u9ToolExecSessionIDs,
	// so the bound is >= 1, not >= 4) — proving the cross-check reads real,
	// non-empty committed text rather than silently no-op'ing.
	classACount := u19CountClassAInWS5Artefact(t)
	require.GreaterOrEqualf(t, classACount, 1,
		"the W5 audit artefact (pkg/gateway/websocket.go) reports %d class-(a) frame types — "+
			"expected >= 1; a broken read of the artefact (renamed anchor, moved file) would also "+
			"report 0 here, so this is itself a Rule-4 positive-lower-bound check on the cross-check",
		classACount)
}
