package gateway

// UAT run 2 R2-3: refusals that are the uploader's input stay 4xx (never 502)
// with a clear text; the rename-suffix rule must not loosen them.
//
// The upload goes through the install review (spec C-3 ask row, C-13, US-7 AS4,
// ST-09 d): under the default trust an unverified upload that was sent straight to
// POST /skills/install answers 409 "Review needed", which would hide the refusal
// this test pins. C-13 has the review stage "with the existing staging code" of
// installSkill, so an upload rule can refuse at the review or at the install that
// follows it; uat2ReviewThenInstall returns whichever answer ends the chain and the
// 400 rule asserted below is unchanged.

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
			w := uat2ReviewThenInstall(t, api, tc.file, uat2GoodZip(t, tc.frontmatterName), nil)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.NotContains(t, w.Body.String(), "server storage or configuration failed")
		})
	}
}
