package skills

// UAT-1 F-2 and the reserved-name wording finding.
//
// F-2: copying a built-in must not carry its metadata.display_name into the
// copy, so the copy is not labelled with the built-in's display name.
// Reserved name (spec r15 §5.10): the BUILTIN_NAME_RESERVED text uses the name
// as the caller entered it ('Plan'), not the lower-cased key.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyOfBuiltin_DropsDisplayNameMetadata(t *testing.T) {
	root := t.TempDir()
	_, err := SyncBuiltins(root, nil)
	require.NoError(t, err)
	src, err := os.ReadFile(filepath.Join(root, "create-agent", "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(src), "display_name: Create Agent (W1)", "instrument: the built-in carries a display_name")

	_, err = copySrcToDst(t, root, "create-agent", "my-agent-maker")
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(root, "my-agent-maker", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "name: my-agent-maker", "positive control: the copy is renamed")
	assert.NotContains(t, string(raw), "display_name", "the copy does not keep the built-in's display_name")

	for _, info := range NewSkillsLoader("", root, "").ListSkills() {
		if info.ID == "my-agent-maker" {
			assert.NotEqual(t, "Create Agent (W1)", info.Name, "the copy's label is not the built-in's display name")
			return
		}
	}
	t.Fatal("the copy is listed")
}

func TestReservedNameText_UsesTheNameAsEntered(t *testing.T) {
	root := t.TempDir()
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "Plan",
		Content: []byte("---\nname: Plan\ndescription: x\n---\nBody.\n"),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrBuiltinNameReserved), "instrument: refused as a reserved name: %v", err)
	var ce *ChangeError
	require.True(t, errors.As(err, &ce))
	assert.True(t, strings.HasPrefix(ce.Text, "'Plan' is the name of a built-in skill"), "the text shows the name as entered: %q", ce.Text)
}
