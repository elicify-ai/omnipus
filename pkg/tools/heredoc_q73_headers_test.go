package tools

import "testing"

// Oracle: founder Q73 A permits exactly PROGRAM [single " -"] <<'MARKER',
// with one space before the optional dash, no shell/eval and an exact closer.
// This pure parser test has no filesystem, writable-root or host prerequisites.
func TestT11Q73_PlainHereDocumentHeaders(t *testing.T) {
	cases := []struct {
		name   string
		header string
		marker string
		want   bool
	}{
		{"plain_program", "python3 <<'PY'", "PY", true},
		{"stdin_dash", "python3 - <<'PY'", "PY", true},
		{"dash_attached_to_program", "python3- <<'X'", "X", false},
		{"tab_stripping", "python3 <<-'X'", "X", false},
		{"stdin_dash_tab_stripping", "python3 - <<-'X'", "X", false},
		{"dash_then_double_dash", "python3 - -- <<'X'", "X", false},
		{"two_dash_arguments", "python3 - - <<'X'", "X", false},
		{"two_spaces_before_dash", "python3  - <<'X'", "X", false},
		{"shell_stdin_dash", "bash - <<'X'", "X", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := tc.header + "\npayload = {\"api\": \"/api/v1\"}\n" + tc.marker
			if got := isPlainHereDocument(command); got != tc.want {
				t.Fatalf("Q73 exact header %q: isPlainHereDocument=%t, want %t", tc.header, got, tc.want)
			}
		})
	}
}
