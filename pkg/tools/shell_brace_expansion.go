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
// and is skipped: the shell never brace-expands here-document bodies. Text
// that cannot be parsed (unbalanced quotes, a `$(...)` the tokenizer refuses,
// nesting deeper than maxBraceScanDepth) counts as "may expand".
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
		outside.WriteByte(cmd[i])
	}
	words, ok := tokenizeShellWords(strings.ReplaceAll(outside.String(), "\n", " "))
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
		if !braceExpandingShells[filepath.Base(w)] {
			continue
		}
		j, hasC := i+1, false
		for j < len(words) && strings.HasPrefix(words[j], "-") && !strings.HasPrefix(words[j], "--") {
			if strings.Contains(words[j], "c") {
				hasC = true
			}
			j++
		}
		if hasC && j < len(words) && mayBraceExpand(words[j], depth+1) {
			return true
		}
	}
	return false
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
// here-document operator at op is a brace-expanding shell or eval. When it
// cannot tell, it answers true (the body is then scanned as shell text).
func hereDocConsumerIsShell(cmd string, mask []bool, op int) bool {
	start := 0
	for k := op - 1; k >= 0; k-- {
		if !mask[k] && strings.IndexByte(";|&\n(", cmd[k]) >= 0 {
			start = k + 1
			break
		}
	}
	words, ok := tokenizeShellWords(cmd[start:op])
	if !ok {
		return true
	}
	head, _ := resolveShellHead(words)
	if head == "" {
		return true
	}
	base := filepath.Base(head)
	return braceExpandingShells[base] || base == "eval"
}
