// shell_brace_expansion.go: decides when the T11 stand-alone quoted-literal
// rule (#882) is withheld for a bash command (founder decisions 2026-10-30
// "simplify the guard", 2026-11-01 "S5c A" and Q11 A). A quoted fragment can
// otherwise be glued into a real path by `{'/tm',}p/x`-style brace expansion,
// which needs an unquoted "{" or "}" in text the shell parses:
//   - the command itself (quotes tracked by shellQuoteMask; escaped braces are
//     quoted);
//   - the script of a nested shell: `sh|bash|zsh|dash|ksh|mksh|ash|fish -c
//     SCRIPT` anywhere in the command (so `find -exec sh -c`, `xargs sh -c`,
//     `env bash -c` are covered), and the arguments of `eval`.
//
// Here-documents (Q11 A): exactly one form keeps the rule, the whole command
// being a plain non-shell program and one plain single-quoted marker
// (isPlainHereDocument). Any other `<<`, wherever it appears in any text
// scanned here, withholds the rule. There is deliberately no scanner for where
// other forms end: comments, arithmetic, delimiter quoting and line joining
// each made such a scanner read live shell text as document data.
//
// When the scan cannot be sure where a nested shell's script is, it answers
// "may expand" and the rule is withheld (T11 round 4, founder: keep it
// simple): text that cannot be parsed (unbalanced quotes, a `$(...)` the
// tokenizer refuses, nesting deeper than maxBraceScanDepth), a long or
// argument-taking option before -c (`bash --norc -c`, `bash -o x -c`), and a
// shell reading its script from standard input (a pipe, -s, or a script file
// named "-" or /dev/stdin). Unquoted separators end a simple command even
// without whitespace (`true;bash -c ...`), and a shell's arguments are read
// only from its own simple command. A backslash-newline line continuation
// outside quotes in any text scanned here also answers "may expand" (T11 round
// 7, S5c): bash removes the pair before it splits words, so `ba\<newline>sh`
// is the word bash, while shellQuoteMask reads the pair as one escaped
// character and would hide it.
//
// Residual, stated: a non-shell program that hands its own data to a shell
// (Python's os.system, perl system, or a program told to run its input) is not
// followed; the guard reads command text only. The here-document form promises
// only that the launching shell treats the text as data.

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

// commandMayBraceExpand reports whether the stand-alone literal rule must be
// withheld for cmd: unless cmd is the approved here-document form, because it,
// or any shell text nested in it, holds `<<`, an unquoted brace or an unquoted
// line continuation, or a nested script the scan cannot locate.
func commandMayBraceExpand(cmd string) bool {
	return !isPlainHereDocument(cmd) && mayBraceExpand(cmd, 0)
}

// isPlainHereDocument reports whether cmd is the one here-document form that
// keeps the rule (founder ruling Q11 A), compared byte for byte with no
// normalisation. cmd must be the text the shell receives (executeRun trims the
// command once, before both this check and the run):
//
//	PROGRAM <<'MARKER'
//	text
//	MARKER
//
// PROGRAM and MARKER are plain words and PROGRAM is not a shell or eval. Lines
// end at LF only, so a CR makes a line differ from the marker exactly as it
// does for the shell. The first line equal to the marker must be the last line
// of the command: the shell ends the document there, so anything after it
// would be live shell text. The text in between is data to the launching
// shell, whatever it looks like. This decides eligibility only; every other
// check on a literal still applies.
func isPlainHereDocument(cmd string) bool {
	header, rest, ok := strings.Cut(cmd, "\n")
	if !ok {
		return false
	}
	program, word, ok := strings.Cut(header, " <<'")
	if !ok || !isPlainWord(program) || isShellOrEval(program) {
		return false
	}
	marker, ok := strings.CutSuffix(word, "'")
	if !ok || !isPlainWord(marker) {
		return false
	}
	lines := "\n" + rest + "\n"
	closer := "\n" + marker + "\n"
	first := strings.Index(lines, closer)
	return first >= 0 && first == len(lines)-len(closer)
}

// isPlainWord reports a nonempty run of ASCII letters, digits and underscores:
// nothing a shell quotes, expands or splits.
func isPlainWord(w string) bool {
	for i := 0; i < len(w); i++ {
		if !isShellWordChar(w[i]) {
			return false
		}
	}
	return w != ""
}

// isShellOrEval reports a program whose standard input is shell text. Case is
// ignored: on a case-insensitive filesystem, such as the macOS default, `BASH`
// runs bash.
func isShellOrEval(program string) bool {
	name := strings.ToLower(program)
	return braceExpandingShells[name] || name == "eval"
}

func mayBraceExpand(cmd string, depth int) bool {
	if depth > maxBraceScanDepth || strings.Contains(cmd, "<<") {
		return true
	}
	mask, balanced := shellQuoteMask(cmd)
	if !balanced || hasUnquotedLineContinuation(cmd) {
		return true
	}
	// An unquoted command separator ends a simple command even without
	// whitespace (`true;bash -c ...`). Each simple command is judged on its
	// own words, so a shell's arguments never run on into the next command
	// (`... | bash; echo done` must not read `echo done` as bash's script
	// file — T11 round 5, S5).
	// An unquoted redirection operator also ends a word, as in bash
	// (`bash>out.txt` is the word bash plus a redirection), so it is written
	// apart here: the interpreter and the redirection's target are then seen
	// (T11 round 6, S5). Only this scan splits so; tokenizeShellWords is shared
	// with other classifiers and keeps its behaviour.
	var segments []string
	var cur strings.Builder
	wordStart := 0
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if (c == '{' || c == '}') && !mask[i] {
			return true
		}
		if !mask[i] && (c == '<' || c == '>') {
			if !isFDNumberWord(cmd, mask, wordStart, i) {
				cur.WriteByte(' ')
			}
			end := redirectionOperatorEnd(cmd, mask, i)
			cur.WriteString(cmd[i:end])
			cur.WriteByte(' ')
			i = end - 1
			wordStart = end
			continue
		}
		if !mask[i] && strings.IndexByte(";&|()\n", c) >= 0 {
			segments = append(segments, cur.String())
			cur.Reset()
			wordStart = i + 1
			continue
		}
		if !mask[i] && (c == ' ' || c == '\t') {
			wordStart = i + 1
		}
		cur.WriteByte(c)
	}
	segments = append(segments, cur.String())
	for _, seg := range segments {
		words, ok := tokenizeShellWords(seg)
		if !ok {
			return true
		}
		if simpleCommandMayBraceExpand(words, depth) {
			return true
		}
	}
	return false
}

// hasUnquotedLineContinuation reports whether cmd holds a backslash followed
// by a newline outside quotes, using shellQuoteMask's quoting rules. Bash
// removes such a pair before it identifies words (Bash Reference Manual,
// "Escape Character"). Inside single quotes the pair is literal; inside double
// quotes bash removes it too, but it only joins text within one quoted word and
// creates no new word, so it does not count here.
func hasUnquotedLineContinuation(cmd string) bool {
	var quote byte // 0 = unquoted, '\'' or '"' = inside that quote
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if ch == '\\' && quote != '\'' {
			if quote == 0 && i+1 < len(cmd) && cmd[i+1] == '\n' {
				return true
			}
			i++
			continue
		}
		switch {
		case quote == 0 && (ch == '\'' || ch == '"'):
			quote = ch
		case quote != 0 && ch == quote:
			quote = 0
		}
	}
	return false
}

// isFDNumberWord reports whether cmd[start:op] is a non-empty run of unquoted
// digits: bash then reads it as the file-descriptor number of the redirection
// at op (`2>err`), not as a word of its own.
func isFDNumberWord(cmd string, mask []bool, start, op int) bool {
	if start >= op {
		return false
	}
	for k := start; k < op; k++ {
		if mask[k] || cmd[k] < '0' || cmd[k] > '9' {
			return false
		}
	}
	return true
}

// redirectionOperatorEnd returns the index after the unquoted redirection
// operator starting at op: `<`, `>`, `>>`, `<>`, `>|`, `>&`, `<&`. mayBraceExpand
// has already withheld the rule for any `<<`.
func redirectionOperatorEnd(cmd string, mask []bool, op int) int {
	j := op + 1
	for j < len(cmd) && !mask[j] {
		c, prev := cmd[j], cmd[j-1]
		switch {
		case c == '<' || c == '>':
		case c == '|' && prev == '>':
		case c == '&' && (prev == '<' || prev == '>'):
		default:
			return j
		}
		j++
	}
	return j
}

// simpleCommandMayBraceExpand judges the words of one simple command: the
// arguments of eval and the script of any brace-expanding shell in it.
func simpleCommandMayBraceExpand(words []string, depth int) bool {
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

// nestedShellMayBraceExpand judges the words after a shell name, within one
// simple command. It follows the rule "cannot be sure, withhold" (T11 round
// 4): only plain short options (letters, none taking an argument) and the
// known argument-less long options (knownNoArgLongShellOptions, e.g. --norc,
// --login) before the script are understood, so the script can be located
// with certainty. Any other long option (--rcfile FILE, an unknown one), a
// +option, an option that takes an argument (-o, -O), and every way of
// reading the script from standard input count as "may brace-expand": -s
// (whatever positional arguments follow it), no -c and no script-file
// argument (a pipe, a here-string, a here-document behind a wrapper), or a
// script file named "-" or /dev/stdin (T11 round 5, S5). A redirection's
// target word is never taken for the script. A shell given a real script
// FILE is not followed: the file's contents are not command text.
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
		if strings.ContainsRune(opt, 's') {
			return true // -s: the script is read from standard input, whatever follows
		}
		if strings.ContainsRune(opt, 'c') {
			hasC = true
		}
		j++
	}
	for j < len(args) && isRedirectionWord(args[j]) {
		if isBareRedirectionOperator(args[j]) {
			j++ // `> out`, `< in`, `2> err`: the next word is the target, not a script
		}
		j++
	}
	if j >= len(args) {
		return true // reads its script from standard input: cannot see it
	}
	if strings.HasPrefix(args[j], "-") || strings.HasPrefix(args[j], "+") {
		return true // an option after a redirection: the script position is not certain
	}
	if !hasC {
		if stdinScriptNames[args[j]] {
			return true // the "script file" is standard input itself
		}
		return false // runs a script file; its contents are not command text
	}
	return mayBraceExpand(args[j], depth+1)
}

// stdinScriptNames are script-file arguments that make the shell read its
// script from standard input after all.
var stdinScriptNames = map[string]bool{
	"-": true, "/dev/stdin": true, "/dev/fd/0": true, "/proc/self/fd/0": true,
}

// isBareRedirectionOperator reports a redirection operator written apart from
// its target word (`>`, `>>`, `<`, `2>`, `>|`, `<>`); the target is the
// next word.
func isBareRedirectionOperator(w string) bool {
	switch strings.TrimLeft(w, "0123456789") {
	case "<", ">", ">>", ">|", "<>", "<&", ">&":
		return true
	}
	return false
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
