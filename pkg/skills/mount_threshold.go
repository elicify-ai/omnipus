package skills

import "fmt"

// DefaultMountSkillsWarnThreshold is the mount-add-time threshold (ADR-072
// D1.2, FR-074, spec-default 500): a mount whose recognised skills
// directory would contribute more discovered skills than this triggers an
// operator-visible warning AT MOUNT-CREATION TIME.
//
// This is explicitly NOT the per-turn menu cap D1.1 removed — see D1.2's own
// table. The threshold changes nothing about what a turn sends: it exists
// only so the operator making the mount decision sees a count and its
// per-turn consequence before it becomes background noise. Nothing in this
// package ever truncates the menu because of it (FR-076); mount creation
// itself proceeds regardless (FR-075) — evaluating that proceed/warn split is
// this function's whole job, not a refusal.
const DefaultMountSkillsWarnThreshold = 500

// MountSkillsDisclosure is what mount-creation time shows the operator about
// a mount's recognised skills directory (D1.2, FR-074/FR-074a/MAJ-004).
type MountSkillsDisclosure struct {
	// Count is how many project skills DiscoverProjectSkills found under
	// this mount. Zero means the mount carries no recognised skills
	// directory at all — both messages below are then empty (FR-039: no
	// action, no warning).
	Count int
	// GrantsMessage states plainly, every time — even a mount with only
	// three skills — that its skills directory grants agents new,
	// auto-loadable instructions, not merely files sitting in the repo
	// (FR-074a, MAJ-004). Independent of ThresholdWarning: it is set
	// whenever Count > 0, regardless of whether the threshold is exceeded.
	GrantsMessage string
	// ThresholdWarning is non-empty only when Count exceeds the threshold
	// (FR-074): states the count and its per-turn consequence. An empty
	// value means no warning is due — this is information, never a refusal
	// (FR-075): the mount is created either way.
	ThresholdWarning string
}

// EvaluateMountSkillsDisclosure discovers project skills under mountRoot and
// builds the mount-creation-time disclosure described by
// MountSkillsDisclosure. Pass threshold <= 0 to use
// DefaultMountSkillsWarnThreshold.
//
// This function performs no mount creation and returns no error: it is a
// pure, side-effect-free computation a later phase's mount-creation path
// calls to decide what to show the operator, always proceeding with creation
// regardless of the result (FR-075).
func EvaluateMountSkillsDisclosure(mountName, mountRoot string, threshold int) MountSkillsDisclosure {
	if threshold <= 0 {
		threshold = DefaultMountSkillsWarnThreshold
	}

	discovered, _ := DiscoverProjectSkills(mountName, mountRoot)
	count := len(discovered)
	if count == 0 {
		return MountSkillsDisclosure{}
	}

	noun := "skill"
	if count != 1 {
		noun = "skills"
	}
	disclosure := MountSkillsDisclosure{
		Count: count,
		GrantsMessage: fmt.Sprintf(
			"This mount's skills directory grants %d %s to every agent working in this workspace as auto-loadable agent instructions — not just files sitting in the repository. Any agent acting here may call and run them, with no separate grant needed.",
			count, noun),
	}

	if count > threshold {
		disclosure.ThresholdWarning = fmt.Sprintf(
			"This mount would contribute %d skills — well beyond a plausible hand-authored collection (threshold: %d). Every one of them will appear in the skills menu on every turn in this workspace. The mount is being created anyway; you may want to confirm this isn't a vendored or generated tree before relying on it.",
			count, threshold)
	}

	return disclosure
}
