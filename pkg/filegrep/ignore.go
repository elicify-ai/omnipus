// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
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
}

func loadIgnoreLayer(fsys fs.FS, dir string) []ignoreLayer {
	var out []ignoreLayer
	for _, name := range []string{".gitignore", ".ignore"} {
		p := name
		if dir != "" {
			p = dir + "/" + name
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		gi := gitignore.CompileIgnoreLines(lines...)
		out = append(out, ignoreLayer{base: dir, gi: gi})
	}
	return out
}

func ignoredBy(layers []ignoreLayer, rel string, isDir bool) bool {
	// Later (deeper) layers win; go-gitignore handles negation within a file.
	verdict := false
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
		if l.gi.MatchesPath(probe) {
			verdict = true
		}
	}
	return verdict
}
