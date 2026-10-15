package agent

const (
	defaultMaxSubTurnDepth = 3
)

// There is no delegated-session lifetime default any more: a delegated or
// steered child, a task run and an external-CLI worker have no time limit and
// end only when they finish or are explicitly stopped (issue #1306). The
// retired defaultSubTurnTimeout / effectiveDelegationTimeout lived here.

type requestedSkillOutcome int

const (
	requestedSkillUnresolvable requestedSkillOutcome = iota
	requestedSkillDenied
	requestedSkillGranted
)
