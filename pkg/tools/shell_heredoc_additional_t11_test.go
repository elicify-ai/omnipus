// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Q11 reconciliation: case-insensitive shell/eval refusal, quoted operators
// outside the approved envelope, and a program word containing a dot. Losing
// the exception preserves normal decisions; it is not a new command block list.
func t11HereDocAdditionalCases(absent string) []t11HereDocRuleCase {
	path := absent + "/v1/users"
	body := "print('" + path + "')\nPY\n"
	out := make([]t11HereDocRuleCase, 0, 8) // 4 case variants, 3 other refusals and 1 normal allowance.
	for _, shell := range []string{"BASH", "Bash", "ZSH", "EVAL"} {
		out = append(out, t11HereDocRuleCase{
			"case variant shell " + shell, shell + " <<'PY'\n" + body,
			false, false, "path outside working dir", path,
		})
	}
	return append(out,
		t11HereDocRuleCase{"quoted marker trailing space", "python3 <<'PY '\n" + body,
			false, false, "path outside working dir", path},
		t11HereDocRuleCase{"dotted program token", "python3.12 <<'PY'\n" + body,
			false, false, "path outside working dir", path},
		t11HereDocRuleCase{"quoted shift in one-liner with literal", "python3 -c \"print('" + path + "'); print(1<<2)\"",
			false, false, "path outside working dir", path},
		t11HereDocRuleCase{"normal quoted shift one-liner", "python3 -c \"print(1<<2)\"",
			true, true, "", ""},
	)
}
