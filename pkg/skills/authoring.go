package skills

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// MaxSkillMarkdownBytes caps the size of a SKILL.md body accepted by the
// authoring tools. Skills are prompt fragments; an oversize file is almost
// always a mistake or an attempt to bloat the context window. 256 KiB is far
// larger than any legitimate skill while still bounding memory and prompt cost.
const MaxSkillMarkdownBytes = 256 * 1024

// versionsDir is the per-skill subdirectory that holds prior SKILL.md snapshots.
const versionsDir = ".versions"

// ErrPathConfinement is returned when a requested skill name would escape the
// skills root (path traversal). The authoring layer is path-confined to a
// single skills directory (FR-9.2 / M-6).
var ErrPathConfinement = errors.New("skill name escapes the skills directory")

// ErrOversize is returned when a SKILL.md body exceeds MaxSkillMarkdownBytes.
var ErrOversize = errors.New("SKILL.md exceeds maximum allowed size")

// ErrAlreadyExists is returned by CreateSkill when a skill of the same name
// already exists in the target root.
var ErrAlreadyExists = errors.New("skill already exists")

// ErrNotFound is returned by EditSkill / ListVersions when no source skill
// exists to edit.
var ErrNotFound = errors.New("skill not found")

// SkillWriter writes and versions skills under a single, fixed skills root
// directory. All writes are path-confined to that root, validated against the
// SKILL.md contract, and snapshotted before any overwrite so a prior version is
// always recoverable (FR-9.2).
//
// Built-in skills are NOT writable through a SkillWriter rooted at the builtin
// directory — the authoring tools construct a SkillWriter rooted at the global
// (user) skills dir, so editing a built-in always produces a user override and
// never mutates the shipped built-in in place.
type SkillWriter struct {
	root string
}

// NewSkillWriter returns a SkillWriter rooted at root. root is the directory
// that holds one subdirectory per skill (e.g. ~/.omnipus/skills).
func NewSkillWriter(root string) *SkillWriter {
	return &SkillWriter{root: filepath.Clean(root)}
}

// Root returns the confinement root for this writer.
func (w *SkillWriter) Root() string { return w.root }

// resolveSkillDir validates name and returns the absolute, confined path to the
// skill's directory under the writer's root. It rejects empty names, names that
// fail the skill name pattern, and any name that resolves outside root (path
// traversal via "..", absolute paths, or separators).
func (w *SkillWriter) resolveSkillDir(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("skill name is required")
	}
	// Reject anything that is not a single, well-formed skill name. This bars
	// "..", "/", "\\", absolute paths, and null bytes before we ever touch the
	// filesystem.
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid skill name %q: must be alphanumeric with hyphens", name)
	}
	if len(name) > MaxNameLength {
		return "", fmt.Errorf("skill name exceeds %d characters", MaxNameLength)
	}

	dir := filepath.Join(w.root, name)
	// Defense in depth: confirm the joined path is still inside root.
	rel, err := filepath.Rel(w.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrPathConfinement
	}
	return dir, nil
}

// ValidateSkillMarkdown checks that content is a well-formed SKILL.md: it is
// within the size limit, has a parseable frontmatter/body, and yields a
// name+description that satisfy the loader's validation rules. The expectedName
// (the skill's directory name) is used as the fallback name when the body has
// no usable title, mirroring SkillsLoader.getSkillMetadata.
//
// This reuses the loader's metadata extraction and SkillInfo.validate so the
// authoring path enforces exactly the same contract as the load path.
func ValidateSkillMarkdown(expectedName, content string) error {
	if len(content) > MaxSkillMarkdownBytes {
		return ErrOversize
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("SKILL.md is empty")
	}

	frontmatter, body := splitFrontmatter(content)

	// Derive metadata the same way the loader does at read time.
	title, bodyDescription := extractMarkdownMetadata(body)
	info := SkillInfo{
		Name:        expectedName,
		Description: bodyDescription,
	}
	if title != "" && namePattern.MatchString(title) && len(title) <= MaxNameLength {
		info.Name = title
	}

	if frontmatter != "" {
		// Reuse the loader's frontmatter parser to pull name/description.
		fm := (&SkillsLoader{}).parseSimpleYAML(frontmatter)
		if n := fm["name"]; n != "" {
			info.Name = n
		}
		if d := fm["description"]; d != "" {
			info.Description = d
		}
	}

	if err := info.validate(); err != nil {
		return fmt.Errorf("invalid SKILL.md: %w", err)
	}
	// The frontmatter name (when present) must match the directory/skill name so
	// a skill cannot masquerade under a different identity than its location.
	if frontmatter != "" {
		fm := (&SkillsLoader{}).parseSimpleYAML(frontmatter)
		if n := strings.TrimSpace(fm["name"]); n != "" && expectedName != "" && !strings.EqualFold(n, expectedName) {
			return fmt.Errorf("invalid SKILL.md: frontmatter name %q does not match skill name %q", n, expectedName)
		}
	}
	return nil
}

// snapshotExisting copies the current SKILL.md (if any) into the skill's
// .versions/ directory under a timestamped filename, so the prior version is
// recoverable after an overwrite. Returns the snapshot path ("" if there was
// nothing to snapshot).
func snapshotExisting(skillDir string) (string, error) {
	current := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(current)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read current SKILL.md for snapshot: %w", err)
	}

	vdir := filepath.Join(skillDir, versionsDir)
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return "", fmt.Errorf("create versions dir: %w", err)
	}
	// Nanosecond UTC timestamp keeps snapshots ordered and unique.
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	snapPath := filepath.Join(vdir, stamp+".SKILL.md")
	if err := fileutil.WriteFileAtomic(snapPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write snapshot: %w", err)
	}
	return snapPath, nil
}

// CreateSkill writes a brand-new skill named name with the given SKILL.md
// content. It fails with ErrAlreadyExists if the skill already exists in this
// writer's root (use EditSkill to modify an existing skill). The write is
// path-confined and validated before anything is persisted.
func (w *SkillWriter) CreateSkill(name, content string) (string, error) {
	skillDir, err := w.resolveSkillDir(name)
	if err != nil {
		return "", err
	}
	if err := ValidateSkillMarkdown(name, content); err != nil {
		return "", err
	}
	if _, statErr := os.Stat(filepath.Join(skillDir, "SKILL.md")); statErr == nil {
		return "", ErrAlreadyExists
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat skill: %w", statErr)
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := fileutil.WriteFileAtomic(skillFile, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write SKILL.md: %w", err)
	}
	slog.Info("skills: created skill", "name", name, "path", skillFile)
	return skillFile, nil
}

// EditSkill updates an existing skill named name with new SKILL.md content. The
// prior version is snapshotted into .versions/ first so it can be rolled back.
//
// If a skill named name does not yet exist in this writer's root but a source
// (e.g. a built-in) is supplied via sourceContent, EditSkill creates a user
// override seeded from the new content — it NEVER mutates the source in place.
// When sourceContent is empty and the skill does not exist locally, EditSkill
// returns ErrNotFound.
//
// The write is path-confined and validated before anything is persisted. The
// returned bool reports whether the write created a new override (true) versus
// edited an already-local skill (false).
func (w *SkillWriter) EditSkill(name, content string, allowCreateOverride bool) (path string, createdOverride bool, err error) {
	skillDir, derr := w.resolveSkillDir(name)
	if derr != nil {
		return "", false, derr
	}
	if verr := ValidateSkillMarkdown(name, content); verr != nil {
		return "", false, verr
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	_, statErr := os.Stat(skillFile)
	localExists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return "", false, fmt.Errorf("stat skill: %w", statErr)
	}

	if !localExists && !allowCreateOverride {
		return "", false, ErrNotFound
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", false, fmt.Errorf("create skill dir: %w", err)
	}

	// Snapshot the current local SKILL.md (if any) before overwriting.
	if localExists {
		if _, snapErr := snapshotExisting(skillDir); snapErr != nil {
			return "", false, snapErr
		}
	}

	if err := fileutil.WriteFileAtomic(skillFile, []byte(content), 0o644); err != nil {
		return "", false, fmt.Errorf("write SKILL.md: %w", err)
	}
	createdOverride = !localExists
	slog.Info("skills: edited skill", "name", name, "path", skillFile, "created_override", createdOverride)
	return skillFile, createdOverride, nil
}

// ListVersions returns the snapshot filenames stored under the skill's
// .versions/ directory, newest first. An empty slice (and nil error) is
// returned when the skill has no snapshots yet.
func (w *SkillWriter) ListVersions(name string) ([]string, error) {
	skillDir, err := w.resolveSkillDir(name)
	if err != nil {
		return nil, err
	}
	vdir := filepath.Join(skillDir, versionsDir)
	entries, err := os.ReadDir(vdir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read versions dir: %w", err)
	}
	versions := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".SKILL.md") {
			versions = append(versions, e.Name())
		}
	}
	// Timestamped names sort lexicographically in chronological order; reverse
	// for newest-first.
	for i, j := 0, len(versions)-1; i < j; i, j = i+1, j-1 {
		versions[i], versions[j] = versions[j], versions[i]
	}
	return versions, nil
}

// ReadVersion returns the content of a specific snapshot file (as returned by
// ListVersions) for the named skill. The snapshot filename is validated to
// prevent traversal out of the .versions/ directory.
func (w *SkillWriter) ReadVersion(name, snapshot string) (string, error) {
	skillDir, err := w.resolveSkillDir(name)
	if err != nil {
		return "", err
	}
	if snapshot == "" || strings.ContainsAny(snapshot, "/\\") || strings.Contains(snapshot, "..") {
		return "", fmt.Errorf("invalid snapshot name %q", snapshot)
	}
	data, err := os.ReadFile(filepath.Join(skillDir, versionsDir, snapshot))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read snapshot: %w", err)
	}
	return string(data), nil
}
