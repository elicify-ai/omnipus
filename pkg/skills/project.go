package skills

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Recognised project-skills directory names (ADR-072 D6, FR-036/FR-037): a
// mount's own ".claude/skills/" and ".omnipus/skills/", each holding
// "<slug>/SKILL.md". No other location or heuristic counts — specifically no
// ".git" presence check and no content sniffing (FR-037, FR-038). Ours wins a
// same-slug clash between the two (D6: "ours winning a slug clash, consistent
// with the existing shelf precedence").
const (
	omnipusProjectSkillsDir = ".omnipus/skills"
	claudeProjectSkillsDir  = ".claude/skills"
)

// projectSkillsDirs is iterated in this fixed order so that, on a same-slug
// clash between the two recognised directories within one mount, the first
// match (.omnipus/skills) is kept and the second is recorded as a collision
// rather than silently overwriting it (D6, Dataset C row 3).
var projectSkillsDirs = []string{omnipusProjectSkillsDir, claudeProjectSkillsDir}

// ProjectMount is the narrow view of a workspace mount that project-skill
// discovery needs: its operator-chosen name (stable, unique within a
// workspace per workspace.ErrMountNameCollision, and the ordering key for
// FR-029/D4.2) and its root directory. Deliberately independent of
// pkg/workspace.Mount so this package's shelf-resolution model has no import
// dependency on it and is testable on its own.
type ProjectMount struct {
	Name string
	Root string
}

// ProjectSkill is a skill discovered under a mount's own recognised skills
// directory (ADR-072 D6, shelf 1 of D4.1's table). It embeds SkillInfo so it
// composes with the rest of this package's skill-info-shaped APIs, and
// additionally names the mount it came from — required to explain a
// cross-mount collision (D4.2) and to confine an authoring write to the
// owning mount (D6.1, a later phase's concern).
type ProjectSkill struct {
	SkillInfo
	MountName string
	MountRoot string
}

// ProjectShelf is a workspace's already-merged project shelf (D4.1 rank 1):
// every project skill visible to any agent acting in that workspace, keyed by
// case-folded slug. Build one with MergeProjectSkills.
type ProjectShelf map[string]ProjectSkill

// CollisionLocation names one of the competing locations behind a recorded
// slug collision (D4.2: "the collision is logged with both paths").
type CollisionLocation struct {
	// Description is a short human-readable label for where this competitor
	// came from, e.g. "mount alpha" or ".omnipus/skills".
	Description string
	Path        string
}

// SlugCollision records a project-skill slug that resolved ambiguously —
// either between the two recognised directories inside one mount, or between
// two mounts on the same workspace — so the resolution is auditable rather
// than silently picked (D4.2: "log/record the collision, don't silently pick
// one"; D6.1.1's "audit the doorway" principle applied to discovery).
type SlugCollision struct {
	Slug string
	// WinnerDescription names the competitor that was kept, matching one of
	// Locations' Description values.
	WinnerDescription string
	Locations         []CollisionLocation
}

// DiscoverProjectSkills scans a single mount for project skills under its
// recognised skills directories (D6, FR-036..038). No other project-detection
// heuristic is applied — specifically no ".git" check and no file-content
// sniffing; a skill's existence is decided solely by
// "<recognised-dir>/<slug>/SKILL.md" being present.
//
// Symlink confinement (FR-077/FR-078, MAJ-005 — Dataset C rows 9, 11, 12):
// every candidate is resolved to its real (symlink-free) path and refused
// when that real path lies outside the mount's own real root. This is
// applied independently at two levels, because either can be true without
// the other:
//   - directory-level (row 9): the recognised skills directory itself is a
//     symlink pointing outside the mount — the whole directory is skipped,
//     nothing under it is discovered.
//   - file-level (row 11): the skills directory is real and in-mount, but one
//     slug's own SKILL.md is a symlink pointing outside the mount — only that
//     one skill is skipped, its siblings are unaffected.
//
// A SKILL.md that is a symlink to another file INSIDE the same mount (row 12)
// resolves inside the mount root and is discovered normally.
func DiscoverProjectSkills(mountName, mountRoot string) ([]ProjectSkill, []SlugCollision) {
	mountRootReal, err := filepath.EvalSymlinks(mountRoot)
	if err != nil {
		// A mount whose own root cannot be resolved (missing, broken) has
		// nothing to discover. This is not logged as a warning here — mount
		// existence is a precondition enforced by the mount store itself, not
		// this discovery step's concern.
		return nil, nil
	}
	mountRootReal = filepath.Clean(mountRootReal)

	found := make(map[string]ProjectSkill) // slug (case-folded) -> winning skill
	wonBy := make(map[string]string)       // slug (case-folded) -> winning dir's description
	var collisions []SlugCollision

	for _, dirRel := range projectSkillsDirs {
		dirPath := filepath.Join(mountRoot, filepath.FromSlash(dirRel))
		if _, ok := realWithinRoot(mountRootReal, dirPath); !ok {
			// Missing, or a symlink (direct or via any parent component)
			// resolving outside the mount — the whole directory is refused,
			// nothing under it is even listed.
			continue
		}

		entries, readErr := os.ReadDir(dirPath)
		if readErr != nil {
			if !os.IsNotExist(readErr) {
				slog.Warn("skills: failed to read project skills directory",
					"mount", mountName, "dir", dirPath, "error", readErr)
			}
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			slug := entry.Name()
			skillFile := filepath.Join(dirPath, slug, "SKILL.md")
			if _, ok := realWithinRoot(mountRootReal, skillFile); !ok {
				// Either no SKILL.md at this path, or it is a symlink
				// resolving outside the mount (row 11) — refuse just this
				// one candidate, its siblings are unaffected.
				continue
			}

			info := SkillInfo{
				ID:     slug,
				Name:   slug,
				Path:   skillFile,
				Source: string(ShelfProject),
			}
			if meta := (&SkillsLoader{}).getSkillMetadata(skillFile); meta != nil {
				info.Description = meta.Description
				if meta.Name != "" {
					info.Name = meta.Name
				}
				info.Author = meta.Author
				info.Version = meta.Version
				info.ArgumentHint = meta.ArgumentHint
			}
			if validateErr := info.validate(); validateErr != nil {
				slog.Warn("skills: invalid project skill; skipped",
					"mount", mountName, "slug", slug, "error", validateErr)
				continue
			}

			key := strings.ToLower(slug)
			if existing, already := found[key]; already {
				collisions = append(collisions, SlugCollision{
					Slug:              slug,
					WinnerDescription: wonBy[key],
					Locations: []CollisionLocation{
						{Description: wonBy[key], Path: existing.Path},
						{Description: dirRel, Path: skillFile},
					},
				})
				slog.Warn("skills: project skill slug collision within mount; earlier recognised directory wins",
					"mount", mountName, "slug", slug, "winner", wonBy[key], "loser", dirRel)
				continue
			}
			found[key] = ProjectSkill{SkillInfo: info, MountName: mountName, MountRoot: mountRoot}
			wonBy[key] = dirRel
		}
	}

	if len(found) == 0 {
		return nil, collisions
	}
	out := make([]ProjectSkill, 0, len(found))
	for _, ps := range found {
		out = append(out, ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, collisions
}

// realWithinRoot resolves candidate to its real (symlink-free) path and
// reports whether that real path lies at or under rootReal (which must
// already be resolved and Clean-ed). A missing path, or one whose real path
// escapes rootReal, reports ok=false.
func realWithinRoot(rootReal, candidate string) (real string, ok bool) {
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	real = filepath.Clean(real)
	if real == rootReal {
		return real, true
	}
	return real, strings.HasPrefix(real, rootReal+string(filepath.Separator))
}

// lookupByName returns the project skill in the shelf whose display name
// case-insensitively matches name, or ok=false (ADR-072 Finding B fix).
// ResolveSkillName's doc comment promises matching "a skill slug (or its
// display name — matched case-insensitively against either) uniformly across
// shelves"; the registry/builtin branch already checks both s.ID and s.Name,
// but the project shelf was keyed and matched by slug alone, so a mount's
// SKILL.md with a `name:` distinct from its directory slug was unreachable by
// that name via any path (agent, /<skill>, delegate's requested_skill). This
// closes that gap, matching the registry/builtin branch's behaviour exactly.
//
// Iterates in deterministic (sorted slug) order so a display-name collision
// between two project skills always resolves to the same winner rather than
// depending on Go's randomised map iteration order.
func (shelf ProjectShelf) lookupByName(name string) (ProjectSkill, bool) {
	keys := make([]string, 0, len(shelf))
	for k := range shelf {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(shelf[k].Name, name) {
			return shelf[k], true
		}
	}
	return ProjectSkill{}, false
}

// MergeProjectSkills combines every mount's discovered project skills into
// one per-workspace project shelf (D4.1 rank 1). A cross-mount slug collision
// — two mounts on the same workspace carrying the same slug — resolves by
// byte-wise ascending mount-name order (FR-029: Go's default sort.Strings,
// not locale-aware, not case-folded, not Unicode-normalised), the same
// deterministic rule D7 uses for instruction files; the first (alphabetically
// earliest) mount wins and the collision is recorded naming both mounts
// (D4.2's ordering rule).
func MergeProjectSkills(mounts []ProjectMount) (ProjectShelf, []SlugCollision) {
	sorted := make([]ProjectMount, len(mounts))
	copy(sorted, mounts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	shelf := make(ProjectShelf)
	winningMount := make(map[string]string) // slug (case-folded) -> owning mount name
	var collisions []SlugCollision

	for _, m := range sorted {
		discovered, dirCollisions := DiscoverProjectSkills(m.Name, m.Root)
		collisions = append(collisions, dirCollisions...)

		for _, ps := range discovered {
			key := strings.ToLower(ps.ID)
			if existing, already := shelf[key]; already {
				collisions = append(collisions, SlugCollision{
					Slug:              ps.ID,
					WinnerDescription: "mount " + winningMount[key],
					Locations: []CollisionLocation{
						{Description: "mount " + winningMount[key], Path: existing.Path},
						{Description: "mount " + m.Name, Path: ps.Path},
					},
				})
				slog.Warn("skills: project skill slug collision across mounts; earlier mount name wins",
					"slug", ps.ID, "winner", winningMount[key], "loser", m.Name)
				continue
			}
			shelf[key] = ps
			winningMount[key] = m.Name
		}
	}

	return shelf, collisions
}
