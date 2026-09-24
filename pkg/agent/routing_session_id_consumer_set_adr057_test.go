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
//     against the W5 audit artefact (pkg/gateway/websocket_forward.go, FR-089) below:
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
//
// ADR-082 amendment (D1, greenfield deletion of the ADR-045
// orphan-foreground-turn watchdog): two of the nine role-B reads named above
// — steering.go's hasLiveCriticalDelegate and turn.go's
// getActiveRootTurnStateForSession — existed solely to answer "is there
// still a genuine foreground turn to reap" for that watchdog (their own doc
// comments said so explicitly), and orphan_watch.go was their sole caller.
// Both are deleted along with the watchdog, dropping the role-B bucket to 7
// reads and the scan file list (below) to eight files. See the assertions'
// own comments for the current, authoritative counts — this paragraph is
// historical context for the original K=17 derivation above, not re-derived
// in place, matching this file's own "Post-merge addition" precedent.
//
// ADR-091 amendment (2026-09-24, "a sub-agent is a session steered by
// another session"): the entire subturn.go ring — subturn.go and its
// 2026-09-15 sibling subturn_result.go — is deleted. Re-derived against the
// CURRENT tree (not by subtracting subturn.go's former per-comment
// contributions from the prior total, per this lane's own instructions):
// the pre-arm bucket drops from 3 to 2 (subturn.go's pendingSpawnKeysForThisCall
// site is gone, no successor); the FR-011 inheritance-copy bucket drops from
// 1 to 0 (subturn.go's childTS.routingSessionID = parentTS.routingSessionID
// assignment is gone — the concept survives via steer_reconstruct.go's
// reconstructSteeredTurn, but as a WRITE sourced from the persisted
// LifecycleRecord's SteeredBy.RootSessionID, never a READ of another
// turnState's live field, so it contributes zero AST reads); and the
// WS-stamping bucket drops from 22 to 18 for TWO separate reasons verified
// independently — subturn.go's own 2 sites (SubTurnSpawnPayload/
// SubTurnEndPayload) are gone with no successor read (the frames survive,
// relocated to steer_frames.go, but now populate SessionID from a
// persisted-record field, not a live routingSessionID read), AND loop.go's
// pre-existing u9ToolExecSessionIDs site independently stopped reading
// routingSessionID at all (see the WS-stamping assertion's own comment
// below for the full explanation) — a change this branch did not make and
// is not attributable to the subturn.go deletion. Role-B (7) and the
// browser-control-gate bucket (1) are unaffected. New total: K = 7 + 2 + 18
// + 0 + 1 = 28. See the assertions' own comments below for the
// authoritative, currently-verified counts — this paragraph, like the
// ADR-082 one above it, is a dated amendment, not a rewrite of the
// historical K=17/K=34 derivations above.
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
// of this verification, found via
//
//	grep -rl "routingSessionID" --include="*.go" pkg/ | grep -v _test.go
//
// all under pkg/agent (the field is unexported and never crosses a package
// boundary — a handful of hits in pkg/session, pkg/tools and
// pkg/tools/browser are prose-only comments naming the field for
// cross-package documentation purposes; Go visibility rules make an actual
// cross-package selector on an unexported field impossible, so those files
// are out of scope and not on this list). cancel.go and events.go are
// verified below to contribute ZERO actual reads — they only mention the
// identifier in prose — which is itself part of the closure proof: scanning
// them and finding nothing is what rules out a read hiding in a file this
// spec's own "Eight sites"/"Three reads" sections never named.
//
// ADR-091 re-derivation (2026-09-24, this delivery): the whole subturn.go
// ring — subturn.go AND its 2026-09-15 identity/result sibling
// subturn_result.go — was deleted (pkg/agent/CLAUDE.md's Delegation
// section: "The subturn.go ring... are gone in the same delivery"), so both
// drop off this list; a worker is now a normal session steered through
// pkg/steer.SessionLauncher, reconstructed by steer_reconstruct.go, which
// picks up a routingSessionID assignment of its own — but ONLY as a write
// (`ts.routingSessionID = session.RoutingSessionID(rec.SteeredBy.RootSessionID)`,
// reading the persisted LifecycleRecord's SteeredBy.RootSessionID, never
// another turnState's live routingSessionID field), so it contributes ZERO
// AST reads under the normative definition (LHS-of-assignment is excluded)
// even though it is now on the grep-seeded list. session_messaging_wire.go
// no longer mentions the identifier at all (the prose that used to name it
// is gone) and so is REMOVED from the list — keeping a file grep no longer
// finds would contradict this list's own stated methodology.
//
// browser_deferral.go was ADDED to this list by wave B123 (ADR-085
// BROWSER-FR-022): its browserRootChatSessionID function reads
// ts.routingSessionID exactly once, to supply the FR-020/FR-021 root-chat
// scope key pkg/tools/browser/tools.go::controlledResult's control-gate
// coverage check needs — a role-B (routing/scope) read, never a persistence
// or addressing identity, classified into its own bucket (u19BucketBrowserGate)
// below rather than folded into an existing one, per FR-022's explicit
// four-part amendment requirement.
var u19RoutingSessionIDScanFiles = []string{
	"steering.go",
	"turn.go",
	"cancel_prearm.go",
	"loop.go",
	"cancel.go",
	"events.go",
	"steer_reconstruct.go",
	"browser_deferral.go",
	// The 2026-09-16 stage-conductor splits (5fb77ec5c for loop.go,
	// 2911aeb85 for external_dispatch.go) moved loop.go's WS-payload
	// stamps into per-stage sibling files — 11 reads left loop.go, every
	// one landing in one of the four loop_run_turn*.go files below,
	// read-preserving.
	//
	// external_dispatch.go is a DIFFERENT, older omission: its two
	// ErrorPayload stamps predate an earlier derivation and the file was
	// simply never on the list at the time — see wantTotal below.
	"external_dispatch.go",
	"loop_run_turn.go",
	"loop_run_turn_iterations.go",
	"loop_run_turn_response.go",
	"loop_run_turn_tools.go",
	// The remaining four mention routingSessionID in prose only (verified
	// 2026-09-24: zero AST reads). Scanning them and finding nothing is
	// itself the closure proof this list exists to make — a read that ever
	// DOES appear there classifies into no bucket and fails closed.
	"loop_policy.go",
	"goal_triggers.go",
	"loop_browser.go",
	"active_turn_info.go",
}

// u19RoutingSessionIDBucket classifies one read into its FR-014 (or, for the
// fourth, FR-011) category.
type u19RoutingSessionIDBucket string

const (
	u19BucketRoleB       u19RoutingSessionIDBucket = "role-B predicate"
	u19BucketPreArm      u19RoutingSessionIDBucket = "pre-arm key"
	u19BucketWSStamping  u19RoutingSessionIDBucket = "WS payload stamping"
	u19BucketInheritance u19RoutingSessionIDBucket = "FR-011 inheritance copy"
	// u19BucketBrowserGate is B123's fifth bucket (ADR-085 BROWSER-FR-022):
	// browser_deferral.go's browserRootChatSessionID reads routingSessionID
	// to supply a SCOPE KEY for the control-gate's FR-020 coverage decision
	// — the same role-B (routing/scope) role the existing role-B predicates
	// play, and never a persistence or addressing identity. Kept as its own
	// bucket rather than folded into u19BucketRoleB because it is a
	// DIFFERENT consumer (the browser control gate, not turn
	// cancellation/reachability) reading for a DIFFERENT purpose, and a
	// reviewer diffing this file wants that distinction visible.
	u19BucketBrowserGate u19RoutingSessionIDBucket = "browser control-gate root-chat key"
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
		case "collectDescendantTurnIDs", "resolveInterruptAnchors", "sessionTurnsStillAlive":
			return u19BucketRoleB
		}
	case "turn.go":
		switch r.funcName {
		case "GetActiveTurnHookForSession", "resolveSessionIDByChannelChat":
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
	case "loop.go", "external_dispatch.go", "loop_run_turn.go",
		"loop_run_turn_iterations.go", "loop_run_turn_response.go",
		"loop_run_turn_tools.go":
		// ADR-091 re-derivation (2026-09-24): loop.go's remaining 5 sites are
		// abortTurn (2: session-restore-failure and hard-abort ErrorPayload
		// stamps), hookAbortError (1), recordRateLimitDenial (1), and
		// runTurn's deferred EventKindTurnEnd emission (1, an anonymous func
		// literal whose ast.FuncDecl-based funcName here is "runTurn", the
		// enclosing named declaration). loop.go's PRE-split u9ToolExecSessionIDs
		// (the site that used to feed tool_call_start/tool_call_result) no
		// longer reads routingSessionID at all — its own doc comment now
		// states outright "routingSessionID is retained only for cascade
		// cancellation and is never a frame destination", and its body
		// returns ts.transcriptSessionID or ts.sessionKey instead. Every
		// error-frame emitter that used to stamp routingSessionID directly
		// (typedTurnExit, and the three pre-turn refusal gates) now funnels
		// through emitTurnErrorFrame → emitErrorEvent → u9ToolExecSessionIDs,
		// so none of them contain a direct routingSessionID read anymore
		// either. This is a REAL, verified behavior change (not a
		// subturn.go-deletion artifact) and is the reason loop.go's count is
		// 5, not the 7 a naive "just drop subturn.go's contribution" delta
		// would predict — flagged here rather than silently absorbed into a
		// bigger number.
		//
		// The 2026-09-16 stage-conductor split moved 11 stamps into the
		// loop_run_turn*.go siblings (runTurn's conductor chain:
		// prepare/finalize/iteration/response/tools stages) — each verified
		// directly (assembleInitialContext x3, resolveWorkspaceAndModel x2,
		// beginIteration x1, finalizeTurn x1 in loop_run_turn.go;
		// handleInitialResponse x1, surfaceEmptyRetryOutcome x1 in
		// loop_run_turn_iterations.go; handleProviderResponse x1 in
		// loop_run_turn_response.go; prepareDispatch x1 in
		// loop_run_turn_tools.go) still stamping an ErrorPayload/RateLimitPayload
		// SessionID directly from ts.routingSessionID, unaffected by either
		// the subturn.go deletion or the u9ToolExecSessionIDs change above.
		// external_dispatch.go carries its own two ErrorPayload stamps
		// (emitExternalCLIErrorEvent, runExternalCLISubTurn) for external-CLI
		// sub-turn exits. There is no pre-arm/role-B site in any of this
		// family, so no funcName disambiguation beyond what's shown above is
		// needed.
		return u19BucketWSStamping
	case "browser_deferral.go":
		// ADR-085 BROWSER-FR-022 (B123): browserRootChatSessionID is
		// browser_deferral.go's ONLY routingSessionID read, and it exists
		// for exactly one purpose — supplying the FR-020/FR-021 root-chat
		// scope key the browser control gate's cross-tab-set coverage
		// check needs. No funcName disambiguation is needed (there is
		// exactly one candidate function in this file).
		if r.funcName == "browserRootChatSessionID" {
			return u19BucketBrowserGate
		}
	}
	t.Fatalf("routingSessionID read %s falls OUTSIDE the FR-014 closed consumer set. "+
		"If this is a NEW consumer, FR-014 forbids it — justify it against ADR-057 and add it to a "+
		"bucket deliberately, updating u19ClassifyRoutingSessionIDRead's switch above.", r.String())
	return ""
}

// u19CountClassAInWS5Artefact reads pkg/gateway/websocket_forward.go's own
// FR-089 W5 audit classification comment (anchored at "ADR-057 FR-089 — W5
// audit classification artefact") and counts its "class (a)" occurrences — a
// LIVE read of the committed artefact, never a hardcoded number, per "#29 MUST
// read that artefact rather than hardcoding a number." Used only as a
// cross-check that the WS-stamping bucket's size is grounded in a real,
// committed classification and not an arbitrary constant this test invented.
// (The artefact originally lived in websocket.go; the gateway's forwarder
// split moved it to websocket_forward.go, host of every frame-construction
// case it classifies.)
func u19CountClassAInWS5Artefact(t *testing.T) int {
	t.Helper()
	path := filepath.Join("..", "gateway", "websocket_forward.go")
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
// then asserts every read classifies into one of the five named buckets
// with the exact expected per-bucket count, and finally asserts the grand
// total is exactly 28 (7 role-B + 2 pre-arm + 18 WS-stamping + 0
// inheritance-copy + 1 browser control-gate — see the ADR-091 amendment in
// this file's header, and each assertion's own comment, for how this
// dropped from the pre-ADR-091 total of 34) — none outside the set, none
// silently missing.
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
	if got := counts[u19BucketRoleB]; got != 7 {
		t.Errorf("role-B predicate reads = %d, want 7 (steering.go's 3 remaining post-ADR-082 "+
			"predicates + turn.go's 3 remaining predicates, one of which — resolveSessionIDByChannelChat "+
			"— contains 2 reads, not 1; plus claimAnyTurnForSession, added 2026-08 per commit "+
			"7f4eab0b's cancel-fallback rebase. ADR-082 D1 deleted the two role-B predicates that "+
			"existed solely for the now-retired orphan-foreground-turn watchdog — dropping this "+
			"bucket by 2 reads from its prior count of 9; see this file's header comment for the "+
			"deleted predicates' names and the original verified discrepancy against the spec's "+
			"pre-verification worked total of 7)", got)
	}
	if got := counts[u19BucketPreArm]; got != 2 {
		t.Errorf("pre-arm key reads = %d, want 2 (FR-016's remaining direct sites: cancel_prearm.go x2. "+
			"ADR-091 deleted subturn.go — and with it the file's former third pre-arm site "+
			"(pendingSpawnKeysForThisCall) — outright, with NO successor read taking its place: the "+
			"steered-child reconstruction path (steer_reconstruct.go) does not build or read a pre-arm "+
			"key from a live turnState at all)", got)
	}
	if got := counts[u19BucketWSStamping]; got != 18 {
		t.Errorf("WS-payload-stamping reads = %d, want 18 (loop.go x5, its four loop_run_turn*.go "+
			"siblings x11, external_dispatch.go x2 — see the classifier's own comment on this bucket "+
			"for the full per-site breakdown). This dropped from a prior verified total of 22 for TWO "+
			"distinct, independently-verified reasons, not one: (1) ADR-091 deleted subturn.go outright, "+
			"removing its 2 WS-stamping sites (SubTurnSpawnPayload.SessionID, SubTurnEndPayload.SessionID) "+
			"with no successor read — the frames themselves SURVIVE (moved to steer_frames.go's "+
			"deliverSubagentStart/deliverSubagentEnd), but now populate SessionID from "+
			"req.SteeringSessionID / rec.SteeredBy.SteeringSessionID (persisted-record fields), never "+
			"from a live turnState.routingSessionID read — so this is a genuine consumer-set shrink, not "+
			"a relocation; and (2) loop.go's own u9ToolExecSessionIDs — historically counted as ONE of "+
			"this bucket's original sites, feeding tool_call_start/tool_call_result — no longer reads "+
			"routingSessionID at all: its doc comment states outright 'routingSessionID is retained only "+
			"for cascade cancellation and is never a frame destination', and every error-frame emitter "+
			"that used to stamp routingSessionID directly (typedTurnExit, and the three pre-turn refusal "+
			"gates: needs_provider/model_unassigned/context_window_unknown) now funnels through that same "+
			"helper. Reason (2) is a real, independently-verified behavior change in the base tree, not an "+
			"ADR-091 subturn.go-deletion artifact — flagged here rather than folded silently into reason "+
			"(1)'s number.", got)
	}
	if got := counts[u19BucketInheritance]; got != 0 {
		t.Errorf("FR-011 inheritance-copy reads = %d, want 0. ADR-091 deleted subturn.go's "+
			"`childTS.routingSessionID = parentTS.routingSessionID` assignment (the sole prior member of "+
			"this bucket) along with the whole file, and verified against the CURRENT tree there is no "+
			"successor READ of this shape anywhere: the routing-session-id inheritance concept itself "+
			"still holds (a steered child's cascade root is still the same identity its steering session "+
			"had), but the MECHANISM changed from a live in-memory field-to-field copy to "+
			"steer_reconstruct.go's `ts.routingSessionID = "+
			"session.RoutingSessionID(rec.SteeredBy.RootSessionID)` — a WRITE sourced from the PERSISTED "+
			"LifecycleRecord's SteeredBy.RootSessionID field, never from another turnState's live "+
			"routingSessionID, so it contributes zero AST reads under this file's normative definition "+
			"(assignment LHS is excluded, and the RHS here reads a DIFFERENT field). The bucket constant "+
			"is kept, at zero, rather than deleted outright: a read that DOES reappear in this shape still "+
			"needs a place to classify into, and 0 is itself a meaningful, verified fact about the current "+
			"tree worth pinning (Rule 4's generative spirit — proving absence, not assuming it).", got)
	}
	if got := counts[u19BucketBrowserGate]; got != 1 {
		t.Errorf("browser control-gate root-chat key reads = %d, want 1 (browser_deferral.go's "+
			"browserRootChatSessionID — ADR-085 BROWSER-FR-022, added by wave B123; unaffected by "+
			"ADR-091)", got)
	}

	// ADR-091 re-derivation (2026-09-24): 7 role-B + 2 pre-arm + 18
	// WS-stamping + 0 inheritance + 1 browser control-gate = 28. Was 34
	// (7 + 3 + 22 + 1 + 1) immediately before this delivery — see the
	// per-bucket comments above for exactly which reads left the set and
	// why (subturn.go's outright deletion: -1 pre-arm, -2 WS-stamping, -1
	// inheritance = -4; plus u9ToolExecSessionIDs's independent, verified
	// behavior change: -2 more WS-stamping = -6 total, 34 - 6 = 28). Every
	// number below this line was re-derived by RUNNING u19FindRoutingSessionIDReads
	// against the current tree (see /tmp scratch verification in this
	// lane's own report), never by subtracting the prior derivation's
	// per-bucket comments from the old total — those comments describe a
	// tree that partly no longer exists.
	//
	// Earlier history (pre-ADR-091, for archaeology only — do not treat as
	// current): was 32 before the 2026-09-16 re-derivation re-attached the
	// 11 reads the stage-conductor splits (5fb77ec5c loop.go, 2911aeb85
	// external_dispatch.go) had moved out of loop.go, and added
	// external_dispatch.go to the scan list at all (its 2 ErrorPayload
	// stamps had existed since e515e5ed9, 2026-08-17, uncounted). Was 31
	// before commit b6ca6055 (2026-09-13) added runTurn's orphan_tool_markup
	// terminal ErrorPayload stamp. Was 30 before wave B123 (ADR-085
	// BROWSER-FR-022) added the browser-gate bucket. Before that: 9 role-B
	// (32 total) before ADR-082 D1 deleted two watchdog-only role-B
	// predicates. Before that: 17 before the 2026-08 UAT remediation
	// widened WS-stamping, 28 before ADR-066 D7's typedTurnExit stamp, 29
	// before ADR-066 D3's context_window_unknown refusal stamp, 30 before
	// ADR-067 FR-016's needs_provider refusal stamp, and 31 before
	// ADR-068 FR-015's model_unassigned refusal stamp.
	const wantTotal = 28
	if len(all) != wantTotal {
		t.Fatalf("total routingSessionID reads = %d, want exactly %d (the closed consumer set) — "+
			"either a new read was added outside the named buckets, or one of the buckets "+
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
		"the W5 audit artefact (pkg/gateway/websocket_forward.go) reports %d class-(a) frame types — "+
			"expected >= 1; a broken read of the artefact (renamed anchor, moved file) would also "+
			"report 0 here, so this is itself a Rule-4 positive-lower-bound check on the cross-check",
		classACount)
}
