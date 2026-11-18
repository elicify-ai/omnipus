// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// The 36 R11 proof decisions supply command bytes, never expected decisions.
// Q11 A and FR-2 require these path-bearing shape misses to retain the complete
// normal refusal. No fixture is submitted to a shell or receiving program.
func t11HereDocProofCases() []t11HereDocRuleCase {
	cont := string([]byte{'\\', '\n'})
	brace := "echo {'/tm',}p/x\n"
	continued := "echo \"echo {'/tm',}p/x\" | ba" + cont + "sh\n"
	outside := "echo x >/tmp/t11-proof\n"
	out := make([]t11HereDocRuleCase, 0, 36) // Exactly the commissioned R11 proof pack.
	forms := []struct{ name, head, tail string }{
		{"A-full", "# <<EOF\n", ""},
		{"A-trail", "true # <<EOF\n", ""},
		{"A-real", "python3 <<EOF\n", "EOF\n"},
		{"A-none", "", ""},
	}
	payloads := []struct{ name, body, path string }{
		{"brace", brace, "/tm"},
		{"cont", continued, "/tm"},
		{"outside", outside, "/tmp/t11-proof"},
	}
	for _, f := range forms {
		for _, p := range payloads {
			out = append(out, t11HereDocRuleCase{"proof " + f.name + "-" + p.name,
				f.head + p.body + f.tail, false, false, "path outside working dir", p.path})
		}
	}
	for _, c := range []struct{ name, cmd string }{
		{"B-arith-exp", "echo $((1<<2))\n" + brace},
		{"B-arith-compound", "((1<<2))\n" + brace},
		{"B-ansi-data", "echo $'a<<b'\n" + brace},
		{"B-ansi-delim", "python3 <<$'EOF'\nnote\nEOF\n" + brace},
		{"B-quoted-data", "echo '<<EOF'\n" + brace},
		{"B-escaped", "true \\<\\<EOF\n" + brace},
		{"B-cmdsub", "echo $(: <<EOF\ninner\nEOF\n)\n" + brace},
		{"B-cmdsub-quoted", "echo \"$(printf %s '<<')\"\n" + brace},
		{"C-dq-join", "python3 <<\"EO" + cont + "F\"\nnote\nEOF\n" + brace},
		{"C-dq-control", "python3 <<\"EOF\"\nnote\nEOF\n" + brace},
		{"C-joined-delim", "cat <<EOF\nnote\nEO" + cont + "F\n" + brace},
		{"C-joined-3line", "cat <<EOF\nnote\nE" + cont + "O" + cont + "F\n" + brace},
		{"C-tab-joined", "cat <<-EOF\nnote\n\tEO" + cont + "F\n" + brace},
		{"C-tab-after-pair", "cat <<-EOF\nnote\n" + cont + "\tEOF\n" + brace},
		{"C-interior-tab", "cat <<-EOF\nnote\n\tEO" + cont + "\tF\n" + brace + "\tEOF\n"},
		{"C-even-backslash", "cat <<EOF\nnote\nEO\\\\\nF\n" + brace + "EOF\n"},
		{"C-odd-3", "cat <<EOF\nnote\nEO\\\\\\\nF\n" + brace},
		{"C-phys-after-pair", "cat <<EOF\nnote" + cont + "EOF\n" + brace + "EOF\n"},
		{"C-quoted-sq", "cat <<'EOF'\nnote\nEO" + cont + "F\n" + brace},
		{"C-quoted-bs", "cat <<\\EOF\nnote\nEO" + cont + "F\n" + brace},
		{"C-quoted-part", "cat <<E\"O\"F\nnote\nEO" + cont + "F\n" + brace},
		{"C-pair-in-opword", "cat <<E" + cont + "OF\nnote\nEOF\n" + brace},
		{"C-ansi-delim", "cat <<$'EOF'\nnote\nEOF\n" + brace},
		{"C-cont-after-word", "cat <<EOF " + cont + "\nnote\nEOF\n" + brace},
	} {
		out = append(out, t11HereDocRuleCase{"proof " + c.name, c.cmd, false, false, "path outside working dir", "/tm"})
	}
	return out
}
