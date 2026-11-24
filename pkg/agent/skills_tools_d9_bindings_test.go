//go:build goolm && stdjson

package agent

// GREEN wiring of the Skills & Tools D9 seams (#1963, C-14) to the
// production stores: sandbox.skill_policies, sandbox.skill_visibility and
// sandbox.skill_trust in config.json, the C-5 install record and the C-24
// revision approval (pkg/skills), and the C-14 fixed estimator input.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"

	"github.com/stretchr/testify/require"
)

func init() {
	seedSkillState = d9SeedSkillState
	nativeLoopTokenEstimators = setFixedTokenEstimators
}

// d9SeedSkillState writes the row's raw D9 state; empty fields are left as
// they are, so a later call (D9#4's approval) adds to the earlier state.
func d9SeedSkillState(t testing.TB, home, slug string, st d9SkillState) {
	t.Helper()
	path := filepath.Join(home, "config.json")
	doc := c15ReadConfig(t, path)
	sandbox, _ := doc["sandbox"].(map[string]any)
	if sandbox == nil {
		sandbox = map[string]any{}
		doc["sandbox"] = sandbox
	}
	setIn := func(field, key, value string) {
		m, _ := sandbox[field].(map[string]any)
		if m == nil {
			m = map[string]any{}
			sandbox[field] = m
		}
		m[key] = value
	}
	key := config.NormalizeSkillKey(slug)
	if st.policy != "" {
		setIn("skill_policies", key, st.policy)
	}
	if st.level != "" {
		setIn("skill_visibility", key, st.level)
	}
	if st.trust != "" {
		sandbox["skill_trust"] = st.trust
	}
	c15WriteConfig(t, path, doc)

	skillsDir := filepath.Join(home, "skills")
	revision, err := skills.ContentRevision(filepath.Join(skillsDir, slug))
	require.NoError(t, err)
	if st.policy != "" {
		category, catErr := skills.InstallCategory("", filepath.Join(skillsDir, slug))
		require.NoError(t, catErr)
		rec := skills.InstallRecord{Category: category}
		if st.verified {
			rec.VerifiedRevision = revision
		}
		require.NoError(t, skills.WriteInstallRecord(skillsDir, slug, rec))
	}
	if st.approved {
		require.NoError(t, skills.RecordTrustApproval(skillsDir, skills.TrustApproval{
			Key: key, Revision: revision, ApprovedBy: "d9-person", ApprovedAt: time.Now(),
		}))
	}
}
