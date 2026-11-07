package systools

import (
	"strings"
	"testing"
)

// r15 §5.4/§10.1: ordinary execution approval, no extra skill ritual.
func TestSkillAuthoringDescriptions_DoNotAdvertiseSecondApprovalRitual(t *testing.T) {
	desc := NewSkillEditTool(nil).Description()
	lower := strings.ToLower(desc)
	for _, forbidden := range []string{"additionally prompts for operator approval", "request for permission to proceed", "approval token"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("edit_skill invents a second ritual %q: %s", forbidden, desc)
		}
	}
	for _, required := range []string{"copy", "delete_file", "delete", "restore", "write_file"} {
		if !strings.Contains(lower, required) {
			t.Errorf("edit_skill must explain supported r15 operation %q: %s", required, desc)
		}
	}
}
