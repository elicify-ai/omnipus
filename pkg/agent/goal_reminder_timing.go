package agent

import "github.com/elicify-ai/omnipus/pkg/config"

// goalReminderPlanningConfig keeps a sweep's existing config snapshot together
// through all keeper decisions. Direct callers read the current loop config;
// neither path caches timers or creates a second clock/override mechanism.
func (al *AgentLoop) goalReminderPlanningConfig(planning []config.PlanningConfig) config.PlanningConfig {
	if len(planning) > 0 {
		return planning[0]
	}
	return planningConfigOf(al)
}
