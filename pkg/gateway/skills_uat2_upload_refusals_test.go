package gateway

// UAT run 2 R2-3: refusals that are the uploader's input stay 4xx (never 502)
// with a clear text; the rename-suffix rule must not loosen them.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInstallSkillUploadRefusals_AreClientErrorsNot502(t *testing.T) {
	cases := []struct{ name, file, frontmatterName string }{
		{"frontmatter name differs from file name", "foo.zip", "bar"},
		{"fewer than 19 digit suffix", "good-skill_123.zip", "good-skill"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
			w := uat2InstallNamed(t, api, tc.file, uat2GoodZip(t, tc.frontmatterName), nil)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.NotContains(t, w.Body.String(), "server storage or configuration failed")
		})
	}
}
