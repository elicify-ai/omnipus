package agent

// effort_red_mapping_d2_test.go — RED tests for the effort mapping table.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: scenarios S-09, S-14, S-48, S-49, S-72, S-73 (test order item
// T-S1), dataset D2 (all 27 rows), FR-046, FR-058, SC-002.
//
// RED reason: pkg/agent/reasoning_effort_map.go::MapEffort is scaffolding and
// returns a sentinel; every mapping assertion fails until GREEN implements
// the DE-5 table.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// d2FixturePath is the ONE shared fixture (Go + TypeScript pin the same file).
const d2FixturePath = "../../src/lib/effort/fixtures/effort-mapping-d2.json"

type d2Target struct {
	Levels  []string `json:"levels"`
	Switch  bool     `json:"switch"`
	Reasons bool     `json:"reasons"`
}

type d2Case struct {
	N                     int       `json:"n"`
	Source                string    `json:"source"`
	Target                d2Target  `json:"target"`
	Value                 string    `json:"value"`
	NoteKind              *string   `json:"noteKind"`
	Blocked               bool      `json:"blocked"`
	Nearest               string    `json:"nearest"`
	ProviderDefaultEscape bool      `json:"providerDefaultEscape"`
	Scenario              string    `json:"scenario,omitempty"`
}

type d2Fixture struct {
	Cases []d2Case `json:"cases"`
}

func loadD2Fixture(t *testing.T) []d2Case {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cleanupCwd(t), d2FixturePath))
	require.NoError(t, err, "shared D2 fixture must exist (Go and TypeScript pin ONE file)")
	var fx d2Fixture
	require.NoError(t, json.Unmarshal(data, &fx), "D2 fixture must parse")
	require.Len(t, fx.Cases, 27, "D2 defines 27 rows (25-27 are the r7 empty-candidate branch)")
	return fx.Cases
}

// cleanupCwd returns the package directory so the relative fixture path works
// regardless of where `go test` is invoked from.
func cleanupCwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}

// TestT_S1_Mapping_D2_Fixture pins every D2 row through MapEffort (SC-002).
// Traces to: D2 rows 1-27; S-09 (rows 1-16), S-14 (rows 20-21), S-73 (row 22),
// S-48/S-49 (stale-level rows are the same table at request time), S-72 (row
// 24), S-09 r7 rows 25-27 (empty candidate, R5-MIN-001).
func TestT_S1_Mapping_D2_Fixture(t *testing.T) {
	cases := loadD2Fixture(t)
	for _, tc := range cases {
		tc := tc
		t.Run(d2CaseName(tc), func(t *testing.T) {
			got := MapEffort(tc.Source, EffortTarget{
				Levels:  tc.Target.Levels,
				Switch:  tc.Target.Switch,
				Reasons: tc.Target.Reasons,
			})
			// SC-002 hard rules first, so a wrong result never hides behind a kind.
			if !tc.Target.Reasons || (len(tc.Target.Levels) == 0 && !tc.Target.Switch) {
				assert.NotEqual(t, "none", got.Value, "non-Off sources never map into none (D2 preamble)")
			}
			if isOffSource(tc.Source) {
				assert.NotContains(t, []string{"low", "medium", "high", "minimal", "xhigh", "max", "on"}, got.Value,
					"Off never maps to a non-Off value (SC-002)")
			}
			if isUnrankedSource(tc.Source) {
				assert.NotEqual(t, "on", got.Value, "an unranked name never maps to On (D2 row 16, S-09)")
			}
			assert.Equal(t, tc.Value, got.Value, "D2 row %d mapped value", tc.N)
			assert.Equal(t, tc.Blocked, got.Blocked, "D2 row %d blocked", tc.N)
			assert.Equal(t, tc.Nearest, got.Nearest, "D2 row %d nearest", tc.N)
			assert.Equal(t, tc.ProviderDefaultEscape, got.ProviderDefaultEscape, "D2 row %d provider-default escape", tc.N)
			wantKind := ""
			if tc.NoteKind != nil {
				wantKind = *tc.NoteKind
			}
			assert.Equal(t, wantKind, got.NoteKind, "D2 row %d note kind", tc.N)
			if wantKind != "" {
				assert.Contains(t, EffortNoteKinds, got.NoteKind, "note kind must be a stable kind")
			}
		})
	}
}

func d2CaseName(tc d2Case) string {
	return strings.TrimSpace(strings.Join([]string{"row", d2Itoa(tc.N), "src=" + d2Label(tc.Source)}, " "))
}

func d2Itoa(n int) string {
	digits := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func d2Label(source string) string {
	if source == "" {
		return "not_set"
	}
	return source
}

func isOffSource(source string) bool { return source == "off" || source == "none" }

func isUnrankedSource(source string) bool {
	_, ranked := effortRank(source)
	return source != "" && source != "off" && source != "none" && source != "on" &&
		source != "provider_default" && !ranked
}

// TestT_S1_Mapping_S09_NoteTexts pins the exact S-09 note strings through the
// note renderer (FR-058: note text is a pure function of the record).
// Traces to: S-09 rows with notes, S-72 ("sent as Medium here"), FR-058.
func TestT_S1_Mapping_S09_NoteTexts(t *testing.T) {
	const model = "deepseek/deepseek-v4.1-flash"
	tests := []struct {
		name     string
		kind     string
		source   string
		Expected string
	}{
		{"mapped level", "mapped", "medium", "Effort set to Low. " + model + " doesn't offer Medium."},
		{"xhigh source", "mapped", "xhigh", "Effort set to High. " + model + " doesn't offer XHigh."},
		{"on source to level", "mapped_on", "on", "Effort set to Medium."},
		{"ranked to switch", "switch_only", "high", "Effort set to On. " + model + " only turns reasoning on or off."},
		{"nothing sendable", "nothing_sendable", "high", "Effort set to Provider default. Omnipus can't change effort for " + model + "."},
		{"unranked default", "unranked_default", "default", "Effort set to Provider default. " + model + " doesn't offer that setting."},
		{"empty candidate", "empty_candidate", "high", "Effort set to Provider default. " + model + " offers no level here that High maps to."},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.Expected, EffortSwitchNote(tc.kind, model, tc.source))
		})
	}
}

// TestT_S1_Mapping_TurnNotes pins the per-turn note texts (FR-058, S-72, S-48
// one-text rule). Traces to: S-72, S-48, S-87, S-91 (one neutral not_supported
// text), FR-058.
func TestT_S1_Mapping_TurnNotes(t *testing.T) {
	const model = "m1"
	tests := []struct {
		name     string
		kind     string
		source   string
		Expected string
	}{
		{"sent as nearest", "mapped", "high", "Sent as Low: " + model + " doesn't offer High."},
		{"not_supported neutral text", "not_supported", "off", "Effort not sent: " + model + " can't turn reasoning off."},
		{"empty candidate at request time", "empty_candidate", "high", "Effort not sent: " + model + " offers no level here that high maps to."},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.Expected, EffortTurnNote(tc.kind, model, tc.source))
		})
	}
}

// TestT_S1_Mapping_S14_Cleared pins S-14 via D2 rows 20-21: a model that does
// not reason clears the chat value and shows no chip side-effect here (the
// chip behaviour itself is a component test).
// Traces to: S-14, D2 rows 20-21.
func TestT_S1_Mapping_S14_Cleared(t *testing.T) {
	for _, src := range []string{"high", "off"} {
		got := MapEffort(src, EffortTarget{Reasons: false})
		assert.Equal(t, "", got.Value, "source %q cleared to Not set when the model does not reason (S-14)", src)
		assert.False(t, got.Blocked, "clearing never blocks (S-14)")
	}
}

// TestT_S1_Mapping_S73_NothingSendable pins S-73 via D2 row 22: Off into a
// nothing-sendable target blocks with the "Let the provider decide" escape.
// Traces to: S-73, D2 row 22.
func TestT_S1_Mapping_S73_NothingSendable(t *testing.T) {
	got := MapEffort("off", EffortTarget{Reasons: true})
	assert.True(t, got.Blocked, "Off into nothing sendable must block (S-73)")
	assert.True(t, got.ProviderDefaultEscape, "the blocked step offers Let the provider decide (S-73, P-Q2 A)")
}

// TestT_S1_Mapping_S48_S49_StaleValues pins the request-time staleness rows:
// a stored Off the model no longer supports (D2 row 8/S-48) blocks at picker
// time and sends nothing at request time; a stored level no longer offered
// maps to the nearest (S-49 = D2 row 5 shape). Traces to: S-48, S-49, D2 rows
// 5, 8.
func TestT_S1_Mapping_S48_S49_StaleValues(t *testing.T) {
	staleOff := MapEffort("off", EffortTarget{Levels: []string{"low", "high"}, Reasons: true})
	assert.True(t, staleOff.Blocked, "Off with no Off on target blocks at the picker (S-10/S-48)")
	assert.Equal(t, "low", staleOff.Nearest, "nearest level marked on the blocked step (S-10)")

	staleMax := MapEffort("max", EffortTarget{Levels: []string{"low", "medium", "high"}, Reasons: true})
	assert.Equal(t, "high", staleMax.Value, "stored max maps to nearest offered high (S-49)")
	assert.Equal(t, "mapped", staleMax.NoteKind, "mapping produces the note (S-49)")
}

// TestT_S1_Mapping_S72_Inherited pins S-72 via D2 row 24: the agent's High
// inherited into a chat model offering low, medium maps to medium.
// Traces to: S-72, D2 row 24.
func TestT_S1_Mapping_S72_Inherited(t *testing.T) {
	got := MapEffort("high", EffortTarget{Levels: []string{"low", "medium"}, Reasons: true})
	assert.Equal(t, "medium", got.Value, "agent's High sent as Medium here (S-72)")
	assert.Equal(t, "mapped", got.NoteKind, "S-72 produces the sent-as note")
}
