// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"io/fs"
)

// vanishedEntry is a fs.DirEntry whose Info() reports the file is gone — the
// state a scorch segment is in when the background merger deleted it between
// WalkDir's ReadDir and our lazy lstat. Faked rather than raced, because the
// real race cannot be triggered on demand: it appeared once in a full-package
// run and not in 10+ isolated runs of the same test, including under load and
// -count=4. A test that waited for it would be flaky in the useless direction.
type vanishedEntry struct{ name string }

func (v vanishedEntry) Name() string { return v.name }

func (v vanishedEntry) IsDir() bool { return false }

func (v vanishedEntry) Type() fs.FileMode { return 0 }

func (v vanishedEntry) Info() (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "lstat", Path: v.name, Err: fs.ErrNotExist}
}

// realEntry wraps a file that IS present, so the positive half of the contract
// is asserted by the same test rather than assumed.
type realEntry struct{ fi fs.FileInfo }

func (r realEntry) Name() string { return r.fi.Name() }

func (r realEntry) IsDir() bool { return r.fi.IsDir() }

func (r realEntry) Type() fs.FileMode { return r.fi.Mode().Type() }

func (r realEntry) Info() (fs.FileInfo, error) { return r.fi, nil }
