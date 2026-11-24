// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

// GREEN binding for TestSkillsSummary_Levels: the production menu entry
// renderer. The seam passes "deny" in the level slot for a denied skill; the
// adapter hands it to the renderer as the separate deny outcome.
func init() {
	skillsMenuLineOf = func(level, name, description string) string {
		s := SkillInfo{ID: name, Name: name, Description: description, Source: "global"}
		if level == "deny" {
			return SkillMenuEntry(s, VisibilityNameAndDescriptionLevel, true)
		}
		return SkillMenuEntry(s, level, false)
	}
}
