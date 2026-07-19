// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// This file is the RRULE expansion library for the Calendar Recurrence
// Redesign (docs/internal/specs/calendar-recurrence-redesign-spec.md). It
// owns:
//   - Occurrence expansion within a range (§"Occurrence expansion endpoint").
//   - Next-fire computation for the scheduler (§"Scheduler").
//   - The bounded validation-support expansion + liveness probe used by
//     ValidateTrigger's RRULE path (§"Validation").
//   - The every_ms forward-only display projection (FR-008a).
//   - Regularity classification + O(1) arithmetic derivation for
//     provably-regular rules (no BY* modifiers), required so a plain
//     FREQ=MINUTELY rule renders complete buckets without iteration.
//
// It does NOT do cron. Legacy `cron_expr` triggers are expanded by the REST
// endpoint directly via gronx (server-zone iteration, D8/Timezone Semantics
// §2) — this file has no cron dependency at all.
//
// DST policy (Timezone Semantics §3, normative): occurrences are wall-clock
// in the rule's IANA tz.
//   - A nonexistent local time (spring-forward gap) resolves to
//     wall-clock + gap length (02:30 -> 03:30 across a 1h gap). Empirically,
//     Go's time.Date already does this by construction (verified in this
//     package's tests) — but that behavior is not a documented guarantee of
//     the time package, so resolveWallClock (below) re-derives and pins it
//     explicitly rather than depending on the stdlib's current internals.
//   - An ambiguous local time (fall-back) resolves to the FIRST
//     (pre-transition, earlier) occurrence. This one DOES need explicit
//     correction: Go's time.Date resolves ambiguous wall-clock times to the
//     SECOND (post-transition) instant (verified empirically), which is the
//     opposite of policy. resolveWallClock corrects this using
//     Time.ZoneBounds (Go 1.19+) to find the offset in effect just before
//     the current zone's transition and picks the earlier candidate when it
//     reproduces the same wall clock.
//   - Two occurrences that normalize to the same instant (a DST collision,
//     e.g. BYHOUR=2,3;BYMINUTE=30 on a spring-forward day, or any rule whose
//     wall-clock stepping walks straight over a gap — rrule-go generates
//     every occurrence, including HOURLY/MINUTELY/SECONDLY ones, via
//     wall-clock construction, not absolute-duration arithmetic, so this can
//     happen even without an explicit BYHOUR list) collapse to a single
//     fire — every expansion path in this file dedupes adjacent identical
//     instants after normalization.
//
// Aged DTSTART (skip-work bound, spec §"Occurrence expansion endpoint" /
// §"Scheduler" rule 1): reaching a queried range or the next occurrence must
// not require walking every occurrence from DTSTART.
//   - Provably-regular rules (no BY* modifiers, and for MONTHLY/YEARLY only
//     when DTSTART's day/month can't be skipped by a short month or a
//     non-leap year) are computed by pure O(1)-relative-to-history
//     arithmetic: an exponential-then-binary search over the occurrence
//     index k (never a linear walk proportional to elapsed history).
//   - Irregular (BY*-bearing) rules WITHOUT COUNT fast-forward their
//     iteration anchor to the INTERVAL-phase-aligned period containing the
//     target instant before touching rrule-go's iterator, so rrule-go only
//     ever walks a small, bounded number of candidates near the target,
//     never from the original DTSTART.
//   - Irregular rules WITH COUNT walk from the original DTSTART, bounded by
//     the validation-time COUNT cap (<= 100,000, Validation §6) — this is
//     spec-accepted, not a defect.

import (
	"errors"
	"fmt"
	"sort"
	"time"
	_ "time/tzdata" // embed IANA tzdata so tz resolution works on minimal systems (FR-018, Constraint #1)

	rrule "github.com/teambition/rrule-go"
)

// iterationCeiling is a hard defense-in-depth safety valve for the irregular
// (rrule-go iterator-driven) expansion paths — distinct from, and looser
// than, any caller-supplied cap. It exists only to bound the walk-from-
// DTSTART case for COUNT-bearing rules (COUNT is validated <= 100,000,
// Validation §6) and to protect against library surprises; well-behaved
// callers stop long before this via their own cap.
const iterationCeiling = 150000

// ---------------------------------------------------------------------------
// Location & DST wall-clock policy
// ---------------------------------------------------------------------------

// loadTZ resolves an IANA timezone name, wrapping the stdlib error so callers
// get a consistent, wrapped message (the REST seam maps any error from this
// package to 400).
func loadTZ(tz string) (*time.Location, error) {
	if tz == "" {
		return nil, errors.New("rrule: tz is required")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("rrule: unknown timezone %q: %w", tz, err)
	}
	return loc, nil
}

// resolveWallClock resolves a wall-clock date/time in loc to a concrete
// instant per the normative DST policy (Timezone Semantics §3):
//   - normal (unambiguous, existing) time -> the obvious instant.
//   - nonexistent (spring-forward gap) time -> wall-clock shifted forward by
//     the gap length (Go's time.Date already does this; this function
//     re-derives it explicitly rather than depending on that as an implicit
//     stdlib guarantee).
//   - ambiguous (fall-back) time -> the FIRST (pre-transition, earlier)
//     occurrence.
//
// Algorithm: construct Go's default resolution `guess`, then inspect the
// zone boundary at `guess` (Time.ZoneBounds, Go 1.19+). If the offset in
// effect immediately before that boundary, applied to the same wall-clock
// fields, reproduces the identical wall clock AND yields an earlier instant
// than guess, the input was ambiguous and ties to two real instants — return
// the earlier one. Otherwise guess is already correct (either unambiguous,
// or a gap that time.Date has already shifted forward).
func resolveWallClock(y int, mo time.Month, d, h, mi, s int, loc *time.Location) time.Time {
	guess := time.Date(y, mo, d, h, mi, s, 0, loc)

	start, _ := guess.ZoneBounds()
	if start.IsZero() {
		return guess // no bounded transition info (e.g. fixed-offset zones); nothing to disambiguate
	}
	prevInstant := start.Add(-time.Nanosecond)
	_, prevOff := prevInstant.Zone()

	candidate := time.Date(y, mo, d, h, mi, s, 0, time.FixedZone("", prevOff))
	cwc := candidate.In(loc)
	if cwc.Year() == y && cwc.Month() == mo && cwc.Day() == d &&
		cwc.Hour() == h && cwc.Minute() == mi && cwc.Second() == s &&
		candidate.Before(guess) {
		return candidate // ambiguous: two instants share this wall clock; take the earlier (first)
	}
	return guess
}

// normalizeOccurrence re-resolves a raw occurrence already constructed by
// rrule-go (which uses time.Date per candidate, including for
// HOURLY/MINUTELY/SECONDLY frequencies — verified empirically, it is
// wall-clock stepping, not absolute-duration arithmetic) against the DST
// wall-clock policy. A no-op for unambiguous times and for already-shifted
// gap times; corrects ambiguous fall-back times to the first occurrence.
func normalizeOccurrence(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)
	return resolveWallClock(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), lt.Minute(), lt.Second(), loc)
}

// ---------------------------------------------------------------------------
// rrule-go plumbing
// ---------------------------------------------------------------------------

// parseOption parses an RFC 5545 RRULE body (no leading "RRULE:", no DTSTART
// line — TaskTrigger.config.rrule is the bare property list, e.g.
// "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10") and anchors it at dtstartMs
// (an absolute Unix-ms instant, localized to loc).
func parseOption(rruleBody string, dtstartMs int64, loc *time.Location) (*rrule.ROption, error) {
	opt, err := rrule.StrToROptionInLocation(rruleBody, loc)
	if err != nil {
		return nil, fmt.Errorf("rrule: parse %q: %w", rruleBody, err)
	}
	// Truncate the anchor to whole seconds: RFC 5545 has no sub-second grammar,
	// and rrule-go emits occurrences at second precision. Without this, a
	// sub-second dtstart_ms (e.g. an anchor of `new Date()` in the editor) makes
	// the liveness probe (HasOccurrenceWithinYears, whose inclusive lower bound
	// is opt.Dtstart-1ns) miss a rule's only occurrence — which lands at the
	// truncated second, before that bound — while NextOccurrenceAfter (whose
	// arithmetic zeros nanos) finds it, so the validator and scheduler disagree.
	// Truncating here, the single anchor choke point, keeps every function
	// (expand, next, validation, liveness) consistent and RFC-correct.
	opt.Dtstart = time.UnixMilli(dtstartMs).Truncate(time.Second).In(loc)
	return opt, nil
}

// intervalOf returns opt.Interval normalized the same way rrule-go itself
// normalizes it internally (< 1 -> 1), so this package's own arithmetic
// agrees with what NewRRule would build.
func intervalOf(opt *rrule.ROption) int {
	if opt.Interval < 1 {
		return 1
	}
	return opt.Interval
}

// ---------------------------------------------------------------------------
// Regularity classification
// ---------------------------------------------------------------------------

// IsRegular reports whether rruleBody is a "provably regular" trigger: a
// fixed-interval rule with no BY* modifiers, for which occurrence k can be
// derived arithmetically (occurrence_k = DTSTART advanced by k*INTERVAL
// periods, wall-clock-safe) rather than iterated. dtstartMs/tz are required
// inputs (not just the rule string) because MONTHLY/YEARLY regularity
// additionally depends on DTSTART's calendar position: "monthly on day 29,
// 30, or 31" and "yearly on Feb 29" both silently changes which months/years
// match plain arithmetic can't safely reproduce (that's the D5/monthly-clamp
// and Feb-29 leap-year cases) — those two frequencies are only regular when
// DTSTART's day/month can't hit that ambiguity. A BY*-free rule string alone
// cannot answer this for those two frequencies.
func IsRegular(rruleBody string, dtstartMs int64, tz string) (bool, error) {
	loc, err := loadTZ(tz)
	if err != nil {
		return false, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return false, err
	}
	if !isStructurallyRegular(opt) {
		return false, nil
	}
	return regularSafeForFreq(opt.Freq, opt.Dtstart), nil
}

// isStructurallyRegular reports whether opt carries no BY* modifiers at all
// (the raw, as-parsed ROption — before rrule-go's own auto-fill-from-DTSTART
// defaulting, which happens only when NewRRule builds the RRule).
func isStructurallyRegular(opt *rrule.ROption) bool {
	return len(opt.Bysetpos) == 0 && len(opt.Bymonth) == 0 && len(opt.Bymonthday) == 0 &&
		len(opt.Byyearday) == 0 && len(opt.Byweekno) == 0 && len(opt.Byweekday) == 0 &&
		len(opt.Byhour) == 0 && len(opt.Byminute) == 0 && len(opt.Bysecond) == 0 &&
		len(opt.Byeaster) == 0
}

// regularSafeForFreq reports whether freq/dtstart's calendar position is
// safe for O(1) calendar arithmetic: YEARLY is unsafe on Feb 29 (fires only
// in leap years — not "add exactly one year"); MONTHLY is unsafe on day 29,
// 30, or 31 (short months would need to skip or clamp, not plain
// arithmetic). Every other combination has no calendar-length ambiguity.
func regularSafeForFreq(freq rrule.Frequency, dtstart time.Time) bool {
	switch freq {
	case rrule.YEARLY:
		return !(dtstart.Month() == time.February && dtstart.Day() == 29)
	case rrule.MONTHLY:
		return dtstart.Day() < 29
	default:
		return true
	}
}

// RegularPeriodMs returns the fixed millisecond spacing between consecutive
// occurrences of a provably-regular rule (see IsRegular), for frequencies
// whose spacing has no calendar-length variability
// (SECONDLY/MINUTELY/HOURLY/DAILY/WEEKLY). ok is false for MONTHLY/YEARLY
// (28-31 day months, 365-366 day years — not a single interval_ms) and for
// irregular rules; the DayBucket.interval_ms wire contract is null in both
// of those cases.
func RegularPeriodMs(rruleBody string, dtstartMs int64, tz string) (periodMs int64, ok bool, err error) {
	loc, err := loadTZ(tz)
	if err != nil {
		return 0, false, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return 0, false, err
	}
	if !isStructurallyRegular(opt) || !regularSafeForFreq(opt.Freq, opt.Dtstart) {
		return 0, false, nil
	}
	interval := int64(intervalOf(opt))
	switch opt.Freq {
	case rrule.SECONDLY:
		return interval * 1000, true, nil
	case rrule.MINUTELY:
		return interval * 60 * 1000, true, nil
	case rrule.HOURLY:
		return interval * 60 * 60 * 1000, true, nil
	case rrule.DAILY:
		return interval * 24 * 60 * 60 * 1000, true, nil
	case rrule.WEEKLY:
		return interval * 7 * 24 * 60 * 60 * 1000, true, nil
	default: // MONTHLY, YEARLY excluded above by regularSafeForFreq's default true — but guard anyway
		return 0, false, nil
	}
}

// ---------------------------------------------------------------------------
// Regular-rule O(1) arithmetic engine
// ---------------------------------------------------------------------------

// advanceByPeriods returns dtstart's wall-clock fields advanced by n whole
// periods of freq, using Go's own field-carry normalization — matching
// rrule-go's own per-candidate construction exactly (rrule-go generates
// every candidate, including HOURLY/MINUTELY/SECONDLY ones, via time.Date
// over wall-clock fields, not time.Duration arithmetic; verified empirically
// in rrule_test.go). The result is raw (pre DST-wall-clock-policy); callers
// apply resolveWallClock.
func advanceByPeriods(dtstart time.Time, loc *time.Location, freq rrule.Frequency, n int) time.Time {
	y, mo, d := dtstart.Date()
	h, mi, s := dtstart.Clock()
	switch freq {
	case rrule.YEARLY:
		return time.Date(y+n, mo, d, h, mi, s, 0, loc)
	case rrule.MONTHLY:
		return time.Date(y, mo+time.Month(n), d, h, mi, s, 0, loc)
	case rrule.WEEKLY:
		return time.Date(y, mo, d+7*n, h, mi, s, 0, loc)
	case rrule.DAILY:
		return time.Date(y, mo, d+n, h, mi, s, 0, loc)
	case rrule.HOURLY:
		return time.Date(y, mo, d, h+n, mi, s, 0, loc)
	case rrule.MINUTELY:
		return time.Date(y, mo, d, h, mi+n, s, 0, loc)
	default: // SECONDLY
		return time.Date(y, mo, d, h, mi, s+n, 0, loc)
	}
}

// regularOccurrenceAt returns the k-th occurrence (0-indexed, k=0 is
// DTSTART) of a provably-regular rule, DST-policy-normalized.
func regularOccurrenceAt(dtstart time.Time, loc *time.Location, freq rrule.Frequency, interval, k int) time.Time {
	raw := advanceByPeriods(dtstart, loc, freq, interval*k)
	return resolveWallClock(raw.Year(), raw.Month(), raw.Day(), raw.Hour(), raw.Minute(), raw.Second(), loc)
}

// seekK returns the smallest k >= lo such that pred(k) holds, given pred is
// monotonic (false...false, true...true) for k >= lo — exponential search to
// find an upper bound, then binary search within it. O(log(k-lo)) calls to
// pred, never a walk proportional to k's absolute magnitude — this is what
// makes the regular-rule arithmetic paths safe against an arbitrarily aged
// DTSTART (years of MINUTELY history is ~20-25 pred evaluations, not
// millions).
func seekK(lo int, pred func(k int) bool) int {
	if pred(lo) {
		return lo
	}
	step := 1
	k := lo
	for !pred(k + step) {
		k += step
		step *= 2
		if step > (1 << 30) { // pathological safety valve; unreachable for any realistic input
			break
		}
	}
	left, right := k, k+step
	for left+1 < right {
		mid := left + (right-left)/2
		if pred(mid) {
			right = mid
		} else {
			left = mid
		}
	}
	return right
}

// regularOccurrencesInRange derives, purely arithmetically (seekK, never a
// walk proportional to elapsed history), the occurrences of a
// provably-regular rule within [fromMs, toMs), capped at cap, with adjacent
// identical instants deduped (the wall-clock stepping that generates
// HOURLY/MINUTELY/SECONDLY candidates can collide across a spring-forward
// gap the same way an explicit BYHOUR list can — see package doc).
func regularOccurrencesInRange(
	dtstart time.Time,
	loc *time.Location,
	freq rrule.Frequency,
	interval, count int,
	until time.Time,
	fromMs, toMs int64,
	limit int,
) ([]int64, bool) {
	occAtOrAfter := func(target int64) int {
		return seekK(0, func(k int) bool {
			if count > 0 && k >= count {
				return true
			}
			return regularOccurrenceAt(dtstart, loc, freq, interval, k).UnixMilli() >= target
		})
	}

	k := occAtOrAfter(fromMs)
	var out []int64
	truncated := false
	var lastMs int64 = -1
	for {
		if count > 0 && k >= count {
			break
		}
		occ := regularOccurrenceAt(dtstart, loc, freq, interval, k)
		if !until.IsZero() && occ.After(until) {
			break
		}
		ms := occ.UnixMilli()
		if ms >= toMs {
			break
		}
		if ms != lastMs {
			out = append(out, ms)
			lastMs = ms
			if len(out) >= limit {
				truncated = true
				break
			}
		}
		k++
	}
	return out, truncated
}

// CountRegularInRange returns the O(1)-arithmetic (never iterated
// proportional to range size or DTSTART age) count of occurrences of a
// provably-regular rule within the half-open [fromMs, toMs) range, plus the
// first in-range occurrence (DayBucket.first_ms). hasAny is false when the
// range contains none (including past COUNT exhaustion or UNTIL).
//
// Known, deliberate limitation: on the (at most 1-2 per year) specific
// calendar day(s) a DST transition falls inside the query range, a
// sub-daily regular rule's count can be off by at most 1 relative to a full
// enumeration, because that day's wall-clock stepping can collapse two
// candidates into one instant (spring-forward) — collapsing that boundary
// case exactly would require iterating the transition day, which conflicts
// with the O(1)-without-iteration mandate (Occurrence expansion endpoint
// §"Caps and work budget"). The error is bounded to the transition day(s)
// only; all other days are exact.
func CountRegularInRange(
	rruleBody string,
	dtstartMs int64,
	tz string,
	fromMs, toMs int64,
) (count int, firstMs int64, hasAny bool, err error) {
	if fromMs >= toMs {
		return 0, 0, false, errors.New("rrule: fromMs must be before toMs")
	}
	loc, err := loadTZ(tz)
	if err != nil {
		return 0, 0, false, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return 0, 0, false, err
	}
	if !isStructurallyRegular(opt) || !regularSafeForFreq(opt.Freq, opt.Dtstart) {
		return 0, 0, false, fmt.Errorf("rrule: %q is not a provably-regular rule", rruleBody)
	}
	interval := intervalOf(opt)
	dtstart := opt.Dtstart
	count0 := opt.Count
	until := opt.Until

	firstK := seekK(0, func(k int) bool {
		if count0 > 0 && k >= count0 {
			return true
		}
		return regularOccurrenceAt(dtstart, loc, opt.Freq, interval, k).UnixMilli() >= fromMs
	})
	if count0 > 0 && firstK >= count0 {
		return 0, 0, false, nil
	}
	first := regularOccurrenceAt(dtstart, loc, opt.Freq, interval, firstK)
	if !until.IsZero() && first.After(until) {
		return 0, 0, false, nil
	}
	if first.UnixMilli() >= toMs {
		return 0, 0, false, nil
	}

	lastKExclusive := seekK(firstK, func(k int) bool {
		if count0 > 0 && k >= count0 {
			return true
		}
		occ := regularOccurrenceAt(dtstart, loc, opt.Freq, interval, k)
		if !until.IsZero() && occ.After(until) {
			return true
		}
		return occ.UnixMilli() >= toMs
	})
	n := lastKExclusive - firstK
	if n <= 0 {
		n = 1
	}
	return n, first.UnixMilli(), true, nil
}

// ---------------------------------------------------------------------------
// Occurrence expansion (irregular / general path via rrule-go)
// ---------------------------------------------------------------------------

// fastForwardAnchor returns a new anchor time for seeding rrule-go's
// iterator, phase-aligned to dtstart's original INTERVAL cycle, at or
// shortly before target. This is an approximation (unlike the exact
// seekK-based regular arithmetic) — it only needs to land rrule-go's
// iterator within a small, bounded number of candidates of target; rrule-go
// itself then finds the exact BY*-filtered occurrences from there. Original
// wall-clock hour/minute/second are preserved exactly (not re-derived from
// the stepped date) so any implicit (no-BYHOUR) time-of-day rrule-go would
// otherwise infer from Dtstart is unaffected by AddDate/time.Date drift on a
// DST-transition day.
func fastForwardAnchor(
	dtstart time.Time,
	loc *time.Location,
	freq rrule.Frequency,
	interval int,
	target time.Time,
) time.Time {
	if interval <= 0 {
		interval = 1
	}
	if !target.After(dtstart) {
		return dtstart
	}
	var periods int
	switch freq {
	case rrule.YEARLY:
		periods = target.Year() - dtstart.Year()
	case rrule.MONTHLY:
		periods = (target.Year()-dtstart.Year())*12 + int(target.Month()-dtstart.Month())
	case rrule.WEEKLY:
		periods = int(target.Sub(dtstart).Hours()/24) / 7
	case rrule.DAILY:
		periods = int(target.Sub(dtstart).Hours() / 24)
	case rrule.HOURLY:
		periods = int(target.Sub(dtstart).Hours())
	case rrule.MINUTELY:
		periods = int(target.Sub(dtstart).Minutes())
	default: // SECONDLY
		periods = int(target.Sub(dtstart).Seconds())
	}
	k := periods / interval
	if k <= 0 {
		return dtstart
	}
	// Safety margin: step back a few extra periods so rrule-go's own
	// iterator (which we hand back to it from here) always starts at or
	// before target, regardless of estimate rounding near month/DST
	// boundaries.
	const slack = 3
	if k > slack {
		k -= slack
	} else {
		k = 0
	}
	if k == 0 {
		return dtstart
	}
	stepped := advanceByPeriods(dtstart, loc, freq, interval*k)
	if freq >= rrule.HOURLY {
		return stepped // duration-based frequency: already precise
	}
	// calendar-based frequency: re-anchor to the ORIGINAL wall-clock h/m/s
	// so implicit (no-BYHOUR) time-of-day derivation is unaffected.
	sy, smo, sd := stepped.Date()
	oh, omi, osec := dtstart.Clock()
	return time.Date(sy, smo, sd, oh, omi, osec, 0, loc)
}

// generateIrregular walks rr's iterator (already seeded at an appropriate
// anchor by the caller), collecting DST-normalized, deduped occurrences
// within [fromMs, toMs), capped at cap, bounded by iterationCeiling as a
// defense-in-depth safety valve.
func generateIrregular(rr *rrule.RRule, loc *time.Location, fromMs, toMs int64, limit int) ([]int64, bool) {
	next := rr.Iterator()
	var out []int64
	truncated := false
	var lastMs int64 = -1
	haveLast := false
	for i := 0; i < iterationCeiling; i++ {
		t, ok := next()
		if !ok {
			break
		}
		norm := normalizeOccurrence(t, loc)
		ms := norm.UnixMilli()
		if ms < fromMs {
			continue
		}
		if ms >= toMs {
			break // ascending iterator: nothing further can be in range
		}
		if haveLast && ms == lastMs {
			continue
		}
		out = append(out, ms)
		lastMs = ms
		haveLast = true
		if len(out) >= limit {
			truncated = true
			break
		}
	}
	return out, truncated
}

// ExpandRRULE expands occurrences of an RFC 5545 RRULE (body only, no
// DTSTART/RRULE: prefix) anchored at dtstartMs in tz, within the half-open
// range [fromMs, toMs), DST-normalized per the Timezone Semantics policy,
// identical-instant-deduped, sorted ascending, stopping at cap occurrences
// with truncated=true. Parse/zone errors are returned (the REST seam maps
// any error from this function to 400). Provably-regular rules (IsRegular)
// are derived by O(1) arithmetic; irregular rules use rrule-go, fast-forward
// seeded per the Aged DTSTART policy.
func ExpandRRULE(rruleBody string, dtstartMs int64, tz string, fromMs, toMs int64, limit int) ([]int64, bool, error) {
	if fromMs >= toMs {
		return nil, false, errors.New("rrule: fromMs must be before toMs")
	}
	if limit <= 0 {
		limit = 1
	}
	loc, err := loadTZ(tz)
	if err != nil {
		return nil, false, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return nil, false, err
	}

	if isStructurallyRegular(opt) && regularSafeForFreq(opt.Freq, opt.Dtstart) {
		out, truncated := regularOccurrencesInRange(
			opt.Dtstart,
			loc,
			opt.Freq,
			intervalOf(opt),
			opt.Count,
			opt.Until,
			fromMs,
			toMs,
			limit,
		)
		return out, truncated, nil
	}

	seedOpt := *opt
	if opt.Count == 0 {
		seedOpt.Dtstart = fastForwardAnchor(opt.Dtstart, loc, opt.Freq, intervalOf(opt), time.UnixMilli(fromMs))
	}
	rr, err := rrule.NewRRule(seedOpt)
	if err != nil {
		return nil, false, fmt.Errorf("rrule: build: %w", err)
	}
	out, truncated := generateIrregular(rr, loc, fromMs, toMs, limit)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, truncated, nil
}

// ---------------------------------------------------------------------------
// Next-occurrence (scheduler)
// ---------------------------------------------------------------------------

// NextOccurrenceAfter returns the first occurrence strictly after afterMs
// (COUNT/UNTIL evaluated statelessly from DTSTART, per Scheduler rule 1 and
// rule 5 "missed occurrences ... COUNT positions consumed by calendar
// time"), ok=false when the series is exhausted (COUNT reached or past
// UNTIL). Regular rules fast-forward arithmetically (O(1) relative to
// elapsed history via seekK); COUNT-free irregular rules seed rrule-go's
// iterator at the FREQ period containing afterMs; COUNT-bearing irregular
// rules walk from DTSTART, bounded by the validated COUNT cap (<= 100,000).
func NextOccurrenceAfter(rruleBody string, dtstartMs int64, tz string, afterMs int64) (int64, bool, error) {
	loc, err := loadTZ(tz)
	if err != nil {
		return 0, false, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return 0, false, err
	}

	if isStructurallyRegular(opt) && regularSafeForFreq(opt.Freq, opt.Dtstart) {
		interval := intervalOf(opt)
		k := seekK(0, func(k int) bool {
			if opt.Count > 0 && k >= opt.Count {
				return true
			}
			return regularOccurrenceAt(opt.Dtstart, loc, opt.Freq, interval, k).UnixMilli() > afterMs
		})
		if opt.Count > 0 && k >= opt.Count {
			return 0, false, nil
		}
		occ := regularOccurrenceAt(opt.Dtstart, loc, opt.Freq, interval, k)
		if !opt.Until.IsZero() && occ.After(opt.Until) {
			return 0, false, nil
		}
		return occ.UnixMilli(), true, nil
	}

	seedOpt := *opt
	if opt.Count == 0 {
		seedOpt.Dtstart = fastForwardAnchor(opt.Dtstart, loc, opt.Freq, intervalOf(opt), time.UnixMilli(afterMs))
	}
	rr, err := rrule.NewRRule(seedOpt)
	if err != nil {
		return 0, false, fmt.Errorf("rrule: build: %w", err)
	}

	afterT := time.UnixMilli(afterMs)
	candidate := rr.After(afterT, false)
	for retries := 0; retries < 4 && !candidate.IsZero(); retries++ {
		norm := normalizeOccurrence(candidate, loc)
		if norm.UnixMilli() > afterMs {
			if !opt.Until.IsZero() && norm.After(opt.Until) {
				return 0, false, nil
			}
			return norm.UnixMilli(), true, nil
		}
		// DST ambiguous-time correction shifted the candidate back to/before
		// afterMs; advance to the raw candidate's successor and retry.
		candidate = rr.After(candidate, false)
	}
	return 0, false, nil
}

// ---------------------------------------------------------------------------
// Validation-support expander + liveness probe
// ---------------------------------------------------------------------------

// ExpandForValidation expands the first min(60 occurrences, 366 days) from
// DTSTART (never fast-forwarded — this is explicitly DTSTART-anchored, used
// for the bounded-window minimum-gap scan, Validation §4), DST-normalized
// and deduped identically to ExpandRRULE (so a DST collision within the
// window is recognized as one occurrence, matching Validation §4's note that
// a surviving rule's occurrences are whole minutes apart, or 0s on a
// dedup-collapsed collision). Bounded and fast (<1s) even for a
// never-matching rule (e.g. Feb 31) — the 366-day cutoff and
// iterationCeiling both bound the walk regardless of rule density.
func ExpandForValidation(rruleBody string, dtstartMs int64, tz string) ([]int64, error) {
	loc, err := loadTZ(tz)
	if err != nil {
		return nil, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return nil, err
	}
	rr, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("rrule: build: %w", err)
	}
	windowEndMs := opt.Dtstart.AddDate(0, 0, 366).UnixMilli()

	next := rr.Iterator()
	var out []int64
	var lastMs int64 = -1
	haveLast := false
	for i := 0; i < iterationCeiling; i++ {
		t, ok := next()
		if !ok {
			break
		}
		norm := normalizeOccurrence(t, loc)
		ms := norm.UnixMilli()
		if ms >= windowEndMs {
			break
		}
		if haveLast && ms == lastMs {
			continue
		}
		out = append(out, ms)
		lastMs = ms
		haveLast = true
		if len(out) >= 60 {
			break
		}
	}
	return out, nil
}

// HasOccurrenceWithinYears is the liveness probe (Validation §5): reports
// whether rruleBody produces at least one occurrence within `years` of
// DTSTART. Uses rrule-go's After() directly (an efficient bounded walk
// internally capped at rrule-go's own MAXYEAR=9999 safety valve), so a
// never-matching rule (e.g. FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31) returns
// false quickly rather than hanging.
func HasOccurrenceWithinYears(rruleBody string, dtstartMs int64, tz string, years int) (bool, error) {
	loc, err := loadTZ(tz)
	if err != nil {
		return false, err
	}
	opt, err := parseOption(rruleBody, dtstartMs, loc)
	if err != nil {
		return false, err
	}
	rr, err := rrule.NewRRule(*opt)
	if err != nil {
		return false, fmt.Errorf("rrule: build: %w", err)
	}
	first := rr.After(opt.Dtstart.Add(-time.Nanosecond), true)
	if first.IsZero() {
		return false, nil
	}
	deadline := opt.Dtstart.AddDate(years, 0, 0)
	return !first.After(deadline), nil
}

// ---------------------------------------------------------------------------
// every_ms display projection (FR-008a)
// ---------------------------------------------------------------------------

// ProjectEveryMs implements the FR-008a forward-only projection for legacy
// `every` triggers, which have no stored anchor (computeNextRun is
// `now + interval`, drift-anchored, pkg/cron/service.go): the first returned
// occurrence is the live job's NextRunAtMS (firstMs, supplied by the
// caller); subsequent occurrences are first + k*everyMs. Occurrences before
// fromMs are omitted (the caller passes max(nowMs, rangeFromMs) as fromMs to
// satisfy "no backward extrapolation"); capped at cap with truncated=true.
func ProjectEveryMs(firstMs, everyMs, fromMs, toMs int64, limit int) ([]int64, bool) {
	if everyMs <= 0 || fromMs >= toMs || limit <= 0 {
		return nil, false
	}
	k := int64(0)
	if firstMs < fromMs {
		k = (fromMs - firstMs + everyMs - 1) / everyMs
	}
	var out []int64
	truncated := false
	for {
		ms := firstMs + k*everyMs
		if ms >= toMs {
			break
		}
		if ms >= fromMs {
			out = append(out, ms)
			if len(out) >= limit {
				truncated = true
				break
			}
		}
		k++
	}
	return out, truncated
}
