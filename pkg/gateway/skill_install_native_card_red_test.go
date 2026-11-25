//go:build goolm && stdjson

package gateway

// S-118/F18: helpers for the actual one-card chain in the order-51 root.
// The registry is an external network edge; the installer is never stubbed.
// There is no fabricated publish success or returned trust/approval verdict.

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nativeInstallR1 = "---\nname: fresh-helper\ndescription: Native one-card staged fixture.\n---\n\nF18-FRESH-R1-INSTRUCTIONS-4K\n"
const nativeInstallR2 = "---\nname: fresh-helper\ndescription: Changed after staging.\n---\n\nF18-FRESH-R2-INSTRUCTIONS-5M\n"

type nativeConsentRegistry struct {
	mu        sync.Mutex
	body      string
	downloads int
}

func (*nativeConsentRegistry) Name() string { return "f18-local" }
func (*nativeConsentRegistry) Search(context.Context, string, int) ([]skills.SearchResult, error) {
	return nil, nil
}
func (*nativeConsentRegistry) GetSkillMeta(context.Context, string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{Slug: "fresh-helper", RegistryName: "f18-local"}, nil
}
func (r *nativeConsentRegistry) DownloadAndInstall(_ context.Context, _ string, _ string, target string) (*skills.InstallResult, error) {
	r.mu.Lock()
	r.downloads++
	body := r.body
	r.mu.Unlock()
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(body), 0o600); err != nil {
		return nil, err
	}
	return &skills.InstallResult{Version: "1.0.0"}, nil // no registry hash: unverified
}
func (r *nativeConsentRegistry) change(body string) { r.mu.Lock(); defer r.mu.Unlock(); r.body = body }
func (r *nativeConsentRegistry) count() int         { r.mu.Lock(); defer r.mu.Unlock(); return r.downloads }

func testNativeSkillInstallCard(t *testing.T, policy, answer string) {
	h := newNativeSkillHarness(t, "ask", "ask")
	root := filepath.Join(h.api.homePath, "skills")
	network := &nativeConsentRegistry{body: nativeInstallR1}
	manager := skills.NewRegistryManager()
	manager.AddRegistry(network)
	installer := tools.NewInstallSkillTool(manager, root)
	if _, ok := any(installer).(tools.ApprovalPreparer); !ok {
		t.Fatal("BLOCKED: actual install_skill has no native pre-card staging/pin implementation — required by C-12 rows 6–8 and S-118; never replace the installer or insert a card to pass this chain")
	}
	h.api.agentLoop.RegisterTool(installer)
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	ceiling := make(map[string]config.ToolPolicy)
	for name, value := range h.api.agentLoop.GetConfig().Sandbox.ToolPolicies {
		ceiling[name] = config.ToolPolicy(value)
	}
	ceiling["install_skill"] = config.ToolPolicy(policy)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: ceiling})
	require.Equal(t, policy, tools.EffectiveToolPolicy(inst.LoadToolPolicy(), installer.Scope(), inst.AgentType, "install_skill"))
	h.script.tool = "install_skill"
	h.script.args = map[string]any{"slug": "fresh-helper", "registry": "f18-local"}
	if answer == "timeout" {
		h.reg = newApprovalRegistryV2(64, time.Second)
		h.api.approvalReg = h.reg
		h.handler.approvalRegV2 = h.reg
		h.api.agentLoop.SetToolApprover(newPolicyApproverAdapter(h.reg, h.handler))
	}
	run := h.begin(t)
	entry := h.pending(t, run)
	snap := h.handler.pendingApprovalSnapshot(entry)
	if policy == "ask" {
		assert.Equal(t, []string{"allow_once", "deny"}, snap.OfferedActions)
	} else {
		assert.Equal(t, []string{"approve_version", "deny"}, snap.OfferedActions)
	}
	assert.Empty(t, snap.AllowDurations)
	require.NotNil(t, snap.Skill)
	assert.Equal(t, "fresh-helper", snap.Skill.Key)
	assert.Equal(t, "fresh-helper", snap.Skill.Name)
	assert.True(t, snap.Skill.Unverified)
	assert.Equal(t, 1, network.count(), "one real registry download before asking")
	staged := nativeStagedSkillFiles(t, root)
	require.Len(t, staged, 1, "one actual pinned staged revision before the card")
	raw, err := os.ReadFile(staged[0])
	require.NoError(t, err)
	assert.Equal(t, nativeInstallR1, string(raw))
	revision, err := skills.ContentRevision(filepath.Dir(staged[0]))
	require.NoError(t, err)
	assert.Equal(t, revision, snap.Skill.Revision)
	assert.NoDirExists(t, filepath.Join(root, "fresh-helper"), "staging never publishes")
	network.change(nativeInstallR2)
	switch answer {
	case "approve":
		action := "allow_once"
		if policy == "allow" {
			action = "approve_version"
		}
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"`+action+`"}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		var response gen.ToolApprovalResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, &gen.ToolApprovalApplied{CallReleased: true, TrustApproved: true, TimedAllowance: false}, response.Applied)
		assert.Nil(t, response.ExpiresAt)
	case "deny":
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	case "timeout": // real registry timer, no callback supplies an expiry result
	default:
		t.Fatalf("fixture: unrecognised answer %s", answer)
	}
	h.finish(t, run, false) // install result, never loads notes-helper instructions
	assert.Empty(t, h.reg.pendingApprovals(), "no second approval")
	assert.Equal(t, 1, network.count(), "approval publishes the pin; never re-downloads R2")
	assert.Empty(t, nativeStagedSkillFiles(t, root), "approved/denied/timed-out pin is discarded")
	if answer == "approve" {
		raw, err := os.ReadFile(filepath.Join(root, "fresh-helper", "SKILL.md"))
		require.NoError(t, err)
		assert.Equal(t, nativeInstallR1, string(raw))
		installed, err := skills.NewSkillWriter(root).SkillRevision("fresh-helper")
		require.NoError(t, err)
		assert.Equal(t, revision, installed)
		// Fix round 4, part D item 2: an install publishes through the skill pipeline, whose history
		// is the per-skill store under skills.SkillHistoryRoot(root)/<slug>/versions/<id>.json
		// (pkg/skills/history.go). The first RED draft read SkillWriter.ListVersions, the legacy
		// ".versions" snapshot folder of the older editing tool, which an install never writes, so
		// that read could not see this publication whatever the installer did. Same oracle: exactly
		// one installed history publication for the one approved install.
		manifests := t9ManifestFiles(t, root, "fresh-helper")
		assert.Len(t, manifests, 1, "one actual installed history publication")
		assertFreshSkillTrust(t, h.api.homePath, "fresh-helper", true)
		assertNativeInstallAuditCount(t, h, 1)
		assert.Empty(t, h.reg.pendingApprovals())
		// A later external edit is a new content revision, never covered by
		// the durable approval of the staged R1, even under permission Allow.
		require.NoError(t, os.WriteFile(filepath.Join(root, "fresh-helper", "SKILL.md"), []byte(nativeInstallR2), 0o600))
		assertFreshSkillTrust(t, h.api.homePath, "fresh-helper", false)
		assertNativeInstallChangedLoad(t, h)
	} else {
		assert.NoDirExists(t, filepath.Join(root, "fresh-helper"))
		assert.NoFileExists(t, trustApprovalsPath(h.api), "refused install records no trust approval")
		assertNativeInstallAuditCount(t, h, 1)
	}
}
func nativeStagedSkillFiles(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(filepath.Join(root, ".staging"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			paths = append(paths, path)
		}
		return nil
	})
	require.NoError(t, err, "unreadable/missing staging is not cleanup proof")
	return paths
}
func assertNativeInstallAuditCount(t *testing.T, h *nativeSkillHarness, count int) {
	t.Helper()
	requests := 0
	for _, row := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskRequested && fields["agent_id"] == "test-agent" && fields["tool_name"] == "install_skill" {
			requests++
		}
	}
	assert.Equal(t, count, requests, "exact native request count, not a seeded card count")
}
func assertNativeInstallChangedLoad(t *testing.T, h *nativeSkillHarness) {
	t.Helper()
	store := agentstore.New(h.api.homePath)
	_, err := store.Update("test-agent", func(a *config.AgentConfig) error { a.Skills = append(a.Skills, "fresh-helper"); return nil })
	require.NoError(t, err)
	cfg := h.api.agentLoop.GetConfig()
	persisted, err := store.Get("test-agent")
	require.NoError(t, err)
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "test-agent" {
			cfg.Agents.List[i] = *persisted
		}
	}
	// Literal independent next-turn Given; no output verdict is injected.
	require.NoError(t, json.Unmarshal([]byte(`{"skill_policies":{"fresh-helper":"allow"}}`), &cfg.Sandbox))
	_, err = h.api.agentLoop.UpsertAgentFast(cfg, "test-agent")
	require.NoError(t, err)
	h.script.tool = "Skill"
	h.script.args = map[string]any{"name": "fresh-helper"}
	run := h.begin(t)
	entry := h.pending(t, run)
	snap := h.handler.pendingApprovalSnapshot(entry)
	assert.Equal(t, []string{"approve_version", "deny"}, snap.OfferedActions)
	assert.Empty(t, snap.AllowDurations)
	require.NotNil(t, snap.Skill)
	revision, err := skills.NewSkillWriter(filepath.Join(h.api.homePath, "skills")).SkillRevision("fresh-helper")
	require.NoError(t, err)
	assert.Equal(t, revision, snap.Skill.Revision)
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
	require.Equal(t, http.StatusOK, w.Code)
	h.finish(t, run, false)
	for _, request := range h.script.captured(run.prompt) {
		raw, err := json.Marshal(request)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "F18-FRESH-R2-INSTRUCTIONS-5M")
	}
}
func nativeAuditScalarValues(fields map[string]any) []string {
	var out []string
	for _, v := range fields {
		if text, ok := v.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// Explicitly do not use time.Sleep, a fake publish outcome, a guessed trust-file
// record container, or an always-allow grant to stand in for these observations.
