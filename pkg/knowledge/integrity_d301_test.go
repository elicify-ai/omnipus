package knowledge

import "testing"

// D3-01: above the retention cap, WHICH findings survive must be deterministic
// (the smallest-N by (Path,Detail)), regardless of the order the store emits
// them — otherwise the stateless cursor pages a different subset on a re-run.
func TestFindingSink_RetainedSubsetIsDeterministicAboveCap(t *testing.T) {
	cat := IntegrityCategories[0]
	// 8 findings, cap of 3 → the retained set must be the 3 smallest paths
	// (p0,p1,p2) no matter the insertion order.
	paths := []string{"p5", "p2", "p7", "p0", "p3", "p1", "p6", "p4"}
	run := func(order []string) []string {
		s := newFindingSink(3)
		for _, p := range order {
			s.add(cat, p, "d")
		}
		var got []string
		for _, r := range s.results() {
			if r.Category == cat {
				for _, f := range r.Findings {
					got = append(got, f.Path)
				}
			}
		}
		return got
	}
	forward := run(paths)
	rev := make([]string, len(paths))
	for i, p := range paths {
		rev[len(paths)-1-i] = p
	}
	reversed := run(rev)
	want := []string{"p0", "p1", "p2"}
	for _, got := range [][]string{forward, reversed} {
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("retained subset not the deterministic smallest-3: got %v want %v", got, want)
		}
	}
	// Total still counts every finding even though only 3 are retained.
	s := newFindingSink(3)
	for _, p := range paths {
		s.add(cat, p, "d")
	}
	for _, r := range s.results() {
		if r.Category == cat && r.Total != len(paths) {
			t.Fatalf("Total=%d, want %d", r.Total, len(paths))
		}
	}
}
