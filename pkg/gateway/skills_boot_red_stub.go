package gateway

// RED scaffolding — landing/skills (train T9, #1458): the skills boot step
// (spec §8.5, test row 30 / S-7.13–S-7.14). Exists ONLY so the RED tests
// compile and fail on behaviour. The implementing worker replaces the bodies
// (keeping the declared names): bootSkills runs §9.3 recovery → §8.2
// SyncBuiltins → §8.4 retirement → the §5.13a dangling-grant sweep, inside
// loadConfigAndProvider immediately after seedAndPersistAgentRoster returns,
// before any agent is built; replayBootSkillAudit emits the SyncReport's
// events through the normal audit path at the existing wiring point
// (immediately after SetSkillsWriteAuditLogger, still inside agent-loop
// initialisation).

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// errRedBootSkillsNotImplemented is the RED-scaffold sentinel.
var errRedBootSkillsNotImplemented = errors.New(
	"gateway: RED scaffold — bootSkills not implemented (landing/skills T9)")

// skillsBootReport keeps this start's SyncReport so the audit replay at the
// wiring point can emit it (§8.5 audit-delivery rule).
var skillsBootReport skills.SyncReport

// bootSkills is the new start-up step (§8.5). It never stops the gateway:
// errors are logged, counted on the boot banner, and retried at the next
// start.
func bootSkills(cfg *config.Config, homePath string) error {
	_ = cfg
	_ = homePath
	return errRedBootSkillsNotImplemented // RED scaffold
}

// replayBootSkillAudit emits the boot report's facts (skill.builtin_updated,
// skill.builtin_retired, skill.builtin_retired_kept — §5.12) through the
// normal audit path. One-shot per start; a replay write failure is counted by
// the existing audit-degraded counter and logged, never fatal.
func replayBootSkillAudit(report skills.SyncReport, l *audit.Logger) {
	_ = report
	_ = l
	// RED scaffold — not implemented.
}
