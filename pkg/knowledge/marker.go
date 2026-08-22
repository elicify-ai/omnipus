// Marker: the knowledge base's own identity on disk (FR-022, FR-023, FR-024).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/library"
)

const (
	// MarkerDirName is the directory Omnipus writes at a knowledge base's root
	// to claim it as one (FR-022). It is DECIDED, not provisional (ADR-067 D1),
	// and is deliberately distinct from ".omnipus/", which already names the
	// workspace memory-room directory (pkg/workspace/instructions.go) — one
	// identifier must not mean two things across the product.
	MarkerDirName = ".omnipus-vault"

	// ObsidianMarkerDirName is Obsidian's own configuration directory. Omnipus
	// READS its presence as a detection signal (FR-020) and NEVER creates it
	// (FR-023). The reasoning for that asymmetry is in this package's doc.go —
	// it is intentional and must not be "tidied up" into symmetry.
	ObsidianMarkerDirName = ".obsidian"

	// markerFileName is the JSON document inside MarkerDirName that carries the
	// knowledge base's identity.
	markerFileName = "vault.json"

	// DefaultTemplatesDirName is where note templates live when the marker does
	// not name somewhere else: .omnipus-vault/templates/ (ADR-067 D2, D12).
	DefaultTemplatesDirName = "templates"

	// markerDirPerm / markerFilePerm: the marker is Omnipus state living inside
	// the operator's own folder. No other process needs to read it, and it will
	// grow to hold collection settings, so it is created owner-only — the same
	// posture the index gets under FR-032. This is a decision of this package,
	// not a clause of the spec; the spec's 0700/0600 requirement is about the
	// index, which lives elsewhere.
	markerDirPerm  fs.FileMode = 0o700
	markerFilePerm fs.FileMode = 0o600
)

var (
	// ErrNotKnowledgeBase is returned when a path that must be a knowledge base
	// is not one — no .omnipus-vault/ and no .obsidian/ at its root.
	ErrNotKnowledgeBase = errors.New("knowledge: folder is not a knowledge base")

	// ErrNoMarker is returned when a folder is a knowledge base by detection
	// (it has .obsidian/) but carries no Omnipus marker, so it has no Omnipus
	// display name or template location yet.
	ErrNoMarker = errors.New("knowledge: no " + MarkerDirName + "/" + markerFileName)

	// ErrMarkerInvalid is returned for a marker that exists but cannot be
	// trusted: unparseable JSON, an empty display name, a templates location
	// that points outside the marker directory. It is deliberately an error
	// rather than a silent fallback to defaults — a collection whose identity
	// we cannot read must say so, not quietly rename itself.
	ErrMarkerInvalid = errors.New("knowledge: marker is invalid")

	// ErrAlreadyKnowledgeBase is returned by CreateInWorkspace when the target
	// folder already carries an Omnipus marker.
	ErrAlreadyKnowledgeBase = errors.New("knowledge: folder is already an Omnipus knowledge base")
)

// Marker is a knowledge base's identity as stored at its root, in
// .omnipus-vault/vault.json.
//
// It holds a display name and a templates location, and NOTHING else
// (ADR-067 D2 — revision 1's speculative "settings" payload was cut because no
// requirement depended on it). Every value in it is RELATIVE or a plain string:
// nothing here may be an absolute path, because the marker travels with the
// folder. That is what makes FR-024's real requirement — "move the folder,
// re-mount it elsewhere, it is still called Research, with no migration step" —
// true by construction rather than by a migration routine nobody wrote.
//
// Marker contents are OPERATOR DATA, not Omnipus configuration (ADR-067 D1):
// read as data, never executed, never interpolated into a prompt unescaped.
type Marker struct {
	// DisplayName is what the operator calls this collection.
	DisplayName string `json:"display_name"`

	// TemplatesDir is where note templates live, as a slash-separated path
	// RELATIVE to the marker directory (.omnipus-vault/). Empty means
	// DefaultTemplatesDirName.
	TemplatesDir string `json:"templates_dir,omitempty"`
}

// Validate reports whether m can be written or trusted after reading.
//
// It enforces two things and no more:
//
//   - a display name that is present and printable. Empty is rejected because a
//     nameless collection has no identity to survive relocation (FR-024), and
//     C0 control characters are rejected because this string reaches log lines
//     and HTTP headers, where a CR or LF is never acceptable.
//   - a templates location that stays inside the marker directory. It is
//     validated with the same library.CleanRelPath every Library path goes
//     through, so "../../etc", "/etc" and a backslash-bearing name are refused
//     identically here and there.
func (m Marker) Validate() error {
	name := strings.TrimSpace(m.DisplayName)
	if name == "" {
		return fmt.Errorf("%w: display_name is empty", ErrMarkerInvalid)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: display_name is not valid UTF-8", ErrMarkerInvalid)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: display_name contains control character %#U", ErrMarkerInvalid, r)
		}
	}
	if m.TemplatesDir != "" {
		cleaned, err := library.CleanRelPath(m.TemplatesDir)
		if err != nil {
			return fmt.Errorf("%w: templates_dir %q: %v", ErrMarkerInvalid, m.TemplatesDir, err)
		}
		if cleaned == "" {
			return fmt.Errorf("%w: templates_dir %q resolves to the marker directory itself", ErrMarkerInvalid, m.TemplatesDir)
		}
	}
	return nil
}

// templatesRel returns the marker-relative templates path, defaulted.
func (m Marker) templatesRel() string {
	if strings.TrimSpace(m.TemplatesDir) == "" {
		return DefaultTemplatesDirName
	}
	cleaned, err := library.CleanRelPath(m.TemplatesDir)
	if err != nil || cleaned == "" {
		// Unreachable for a Validate()d marker; kept so a future caller that
		// skips Validate cannot turn a bad value into an escaping path.
		return DefaultTemplatesDirName
	}
	return cleaned
}

// MarkerDir returns the absolute path of the Omnipus marker directory for a
// knowledge base rooted at root.
func MarkerDir(root string) string { return filepath.Join(root, MarkerDirName) }

// MarkerPath returns the absolute path of the marker document itself.
func MarkerPath(root string) string { return filepath.Join(MarkerDir(root), markerFileName) }

// TemplatesPath returns the absolute path of the templates directory for a
// knowledge base rooted at root, given its marker.
func TemplatesPath(root string, m Marker) string {
	return filepath.Join(MarkerDir(root), filepath.FromSlash(m.templatesRel()))
}

// ReadMarker reads and validates the Omnipus marker at root.
//
// Returns ErrNoMarker when the folder carries no marker document — which is the
// ordinary case for an Obsidian vault Omnipus has never written to, NOT an
// error condition for detection. Detection never calls this: it decides on
// directory entries alone (FR-021).
func ReadMarker(root string) (Marker, error) {
	raw, err := os.ReadFile(MarkerPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Marker{}, fmt.Errorf("%w at %s", ErrNoMarker, root)
		}
		return Marker{}, fmt.Errorf("knowledge: read marker at %s: %w", root, err)
	}
	var m Marker
	if err := json.Unmarshal(raw, &m); err != nil {
		// Loud, not defaulted. A corrupt marker that silently became
		// {DisplayName: ""} would rename the operator's collection to nothing
		// and report success.
		return Marker{}, fmt.Errorf("%w: parse %s: %v", ErrMarkerInvalid, MarkerPath(root), err)
	}
	if err := m.Validate(); err != nil {
		return Marker{}, err
	}
	return m, nil
}

// writeMarkerInto writes the marker document and the templates directory beneath
// an already-open, containment-checked root directory handle.
//
// dirRel is the knowledge base's path relative to r, in slash form ("" means r
// itself). Every path this function touches is created THROUGH r, so it cannot
// escape r even if dirRel were hostile — os.Root enforces that at the syscall
// boundary rather than by string inspection.
//
// It writes MarkerDirName and never ObsidianMarkerDirName (FR-022, FR-023).
// There is deliberately no code path in this package that creates
// ObsidianMarkerDirName; see doc.go.
func writeMarkerInto(r *os.Root, dirRel string, m Marker) error {
	if err := m.Validate(); err != nil {
		return err
	}
	markerRel := path.Join(dirRel, MarkerDirName)
	if err := r.MkdirAll(markerRel, markerDirPerm); err != nil {
		return fmt.Errorf("knowledge: create %s: %w", MarkerDirName, err)
	}
	if err := r.MkdirAll(path.Join(markerRel, m.templatesRel()), markerDirPerm); err != nil {
		return fmt.Errorf("knowledge: create templates directory: %w", err)
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("knowledge: encode marker: %w", err)
	}
	encoded = append(encoded, '\n')
	f, err := r.OpenFile(path.Join(markerRel, markerFileName), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, markerFilePerm)
	if err != nil {
		return fmt.Errorf("knowledge: create marker document: %w", err)
	}
	if _, err := f.Write(encoded); err != nil {
		_ = f.Close()
		return fmt.Errorf("knowledge: write marker document: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("knowledge: close marker document: %w", err)
	}
	return nil
}
