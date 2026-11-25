//go:build goolm && stdjson

package gateway

import (
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Native bindings of the Skills & Tools seams (#1963) under the fix-round-3
// guard (tests/helpers/nativebinding): only direct named production
// functions whose signature matches the seam are bound here.

func init() {
	bindNativeInstallReviewClock = (*restAPI).setInstallReviewClock
	exactActionToolsOf = tools.ExactActionTools
	injectScheduledAllowanceClock = (*agent.AgentLoop).SetAllowanceClock
	bindNativeSkillEffectInputs = (*agent.AgentLoop).SetSkillEffectHooks
	skillUpgradeRunOf = skillperm.Run
}
