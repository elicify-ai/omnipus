// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package bus

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-126 (TestProgramProvenance_JoinAlgebra — the I-2
// guard), FR-010 (eligibility), FR-041, FR-045 (guard tests must fail on a
// planted violation).
//
// Every expected value derives from the SPEC: provenance is either
// `unresolved` or a non-empty set of origins over {web, independent,
// connector:<id>}; join is the union (never dropping or converting an
// origin); unresolved absorbs; eligibility requires every origin allowed
// (monotone restriction). The in-test negative control plants a join that
// drops an origin and asserts the checker fails it — the FR-045 "planted
// violation" run, done test-side.

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// algebraOrigins is the spec S-126 origin alphabet: {web, independent,
// connector:a, connector:b}.
var algebraOrigins = []Origin{OriginWeb, OriginIndependent, ConnectorOrigin("a"), ConnectorOrigin("b")}

// allProvenances returns the spec's 16 values: the 15 non-empty subsets of the
// origin alphabet plus `unresolved`.
func allProvenances() []Provenance {
	n := len(algebraOrigins)
	out := make([]Provenance, 0, 1<<n) // includes unresolved (zero value) first
	out = append(out, UnresolvedProvenance())
	for mask := 1; mask < 1<<n; mask++ {
		var set []Origin
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				set = append(set, algebraOrigins[i])
			}
		}
		out = append(out, resolvedP(set...))
	}
	return out
}

// allSettings returns the spec's 16 on/off assignments over the four origins'
// settings.
func allSettings() []func(Origin) bool {
	n := len(algebraOrigins)
	out := make([]func(Origin) bool, 0, 1<<n)
	for mask := 0; mask < 1<<n; mask++ {
		allowed := map[Origin]bool{}
		for i, o := range algebraOrigins {
			allowed[o] = mask&(1<<i) != 0
		}
		out = append(out, func(o Origin) bool { return allowed[o] })
	}
	return out
}

// resolvedP constructs a resolved provenance directly. The checker and the
// exact pins need REAL resolved values even while NewProvenance is an empty
// stub (it returns unresolved): Join is the unit under test, not the
// constructor.
func resolvedP(origins ...Origin) Provenance {
	return Provenance{origins: origins}
}

func originsKey(p Provenance) string {
	set := p.Origins()
	sorted := append([]Origin(nil), set...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return fmt.Sprint(sorted)
}

// canonicalKey is the ORDER- AND DUPLICATION-INSENSITIVE set key (CHECK F12):
// the spec defines provenance as a SET of origins — the join's result is
// compared as a set, never as an ordered slice, so an implementation that
// returns origins in any order (or deduplicates on read) satisfies the
// algebra while a dropped, converted or duplicated origin still shows.
func canonicalKey(p Provenance) string {
	if p.IsUnresolved() {
		return "unresolved"
	}
	set := map[Origin]bool{}
	for _, o := range p.Origins() {
		set[o] = true
	}
	sorted := make([]Origin, 0, len(set))
	for o := range set {
		sorted = append(sorted, o)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return fmt.Sprint(sorted)
}

// canonicalUnion is the spec's join definition (I-2): the union of the two
// origin sets, unresolved-absorbing — the checker's independent oracle for
// the exact-union property.
func canonicalUnion(a, b Provenance) string {
	if a.IsUnresolved() || b.IsUnresolved() {
		return "unresolved"
	}
	set := map[Origin]bool{}
	for _, o := range a.Origins() {
		set[o] = true
	}
	for _, o := range b.Origins() {
		set[o] = true
	}
	sorted := make([]Origin, 0, len(set))
	for o := range set {
		sorted = append(sorted, o)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return fmt.Sprint(sorted)
}

// requireSameOriginSet asserts got holds exactly the wanted origins, in any
// order (the spec pins the set, never an order).
func requireSameOriginSet(t *testing.T, want []Origin, got Provenance, msg string) {
	t.Helper()
	require.Equal(t, fmt.Sprint(sortedOrigins(want)), canonicalKey(got), msg)
}

func sortedOrigins(in []Origin) []Origin {
	out := append([]Origin(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// eligibleBySpec is the checker's own spec-derived eligibility (FR-010):
// unresolved is never eligible; otherwise every origin in the set must be
// allowed. The algebra properties are properties of the JOIN function, so the
// checker evaluates eligibility independently of the production Eligible —
// that keeps the instrument check meaningful even while Eligible is a stub.
func eligibleBySpec(p Provenance, allowed func(Origin) bool) bool {
	if p.IsUnresolved() {
		return false
	}
	for _, o := range p.Origins() {
		if !allowed(o) {
			return false
		}
	}
	return true
}

// checkJoinProperties evaluates the I-2 properties for one join function over
// every provenance pair and setting assignment, returning descriptions of
// every violation (empty means the join satisfies the algebra).
func checkJoinProperties(join func(a, b Provenance) Provenance) []string {
	var violations []string
	props := allProvenances()
	settings := allSettings()
	for _, x := range props {
		for _, y := range props {
			j := join(x, y)
			if x.IsUnresolved() || y.IsUnresolved() {
				if !j.IsUnresolved() {
					violations = append(violations, fmt.Sprintf("absorption: join(%s,%s)=%s want unresolved",
						originsKey(x), originsKey(y), originsKey(j)))
				}
				continue // the remaining properties are defined on resolved values
			}
			// The exact union (I-2: "Joining two provenances is the union of
			// their origin sets") — compared as canonical sets, per pair
			// (CHECK F12).
			if canonicalKey(j) != canonicalUnion(x, y) {
				violations = append(violations, fmt.Sprintf("exact union: join(%s,%s)=%s want %s",
					originsKey(x), originsKey(y), canonicalKey(j), canonicalUnion(x, y)))
			}
			if canonicalKey(join(y, x)) != canonicalKey(j) {
				violations = append(violations, fmt.Sprintf("commutativity: join(%s,%s) != join(%s,%s)",
					originsKey(x), originsKey(y), originsKey(y), originsKey(x)))
			}
			if canonicalKey(join(x, j)) != canonicalKey(j) {
				violations = append(violations, fmt.Sprintf("idempotence: join(%s,join(%s,%s)) != join(%s,%s)",
					originsKey(x), originsKey(x), originsKey(y), originsKey(x), originsKey(y)))
			}
			for _, allowed := range settings {
				ej, ex, ey := eligibleBySpec(j, allowed), eligibleBySpec(x, allowed), eligibleBySpec(y, allowed)
				if ej && (!ex || !ey) {
					violations = append(violations,
						fmt.Sprintf("monotone restriction: join(%s,%s) eligible but x=%v y=%v (settings %v)",
							originsKey(x), originsKey(y), ex, ey, settingsKey(allowed)))
				}
			}
		}
	}
	return violations
}

func settingsKey(allowed func(Origin) bool) string {
	out := ""
	for _, o := range algebraOrigins {
		if allowed(o) {
			out += string(o) + ","
		}
	}
	return out
}

// withheldBySpec is the checker's spec-derived set of disallowed origins
// (empty when eligible or unresolved — FR-012 names them in the notice).
func withheldBySpec(p Provenance, allowed func(Origin) bool) []Origin {
	if p.IsUnresolved() {
		return nil
	}
	var out []Origin
	for _, o := range p.Origins() {
		if !allowed(o) {
			out = append(out, o)
		}
	}
	return out
}

func TestProgramProvenance_JoinAlgebra(t *testing.T) {
	props := allProvenances()
	require.Len(t, props, 16, "spec S-126: 15 non-empty origin sets plus unresolved")

	t.Run("exact_values", func(t *testing.T) {
		// Exact-value pins (spec I-2 and the round-3 example), compared as
		// SETS — the spec pins the origin set, never an order (CHECK F12).
		web := resolvedP(OriginWeb)
		connA := resolvedP(ConnectorOrigin("a"))
		// NewProvenance must produce exactly the given set (its own pin):
		requireSameOriginSet(t, []Origin{OriginWeb}, NewProvenance(OriginWeb),
			"NewProvenance(web) must be a resolved single-origin set")
		mixed := Join(web, connA)
		require.False(t, mixed.IsUnresolved(), "joining two resolved values stays resolved")
		requireSameOriginSet(t, []Origin{OriginWeb, ConnectorOrigin("a")}, mixed,
			"join({web},{connector:a}) must be {web, connector:a} — no origin dropped or converted")

		solo := Join(web, web)
		requireSameOriginSet(t, []Origin{OriginWeb}, solo, "join is idempotent on identical sets")

		absorbed := Join(web, UnresolvedProvenance())
		require.True(t, absorbed.IsUnresolved(), "unresolved absorbs (spec I-2)")
		require.Empty(t, absorbed.Origins())

		three := Join(Join(web, connA), resolvedP(OriginIndependent))
		requireSameOriginSet(t, []Origin{OriginIndependent, OriginWeb, ConnectorOrigin("a")}, three,
			"union of three origins keeps all of them")

		// The spec's round-3 example, with a Telegram connection: web chat
		// off and Telegram on joins to {web, connector:telegram}, withheld
		// because web is off — in either order.
		tg := ConnectorOrigin("telegram")
		both := Join(resolvedP(OriginWeb), resolvedP(tg))
		requireSameOriginSet(t, []Origin{OriginWeb, tg}, both, "the example's joined set")
		bothReversed := Join(resolvedP(tg), resolvedP(OriginWeb))
		require.Equal(t, canonicalKey(both), canonicalKey(bothReversed), "join is commutative here")
		webOff := func(o Origin) bool { return o != OriginWeb }
		require.False(t, both.Eligible(webOff),
			"{web, connector:telegram} is withheld when the web-chat setting is off")
		require.Equal(t, []Origin{OriginWeb}, both.WithheldOrigins(webOff),
			"the withheld origin is web, not telegram")
	})

	t.Run("join_with_itself_is_itself_for_every_set", func(t *testing.T) {
		// CHECK F12: join(x,x)==x for all 15 resolved sets (order-insensitive)
		// — the identity the dedup rule rests on.
		for _, x := range allProvenances() {
			if x.IsUnresolved() {
				continue
			}
			require.Equal(t, canonicalKey(x), canonicalKey(Join(x, x)),
				"join(x,x)==x for %s", canonicalKey(x))
		}
	})

	t.Run("eligibility_per_origin", func(t *testing.T) {
		// FR-010: unresolved → withheld; otherwise every origin must be
		// allowed. D-5 rows 1–5 pin the per-origin decisions.
		webOn := func(o Origin) bool { return o == OriginWeb || o == OriginIndependent }
		tgOn := func(o Origin) bool {
			if o == ConnectorOrigin("telegram") {
				return true
			}
			return o == OriginWeb || o == OriginIndependent
		}
		require.False(t, UnresolvedProvenance().Eligible(webOn), "unresolved is never eligible")
		require.True(t, resolvedP(OriginWeb).Eligible(webOn), "web allowed → given (D-5 row 1)")
		require.False(t, resolvedP(OriginWeb).Eligible(func(o Origin) bool { return false }),
			"web off → withheld (D-5 row 2)")
		require.False(t, resolvedP(ConnectorOrigin("telegram")).Eligible(webOn),
			"telegram unset → withheld (D-5 row 3)")
		require.True(t, resolvedP(ConnectorOrigin("telegram")).Eligible(tgOn),
			"telegram allowed → given (D-5 row 4)")
		mixed := resolvedP(ConnectorOrigin("telegram"), ConnectorOrigin("discord"))
		require.False(t, mixed.Eligible(tgOn), "one disallowed connection withholds (D-5 row 5)")
		require.Equal(t, []Origin{ConnectorOrigin("discord")}, mixed.WithheldOrigins(tgOn),
			"the notice names discord")
	})

	t.Run("algebra_over_every_pair_and_setting", func(t *testing.T) {
		// Spec S-126: eligible(join(x,y)) ⇒ eligible(x) ∧ eligible(y);
		// join commutative, associative, idempotent; unresolved absorbs.
		// NOTE (RED): the stubbed Join is the constant unresolved value,
		// which satisfies the algebra vacuously — this subtest is the
		// GREEN-time guard; the exact pins above carry today's RED.
		associativeViolations := checkAssociativity()
		require.Empty(t, associativeViolations, "associativity violations: %v", associativeViolations)
		require.Empty(t, checkJoinProperties(Join),
			"the real Join must satisfy the I-2 algebra over all 16 provenances and 16 settings")
	})

	t.Run("eligible_matches_the_spec_oracle", func(t *testing.T) {
		// FR-010 oracle: the production Eligible must agree with the
		// spec-derived eligibility for every provenance and setting.
		for _, p := range allProvenances() {
			for _, allowed := range allSettings() {
				require.Equal(t, eligibleBySpec(p, allowed), p.Eligible(allowed),
					"Eligible disagrees with FR-010 for %s (settings %v)", originsKey(p), settingsKey(allowed))
				require.Equal(t, withheldBySpec(p, allowed), p.WithheldOrigins(allowed),
					"WithheldOrigins disagrees with FR-010 for %s (settings %v)", originsKey(p), settingsKey(allowed))
			}
		}
	})

	t.Run("planted_dropping_join_fails_the_checker", func(t *testing.T) {
		// Instrument check (spec S-126): a planted join that maps
		// {web} ∪ {connector:a} to {connector:a} must FAIL the checker.
		droppingJoin := func(a, b Provenance) Provenance {
			if a.IsUnresolved() || b.IsUnresolved() {
				return UnresolvedProvenance()
			}
			set := map[Origin]bool{}
			for _, o := range a.Origins() {
				set[o] = true
			}
			// The planted fault: web is dropped whenever a connector origin
			// is present (origin dropped = non-monotone restriction).
			hasConnector := false
			for _, o := range b.Origins() {
				if IsConnectorOrigin(o) {
					hasConnector = true
				}
				set[o] = true
			}
			if hasConnector {
				delete(set, OriginWeb)
			}
			keys := make([]Origin, 0, len(set))
			for o := range set {
				keys = append(keys, o)
			}
			if len(keys) == 0 {
				return UnresolvedProvenance()
			}
			return Provenance{origins: keys} // constructed directly for the plant
		}
		violations := checkJoinProperties(droppingJoin)
		require.NotEmpty(t, violations,
			"instrument check: the planted origin-dropping join MUST be caught by the algebra checker")
	})
}

// checkAssociativity evaluates join((x,y),z) == join(x,(y,z)) for every
// triple of resolved provenances.
func checkAssociativity() []string {
	var violations []string
	props := allProvenances()
	for _, x := range props {
		for _, y := range props {
			for _, z := range props {
				if x.IsUnresolved() || y.IsUnresolved() || z.IsUnresolved() {
					continue
				}
				left := Join(Join(x, y), z)
				right := Join(x, Join(y, z))
				if originsKey(left) != originsKey(right) {
					violations = append(violations, fmt.Sprintf(
						"associativity: join(join(%s,%s),%s)=%s != join(%s,join(%s,%s))=%s",
						originsKey(x), originsKey(y), originsKey(z), originsKey(left),
						originsKey(x), originsKey(y), originsKey(z), originsKey(right)))
				}
			}
		}
	}
	return violations
}
