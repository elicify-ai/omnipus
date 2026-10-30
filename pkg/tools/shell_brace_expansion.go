// shell_brace_expansion.go: decides whether any text the shell will parse in a
// bash command contains an unquoted "{" or "}" — the precondition for shell
// brace expansion. The T11 stand-alone quoted-literal rule (#882) is withheld
// whenever this is true (founder decision 2026-10-30, "simplify the guard"),
// so a quoted fragment can never be glued into a real path by
// `{'/tm',}p/x`-style expansion.
//
// Text the shell parses:
//   - the command itself (quotes tracked by shellQuoteMask; escaped braces are
//     quoted);
//   - the script of a nested shell: `sh|bash|zsh|dash|ksh|mksh|ash|fish -c
//     SCRIPT` anywhere in the command (so `find -exec sh -c`, `xargs sh -c`,
//     `env bash -c` are covered), and the arguments of `eval`;
//   - a here-document body whose command is one of those shells or `eval`.
//
// A here-document body fed to any other program (python3 <<'PY' ...) is data
// and is skipped: the shell never brace-expands here-document bodies.
//
// When the scan cannot be sure where a nested shell's script is, it answers
// "may expand" and the exemption is withheld (T11 round 4, founder: keep it
// simple): text that cannot be parsed (unbalanced quotes, a `$(...)` the
// tokenizer refuses, nesting deeper than maxBraceScanDepth), a long or
// argument-taking option before -c (`bash --norc -c`, `bash -o x -c`), and a
// shell reading its script from standard input (a pipe, a here-string, or a
// here-document behind a wrapper such as env, command or nice). Unquoted
// separators split words even without whitespace (`true;bash -c ...`).
//
// Residual, stated: a non-shell program that hands its own data to a shell
// (Python's os.system, perl system) is not followed; the guard reads command
// text only.

package tools

import (
	"path/filepath"
	"strings"
)

const maxBraceScanDepth = 4

// braceExpandingShells are the interpreters whose script text is shell code
// that can brace-expand.
var braceExpandingShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"mksh": true, "ash": true, "fish": true,
}

// commandMayBraceExpand reports whether cmd, or any shell text nested in it,
// contains an unquoted brace.
func commandMayBraceExpand(cmd string) bool {
	return mayBraceExpand(cmd, 0)
}

func mayBraceExpand(cmd string, depth int) bool {
	if depth > maxBraceScanDepth {
		return true
	}
	mask, balanced := shellQuoteMask(cmd)
	if !balanced {
		return true
	}
	bodies, ok := hereDocBodies(cmd, mask)
	if !ok {
		return true
	}
	inBody := make([]bool, len(cmd))
	var outside strings.Builder
	for _, b := range bodies {
		for i := b.start; i < b.end; i++ {
			inBody[i] = true
		}
		if b.shell && mayBraceExpand(cmd[b.start:b.end], depth+1) {
			return true
		}
	}
	for i := 0; i < len(cmd); i++ {
		if inBody[i] {
			continue
		}
		if (cmd[i] == '{' || cmd[i] == '}') && !mask[i] {
			return true
		}
		// An unquoted command separator splits words even without
		// whitespace (`true;bash -c ...`), so it becomes a space here.
		if !mask[i] && strings.IndexByte(";&|()\n", cmd[i]) >= 0 {
			outside.WriteByte(' ')
			continue
		}
		outside.WriteByte(cmd[i])
	}
	words, ok := tokenizeShellWords(outside.String())
	if !ok {
		return true
	}
	for i, w := range words {
		if w == "eval" && i+1 < len(words) {
			if mayBraceExpand(strings.Join(words[i+1:], " "), depth+1) {
				return true
			}
			continue
		}
		if braceExpandingShells[filepath.Base(w)] && nestedShellMayBraceExpand(words[i+1:], depth) {
			return true
		}
	}
	return false
}

// nestedShellMayBraceExpand judges the words after a shell name. It follows
// the rule "cannot be sure, withhold" (T11 round 4): only plain short options
// (letters, none taking an argument) and the known argument-less long
// options (knownNoArgLongShellOptions, e.g. --norc, --login) before the
// script are understood, so the script can be located with certainty. Any
// other long option (--rcfile FILE, an unknown one), a +option, an option
// that takes an argument (-o, -O), or a shell that reads its script from
// standard input
// (no -c and no script-file argument: a pipe, a here-string, a
// here-document behind a wrapper) all count as "may brace-expand". A shell
// given a script FILE is not followed: the file's contents are not command
// text.
func nestedShellMayBraceExpand(args []string, depth int) bool {
	j, hasC := 0, false
	for j < len(args) && (strings.HasPrefix(args[j], "-") || strings.HasPrefix(args[j], "+")) {
		opt := args[j]
		if knownNoArgLongShellOptions[opt] {
			j++
			continue
		}
		if !isPlainShortOptionCluster(opt) {
			return true
		}
		if strings.ContainsRune(opt, 'c') {
			hasC = true
		}
		j++
	}
	for j < len(args) && isRedirectionWord(args[j]) {
		if strings.HasPrefix(strings.TrimLeft(args[j], "0123456789"), "<<<") {
			return true // a here-string feeds the shell its script on standard input
		}
		j++
	}
	if j >= len(args) {
		return true // reads its script from standard input: cannot see it
	}
	if !hasC {
		return false // runs a script file; its contents are not command text
	}
	return mayBraceExpand(args[j], depth+1)
}

// knownNoArgLongShellOptions are the bash/sh long options that take no
// argument, so the script position after them is certain. Options that take
// an argument (--rcfile, --init-file) and any option not listed here make
// the scan withhold the exemption.
var knownNoArgLongShellOptions = map[string]bool{
	"--norc": true, "--noprofile": true, "--login": true, "--posix": true,
	"--noediting": true, "--restricted": true, "--verbose": true,
	"--debugger": true,
}

// isPlainShortOptionCluster accepts "-x" style clusters of letters that take
// no argument. "-o"/"-O" (which consume the next word), "--…", "+…" and
// anything else are not understood.
func isPlainShortOptionCluster(opt string) bool {
	if len(opt) < 2 || opt[0] != '-' || opt[1] == '-' {
		return false
	}
	for _, r := range opt[1:] {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isLetter || r == 'o' || r == 'O' {
			return false
		}
	}
	return true
}

// isRedirectionWord reports a redirection token such as "<<EOF", "<file",
// ">out", "2>err" or "<<<word" (it is not a script argument).
func isRedirectionWord(w string) bool {
	trimmed := strings.TrimLeft(w, "0123456789")
	return strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, ">")
}

// hereDocBody is one here-document body: cmd[start:end], and whether the
// command consuming it is a shell (its body is then shell text).
type hereDocBody struct {
	start, end int
	shell      bool
}

// hereDocBodies finds the bodies of the unquoted `<<` / `<<-` here-documents
// in cmd (not `<<<` here-strings). A body runs from the line after the
// operator to the line equal to its delimiter (leading tabs ignored for
// `<<-`), or to the end of the text when no such line exists, which is how
// the shell treats it. ok is false when a delimiter cannot be read.
func hereDocBodies(cmd string, mask []bool) ([]hereDocBody, bool) {
	var out []hereDocBody
	for i := 0; i+1 < len(cmd); i++ {
		if mask[i] || cmd[i] != '<' || cmd[i+1] != '<' {
			continue
		}
		if i+2 < len(cmd) && cmd[i+2] == '<' {
			i += 2 // here-string, not a here-document
			continue
		}
		op := i
		j := i + 2
		stripTabs := false
		if j < len(cmd) && cmd[j] == '-' {
			stripTabs = true
			j++
		}
		for j < len(cmd) && (cmd[j] == ' ' || cmd[j] == '\t') {
			j++
		}
		delim, next, ok := readHereDocDelimiter(cmd, j)
		if !ok {
			return nil, false
		}
		nl := strings.IndexByte(cmd[next:], '\n')
		if nl < 0 {
			i = next
			continue // operator on the last line: no body
		}
		start := next + nl + 1
		end, resume := len(cmd), len(cmd)
		for pos := start; pos < len(cmd); {
			lineEnd := strings.IndexByte(cmd[pos:], '\n')
			line := cmd[pos:]
			if lineEnd >= 0 {
				line = cmd[pos : pos+lineEnd]
			}
			check := line
			if stripTabs {
				check = strings.TrimLeft(line, "\t")
			}
			if check == delim {
				end = pos
				resume = pos + len(line)
				break
			}
			if lineEnd < 0 {
				break
			}
			pos += lineEnd + 1
		}
		out = append(out, hereDocBody{start: start, end: end, shell: hereDocConsumerIsShell(cmd, mask, op)})
		i = resume
	}
	return out, true
}

// readHereDocDelimiter reads the delimiter word at cmd[j:], removing quotes
// and backslashes as the shell does. It returns the word and the index after it.
func readHereDocDelimiter(cmd string, j int) (string, int, bool) {
	var b strings.Builder
	var quote byte
	k := j
	for ; k < len(cmd); k++ {
		c := cmd[k]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				b.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\' && k+1 < len(cmd):
			k++
			b.WriteByte(cmd[k])
		case c == ' ' || c == '\t' || c == '\n' || strings.IndexByte(";|&<>()", c) >= 0:
			if b.Len() == 0 || quote != 0 {
				return "", k, false
			}
			return b.String(), k, true
		default:
			b.WriteByte(c)
		}
	}
	if quote != 0 || b.Len() == 0 {
		return "", k, false
	}
	return b.String(), k, true
}

// hereDocConsumerIsShell reports whether the simple command that owns the
// here-document operator at op runs a brace-expanding shell or eval anywhere
// in its words. When it cannot tell, it answers true (the body is then
// scanned as shell text).
func hereDocConsumerIsShell(cmd string, mask []bool, op int) bool {
	start := 0
	for k := op - 1; k >= 0; k-- {
		if !mask[k] && strings.IndexByte(";|&\n(", cmd[k]) >= 0 {
			start = k + 1
			break
		}
	}
	words, ok := tokenizeShellWords(cmd[start:op])
	if !ok || len(words) == 0 {
		return true
	}
	// Any shell or eval word in the simple command counts, not only its head:
	// a wrapper such as env, command, nice, exec, nohup, time or sudo puts
	// the shell later in the word list (T11 round 4).
	for _, w := range words {
		base := filepath.Base(w)
		if braceExpandingShells[base] || base == "eval" {
			return true
		}
	}
	return false
}
