// shell_standalone_literal.go: the T11 (#882) stand-alone quoted-literal rule
// for the bash command-text path guard (spec
// docs/internal/specs/t11-documents-and-3p-agents-spec.md §3.1).
//
// The guard refuses ordinary script text such as print('/api/v1/users')
// because the quoted route looks like an absolute path outside the working
// folder. This file decides when such a candidate is NOT refused: all six
// spec conditions must hold.
//
//  1. The candidate is the entire content of one matching pair of quotes.
//  2. It contains none of * ? [ ] { } % $ ` ~ \ and no ".." segment.
//  3. It is not expansion-derived (applied by the caller).
//  4. Its first component below the filesystem root does not exist.
//  5. The gateway's effective user cannot write the root (rootWritableProbe;
//     always "writable" on Windows, so the rule is off there).
//  6. It stands alone: skipping whitespace including newlines, the nearest
//     character before the opening quote is one of ( , = [ { : or the start
//     of the shell word, and the nearest after the closing quote is one of
//     ) , ] } ; or the end of the shell word.
//
// Conditions 4 and 5 together mean the path's top folder neither exists nor
// can be created by this process, so no read or write through the literal can
// succeed. Condition 6 keeps the literal from being one fragment of a longer
// path ('/tm' 'p/x', '/tm' + 'p/x', '/{}'.format(...), r'/x', f'/x').
//
// The guard still reads command text only; this rule does not make it a
// boundary. What stays refused is recorded in the spec ("Recorded
// limitation").

package tools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// rootWritableProbe reports whether the gateway's effective user can write
// the filesystem root (spec §3.1 condition 5). A package variable so tests
// can force either branch; production uses the platform probe
// (shell_standalone_literal_unix.go / _windows.go).
var rootWritableProbe = rootIsWritable

// standAloneLiteralForbiddenChars is spec §3.1 condition 2's character set.
const standAloneLiteralForbiddenChars = "*?[]{}%$`~\\"

// shellQuoteRegion is one top-level shell quoted span: open and close are the
// byte offsets of the opening and closing quote characters.
type shellQuoteRegion struct {
	open, close int
	quote       byte
}

// shellQuoteRegions lists the top-level quoted spans of s using the same
// escaping rules as shellQuoteMask (a backslash escapes outside single
// quotes). ok is false for unbalanced quoting; callers then decline the rule.
func shellQuoteRegions(s string) ([]shellQuoteRegion, bool) {
	var regions []shellQuoteRegion
	var quote byte
	open := -1
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' && quote != '\'' {
			i++
			continue
		}
		switch {
		case quote == 0 && (ch == '\'' || ch == '"'):
			quote, open = ch, i
		case quote != 0 && ch == quote:
			regions = append(regions, shellQuoteRegion{open: open, close: i, quote: quote})
			quote, open = 0, -1
		}
	}
	return regions, quote == 0
}

func isShellSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isShellWordChar(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// standAloneQuotedLiteral reports whether the candidate cmd[start:end] meets
// spec §3.1 conditions 1, 2 and 6 (the text conditions). It never touches the
// filesystem; literalTopCannotExist adds conditions 4 and 5.
func standAloneQuotedLiteral(cmd string, start, end int) bool {
	// Condition 1: the bytes either side are the same quote character.
	if start < 1 || end >= len(cmd) || end <= start {
		return false
	}
	q := cmd[start-1]
	if (q != '\'' && q != '"') || cmd[end] != q {
		return false
	}
	// Condition 2.
	content := cmd[start:end]
	if strings.ContainsAny(content, standAloneLiteralForbiddenChars) {
		return false
	}
	for _, seg := range strings.Split(content, "/") {
		if seg == ".." {
			return false
		}
	}

	regions, ok := shellQuoteRegions(cmd)
	if !ok {
		return false
	}
	openQ, closeQ := start-1, end
	// The literal's own quotes are either one top-level shell region (shell
	// text such as `echo '/api/v1'`, or a here-document body scanned as shell
	// text), or plain characters inside an enclosing region of the other
	// quote kind (`python3 -c "print('/api/v1')"`). Anything else — the two
	// quotes belong to different regions — is not one matching pair.
	var outer *shellQuoteRegion
	ownRegion := false
	for i := range regions {
		r := &regions[i]
		if r.open == openQ && r.close == closeQ {
			ownRegion = true
			break
		}
		if r.open < openQ && closeQ < r.close && r.quote != q {
			outer = r
			break
		}
	}
	if !ownRegion && outer == nil {
		return false
	}
	return standsAloneBefore(cmd, openQ, outer) && standsAloneAfter(cmd, closeQ, outer)
}

// standsAloneBefore is condition 6's left side for the opening quote at openQ.
func standsAloneBefore(cmd string, openQ int, outer *shellQuoteRegion) bool {
	i := openQ - 1
	for i >= 0 && isShellSpace(cmd[i]) {
		i--
	}
	if i < 0 {
		return true // start of the command text
	}
	if strings.IndexByte("(,=[{:", cmd[i]) >= 0 {
		return true
	}
	if outer != nil {
		// Start of the shell word: only whitespace separates the literal from
		// the enclosing quote, and that quote itself starts a shell word.
		return i == outer.open && shellWordStartsAt(cmd, outer.open)
	}
	// A top-level quote preceded by whitespace and an ordinary word (for
	// example `echo '/api/v1'`) starts its own shell word.
	return i < openQ-1 && isShellWordChar(cmd[i])
}

// standsAloneAfter is condition 6's right side for the closing quote at closeQ.
func standsAloneAfter(cmd string, closeQ int, outer *shellQuoteRegion) bool {
	j := closeQ + 1
	for j < len(cmd) && isShellSpace(cmd[j]) {
		j++
	}
	if j >= len(cmd) {
		return true // end of the command text
	}
	if strings.IndexByte("),]};", cmd[j]) >= 0 {
		return true
	}
	// End of the shell word: only whitespace separates the literal from the
	// enclosing quote's close, and the shell word ends there.
	return outer != nil && j == outer.close && shellWordEndsAfter(cmd, outer.close)
}

func shellWordStartsAt(cmd string, i int) bool {
	return i == 0 || isShellSpace(cmd[i-1]) || strings.IndexByte(";|&(", cmd[i-1]) >= 0
}

func shellWordEndsAfter(cmd string, i int) bool {
	return i+1 >= len(cmd) || isShellSpace(cmd[i+1]) || strings.IndexByte(";|&)", cmd[i+1]) >= 0
}

// literalTopCannotExist is spec §3.1 conditions 4 and 5 for the cleaned
// absolute path p: the root is not writable for the gateway's effective user
// and the first component below the root does not exist. Any doubt (an Lstat
// error other than not-exist, a path with no first component) answers false,
// which keeps today's refusal.
func literalTopCannotExist(p string) bool {
	if rootWritableProbe() {
		return false
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if first == "" || first == "." || first == ".." {
		return false
	}
	_, err := os.Lstat(filepath.Join("/", first))
	return errors.Is(err, fs.ErrNotExist)
}
