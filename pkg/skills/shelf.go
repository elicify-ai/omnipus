package skills

import "strings"

// SkillShelf identifies which of the three agent-visible shelves a resolved
// or listed skill came from (ADR-072 D4.1's table), in shelf-rank order —
// most specific wins.
type SkillShelf string

const (
	// ShelfProject is rank 1: a mount's own ".claude/skills"/".omnipus/skills".
	// The grant instrument is the mount itself (D4.1) — no per-agent slug
	// grant is consulted for this shelf.
	ShelfProject SkillShelf = "project"
	// ShelfRegistry is rank 2: $OMNIPUS_HOME/skills, gated by the agent's
	// per-agent grant list (D5 default-none semantics).
	ShelfRegistry SkillShelf = "registry"
	// ShelfBuiltin is rank 3: the embedded skills Omnipus ships, gated by the
	// SAME per-agent grant list as the registry shelf (D4.1 row 3).
	ShelfBuiltin SkillShelf = "builtin"
)

// ResolvedSkill is what ResolveSkillName hands back on a successful
// resolution: the canonical slug (never the free-form display name, even
// when the caller searched by display name — mirrors the existing
// pkg/agent ContextBuilder.ResolveSkillName contract) plus which shelf it
// came from and the on-disk path to load it from.
type ResolvedSkill struct {
	Slug  string
	Shelf SkillShelf
	Path  string
	// MountName is set only when Shelf == ShelfProject, naming the mount the
	// resolved skill came from.
	MountName string
}

// ResolveSkillName resolves a skill slug (or its display name — matched
// case-insensitively against either, mirroring the existing agent-level
// contract) against the full per-shelf grant model (ADR-072 D4.1, D4.2):
//
//  1. Registry and builtin shelves (registryAndBuiltin — pass the result of
//     a SkillsLoader's ListSkills() filtered to "global"/"builtin" sources;
//     this function does not know about a loader's vestigial "workspace"
//     shelf, which the project shelf below supersedes). A match here is only
//     eligible when allowed(slug) reports true — that is D5's grant gate.
//  2. Project shelf (projectShelf — see MergeProjectSkills), gated by mount
//     membership alone, no allowed() check (D4.1).
//
// D4.2's one carve-out: when the agent HOLDS a grant for this slug on the
// registry or builtin shelf (step 1 succeeds), that shelf wins outright and
// a same-slug project skill never displaces it — the collision is still
// recorded so it is not silently shadowed. This carve-out is scoped to a
// slug the agent's grant currently reaches: a grant naming a registry slug
// that is no longer installed cannot match step 1 (ListSkills no longer
// lists it), so it does not compete — the project skill of that slug
// resolves normally (FR-028a, the "dangling grant" case). Likewise a
// registry/builtin slug the agent has NOT been granted does not win step 1
// either (allowed() is false), so an ungranted registry install never blocks
// a project skill of the same name — D4.1's whole point is that mounting
// requires no separate grant.
func ResolveSkillName(registryAndBuiltin []SkillInfo, allowed func(slug string) bool, projectShelf ProjectShelf, name string) (ResolvedSkill, bool, *SlugCollision) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ResolvedSkill{}, false, nil
	}

	var granted *SkillInfo
	for i := range registryAndBuiltin {
		s := &registryAndBuiltin[i]
		if !strings.EqualFold(s.ID, trimmed) && !strings.EqualFold(s.Name, trimmed) {
			continue
		}
		if allowed != nil && allowed(s.ID) {
			granted = s
			break
		}
	}

	if granted != nil {
		shelf := ShelfRegistry
		if granted.Source == "builtin" {
			shelf = ShelfBuiltin
		}
		resolved := ResolvedSkill{Slug: granted.ID, Shelf: shelf, Path: granted.Path}

		// D4.2: even though the registry/builtin grant wins, a same-slug
		// project skill is a collision worth recording, not silence.
		if projectShelf != nil {
			if ps, ok := projectShelf[strings.ToLower(granted.ID)]; ok {
				return resolved, true, &SlugCollision{
					Slug:              granted.ID,
					WinnerDescription: string(shelf),
					Locations: []CollisionLocation{
						{Description: string(shelf), Path: granted.Path},
						{Description: "mount " + ps.MountName, Path: ps.Path},
					},
				}
			}
		}
		return resolved, true, nil
	}

	// No granted registry/builtin match — either absent entirely (including
	// a dangling grant for an uninstalled slug, FR-028a) or present but not
	// granted. Fall through to the project shelf, keyed by slug alone.
	if projectShelf != nil {
		if ps, ok := projectShelf[strings.ToLower(trimmed)]; ok {
			return ResolvedSkill{Slug: ps.ID, Shelf: ShelfProject, Path: ps.Path, MountName: ps.MountName}, true, nil
		}
	}

	return ResolvedSkill{}, false, nil
}
