// Omnipus — confined directory traversal for Library lifecycle preflight.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package library

import (
	"io/fs"
	"path"
)

// WalkDirs visits real directories in and under an existing Library folder.
// Unlike List, it reports every directory-read failure: a move preflight must
// never mistake an unreadable child for proof that the child has no tracked
// knowledge-base files. os.Root.FS confines each read; WalkDir does not follow
// symlinks found beneath the starting folder.
func (r *Root) WalkDirs(rel string, visit func(string) error) error {
	name := rel
	if name == "" {
		name = "."
	}
	rt, sub := r.resolve(name)
	return translateErr(fs.WalkDir(rt.FS(), sub, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		child := rel
		if p != sub {
			if sub == "." {
				child = path.Join(rel, p)
			} else {
				child = path.Join(rel, p[len(sub)+1:])
			}
		}
		return visit(child)
	}))
}
