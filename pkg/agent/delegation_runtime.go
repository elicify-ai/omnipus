package agent

import (
	"time"
)

const (
	defaultMaxSubTurnDepth = 3

	// defaultSubTurnTimeout is the built-in lifetime for a delegated/steered
	// child session when neither a per-call timeout_seconds nor
	// performance.delegation_timeout_minutes was configured (D9: 0 = default;
	// founder raised the default from 5 to 30 minutes, 2026-09-23). Its two
	// readers are both child-session paths — effectiveDelegationTimeout (the
	// delegation resolver, below) and the external-CLI runner's per-run
	// fallback (external_dispatch.go::prepareRunOptions, reached only from the
	// task executor, the runner's one remaining caller) — so changing it here
	// moves the default for both, and neither is a path that must stay at 5.
	defaultSubTurnTimeout = 30 * time.Minute
)

type requestedSkillOutcome int

const (
	requestedSkillUnresolvable requestedSkillOutcome = iota
	requestedSkillDenied
	requestedSkillGranted
)

func (al *AgentLoop) effectiveDelegationTimeout() time.Duration {
	minutes, err := al.cfg.Performance.EffectiveDelegationTimeoutMinutes()
	if err != nil || minutes <= 0 {
		return defaultSubTurnTimeout
	}
	return time.Duration(minutes) * time.Minute
}
