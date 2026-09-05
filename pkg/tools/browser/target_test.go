package browser

// Unit tests for the locator contract (spec §10 order 8). No browser: every
// assertion here is about the rules a locator is judged by BEFORE any CDP call
// is issued, which is precisely the property the spec's dataset demands
// ("a validation error naming the field; no CDP call issued").

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLocator_ConflictNamesBothFields(t *testing.T) {
	cases := []struct {
		name     string
		loc      Locator
		mustName []string
	}{
		{
			name:     "css and role",
			loc:      Locator{Selector: "button.confirm", Role: "button"},
			mustName: []string{"selector", "role"},
		},
		{
			name:     "text and name",
			loc:      Locator{Text: "Submit", Name: "Submit"},
			mustName: []string{"text", "name"},
		},
		{
			name:     "css and text and role and name",
			loc:      Locator{Selector: "div", Text: "Go", Role: "button", Name: "Go"},
			mustName: []string{"selector", "text", "role", "name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateLocator("browser_click", tc.loc)
			if err == nil {
				t.Fatalf("two locator kinds must conflict, got no error for %+v", tc.loc)
			}
			var conflict *ErrLocatorConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("want *ErrLocatorConflict, got %T: %v", err, err)
			}
			for _, f := range tc.mustName {
				if !strings.Contains(conflict.Error(), f) {
					t.Errorf("the conflict error must NAME every populated field; %q is missing from %q", f, conflict.Error())
				}
			}
			// It must never pick a winner: no wording that implies one of
			// the two was used.
			for _, banned := range []string{"ignored", "takes precedence", "using selector", "using role"} {
				if strings.Contains(strings.ToLower(conflict.Error()), banned) {
					t.Errorf("a conflict must never imply a winner; error says %q", conflict.Error())
				}
			}
		})
	}
}

// TestLocator_PerToolMatrix_Table asserts spec §3's per-tool matrix ROW BY
// ROW. The matrix is the whole justification for the Locator struct, so it is
// checked directly rather than inferred from a tool passing.
func TestLocator_PerToolMatrix_Table(t *testing.T) {
	type row struct {
		tool     string
		css      bool
		text     bool
		roleName bool
	}
	rows := []row{
		{"browser_click", true, true, true},
		{"browser_get_text", true, true, true},
		{"browser_wait", true, true, true},
		{"browser_hover", true, true, true},
		{"browser_select_option", true, true, true},
		{"browser_upload_file", true, true, true},
		// `text` is the VALUE typed / the key dispatched on these two.
		{"browser_type", true, false, true},
		{"browser_press_key", true, false, true},
	}

	for _, r := range rows {
		t.Run(r.tool, func(t *testing.T) {
			// CSS
			_, err := validateLocator(r.tool, Locator{Selector: "#a"})
			if (err == nil) != r.css {
				t.Errorf("%s: css accepted=%v, want %v (err=%v)", r.tool, err == nil, r.css, err)
			}
			// Text alone
			_, err = validateLocator(r.tool, Locator{Text: "Submit"})
			if (err == nil) != r.text {
				t.Errorf("%s: text accepted=%v, want %v (err=%v)", r.tool, err == nil, r.text, err)
			}
			if !r.text && err != nil {
				var conflict *ErrLocatorConflict
				if !errors.As(err, &conflict) {
					t.Errorf("%s: a rejected text locator must be an *ErrLocatorConflict naming the field, got %T", r.tool, err)
				} else if !strings.Contains(conflict.Error(), "text") {
					t.Errorf("%s: the rejection must name `text`; got %q", r.tool, conflict.Error())
				}
			}
			// Role+name
			_, err = validateLocator(r.tool, Locator{Role: "button", Name: "Submit"})
			if (err == nil) != r.roleName {
				t.Errorf("%s: role+name accepted=%v, want %v (err=%v)", r.tool, err == nil, r.roleName, err)
			}
		})
	}
}

func TestLocator_NoLocatorIsAnError(t *testing.T) {
	if _, err := validateLocator("browser_click", Locator{}); err == nil {
		t.Fatal("an empty locator must be rejected, not treated as 'match anything'")
	}
}

func TestLocator_IndexValidatedBeforeAnyCDPCall(t *testing.T) {
	neg := -1
	zero := 0

	if _, err := validateLocator("browser_click", Locator{Role: "button", Name: "Go", Index: &neg}); err == nil {
		t.Error("index -1 must be rejected")
	} else if !strings.Contains(err.Error(), "index") {
		t.Errorf("the error must name the field; got %q", err.Error())
	}

	// `index` on a CSS/text locator is rejected rather than silently
	// ignored: an agent that passed it believes it disambiguated a
	// multi-match, and a silent drop leaves that belief intact.
	if _, err := validateLocator("browser_click", Locator{Selector: "button", Index: &zero}); err == nil {
		t.Error("index on a CSS locator must be rejected, not silently ignored")
	}

	if _, err := validateLocator("browser_click", Locator{Role: "button", Name: "Go", Index: &zero}); err != nil {
		t.Errorf("index 0 with role+name is legal; got %v", err)
	}
}

// resolveTarget must reject an invalid locator without touching CDP. A
// cancelled context stands in for "there is no browser here": if resolveTarget
// tried to talk to one, this would fail with a context error instead of the
// validation error.
func TestResolveTarget_ValidationHappensBeforeCDP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, cleanup, err := resolveTarget(ctx, "browser_click", Locator{Selector: "#a", Role: "button"}, time.Second)
	cleanup()
	var conflict *ErrLocatorConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("want a locator conflict before any CDP work, got %T: %v", err, err)
	}
}

func TestDisplayRoleName_RendersRoleAndName(t *testing.T) {
	got := displayRoleName(Locator{Role: "button", Name: "Submit"})
	if got != `role=button name="Submit"` {
		t.Errorf("displayRoleName = %q, want %q", got, `role=button name="Submit"`)
	}
}

func TestDescribeFirstCandidates_NamesAtMostThree(t *testing.T) {
	cands := []axCandidate{
		{name: "One", role: "button"},
		{name: "Two", role: "button"},
		{name: "Three", role: "button"},
		{name: "Four", role: "button"},
	}
	got := describeFirstCandidates(cands, 3)
	for _, want := range []string{"One", "Two", "Three"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ambiguity error must name the first three candidates; %q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "Four") {
		t.Errorf("only the first three candidates are named; got %q", got)
	}
}

// readSourceForTest reads a source file relative to this package directory.
// Used by the handful of assertions whose subject is a wiring fact that cannot
// be observed at runtime from inside this package.
func readSourceForTest(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}
