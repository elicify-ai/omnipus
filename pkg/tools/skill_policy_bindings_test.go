//go:build goolm && stdjson

package tools

// Native bindings of the Skills & Tools seams (#1963) under the fix-round-3
// guard (tests/helpers/nativebinding): only direct named production
// functions whose signature matches the seam are bound here. Every other
// seam stays unbound (loud BLOCKED) until the test author reconciles it
// with the real function named in the backend handoff.

func init() {
	discardStagedInstallSkill = (*InstallSkillTool).DiscardStaged
	skillOutcomeOf = SkillOutcomeOf
}
