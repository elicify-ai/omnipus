package agent

const (
	// defaultMaxSubTurnDepth is the safety-backstop cap on onward delegation
	// depth: the effective cap when NEITHER the per-edge Depth nor
	// performance.max_delegation_depth expresses a value
	// (resolveEffectiveDelegationDepth). Raised from 3 to 10 by the founder
	// ruling of 2026-10-27 ("increase the delegation depth to 10 for all
	// agents and make it the default").
	//
	// The same-valued mirrors of this number live outside pkg/agent and MUST
	// move together with it — pkg/gateway::delegationDepthCeilingFallback,
	// pkg/sysagent/tools::workspaceDelegationDepthCeilingFallback,
	// pkg/coreagent::FreshSelfDelegationMaxDepth and
	// pkg/tools::defaultOwnershipWalkMaxDepth each re-declare this value
	// because their packages cannot import pkg/agent's internals.
	defaultMaxSubTurnDepth = 10
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
