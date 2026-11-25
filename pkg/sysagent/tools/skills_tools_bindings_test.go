package systools_test

import "github.com/elicify-ai/omnipus/pkg/config"

// Native bindings of the Skills & Tools seams (#1963) for this package,
// under the fix-round-3 guard (tests/helpers/nativebinding): only direct
// named production functions whose signature matches the seam are bound.

func init() {
	setGlobalSkillPolicyForTest = config.SetGlobalSkillPolicy
}
