// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// PathMarker is the marker-provenance seam a Root may carry (w4 spec §5.4,
// founder Q5=A): the workspace's mail-derived marker store, attached by the
// caller that knows both the root and the store (the gateway's shared root
// opener and the mail-attachment save service), so the Library file
// operations re-key a file's marker when they move the file. The interface
// lives HERE, in the leaf, and is satisfied structurally by the store —
// pkg/library must not import its consumer (root.go's header; the save
// service below this leaf already imports this package).
//
// Every method is PREFIX-aware: it applies to the path itself and — because
// Rename/Delete/CopyInto accept directories — to every entry recorded
// beneath it. Matching is boundary-checked (p == rel or strings.HasPrefix(p,
// rel+"/")), so "a.html" never matches "a.html.bak".
//
// All methods are best-effort at the call sites: a marker re-key that fails
// after the file operation landed is logged loudly, never fails the file
// operation (the file has already moved; surfacing an error would describe a
// rename that did happen as one that did not). A marker the store cannot
// read fails safe to the stricter preview profile at SERVE time (§5.4's
// marker-unavailable rule), never to the script-permitting one.
type PathMarker interface {
	// MovePrefix re-keys every entry recorded at fromRel or beneath it to
	// the corresponding path under toRel — the rename/move provenance
	// obligation. A moved entry's allowance resets to scripts-off (the
	// shipped Move semantic: a new location starts un-answered).
	MovePrefix(fromRel, toRel string) error

	// CopyPrefix duplicates the SOURCE store's entries recorded at fromRel
	// or beneath it into the receiving store at toRel — the copy provenance
	// obligation (the copy is equally mail-derived; each copy's allowance is
	// its own and starts un-answered, per §5.4's per-file rule). The source
	// is passed explicitly so a copy across two stores (two workspaces) and
	// a copy within one compose through the same interface, with no type
	// assertion and no second mechanism.
	CopyPrefix(src PathMarker, fromRel, toRel string) error

	// DeletePrefix drops the entries recorded at rel or beneath it — the
	// delete provenance obligation. A missing entry is orphan-tolerant
	// (mirroring the Library's own delete tolerance); a store read failure
	// is returned, never guessed around.
	DeletePrefix(rel string) error

	// MailDerivedUnder lists the workspace-relative paths this store marks
	// mail-derived at prefix or beneath it, sorted. It is on the interface
	// because CopyPrefix is built on it: the source store's entries must be
	// readable through the same seam, whatever implements it.
	MailDerivedUnder(prefix string) ([]string, error)
}

// AttachPathMarker wires the store this Root's file operations will keep in
// step with the files. Nil-safe by construction: a Root with no attached
// marker (every call site that has not been wired, tests included) simply
// carries no provenance obligation, and the operation hooks below skip.
//
// Attaching is idempotent and cheap — the store is a stateless view over one
// index file, so re-attaching an equal store per request is the intended
// shape, not state to preserve.
func (r *Root) AttachPathMarker(m PathMarker) {
	if m == nil {
		return
	}
	r.marker = m
}

// markerMove re-keys markers after a successful rename/move. Best-effort:
// logged, never fails the file operation that already landed.
func markerMove(m PathMarker, fromRel, toRel string) {
	if m == nil {
		return
	}
	if err := m.MovePrefix(fromRel, toRel); err != nil {
		logger.WarnCF("library", "mail-derived marker re-key after rename failed; the file's scripts-off posture may be lost at the new path",
			map[string]any{"from": fromRel, "to": toRel, "error": err.Error()})
	}
}

// markerCopy duplicates markers after a successful CopyInto. Best-effort for
// the same reason as markerMove: the copy exists either way.
func markerCopy(src, dst PathMarker, fromRel, toRel string) {
	if src == nil || dst == nil {
		return
	}
	if err := dst.CopyPrefix(src, fromRel, toRel); err != nil {
		logger.WarnCF("library", "mail-derived marker copy after CopyInto failed; the copy may render under the ordinary profile",
			map[string]any{"from": fromRel, "to": toRel, "error": err.Error()})
	}
}

// markerDrop drops markers after a successful Delete. Best-effort: an entry
// left behind marks a path that no longer exists — harmless, never a scripts
// upgrade — so a failed drop is only noise; it is still logged.
func markerDrop(m PathMarker, rel string) {
	if m == nil {
		return
	}
	if err := m.DeletePrefix(rel); err != nil {
		logger.WarnCF("library", "mail-derived marker drop after delete failed; a stale entry may remain in the marker index",
			map[string]any{"path": rel, "error": err.Error()})
	}
}
