//go:build goolm && stdjson

package doctor

// Rider #99 (S-79, Test Implementation Order 32): with unverified skills
// allowed without a check (skill_trust allow_all), `omnipus doctor` prints a
// warning that unverified skills are allowed without a check, and how to
// choose Ask in Skills & Tools. Oracle: S-79's Then line. RED today:
// command.go::checkConfig runs five checks and none covers skill trust,
// although Sandbox.SkillTrust's own doc comment promises this warning —
// the documented behaviour is not implemented, which is exactly what this
// rider pins.
import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestRiderS79_DoctorWarnsWhenUnverifiedSkillsAllowed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.SkillTrust = config.SkillTrustAllowAll
	warnings := checkConfig(cfg)
	for _, w := range warnings {
		msg := strings.ToLower(w.message)
		if strings.Contains(msg, "unverified skills are allowed") && strings.Contains(msg, "skills & tools") {
			if w.code == "" {
				t.Error("doctor warning for skill trust carries no code — every doctor warning has one (see the [i] code: message report shape)")
			}
			return
		}
	}
	t.Fatalf("BLOCKED: checkConfig returned %d warning(s) with skill_trust=allow_all, none says unverified skills are allowed without a check and names Skills & Tools — required by S-79 (rider #99); the Sandbox.SkillTrust doc comment promises this warning", len(warnings))
}
