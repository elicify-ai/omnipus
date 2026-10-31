package tools

// RED scaffolding — landing/skills (train T9): T8's prune input (§5.3, §15
// item 1). `RetiredToolNames` is the list the shared T8 field-selection
// helper prunes from selectable names. Exists ONLY so the RED gate tests
// compile and fail on behaviour; the implementing worker replaces the body
// with the real list (create_skill, remove_skill) and removes this marker.

// RetiredToolNames lists retired tool names (T8 prune input).
var RetiredToolNames = []string{} // RED scaffold — empty until implemented
