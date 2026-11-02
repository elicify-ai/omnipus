package gateway

// Validation diagnostics must not echo skill content: REST leg. A skill whose
// SKILL.md has malformed frontmatter quoting a stored secret value, or whose
// SKILL.md is a link whose target text holds one, is copied through the REST
// route; the refusal body must not carry the value.
//
// Failability: before commit 486ebc9fd the refusal quoted the YAML parser
// message and the link target.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const s03RestSecret = "s03-rest-stored-secret-value-5d2b"

func s03WithSecret(t *testing.T) {
	t.Helper()
	prev := skills.SecretValuesProvider
	t.Cleanup(func() { skills.SecretValuesProvider = prev })
	skills.SecretValuesProvider = func() ([]credentials.NamedValue, error) {
		return []credentials.NamedValue{{Name: "S03_REST_TOKEN", Value: s03RestSecret}}, nil
	}
}

func TestRestCopy_RefusalNeverEchoesSkillContent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, dir string)
	}{
		{"malformed frontmatter", func(t *testing.T, dir string) {
			md := "---\nname: src\ndescription: !!int " + s03RestSecret + "\n---\n\nbody\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
		}},
		{"symlinked SKILL.md", func(t *testing.T, dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "SKILL.md")))
			if err := os.Symlink("/nonexistent/"+s03RestSecret+"/SKILL.md", filepath.Join(dir, "SKILL.md")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			dir, _ := t9SkillFixture(t, api, "src")
			dir = filepath.Join(dir, "src")
			tc.write(t, dir)
			s03WithSecret(t)

			w := t9Do(api.HandleSkills, http.MethodPost, "/api/v1/skills/src/copy", "", `{"new_id":"dst"}`)
			require.GreaterOrEqual(t, w.Code, 400, "the copy of a malformed skill is refused: %s", w.Body.String())
			assert.NotContains(t, w.Body.String(), s03RestSecret, "the REST body never carries the value")
			_, statErr := os.Stat(filepath.Join(api.homePath, "skills", "dst"))
			assert.True(t, errors.Is(statErr, os.ErrNotExist), "nothing was created")
		})
	}
}
