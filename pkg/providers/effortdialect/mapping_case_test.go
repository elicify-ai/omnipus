package effortdialect

// mapping_case_test.go — the Go half of the odd-case twin check
// (effort-levels-spec-r10.md, "Mapping table (DE-5)", row "Unranked name";
// SC-002). The TypeScript half is src/lib/effort/mapEffortCase.test.ts; both
// read the one shared fixture below.

import (
	"encoding/json"
	"os"
	"testing"
)

const caseFixturePath = "../../../src/lib/effort/fixtures/effort-mapping-case.json"

type caseFixtureRow struct {
	N      int    `json:"n"`
	Source string `json:"source"`
	Target struct {
		Levels  []string `json:"levels"`
		Switch  bool     `json:"switch"`
		Reasons bool     `json:"reasons"`
	} `json:"target"`
	Value                 string  `json:"value"`
	NoteKind              *string `json:"noteKind"`
	Blocked               bool    `json:"blocked"`
	Nearest               string  `json:"nearest"`
	ProviderDefaultEscape bool    `json:"providerDefaultEscape"`
}

// TestMapEffort_OddCaseIsUnranked pins exact-match level names: "HIGH" and
// "OFF" are unranked names, so they map to Provider default with the
// unranked note (never On, never a ranked level, never Off) and match a
// target level only exactly.
func TestMapEffort_OddCaseIsUnranked(t *testing.T) {
	data, err := os.ReadFile(caseFixturePath)
	if err != nil {
		t.Fatalf("shared odd-case fixture must exist: %v", err)
	}
	var fx struct {
		Cases []caseFixtureRow `json:"cases"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("odd-case fixture must parse: %v", err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("odd-case fixture has no rows")
	}
	for _, tc := range fx.Cases {
		got := MapEffort(tc.Source, Target{Levels: tc.Target.Levels, Switch: tc.Target.Switch, Reasons: tc.Target.Reasons})
		wantKind := ""
		if tc.NoteKind != nil {
			wantKind = *tc.NoteKind
		}
		if got.Value != tc.Value || got.NoteKind != wantKind || got.Blocked != tc.Blocked ||
			got.Nearest != tc.Nearest || got.ProviderDefaultEscape != tc.ProviderDefaultEscape {
			t.Errorf("row %d source %q: got %+v, want value=%q noteKind=%q blocked=%v nearest=%q escape=%v",
				tc.N, tc.Source, got, tc.Value, wantKind, tc.Blocked, tc.Nearest, tc.ProviderDefaultEscape)
		}
	}
	for _, v := range []string{"HIGH", "High", "OFF", "None"} {
		if _, ok := Rank(v); ok {
			t.Errorf("Rank(%q) ranked; level names match exactly", v)
		}
		if IsOff(v) {
			t.Errorf("IsOff(%q) true; Off matches exactly", v)
		}
	}
}
