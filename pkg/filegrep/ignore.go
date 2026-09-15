// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"errors"
	"io/fs"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// ignoreLayer is one nested .gitignore/.ignore matcher (FR-007): a path is
// pruned when the DEEPEST matching pattern says ignore, honoring negations,
// exactly gitignore's precedence (delegated to the library per O1/MIN-004).
type ignoreLayer struct {
	base string // dir the ignore file lives in, "" for root
	gi   *gitignore.GitIgnore
	// touch answers "does any pattern in this file concern this path at
	// all" independent of gi's own resolved ignore/negate verdict — see
	// touchLines.
	touch *gitignore.GitIgnore
}

// ignoreFileNames are checked, in order, in every directory that can carry
// ignore rules: the walk root, each subdirectory descended into
// (loadIgnoreLayer), and every ancestor directory a caller supplies via
// Root.AncestorIgnore (LoadAncestorIgnore).
var ignoreFileNames = [...]string{".gitignore", ".ignore"}

// touchLines derives, from one ignore file's raw lines, a pattern set that
// answers "does any pattern here concern this path" rather than "does this
// file ignore it": every negation's leading "!" is stripped (using the same
// trim/prefix check getPatternFromLine applies before testing for one) so
// the underlying pattern still fires as an ordinary match. go-gitignore
// resolves negation internally per file and its MatchesPath collapses "no
// pattern touched this path" and "a negation cancelled an in-file match"
// into the same false result — its pattern list is unexported, so there is
// no lower-level query to ask instead. ignoredBy needs that distinction so
// a DEEPER layer's negation can override a SHALLOWER layer's ignore verdict
// even when the deeper layer's own file has nothing else to say about the
// path (F5).
func touchLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		trimmed = strings.Trim(trimmed, " ")
		if strings.HasPrefix(trimmed, "!") {
			out[i] = trimmed[1:]
			continue
		}
		out[i] = line
	}
	return out
}

func newIgnoreLayer(dir string, lines []string) ignoreLayer {
	return ignoreLayer{
		base:  dir,
		gi:    gitignore.CompileIgnoreLines(lines...),
		touch: gitignore.CompileIgnoreLines(touchLines(lines)...),
	}
}

// readIgnoreFiles reads whichever of ignoreFileNames exist in dir, returning
// each found file's raw lines (in ignoreFileNames order). unreadable counts
// a name that EXISTS but failed to read — finding F-E: the previous version
// collapsed that case with the ordinary "no such file" case (which is most
// directories, for either ignore filename, and is not worth counting), so a
// permission-denied or I/O error on a real .gitignore/.ignore silently
// dropped that file's rules with no trace anywhere in the response. A
// missing file is still not counted — that remains the routine, silent
// case; only "it exists and reading it failed" increments unreadable.
func readIgnoreFiles(fsys fs.FS, dir string) (out [][]string, unreadable int) {
	for _, name := range ignoreFileNames {
		p := name
		if dir != "" {
			p = dir + "/" + name
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				unreadable++
			}
			continue
		}
		out = append(out, strings.Split(string(data), "\n"))
	}
	return out, unreadable
}

func loadIgnoreLayer(fsys fs.FS, dir string) (out []ignoreLayer, unreadable int) {
	files, unreadable := readIgnoreFiles(fsys, dir)
	out = make([]ignoreLayer, 0, len(files))
	for _, lines := range files {
		out = append(out, newIgnoreLayer(dir, lines))
	}
	return out, unreadable
}

// AncestorIgnoreLayer is the raw content of one .gitignore/.ignore file that
// lives ABOVE a Root.FS a caller has narrowed to a subdirectory of a larger
// tree (F8). See LoadAncestorIgnore.
type AncestorIgnoreLayer struct {
	// Dir is the file's directory, relative to the TRUE (unscoped) root —
	// "" for that root's own .gitignore/.ignore.
	Dir string
	// Lines is the raw file content split on "\n", exactly what
	// loadIgnoreLayer would have produced had Root.FS reached that far up.
	Lines []string
}

// LoadAncestorIgnore reads every .gitignore/.ignore file strictly above
// scope — from trueRoot's own root down to (but not including) scope itself
// — so a caller about to narrow its fs.FS to scope (os.Root.OpenRoot,
// fs.Sub) can preserve the ignore layers a search starting at trueRoot would
// otherwise have honored (F8/FR-007). Call it BEFORE narrowing: trueRoot
// must still reach those ancestor directories. Pair the result with
// Root.ScopePrefix set to the same scope.
//
// unreadable is finding F-E's signal: how many ancestor .gitignore/.ignore
// files existed but could not be read. Before this, a caller had no way to
// tell "ancestor layers loaded" from "ancestor layers failed to load" —
// this function returned no error at all, so a permission-denied ancestor
// ignore file silently applied none of its rules with nothing to show for
// it. A caller that ignores this return keeps today's exact behavior
// (rules from that file simply do not apply, same as if it never existed);
// a caller that wants to surface it can count/log it.
func LoadAncestorIgnore(trueRoot fs.FS, scope string) (out []AncestorIgnoreLayer, unreadable int) {
	scope = strings.Trim(scope, "/")
	if scope == "" {
		return nil, 0
	}
	segments := strings.Split(scope, "/")
	dir := ""
	for i := 0; i < len(segments); i++ {
		lines, u := readIgnoreFiles(trueRoot, dir)
		unreadable += u
		for _, l := range lines {
			out = append(out, AncestorIgnoreLayer{Dir: dir, Lines: l})
		}
		if dir == "" {
			dir = segments[i]
		} else {
			dir = dir + "/" + segments[i]
		}
	}
	return out, unreadable
}

// ignoredByFrom reports whether rel is pruned by layers, processed shallow
// to deep starting from seed (false — "not ignored" — for a plain walk
// root; a caller-supplied ancestor chain's own verdict via
// ancestorContext.seed for F8): a layer that has nothing to say about rel
// (touch reports false) leaves the running verdict from shallower layers
// untouched; a layer that DOES touch rel replaces the verdict outright with
// its own (gi's resolved ignore/negate outcome), so the deepest touching
// layer always wins, negations included.
func ignoredByFrom(seed bool, layers []ignoreLayer, rel string, isDir bool) bool {
	verdict := seed
	for _, l := range layers {
		sub := rel
		if l.base != "" {
			if !strings.HasPrefix(rel, l.base+"/") {
				continue
			}
			sub = strings.TrimPrefix(rel, l.base+"/")
		}
		probe := sub
		if isDir {
			probe = sub + "/"
		}
		if !l.touch.MatchesPath(probe) {
			continue
		}
		verdict = l.gi.MatchesPath(probe)
	}
	return verdict
}
