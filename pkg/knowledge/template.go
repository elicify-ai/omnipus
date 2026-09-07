// Templates: the shape a new note starts from (ADR-067 D12, FR-100..FR-102).
//
// A collection's templates are the operator's own files, living in the marker
// directory (.omnipus-vault/templates/ by default, or wherever the marker says
// — marker.go already resolves that). This file does three things with them and
// nothing else: list them, read one, and expand its placeholders.
//
// # Templates are DATA, and this file is not an interpreter
//
// ADR-067 D1 is explicit that marker contents are "operator data, not Omnipus
// configuration: read as data, never executed". FR-102 turns that into a
// requirement with a boundary — "MUST substitute only a fixed documented
// placeholder set and MUST NOT execute template content".
//
// So expansion here is a single left-to-right textual pass over four literal
// tokens. There is no expression language, no format string, no conditional, no
// include, no shell-out, and deliberately no way to add one without changing
// this file. In particular:
//
//   - An unknown "{{whatever}}" is left EXACTLY as written. It is not blanked,
//     because blanking silently deletes something the operator typed, and a
//     template that quietly loses a line is the same class of failure as a
//     write that quietly loses a note.
//   - Templater syntax ("<% tp.file.title %>"), shell syntax ("$(id)",
//     "${HOME}") and anything else instruction-shaped stays literal. Omnipus
//     does not own those syntaxes and must not half-implement them.
//   - Expansion never re-scans what it substituted. A note titled "{{date}}"
//     produces the literal text "{{date}}" in the body, not today's date. A
//     single pass is what makes that true by construction rather than by a
//     recursion depth limit that someone later raises.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/library"
)

// Template placeholder tokens — THE fixed, documented set FR-102 requires.
//
// Adding a member here is a product decision, not a refactor: every token in
// this list is a piece of Omnipus state that a template author can pull into a
// file Omnipus then writes into the operator's collection. Four is what US-12
// AS-4 needs (frontmatter and structure that a schema will accept); anything
// beyond it needs a requirement first.
const (
	// TemplateVarTitle is replaced by the new note's title — its filename
	// stem, unless the caller supplies one explicitly.
	TemplateVarTitle = "{{title}}"

	// TemplateVarDate is replaced by the creation date, ISO 8601 (2006-01-02).
	TemplateVarDate = "{{date}}"

	// TemplateVarTime is replaced by the creation time of day, 24-hour (15:04).
	TemplateVarTime = "{{time}}"

	// TemplateVarDateTime is replaced by the creation instant, RFC 3339.
	TemplateVarDateTime = "{{datetime}}"
)

// Layouts for the three time-derived placeholders. They are constants rather
// than caller-supplied formats on purpose: a caller-supplied layout is a tiny
// interpreter, and FR-102 exists to keep this from becoming one.
const (
	templateDateLayout     = "2006-01-02"
	templateTimeLayout     = "15:04"
	templateDateTimeLayout = time.RFC3339
)

var (
	// ErrTemplateNotFound means the collection has no template under that
	// name. It is a distinct sentinel from a read failure because the two
	// need different operator advice: "pick another template" versus "your
	// templates directory is unreadable".
	ErrTemplateNotFound = errors.New("knowledge: template not found")

	// ErrTemplateNameRefused means the supplied template name is not a
	// relative path inside the collection's templates directory — a
	// traversal, an absolute path, or a name that resolves out through a
	// symlink. Refused, never clamped: a template read that silently became
	// a read of some other file would put that file's contents into a note.
	ErrTemplateNameRefused = errors.New("knowledge: template name refused")
)

// TemplateVars carries the values the fixed placeholder set is substituted
// from. Every field is data supplied by the caller; nothing here is read from
// the environment at expansion time, so expanding the same template with the
// same vars is deterministic and testable without a clock.
type TemplateVars struct {
	// Title is what {{title}} becomes. Empty is legal and yields an empty
	// substitution — the caller (CreateNote) defaults it to the note's
	// filename stem before it gets here.
	Title string

	// Now is the instant {{date}}, {{time}} and {{datetime}} are rendered
	// from. A zero value means "no clock was supplied": the three
	// time-derived tokens are then left LITERAL rather than rendered from
	// the zero time, because "0001-01-01" in an operator's note is a wrong
	// answer presented as a right one, while "{{date}}" is visibly
	// unexpanded.
	Now time.Time
}

// TemplatePlaceholders returns the fixed documented placeholder set, in the
// order it is applied. Exported so a settings surface can show the operator
// exactly what is substituted, and so a test can assert the set has not grown
// silently.
func TemplatePlaceholders() []string {
	return []string{TemplateVarTitle, TemplateVarDate, TemplateVarTime, TemplateVarDateTime}
}

// ExpandTemplate substitutes the fixed placeholder set in src and returns the
// result. Everything that is not one of those exact tokens is copied through
// byte for byte.
//
// The substitution is a SINGLE left-to-right pass: output already written is
// never re-examined, so a substituted value containing "{{date}}" stays that
// text. See this file's header for why that is a requirement and not an
// implementation detail.
func ExpandTemplate(src []byte, vars TemplateVars) []byte {
	if len(src) == 0 {
		return append([]byte(nil), src...)
	}
	repl := templateReplacements(vars)
	out := make([]byte, 0, len(src))
	s := string(src)
	for i := 0; i < len(s); {
		// "{{" is the only thing that can begin a placeholder, so the
		// common case is a plain byte copy.
		if s[i] != '{' || i+1 >= len(s) || s[i+1] != '{' {
			out = append(out, s[i])
			i++
			continue
		}
		matched := false
		for _, token := range TemplatePlaceholders() {
			if strings.HasPrefix(s[i:], token) {
				if value, ok := repl[token]; ok {
					out = append(out, value...)
					i += len(token)
					matched = true
				}
				break
			}
		}
		if !matched {
			// Either an unknown "{{…}}" or a known token with no value
			// available (a zero clock). Copy the opening brace and carry
			// on from the next byte, leaving the whole run literal.
			out = append(out, s[i])
			i++
		}
	}
	return out
}

// templateReplacements maps each supported token to its value. A token absent
// from the map is left literal by ExpandTemplate.
func templateReplacements(vars TemplateVars) map[string]string {
	repl := map[string]string{TemplateVarTitle: vars.Title}
	if !vars.Now.IsZero() {
		repl[TemplateVarDate] = vars.Now.Format(templateDateLayout)
		repl[TemplateVarTime] = vars.Now.Format(templateTimeLayout)
		repl[TemplateVarDateTime] = vars.Now.Format(templateDateTimeLayout)
	}
	return repl
}

// TemplateInfo describes one template available in a collection.
type TemplateInfo struct {
	// Name is the template's name relative to the templates directory, in
	// slash form. It is what ReadTemplate and CreateNote take.
	Name string

	// AbsPath is the template file's absolute path. It exists so a settings
	// surface can open the file for editing (FR-101) — the operator edits
	// their template as a file, not through a lossy round trip.
	AbsPath string

	// Size is the template's size in bytes.
	Size int64
}

// ListTemplates returns the collection's templates, sorted by name.
//
// FR-101 — "template surfaces reachable without enabling hidden files" — is
// satisfied structurally rather than by a flag: the templates directory is
// found through the MARKER, not by listing the collection and un-hiding
// dotfiles, so there is no includeHidden parameter here to get wrong. A caller
// that has a Collection can always reach its templates; the Library's dotfile
// filter never enters the picture.
//
// A collection with no templates directory yet is NOT an error — that is the
// ordinary state of an Obsidian vault Omnipus has never written to, and of a
// brand-new knowledge base. It returns an empty list.
//
// Only regular files at the top level are listed. A symlink is skipped rather
// than followed, matching FR-044's treatment everywhere else in this package:
// a template symlinked to /etc/passwd must not become a note's contents.
func ListTemplates(fsys LinkFS, c *Collection) ([]TemplateInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil collection", ErrCollectionRootInvalid)
	}
	dir := c.TemplatesDir()
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("knowledge: list templates in %s: %w", dir, err)
	}
	out := make([]TemplateInfo, 0, len(entries))
	for _, de := range entries {
		if !de.Type().IsRegular() {
			continue
		}
		info, statErr := de.Info()
		var size int64
		if statErr == nil {
			size = info.Size()
		}
		out = append(out, TemplateInfo{
			Name:    de.Name(),
			AbsPath: filepath.Join(dir, de.Name()),
			Size:    size,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ReadTemplate returns one template's raw bytes, unexpanded.
//
// name is relative to the templates directory. It goes through the SAME
// containment gate every other path in this package goes through
// (CollectionRoot.ResolveContained in contain.go) rather than a second,
// template-specific check: "../../../.ssh/id_rsa", "/etc/passwd" and a
// symlinked template all have to be refused, and they are refused by the code
// that already knows how, at the point where symlinks have been resolved.
func ReadTemplate(fsys LinkFS, c *Collection, name string) ([]byte, error) {
	abs, err := templatePath(fsys, c, name)
	if err != nil {
		return nil, err
	}
	f, err := fsys.Open(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q", ErrTemplateNotFound, name)
		}
		return nil, fmt.Errorf("knowledge: read template %q: %w", name, err)
	}
	defer func() { _ = f.Close() }()

	// A template is a note-shaped file the operator maintains by hand; it
	// has no business being enormous, and reading an unbounded amount here
	// would let a stray multi-gigabyte file in the templates directory
	// become a memory event during note creation.
	limited := io.LimitReader(f, maxTemplateBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("knowledge: read template %q: %w", name, err)
	}
	if int64(len(raw)) > maxTemplateBytes {
		return nil, fmt.Errorf("knowledge: template %q is larger than %d bytes", name, maxTemplateBytes)
	}
	return raw, nil
}

// maxTemplateBytes caps one template. Notes themselves have NO size cap
// (FR-034a) and this is not one: it bounds a hand-written template, which is a
// different thing from an operator's note.
const maxTemplateBytes int64 = 1 << 20 // 1 MiB

// templatePath resolves a template name to a contained absolute path without
// reading it.
func templatePath(fsys LinkFS, c *Collection, name string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("%w: nil collection", ErrCollectionRootInvalid)
	}
	cleaned, err := library.CleanRelPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrTemplateNameRefused, name, err)
	}
	if cleaned == "" {
		return "", fmt.Errorf("%w: %q names the templates directory, not a template", ErrTemplateNameRefused, name)
	}
	root, err := NewCollectionRoot(fsys, c.Root())
	if err != nil {
		return "", err
	}
	rel := path.Join(MarkerDirName, c.Marker().templatesRel(), cleaned)

	// The link check comes FIRST, on the unresolved spelling, because
	// ResolveContained returns the path with every symlink already resolved —
	// by the time it has answered, a symlink to an in-collection file is
	// indistinguishable from that file. FR-044 skips symlinks everywhere in
	// this package, whether or not their target happens to be contained, so
	// the refusal has to be made where the link is still visible. lstat,
	// never stat: following it here would defeat the point.
	lexical := filepath.Join(root.Path(), filepath.FromSlash(rel))
	if li, lerr := fsys.Lstat(lexical); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %q is a symbolic link", ErrTemplateNameRefused, name)
	}

	abs, err := root.ResolveContained(fsys, rel)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrTemplateNameRefused, name, err)
	}
	fi, err := fsys.Lstat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %q", ErrTemplateNotFound, name)
		}
		return "", fmt.Errorf("knowledge: stat template %q: %w", name, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %q is not a regular file", ErrTemplateNameRefused, name)
	}
	return abs, nil
}
