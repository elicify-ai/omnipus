// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"fmt"
	"os"
	"path/filepath"
)

// OwnFolder returns Omnipus's own folder for registration on goos, for the
// omnipus program file executable (office-preview spec §3.9 OF-1): on Linux
// dir(executable)/../share/omnipus, cleaned; "" on every other platform,
// where the own-folder rule does not apply (OF-2).
func OwnFolder(executable, goos string) string {
	if goos != "linux" {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "share", "omnipus"))
}

// strictlyInside reports whether p is a descendant of dir by whole path
// components, never dir itself (OF-3 "strictly inside").
func strictlyInside(p, dir string) bool {
	return p != dir && within(p, dir)
}

// ownFolderCheck holds Omnipus's own folder as written and with its links
// resolved; resolved is "" when the folder cannot be resolved (missing), in
// which case nothing is inside it.
type ownFolderCheck struct {
	written  string
	resolved string
}

func newOwnFolderCheck(own string) ownFolderCheck {
	c := ownFolderCheck{written: own}
	if resolved, err := filepath.EvalSymlinks(own); err == nil {
		c.resolved = resolved
	}
	return c
}

// declaredInside reports whether p, cleaned and with its links not
// followed, lies strictly inside the own folder as written or resolved.
func (c ownFolderCheck) declaredInside(p string) bool {
	if c.resolved == "" {
		return false
	}
	return strictlyInside(p, c.written) || strictlyInside(p, c.resolved)
}

// resolvedInside reports whether p, a path with every link resolved,
// lies strictly inside the own folder resolved.
func (c ownFolderCheck) resolvedInside(p string) bool {
	return c.resolved != "" && strictlyInside(p, c.resolved)
}

// registeredOutsideOwn is the OF-4 use check for an operator-registered
// root on Linux: nil when the root, as recorded and with every link
// resolved now, lies strictly inside Omnipus's own folder; otherwise a
// StatusOutsideOwn problem. A root whose links cannot be resolved but whose
// recorded path is inside is left to the caller's own handling (the root is
// then reported missing or unreadable; nothing in it is used). It reads only
// path metadata, nothing inside the root.
func (r *Resolver) registeredOutsideOwn(root string) *ComponentError {
	own := OwnFolder(r.Executable, r.goos())
	if own == "" {
		return nil
	}
	check := newOwnFolderCheck(own)
	outside := &ComponentError{
		Status: StatusOutsideOwn,
		Reason: fmt.Sprintf("registered root %s is outside Omnipus's own folder %s", root, own),
		Root:   root,
		Own:    own,
	}
	if !check.declaredInside(filepath.Clean(root)) {
		return outside
	}
	if resolved, ok := linksResolved(root); ok && !check.resolvedInside(resolved) {
		return outside
	}
	return nil
}

// CheckRegisteredRoot repeats the OF-4 use check for an already resolved
// component before it is used again (a cached LibreOffice): nil for a
// packaged component and off Linux.
func (r *Resolver) CheckRegisteredRoot(c *Component) *ComponentError {
	if c == nil || c.Manifest == nil || c.Manifest.Trust != TrustOperatorRegistered {
		return nil
	}
	return r.registeredOutsideOwn(c.Manifest.Root)
}

// registrationOutsideOwn is the OF-3 registration check: nil when folder
// (as given) may be registered on goos for the program file program;
// otherwise the exact OF-3 refusal for kind. It looks at path metadata
// only.
func registrationOutsideOwn(kind Kind, folder, program, goos string) error {
	own := OwnFolder(program, goos)
	if own == "" {
		return nil
	}
	given, err := filepath.Abs(folder)
	if err != nil {
		return err
	}
	check := newOwnFolderCheck(own)
	// A folder that cannot be resolved (missing) but is declared inside is
	// left to the registration's own folder checks, which report it.
	resolved, resolvedOK := linksResolved(given)
	if check.declaredInside(given) && (!resolvedOK || check.resolvedInside(resolved)) {
		return nil
	}
	return outsideOwnRegistrationError(kind, given, resolved, resolvedOK, own)
}

// registeredRootOutsideOwn is OF-3 (b) for the root a registration would
// record (every link already resolved); folder is the folder as given.
func registeredRootOutsideOwn(kind Kind, folder, root, program, goos string) error {
	own := OwnFolder(program, goos)
	if own == "" {
		return nil
	}
	if newOwnFolderCheck(own).resolvedInside(root) {
		return nil
	}
	given, err := filepath.Abs(folder)
	if err != nil {
		return err
	}
	resolved, resolvedOK := linksResolved(given)
	return outsideOwnRegistrationError(kind, given, resolved, resolvedOK, own)
}

// linksResolved returns p with every link resolved, and false when that is
// not possible (p is missing or unreachable).
func linksResolved(p string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(p)
	return resolved, err == nil
}

func outsideOwnRegistrationError(kind Kind, given, resolved string, resolvedOK bool, own string) error {
	shown := given
	if resolvedOK && resolved != given {
		shown += " (links lead to " + resolved + ")"
	}
	if kind == KindOffice {
		return fmt.Errorf("LibreOffice folder %s is outside Omnipus's own folder (%s); on Linux, Omnipus accepts only a LibreOffice inside that folder. Re-run the Omnipus installer, which puts LibreOffice in %s, or put LibreOffice in a new folder inside %s and register that folder",
			shown, own, filepath.Join(own, "office"), own)
	}
	return fmt.Errorf("PDF renderer folder %s is outside Omnipus's own folder (%s); on Linux, Omnipus accepts only a PDF renderer inside that folder. Re-run the Omnipus installer, which puts the renderer in %s, or unpack the renderer release in a new folder inside %s and register that folder",
		shown, own, filepath.Join(own, "pdfium"), own)
}

// registrationProgram is the omnipus program file a registration judges
// Omnipus's own folder by: opts.Program, or the running program resolved
// as NewResolver resolves it. Off Linux the rule does not apply and the
// program is not needed: "".
func registrationProgram(opts RegisterOptions) (string, error) {
	if opts.goos() != "linux" {
		return "", nil
	}
	if opts.Program != "" {
		return opts.Program, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("the omnipus program file cannot be located: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
		exe = resolved
	}
	return exe, nil
}
