// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxScratchLinks bounds link following in one lookup, so a cycle of links
// fails the operation instead of looping.
const maxScratchLinks = 40

// errOutsideScratch is the failure of an operation that met a link leading
// outside the call's scratch folder (spec §3.1 D3.7).
var errOutsideScratch = errors.New("a link leads outside the preview scratch folder")

// inside resolves name, a path relative to the scratch folder, to a path
// relative to it that holds no link, following only links that stay inside
// (spec §3.1 D3.7). A link leading outside fails, even when what it points to
// does not exist; the part of name that does not exist is returned as it is.
//
// The walk exists because os.Root refuses every absolute link, while D3.7
// keeps a link that stays inside usable whichever way it is written. The
// protection itself is os.Root: every lookup here and every operation on the
// returned path goes through c.root, the handle opened before the first
// LibreOffice start, so nothing is looked up again by name from outside it.
func (c *call) inside(name string) (string, error) {
	pending := scratchParts(name)
	var done []string
	for links := 0; len(pending) > 0; {
		part := pending[0]
		pending = pending[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(done) == 0 {
				return "", errOutsideScratch
			}
			done = done[:len(done)-1]
			continue
		}
		next := filepath.Join(append(append([]string(nil), done...), part)...)
		info, err := c.root.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.Join(append([]string{next}, pending...)...), nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			done = append(done, part)
			continue
		}
		if links++; links > maxScratchLinks {
			return "", errOutsideScratch
		}
		target, err := c.root.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			rest, ok := c.belowScratch(target)
			if !ok {
				return "", errOutsideScratch
			}
			done, target = nil, rest
		}
		pending = append(scratchParts(target), pending...)
	}
	return filepath.Join(done...), nil
}

// belowScratch returns the part of an absolute link target below the scratch
// folder. Only the folder's two spellings recorded before the first start
// count; any other way of writing it is treated as leading outside.
func (c *call) belowScratch(target string) (string, bool) {
	for _, dir := range []string{c.dir, c.realDir} {
		if target == dir {
			return "", true
		}
		if rest, ok := strings.CutPrefix(target, dir+string(filepath.Separator)); ok {
			return rest, true
		}
	}
	return "", false
}

func scratchParts(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}

// removeInside removes the file at name. A link at any level leading outside
// fails it, the last one included; a link staying inside is removed itself.
// A file that is not there is not a failure.
func (c *call) removeInside(name string) error {
	if _, err := c.inside(name); err != nil {
		return err
	}
	parent, err := c.inside(filepath.Dir(name))
	if err != nil {
		return err
	}
	if err = c.root.Remove(filepath.Join(parent, filepath.Base(name))); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// createInside creates name with data, never through what stood there: the
// file must not exist (removeInside runs first), and its folder is resolved
// by inside.
func (c *call) createInside(name string, data []byte) error {
	parent, err := c.inside(filepath.Dir(name))
	if err != nil {
		return err
	}
	f, err := c.root.OpenFile(filepath.Join(parent, filepath.Base(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return errors.Join(err, f.Close())
}

// removeScratch removes the call's folders in the order of spec §3.4 D2:
// empty work through its handle, close that handle, remove work through the
// call folder's handle (§3.4 D5), remove the record, close the call folder's
// handle and remove the call folder (Windows will not delete an open folder).
// Any failure before the record goes keeps the record, so startup recovery
// retries. RemoveAll removes a link itself, never what it points to.
func (c *call) removeScratch() error {
	err := errors.Join(c.clearScratch(), c.root.Close())
	if err == nil {
		err = c.callRoot.RemoveAll(workName)
	}
	if err == nil {
		err = c.callRoot.Remove(ownerFile)
	}
	if err = errors.Join(err, c.callRoot.Close()); err != nil {
		return err
	}
	return os.Remove(c.callDir)
}

// workUnreplaced reports whether <id>/work is still the real folder the call's
// scratch handle was opened on (spec §3.4 D6). A first start could replace it,
// with a link included; a second start must not run on a folder it chose.
func (c *call) workUnreplaced() bool {
	named, err := c.callRoot.Lstat(workName)
	if err != nil || !named.IsDir() {
		return false
	}
	opened, err := c.root.Stat(".")
	return err == nil && os.SameFile(named, opened)
}

func (c *call) clearScratch() error {
	top, err := c.root.Open(".")
	if err != nil {
		return err
	}
	names, err := top.Readdirnames(-1)
	if closeErr := top.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	var failed error
	for _, name := range names {
		failed = errors.Join(failed, c.root.RemoveAll(name))
	}
	return failed
}
