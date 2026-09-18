package systools

import (
	"strings"
	"testing"
)

// M3: create_skill / edit_skill Descriptions used to tell the model that the
// write "additionally prompts for operator approval" depending on policy.
// Ava's shipped defaults Allow these tools; confirmation is the one
// conversational user reply already obtained (ADR-090 §5.2). An Ask ceiling
// is real execution policy, not a second confirmation ritual.

func TestSkillAuthoringDescriptions_DoNotAdvertiseSecondApprovalRitual(t *testing.T) {
	create := NewSkillCreateTool(nil).Description()
	edit := NewSkillEditTool(nil).Description()
	for name, desc := range map[string]string{"create_skill": create, "edit_skill": edit} {
		lower := strings.ToLower(desc)
		if strings.Contains(lower, "additionally prompts for operator approval") {
			t.Errorf("%s Description still advertises a second operator-approval prompt:\n%s", name, desc)
		}
		if strings.Contains(lower, "request for permission to proceed") {
			t.Errorf("%s Description must not invent a permission-to-proceed gate:\n%s", name, desc)
		}
		if !strings.Contains(lower, "not a substitute") && !strings.Contains(lower, "not a confirmation") {
			t.Errorf("%s Description must distinguish execution Ask from proposal confirmation:\n%s", name, desc)
		}
		if strings.Contains(lower, "approval token") {
			t.Errorf("%s Description must not introduce an approval token:\n%s", name, desc)
		}
	}
}
