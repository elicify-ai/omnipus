//go:build goolm && stdjson

package tools

// Order 55 — preparer/pin COMPONENT tests, not a one-card acceptance claim.
// The actual loop/card/HTTP/trust chain is now exercised by the S-118 rows in
// pkg/gateway/skill_approval_actions_red_test.go. CHECK F18: traversal errors
// propagate, and the successful publish compares complete literal bytes.
// GREEN and mutation proof remain deferred to independent CHECK.
// TestInstallSkill_OneCard (S-118; C-12 rows 6–8). Oracle:
// skills-tools-redesign-spec.md —
//   C-12 row 6 / S-118: install_skill by an agent, effective Ask, unverified
//        skill, trust Ask → ONE install card naming the skill as unverified
//        with its staged revision and verification result, offering "Install
//        and approve this version" (Approve Once) and Deny, no time choice.
//        Approving installs exactly that revision and stores the trust
//        approval; no second card. Denying installs nothing and removes the
//        staged copy.
//   C-12 row 8: effective Allow, unverified, trust Ask → the SAME staging,
//        one trust card bound to the staged revision (card shape is the
//        decision table's — order 47's D10#25 — and the handler's, order 51).
//   C-12 rows 6–8 note: "Rows 6 and 8 stage the skill (download and unpack
//        into the existing staging area; never published) BEFORE the card is
//        raised, through the existing pre-approval seam
//        (pkg/agent/loop_run_turn_tools.go::requestAskApproval →
//        prepareApprovalRequest)". The production carrier for card facts and
//        the pinned approval is tools.ApprovalPreparer (loop_pre_approval.go
//        ::prepareApprovalRequest): PrepareForApproval returns the facts the
//        card shows and a pin the approved execution binds.
//
// The fact keys below mirror the C-9 skill object order 13 pinned for the
// frame (skill key, revision, unverified, verification) — the facts FEED that
// frame, so GREEN carries the same values.
//
// RED today: install_skill implements no ApprovalPreparer (it installs
// directly, pkg/tools/skills_install.go), so nothing stages before a card and
// no card facts exist. Every leg fails on that missing implementation.
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// mutableSkillRegistry is a fake registry whose weather payload can change
// between the stage and the approve (the "installs exactly that revision"
// binding of S-118 leg 2).
type mutableSkillRegistry struct{ body string }

func (r *mutableSkillRegistry) Name() string { return "mutable" }

func (r *mutableSkillRegistry) Search(_ context.Context, _ string, _ int) ([]skills.SearchResult, error) {
	return nil, nil
}

func (r *mutableSkillRegistry) GetSkillMeta(_ context.Context, slug string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{Slug: slug, RegistryName: "mutable"}, nil
}

func (r *mutableSkillRegistry) DownloadAndInstall(_ context.Context, slug, _ string, targetDir string) (*skills.InstallResult, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	body := "---\nname: " + slug + "\ndescription: " + r.body + "\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		return nil, err
	}
	// No registry hash in the result: the installed skill is unverified (C-5),
	// exactly the row's Given.
	return &skills.InstallResult{Version: "1.0.0"}, nil
}

// newOneCardTool builds the real install_skill tool against a mutable
// registry and an OMNIPUS_HOME whose trust is Ask with no approvals stored.
func newOneCardTool(t *testing.T) (*InstallSkillTool, *mutableSkillRegistry, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfgDir := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(cfgDir, []byte(`{"sandbox":{"skill_trust":"ask"}}`), 0o600))
	globalSkills := filepath.Join(home, "skills")
	require.NoError(t, os.MkdirAll(globalSkills, 0o755))
	reg := &mutableSkillRegistry{body: "the reviewed revision"}
	mgr := skills.NewRegistryManager()
	mgr.AddRegistry(reg)
	return NewInstallSkillTool(mgr, globalSkills), reg, globalSkills
}

func TestInstallSkill_OneCard(t *testing.T) {
	installArgs := func() map[string]any {
		return map[string]any{"slug": "weather", "registry": "mutable"}
	}

	t.Run("S-118 leg 1: the unverified install stages before the card and its facts name the staged revision", func(t *testing.T) {
		tool, _, globalSkills := newOneCardTool(t)

		// The production pre-approval seam (loop_pre_approval.go) only feeds
		// card facts for a tool implementing tools.ApprovalPreparer.
		preparer, ok := any(tool).(ApprovalPreparer)
		require.True(t, ok,
			"S-118 C-12 row 6: install_skill must implement tools.ApprovalPreparer so "+
				"prepareApprovalRequest can stage the skill BEFORE the card is raised (rows 6–8 note)")

		facts, pin, refusal := preparer.PrepareForApproval(context.Background(), installArgs())
		require.Nil(t, refusal, "S-118: no pre-card refusal for the row's Given")
		require.NotNil(t, pin, "S-118: the staged copy is pinned for the approved execution")
		require.Equal(t, "weather", facts["skill"], "C-9-aligned fact: the skill key")
		require.Equal(t, true, facts["unverified"], "C-9-aligned fact: the card names the skill as unverified")
		require.NotEmpty(t, facts["verification"], "C-12 row 6: the card carries the verification result")
		stagedRevision, _ := facts["revision"].(string)
		require.NotEmpty(t, stagedRevision, "C-12 row 6: the card carries the staged revision")

		// The staged copy exists under the existing staging area, never
		// published: no installed weather yet.
		stagingRoot := filepath.Join(globalSkills, ".staging")
		require.DirExists(t, stagingRoot, "rows 6-8: the skill is staged into the existing staging area")
		staged := false
		require.NoError(t, filepath.Walk(stagingRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && info.Name() == "SKILL.md" {
				staged = true
			}
			return nil
		}))
		require.True(t, staged, "S-118: the staged copy with SKILL.md exists before the card")
		_, statErr := os.Stat(filepath.Join(globalSkills, "weather"))
		require.True(t, errors.Is(statErr, os.ErrNotExist), "S-118: staging never publishes (nothing installed yet)")
	})

	t.Run("S-118 leg 2: approving installs exactly the staged revision, even when the registry changed", func(t *testing.T) {
		tool, reg, globalSkills := newOneCardTool(t)
		preparer, ok := any(tool).(ApprovalPreparer)
		require.True(t, ok, "S-118: install_skill must implement ApprovalPreparer (leg 1)")

		facts, pin, refusal := preparer.PrepareForApproval(context.Background(), installArgs())
		require.Nil(t, refusal)
		stagedRevision, _ := facts["revision"].(string)
		require.NotEmpty(t, stagedRevision)

		// The registry's payload changes between the stage and the approve;
		// the publish must be exactly the staged folder of that review.
		reg.body = "changed after the review"
		res := tool.Execute(WithApprovalPin(context.Background(), pin), installArgs())
		require.False(t, res.IsError, "S-118: the approved install publishes: %s", res.ForLLM)

		raw, err := os.ReadFile(filepath.Join(globalSkills, "weather", "SKILL.md"))
		require.NoError(t, err, "the approved install installed weather")
		require.Equal(t, "---\nname: weather\ndescription: the reviewed revision\n---\n\nBody.\n", string(raw),
			"S-118: complete literal reviewed bytes, not a substring or a re-download of R2")
		installed, revErr := skills.NewSkillWriter(globalSkills).SkillRevision("weather")
		require.NoError(t, revErr, "the installed skill carries its revision")
		require.Equal(t, stagedRevision, installed,
			"S-118: the installed revision equals the reviewed (staged) revision")
	})

	t.Run("S-118 leg 3: denying installs nothing and removes the staged copy", func(t *testing.T) {
		nativebinding.Require(t, "pkg/tools", discardStagedInstallSkill,
			"S-118 leg 3 / C-12 rows 6–8: actual InstallSkillTool denial-path discard; no test RemoveAll")
		tool, _, globalSkills := newOneCardTool(t)
		preparer, ok := any(tool).(ApprovalPreparer)
		require.True(t, ok, "S-118: install_skill must implement ApprovalPreparer (leg 1)")

		_, pin, refusal := preparer.PrepareForApproval(context.Background(), installArgs())
		require.Nil(t, refusal)
		require.NoError(t, discardStagedInstallSkill(tool, pin), "native installer must discard the actual prepared pin")

		_, statErr := os.Stat(filepath.Join(globalSkills, "weather"))
		require.True(t, errors.Is(statErr, os.ErrNotExist), "S-118 leg 3: denying installs nothing")
		staged := false
		stagingRoot := filepath.Join(globalSkills, ".staging")
		require.NoError(t, filepath.Walk(stagingRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && info.Name() == "SKILL.md" {
				staged = true
			}
			return nil
		}), "the staging root must exist to prove the copy is gone")
		require.False(t, staged, "S-118 leg 3: the staged copy is removed")
	})

	t.Run("C-12 row 8: an effective-Allow install stages the same way for its trust card", func(t *testing.T) {
		// Row 8's card differs (trust only, bound to the staged revision) but
		// rows 6 and 8 stage identically; the tool-side contract is the same
		// call. The permission difference lives in the loop's resolution.
		tool, _, _ := newOneCardTool(t)
		preparer, ok := any(tool).(ApprovalPreparer)
		require.True(t, ok, "C-12 row 8: install_skill must implement ApprovalPreparer")
		facts, pin, refusal := preparer.PrepareForApproval(context.Background(), installArgs())
		require.Nil(t, refusal)
		require.NotNil(t, pin, "row 8: the trust card is bound to the staged revision")
		require.NotEmpty(t, facts["revision"], "row 8: the trust card names the staged revision")
	})
}

// Bind a DIRECT named production installer function/method expression. There
// is no testing.TB argument: production must not import a test helper to bind.
// Native provenance is enforced above; actual pin/file observers stay real.
// No native discard operation exists at this revision; reconciliation must
// name its actual production symbol before this component test can turn green.
var discardStagedInstallSkill func(tool *InstallSkillTool, pin any) error
