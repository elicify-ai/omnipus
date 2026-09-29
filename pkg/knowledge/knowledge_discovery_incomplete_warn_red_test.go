// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-037, TDD Plan
// row 86 (va-qa3 dispatch, P4, team-lead ruling 2026-09-29).
//
// Oracle, verbatim: "Incomplete discovery (an unreadable subtree,
// SkipUnreadable) MUST NOT retire any member of the record — the member is
// kept. ... the kept-member notice is a server Warn log only (naming the
// collection and the skipped subtree), NOT a user-visible warning; the
// unreadable subfolder itself is still reported on every surface under
// FR-VA-025 — that visible report is unchanged. Test 86 (member kept after
// an incomplete walk; the Warn log line is emitted)."
//
// "The record" is the pipeline-owned outside-vault membership record
// (view_membership.json) FR-VA-032/033/038/039 all key off — this is the
// SAME missing record every other P2-P4 file in this dispatch documents
// (confirmed by grep: zero non-generated, non-test hits for
// "ViewMembership"/"view_membership" anywhere). "A member is kept" cannot
// be observed when there is no record to have members in.
//
// A separate, narrower fact was ALSO verified by reading (2026-09-29):
// records.LoadViews (pkg/records/view.go, the current view-discovery
// entry point — there is no records.DiscoverViewFiles symbol anywhere) has
// no Warn-level (or any-level) log call at all on a per-file read error —
// it only appends a ViewRejection to the report. This means even the
// NARROWER "a Warn log line is emitted naming the skipped paths" half of
// test 86 has no log call to capture today, confirmed by grep
// (`grep -rn "slog\." pkg/records/view.go` — zero hits) — the codebase's
// standard logger is log/slog (not zerolog), and the test-capture pattern
// (`slog.SetDefault(slog.New(slog.NewTextHandler(buf, ...)))`, restored via
// `defer slog.SetDefault(slog.New(oldHandler))`) already exists elsewhere
// (e.g. pkg/gateway/rest_onboarding_test.go, pkg/knowledge/watch_test.go)
// and is the pattern a real GREEN implementation's test should follow —
// but there is nothing to capture yet.
//
// BLOCKED per the qa-lead RED protocol: both halves of test 86 (the record
// keeping its member, and the Warn log naming it) depend on machinery that
// does not exist anywhere in the codebase.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestIncompleteDiscovery_KeepsRecordMemberAndLogsWarnNamingSkippedPaths$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

func TestIncompleteDiscovery_KeepsRecordMemberAndLogsWarnNamingSkippedPaths(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-037/test 86 requires (a) a pipeline-owned membership record whose " +
		"'member kept after an incomplete walk' behavior could be observed — no such record exists " +
		"anywhere in the codebase (see this file's header), and (b) a Warn-level log call naming the " +
		"skipped subtree and the retained member — records.LoadViews (pkg/records/view.go) has no " +
		"logger call of any kind on a per-file read error today (confirmed by grep: zero slog hits), " +
		"only a ViewRejection appended to its report. Neither half of test 86 has anything to plant " +
		"a fixture against.")
}
