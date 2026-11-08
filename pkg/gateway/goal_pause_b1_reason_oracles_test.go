package gateway

// PRE-CHANGE oracle data: eight genuine producer inputs. Exact frozen wants.
// Two normalized formatter-only records remain required post-API supplements.

import "strings"

type b1DraftReasonCase struct{ name, input, want string }

func b1DraftReasonCases() []b1DraftReasonCase {
	return []b1DraftReasonCase{
		{"OnePoint", "a", "a"},
		{"159Points", strings.Repeat("a", 159), strings.Repeat("a", 159)},
		{"160PointsUnchanged", strings.Repeat("a", 160), strings.Repeat("a", 160)},
		{"161Points", strings.Repeat("a", 161), strings.Repeat("a", 159) + "…"},
		{"160MultibytePointsUnchanged", strings.Repeat("界", 160), strings.Repeat("界", 160)},
		{"161MultibytePoints", strings.Repeat("界", 161), strings.Repeat("界", 159) + "…"},
		{"CombiningCodePointsNotGraphemes", strings.Repeat("e\u0301", 80) + "x", strings.Repeat("e\u0301", 79) + "e…"},
		{"TrimTruncatedPrefixOnly", strings.Repeat("a", 157) + "  " + "bb", strings.Repeat("a", 157) + "…"},
	}
}
