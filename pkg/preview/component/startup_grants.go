// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// ConfinementStartupGrants lists the paths the confined Linux startup rules
// grant read+execute on (office-preview spec §3.8 BR-2.1, founder Q100 A), in
// BR-2.1's order: for LibreOffice the registered root (if any; on Linux only
// one inside Omnipus's own folder, §3.9 OF-4) then the
// packaged slots in PackagedCandidates order, the same for the PDF renderer,
// then the omnipus program file itself — never its folder. Every path is real
// (symlinks resolved) and listed once.
//
// A packaged slot is listed when it exists, whether or not it holds a
// manifest: a slot is trusted by its location and its content is still
// verified on every call (§3.2 item 1). A registered manifest adds its root
// only when it passes ParseManifest and the root exists. notes carries one
// line per skipped entry with its reason, for the gateway's
// sandbox.component_grants log line.
//
// The list only says what to ask for. Refusals (the filesystem root, a
// restricted system path, a folder an agent can write) are the sandbox's
// decision: sandbox.DefaultBootPolicyForModel drops those entries.
func ConfinementStartupGrants(r *Resolver) (grants []string, notes []string) {
	seen := make(map[string]bool)
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			grants = append(grants, p)
		}
	}
	for _, kind := range []Kind{KindOffice, KindPDFRenderer} {
		root, note := registeredStartupRoot(r, kind)
		if note != "" {
			notes = append(notes, note)
		}
		if root != "" {
			add(root)
		}
		for _, cand := range r.PackagedCandidates(kind) {
			resolved, err := filepath.EvalSymlinks(cand)
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s packaged slot %s skipped: %s", kind, filepath.Clean(cand), missingOr(err)))
				continue
			}
			add(resolved)
		}
	}
	program, err := filepath.EvalSymlinks(r.Executable)
	if err != nil {
		notes = append(notes, fmt.Sprintf("omnipus program file %s skipped: %s", r.Executable, missingOr(err)))
		return grants, notes
	}
	info, err := os.Stat(program)
	switch {
	case err != nil:
		notes = append(notes, fmt.Sprintf("omnipus program file %s skipped: %s", program, missingOr(err)))
	case !info.Mode().IsRegular():
		notes = append(notes, fmt.Sprintf("omnipus program file %s skipped: not a regular file", program))
	default:
		add(program)
	}
	return grants, notes
}

// registeredStartupRoot returns the real path of the root named in kind's
// registered manifest, or a note saying why there is none to grant. No
// registration at all is neither a grant nor a note. On Linux a root outside
// Omnipus's own folder is never granted (office-preview spec §3.9 OF-4).
func registeredStartupRoot(r *Resolver, kind Kind) (string, string) {
	home := r.Home
	if home == "" {
		return "", ""
	}
	manifest := filepath.Join(RegisteredDir(home, kind), ManifestFile)
	data, err := os.ReadFile(manifest)
	if errors.Is(err, os.ErrNotExist) {
		return "", ""
	}
	if err != nil {
		return "", fmt.Sprintf("%s registered manifest %s skipped: %v", kind, manifest, err)
	}
	m, err := ParseManifest(data, kind)
	if err != nil {
		return "", fmt.Sprintf("%s registered manifest %s skipped: invalid: %v", kind, manifest, err)
	}
	if p := r.registeredOutsideOwn(m.Root); p != nil {
		if p.Status == StatusOutsideOwn {
			return "", fmt.Sprintf("%s registered root %s skipped: outside Omnipus's own folder %s", kind, p.Root, p.Own)
		}
		return "", fmt.Sprintf("%s registered root %s skipped: %s", kind, m.Root, p.Reason)
	}
	root, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		return "", fmt.Sprintf("%s registered root %s skipped: %s", kind, m.Root, missingOr(err))
	}
	return root, ""
}

func missingOr(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	return err.Error()
}

// StartupGrantRecord is what the confined Linux startup rules granted and
// refused for the Office preview components, recorded when the rules were
// made (office-preview spec §3.8 BR-2.5). Landlock binds a rule to the folder
// it opened, not to its name, so each granted folder keeps the identity
// (device and inode) it had then. The zero value records nothing.
type StartupGrantRecord struct {
	granted []grantedFolder
	refused []refusedFolder
}

type grantedFolder struct {
	path string
	info os.FileInfo
}

type refusedFolder struct {
	path           string
	writableFolder string
}

// AddGranted records path, which the startup rules grant, with its identity
// now. It fails when path cannot be read for its identity; such a path is
// not recorded, so a component there is treated as not granted.
func (rec *StartupGrantRecord) AddGranted(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	rec.granted = append(rec.granted, grantedFolder{path: path, info: info})
	return nil
}

// AddRefused records path, which the startup rules refused. writableFolder
// names the writable folder it lies in or contains; empty for the other
// refusals (the filesystem root, a restricted system path).
func (rec *StartupGrantRecord) AddRefused(path, writableFolder string) {
	rec.refused = append(rec.refused, refusedFolder{path: path, writableFolder: writableFolder})
}

// Granted lists the recorded granted paths, in the order recorded.
func (rec *StartupGrantRecord) Granted() []string {
	out := make([]string, 0, len(rec.granted))
	for _, g := range rec.granted {
		out = append(out, g.path)
	}
	return out
}

// activeStartupGrants is the record CheckStartupGrant enforces; nil means the
// check does not apply (the open model, macOS, Windows, a sandbox that is not
// enforcing, or a process that is not the gateway).
var activeStartupGrants atomic.Pointer[StartupGrantRecord]

// SetStartupGrants makes rec the record every later component lookup in this
// process is checked against. The gateway calls it once, after its startup
// rules are enforcing on Linux under the confined model; nil turns the check
// off.
func SetStartupGrants(rec *StartupGrantRecord) {
	activeStartupGrants.Store(rec)
}

// StartupGrantsActive reports whether a startup record is active in this
// process (SetStartupGrants set one): the confined Linux gateway with an
// enforcing kernel sandbox (office-preview spec §3.9 RS-1).
func StartupGrantsActive() bool {
	return activeStartupGrants.Load() != nil
}

// CheckStartupGrant reports whether dir, a component folder about to be used
// (a registered root or a packaged slot), may be used under this process's
// startup rules (BR-2.5). It reads no file in dir, only its metadata. It
// returns nil when no record is active, when dir does not exist (the caller's
// own missing/damaged handling reports that), and when dir is a granted
// folder with the same identity it had when the rules were made. Otherwise:
//
//   - its real path cannot be resolved for another reason: StatusDamaged;
//   - refused at the start for a writable folder: StatusUnsafe, Reason = that
//     folder;
//   - refused at the start for another reason: StatusDamaged, "program folder
//     refused by the sandbox: <dir>";
//   - installed, registered, moved or replaced after the start:
//     StatusAfterStart.
func CheckStartupGrant(dir string) *ComponentError {
	rec := activeStartupGrants.Load()
	if rec == nil {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return damagedBy(err, "component folder unreadable: %v", err)
	}
	for _, f := range rec.refused {
		if f.path != resolved {
			continue
		}
		if f.writableFolder != "" {
			return &ComponentError{Status: StatusUnsafe, Reason: f.writableFolder}
		}
		return damaged("program folder refused by the sandbox: %s", resolved)
	}
	info, statErr := os.Stat(resolved)
	for _, g := range rec.granted {
		if g.path == resolved && statErr == nil && os.SameFile(g.info, info) {
			return nil
		}
	}
	return &ComponentError{Status: StatusAfterStart, Reason: "installed or registered after Omnipus started"}
}
