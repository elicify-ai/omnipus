// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Every literal-bearing case has an independently required normal refusal.
// allow changes only for the founder-approved envelope; no command is executed.
func t11HereDocLiteralCase(name, cmd string, allow bool) t11HereDocRuleCase {
	return t11HereDocRuleCase{name, cmd, allow, false, "path outside working dir", ""}
}

func t11HereDocProgramCases(absent string) []t11HereDocRuleCase {
	body := "print('" + absent + "/v1/users')\n"
	// Rows below: 13 program forms, 9 shells, 3 wrappers, 5 headers and 1 plain program.
	out := make([]t11HereDocRuleCase, 0, 31)
	programs := []struct{ name, token string }{
		{"program has an argument", "python3 -I"},
		{"single-quoted program", "'python3'"},
		{"double-quoted program", `"python3"`},
		{"escaped program bytes", `pytho\n3`},
		{"globbed program", "py*thon3"},
		{"variable program", "${PYTHON}"},
		{"backtick program", "`printf python3`"},
		{"assignment before program", "MODE=test python3"},
		{"wrapper before plain program", "env python3"},
		{"separator in program position", "true; python3"},
		{"and-list before program", "true && python3"},
		{"pipeline before program", "true | python3"},
		{"another redirection", "python3 >out"},
	}
	for _, p := range programs {
		c := t11HereDocLiteralCase(p.name, p.token+" <<'PY'\n"+body+"PY\n", false)
		if p.name == "backtick program" {
			// Q11 retains the earlier S1 command-position substitution refusal,
			// specified by TestBashSubstitutionGuard_DangerousSubstitutionsBlocked.
			c.normalReason = "command substitution in command position"
		}
		out = append(out, c)
	}
	for _, shell := range []string{"sh", "bash", "dash", "zsh", "ksh", "mksh", "ash", "fish", "eval"} {
		out = append(out, t11HereDocLiteralCase("direct shell "+shell, shell+" <<'PY'\n"+body+"PY\n", false))
	}
	for _, wrapper := range []string{"env bash", "command bash", "nice sh"} {
		out = append(out, t11HereDocLiteralCase("wrapper shell "+wrapper, wrapper+" <<'PY'\n"+body+"PY\n", false))
	}
	valid := "python3 <<'PY'\n" + body + "PY\n"
	for _, c := range []struct{ name, cmd string }{
		{"prefix command before header", "true\n" + valid},
		{"prefix comment before header", "# header\n" + valid},
		{"header comment after marker", "python3 <<'PY' # header\n" + body + "PY\n"},
		{"header separator after marker", "python3 <<'PY';\n" + body + "PY\n"},
		{"two header documents", "python3 <<'PY' <<'NEXT'\n" + body + "PY\nnote\nNEXT\n"},
	} {
		out = append(out, t11HereDocLiteralCase(c.name, c.cmd, false))
	}
	out = append(out, t11HereDocLiteralCase("another plain program token", "node <<'PY'\n"+body+"PY\n", true))
	return out
}

func t11HereDocMarkerCases(absent string) []t11HereDocRuleCase {
	body := "print('" + absent + "/v1/users')\n"
	out := make([]t11HereDocRuleCase, 0, 17) // 3 plain markers and 14 unsupported forms.
	for _, marker := range []string{"A", "P1_", "a9_Z"} {
		out = append(out, t11HereDocLiteralCase("plain ASCII marker "+marker, "python3 <<'"+marker+"'\n"+body+marker+"\n", true))
	}
	for _, c := range []struct{ name, operator, end string }{
		{"unquoted marker", "<<PY", "PY"},
		{"double-quoted marker", `<<"PY"`, "PY"},
		{"partly quoted marker", `<<P"Y"`, "PY"},
		{"escaped marker", `<<\PY`, "PY"},
		{"ANSI-quoted marker", "<<$'PY'", "PY"},
		{"empty marker", "<<''", ""},
		{"marker punctuation hyphen", "<<'P-Y'", "P-Y"},
		{"marker punctuation dot", "<<'P.Y'", "P.Y"},
		{"marker contains space", "<<'P Y'", "P Y"},
		{"marker contains backslash", "<<'PY\\'", "PY\\"},
		{"non-ASCII marker", "<<'Pé'", "Pé"},
		{"marker spans a physical line", "<<'P\nY'", "P\nY"},
		{"tab-stripping operator", "<<-'PY'", "\tPY"},
		{"here-string operator", "<<<'PY'", "PY"},
	} {
		out = append(out, t11HereDocLiteralCase(c.name, "python3 "+c.operator+"\n"+body+c.end+"\n", false))
	}
	return out
}

func t11HereDocBoundaryCases(absent string) []t11HereDocRuleCase {
	body := "print('" + absent + "/v1/users')\n"
	head := "python3 <<'PY'\n"
	valid := head + body + "PY\n"
	out := make([]t11HereDocRuleCase, 0, 25) // 8 matching and 17 nonmatching envelopes.
	for _, c := range []struct{ name, cmd string }{
		{"U-1 dict and print in canonical envelope", head + "d = {\"api\": \"" + absent + "/v1\"}\n" + body + "PY\n"},
		{"canonical final marker without newline", head + body + "PY"},
		{"blank body lines and inert continuation", head + "\n" + body + "value = 1 + \\\n2\n\nPY\n"},
		{"body contains operator-looking text", head + body + "# <<FAKE\n((1<<2))\ncat <<'OTHER'\nPY\n"},
		{"body contains marker prefix", head + "PYmore\n" + body + "PY\n"},
		{"body contains indented marker lookalike", head + " PY\n" + body + "PY\n"},
		{"body contains tabbed marker lookalike", head + "\tPY\n" + body + "PY\n"},
		{"body contains trailing-space marker lookalike", head + "PY \n" + body + "PY\n"},
	} {
		out = append(out, t11HereDocLiteralCase(c.name, c.cmd, true))
	}
	for _, c := range []struct{ name, cmd string }{
		{"missing closing marker", head + body},
		{"only a marker prefix closes", head + body + "PYmore\n"},
		{"only an indented marker closes", head + body + " PY\n"},
		{"only a tabbed marker closes", head + body + "\tPY\n"},
		{"trailing-space marker without exact final closer", head + body + "PY \nnote\n"},
		{"closing marker has separator", head + body + "PY;\n"},
		{"closing marker has comment", head + body + "PY # closer\n"},
		{"closing marker is on body line", head + body + "print('local');PY\n"},
		{"continued closing marker", head + body + "P\\\nY\n"},
		{"command after exact closer", valid + "printf done\n"},
		{"comment after exact closer", valid + "# done\n"},
		{"first exact closer precedes a later closer", valid + "printf done\nPY\n"},
		{"first exact closer precedes the literal", head + "PY\n" + body + "PY\n"},
		{"CRLF header and closing lines", "python3 <<'PY'\r\n" + body + "PY\r\n"},
		{"mixed CR header hides earlier CR closer", "python3 <<'PY'\r\n" + body + "PY\r\nprintf done\nPY\n"},
		{"CR-only line boundaries", "python3 <<'PY'\r" + "print('" + absent + "/v1/users')\rPY"},
		{"non-LF header boundary", "python3 <<'PY'\u0085" + body + "PY\n"},
	} {
		out = append(out, t11HereDocLiteralCase(c.name, c.cmd, false))
	}
	return out
}

func t11HereDocNormalAllowances() []t11HereDocRuleCase {
	// These misses need no stand-alone-literal exception. Q11 is not a new
	// command block list, syntax checker or promise that a command will execute.
	cases := []struct{ name, cmd string }{
		{"normal full comment", "# <<EOF\nprintf ok\n"},
		{"normal trailing comment", "true # <<EOF\nprintf ok\n"},
		{"normal compound arithmetic", "((1<<2))"},
		{"normal quoted operator data", "echo '<<EOF'"},
		{"normal unquoted document", "python3 <<EOF\nprint('local')\nEOF\n"},
		{"normal continued double-quoted marker", "python3 <<\"P\\\nY\"\nprint('local')\nPY\n"},
		{"normal command after document", "python3 <<'PY'\nprint('local')\nPY\nprintf ok\n"},
		{"normal direct-shell document", "sh <<'PY'\nprintf ok\nPY\n"},
		{"normal extra program argument", "python3 -I <<'PY'\nprint('local')\nPY\n"},
	}
	out := make([]t11HereDocRuleCase, 0, len(cases))
	for _, c := range cases {
		out = append(out, t11HereDocRuleCase{c.name, c.cmd, true, true, "", ""})
	}
	return out
}
