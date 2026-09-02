package skills

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
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

// ErrProjectWriteEscapesMount is returned by ResolveProjectSkillWriter when a
// project skill's resolved on-disk location does not actually lie within the
// mount root it claims to belong to — a tampered or otherwise inconsistent
// ProjectShelf entry. Defense in depth: every entry DiscoverProjectSkills /
// MergeProjectSkills produce already satisfies this by construction (they
// real-path-confine every candidate to the mount at discovery time, D6
// FR-077/078), so this only fires against a shelf built or mutated some
// other way.
var ErrProjectWriteEscapesMount = errors.New("project skill write escapes its mount root")

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

	// Derive metadata the same way the loader does at read time. The stable
	// identity is the slug (the directory/skill name = expectedName), which is
	// what info.validate() slug-checks via the ID field. The frontmatter `name`
	// is the human-readable DISPLAY name and is intentionally NOT slug-validated
	// nor required to equal the slug (e.g. slug "daily-briefing" → display name
	// "Daily Briefing").
	_, bodyDescription := extractMarkdownMetadata(body)
	info := SkillInfo{
		ID:          expectedName,
		Name:        expectedName,
		Description: bodyDescription,
	}

	if frontmatter != "" {
		// Reuse the loader's frontmatter parser to pull name/description.
		fm := (&SkillsLoader{}).parseSimpleYAML(frontmatter)
		if n := fm["name"]; n != "" {
			info.Name = n
		}
		// S59 fix: once the frontmatter DECLARES a `description` key at all —
		// even as `description:` (empty) or `description: null` — that
		// declaration is authoritative and must not be silently papered over
		// by the body's first paragraph. parseSimpleYAML's map drops
		// empty/null values entirely, so `fm["description"] != ""` alone
		// cannot distinguish "declared empty" from "not declared" — an
		// author who writes an empty `description:` field was previously let
		// through validation because info.Description still held whatever
		// text happened to follow the H1 heading in the body, which
		// ValidateSkillDescription then validated instead of the real
		// (empty) frontmatter value. Only fall back to the body-extracted
		// description when the frontmatter has no `description` key at all.
		if frontmatterHasKey(frontmatter, "description") {
			info.Description = fm["description"]
		}
	}

	if err := ValidateSkillDescription(expectedName, info.Description); err != nil {
		return fmt.Errorf("invalid SKILL.md: %w", err)
	}
	if err := info.validate(); err != nil {
		return fmt.Errorf("invalid SKILL.md: %w", err)
	}
	return nil
}

// ValidateSkillDescription enforces ADR-072 D2 / spec FR-010/FR-011/FR-012:
// once nothing loads automatically, a skill's one-line description is the
// ONLY thing the model sees before deciding whether to call it, so this is
// an authoring-time rule, not a hope. It rejects:
//
//   - an empty or whitespace-only description (FR-010);
//   - a description that merely restates the skill's own name/slug (FR-011),
//     under the EXACT comparison the spec names — case-fold both sides, then
//     strip every whitespace and punctuation rune, then test equality. This
//     is deliberately NOT fuzzy matching and NOT edit distance: a
//     description that adds real words ("Handles release notes" for slug
//     "release-notes") is a restatement of nothing and must be accepted —
//     only an exact echo, differing solely in case/spacing/punctuation
//     ("Release Notes", "release notes."), is rejected;
//   - a description exceeding MaxDescriptionLength characters (FR-012).
//
// Deliberately scoped to the AUTHORING path only (ValidateSkillMarkdown,
// called by CreateSkill/EditSkill) — NOT folded into SkillInfo.validate(),
// which also runs for skills merely discovered on disk (ListSkills,
// DiscoverProjectSkills). D2 states this is "enforced where skills are
// authored", not a retroactive check on every already-installed skill file;
// running the name-echo rule there too would risk silently hiding an
// already-installed skill whose description happens to echo its name, which
// is a availability regression this ADR never asked for.
func ValidateSkillDescription(skillName, description string) error {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return errors.New("description is required and must not be empty or whitespace-only")
	}
	if len(description) > MaxDescriptionLength {
		return fmt.Errorf("description exceeds the %d-character limit (got %d)", MaxDescriptionLength, len(description))
	}
	if skillName != "" && normalizeDescriptionForEcho(description) == normalizeDescriptionForEcho(skillName) {
		return fmt.Errorf(
			"description merely restates the skill's name %q — state WHEN to use the skill instead, "+
				"e.g. \"Use when the user asks to cut a release or publish notes\" rather than \"%s\"",
			skillName, skillName,
		)
	}
	return nil
}

// frontmatterHasKey reports whether the given YAML frontmatter block
// declares key at all, regardless of whether its value is empty, null, or
// non-empty. parseSimpleYAML's string-map result cannot make this
// distinction on its own (it drops empty/null values from the map), so
// ValidateSkillMarkdown uses this to tell "the author wrote an empty
// description:" (must fail validation) apart from "the author wrote no
// description key" (fall back to the body-extracted description, the
// existing legacy-compatibility behavior). Malformed YAML is treated as "key
// not present" — ValidateSkillMarkdown's own frontmatter parsing already
// tolerates a parse failure by falling back to zero values, and this must
// not behave more strictly than that fallback.
func frontmatterHasKey(frontmatter, key string) bool {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return false
	}
	_, ok := raw[key]
	return ok
}

// normalizeDescriptionForEcho implements FR-011's exact, non-fuzzy
// comparison: case-fold, then strip every whitespace and punctuation rune.
// No edit distance, no fuzzy matching — "Handles release notes" must NOT
// collapse to the same normalized string as "release-notes" (Dataset F row
// 10, deliberately accepted); only an actual restatement does.
func normalizeDescriptionForEcho(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
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
func (w *SkillWriter) EditSkill(
	name, content string,
	allowCreateOverride bool,
) (path string, createdOverride bool, err error) {
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

// RemoveSkill permanently deletes the named skill's whole directory — its
// SKILL.md and any .versions/ snapshots — from this writer's root. There is
// no undo. It returns ErrNotFound when no such skill exists locally in this
// root; unlike EditSkill, there is no "source"/override fallback, since there
// is nothing sensible to fall back to when the operation is a delete.
func (w *SkillWriter) RemoveSkill(name string) error {
	skillDir, err := w.resolveSkillDir(name)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(skillDir, "SKILL.md")); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNotFound
		}
		return fmt.Errorf("stat skill: %w", statErr)
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("remove skill: %w", err)
	}
	slog.Info("skills: removed skill", "name", name, "path", skillDir)
	return nil
}

// ResolveProjectSkillWriter resolves slug against a workspace's already-built
// project shelf (ADR-072 D6.1, FR-065/066/068) and, when it names a project
// skill, returns a SkillWriter rooted at that skill's OWN recognised skills
// directory inside the mount that owns it — never the central (global)
// skills root that NewSkillWriter is normally called with elsewhere. Every
// write or removal performed through the returned writer therefore lands
// directly in the project's own repository (D6.1: "the write goes into that
// project's own file... it does not fork a copy into the central registry")
// and never anywhere else — there is no code path here that can also touch a
// central-library copy.
//
// Before constructing anything, the resolved skill's location is confined to
// its claimed mount root using the same lexical-containment rule
// resolveSkillDir applies to every ordinary write (FR-068): a shelf entry
// whose Path does not actually lie under its own MountRoot is refused with
// ErrProjectWriteEscapesMount rather than silently handed a writer that could
// touch something outside the mount. Every legitimate shelf entry (built by
// DiscoverProjectSkills / MergeProjectSkills) already satisfies this, since
// discovery itself real-path-confines every candidate to the mount before
// admitting it (FR-077/078) — this is a second, independent check against a
// shelf built or hand-edited some other way, not a load-bearing path for the
// happy case.
//
// ResolveProjectSkillWriter returns ErrNotFound when slug is not present on
// this project shelf — callers use that to fall through to the ordinary
// (global-root) authoring path for a registry, builtin, or brand-new skill.
// It resolves an EXISTING project skill only; authoring a brand-new project
// skill from nothing is out of scope (D6.1's own scenarios are "editing"/
// "removing" a project skill that is already discovered on the shelf).
func ResolveProjectSkillWriter(shelf ProjectShelf, slug string) (*SkillWriter, ProjectSkill, error) {
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" || shelf == nil {
		return nil, ProjectSkill{}, ErrNotFound
	}
	ps, ok := shelf[strings.ToLower(trimmed)]
	if !ok {
		return nil, ProjectSkill{}, ErrNotFound
	}

	skillDir := filepath.Dir(filepath.Clean(ps.Path)) // .../<recognised-dir>/<slug>
	recognisedDir := filepath.Dir(skillDir)           // .../<recognised-dir>
	mountRoot := filepath.Clean(ps.MountRoot)

	rel, relErr := filepath.Rel(mountRoot, recognisedDir)
	if relErr != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, ProjectSkill{}, ErrProjectWriteEscapesMount
	}

	return NewSkillWriter(recognisedDir), ps, nil
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
