// subturn_requested_skill_test.go — ADR-072 D9 regression coverage for
// delegate's `requested_skill` (spec FR-050..056): resolution happens
// against the CHILD's (execSource's) OWN ContextBuilder — never the
// delegating parent's — and the three outcomes (granted/denied/unresolvable)
// must be distinguishable, not just "failed".
//
// See docs/internal/architecture/ADR-072-skill-activation-and-loading.md D9
// and docs/internal/specs/skill-activation-and-loading-spec.md FR-050..056,
// test rows 40-44.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// forcedSkillsCapturingProvider is a providers.LLMProvider stub that records,
// at Chat() call time, the CHILD sub-turn's own opts.ForcedSkills (observed
// via turnStateFromContext, the same context path
// subturn_target_identity_test.go's policyCapturingProvider uses) — the
// exact field ADR-072 D9 names as the mechanism a granted requested_skill
// reaches the child through ("appended to the child's
// processOptions.ForcedSkills").
type forcedSkillsCapturingProvider struct {
	mu              sync.Mutex
	calls           int
	sawForcedSkills []string
}

func (p *forcedSkillsCapturingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if ts := turnStateFromContext(ctx); ts != nil {
		p.sawForcedSkills = append([]string(nil), ts.opts.ForcedSkills...)
	}
	return &providers.LLMResponse{Content: "child output"}, nil
}

func (p *forcedSkillsCapturingProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func (p *forcedSkillsCapturingProvider) snapshot() (calls int, forcedSkills []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.sawForcedSkills
}

// rsTestFixture bundles everything a requested_skill test needs: a real
// two-agent AgentLoop (a delegating parent and a delegation target), each
// with its own workspace, plus the capturing provider both agents share.
type rsTestFixture struct {
	al              *AgentLoop
	parent          *AgentInstance
	target          *AgentInstance
	provider        *forcedSkillsCapturingProvider
	parentWorkspace string
	targetWorkspace string
}

// newRSFixture builds the fixture with the given per-agent skill grants
// (config.AgentConfig.Skills — nil/empty both deny everything per ADR-072
// D5). Neither workspace has any skill file written yet; callers write their
// own via rsWriteSkill.
func newRSFixture(t *testing.T, parentSkills, targetSkills []string) *rsTestFixture {
	t.Helper()
	parentWorkspace := t.TempDir()
	targetWorkspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:           parentWorkspace,
				DefaultModel:   config.DefaultModel{Provider: "mock", Model: "parent-model"},
				DefaultAgentID: "rs-parent",
			},
			List: []config.AgentConfig{
				{
					ID:      "rs-parent",
					Type:    config.AgentTypeCore,
					Default: true,
					Home:    parentWorkspace,
					Skills:  parentSkills,
				},
				{
					ID:     "rs-target",
					Type:   config.AgentTypeWorker,
					Home:   targetWorkspace,
					Skills: targetSkills,
					// No Subagents.Executor — resolves native dispatch.
				},
			},
		},
	}

	provider := &forcedSkillsCapturingProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	target, ok := al.registry.GetAgent("rs-target")
	if !ok {
		t.Fatal("test setup: target agent not registered")
	}

	return &rsTestFixture{
		al:              al,
		parent:          parent,
		target:          target,
		provider:        provider,
		parentWorkspace: parentWorkspace,
		targetWorkspace: targetWorkspace,
	}
}

// rsWriteSkill creates a valid SKILL.md under workspace/skills/<slug>/ —
// same shape as skill_allowlist_test.go's writeSkill, duplicated here (this
// file's own fixture) rather than reused across files to keep this test's
// dependency surface self-contained and explicit about exactly what shelf
// each scenario's skill lives on.
func rsWriteSkill(t *testing.T, workspace, slug string) {
	t.Helper()
	writeSkill(t, workspace, slug)
}

// rsSpawn mints a real parent session/turnState and calls spawnSubTurn
// delegating from fx.parent to "rs-target" with the given task text and
// requested_skill.
func rsSpawn(t *testing.T, fx *rsTestFixture, task, requestedSkill string) (*tools.ToolResult, error) {
	t.Helper()
	parentSessionID, sessionStore := stiMintParentSession(t, fx.al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "rs-parent-turn",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               fx.parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     sessionStore,
	}
	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-rs")
	return spawnSubTurn(spawnCtx, fx.al, parentTS, SubTurnConfig{
		Model:          "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt:   task,
		TargetAgentID:  "rs-target",
		Async:          false,
		RequestedSkill: requestedSkill,
	})
}

// TestDelegate_RequestedSkillLoadsInChildFirstTurn (spec test row 40,
// FR-050/052/056): a requested_skill the delegation TARGET is granted must
// load into the child's first turn via ForcedSkills, and the sub-turn must
// otherwise complete normally (no dispatch failure).
func TestDelegate_RequestedSkillLoadsInChildFirstTurn(t *testing.T) {
	fx := newRSFixture(t, nil, []string{"finance-news"})
	rsWriteSkill(t, fx.targetWorkspace, "finance-news")

	result, err := rsSpawn(t, fx, "please help with the numbers", "finance-news")
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.IsError || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	calls, forced := fx.provider.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — the child sub-turn did not run")
	}
	if len(forced) != 1 || forced[0] != "finance-news" {
		t.Fatalf("child's opts.ForcedSkills = %v, want [finance-news] — the granted requested_skill "+
			"must be loaded into the child's first turn via ForcedSkills (ADR-072 D9)", forced)
	}
}

// TestDelegate_RequestedSkillDeniedByReceiverGrant (spec test row 41,
// FR-053): a requested_skill that EXISTS on the target's own shelf but is
// NOT in the target's grant list must fail the delegation at dispatch —
// before the child's first model call — with the DelegationDeniedCode
// discriminator, naming both agent and skill.
func TestDelegate_RequestedSkillDeniedByReceiverGrant(t *testing.T) {
	// The skill is installed on the target's own shelf, but the target's
	// grant list does not include it — this is "denied", not "not found":
	// existence and grant are two different questions.
	fx := newRSFixture(t, nil, []string{"some-other-skill"})
	rsWriteSkill(t, fx.targetWorkspace, "finance-news")

	result, err := rsSpawn(t, fx, "please help with the numbers", "finance-news")
	if err == nil {
		t.Fatalf("expected a dispatch-time error, got result=%+v, err=nil", result)
	}
	if !errors.Is(err, tools.ErrRequestedSkillDenied) {
		t.Fatalf("err = %v, want it to wrap tools.ErrRequestedSkillDenied", err)
	}
	if errors.Is(err, tools.ErrRequestedSkillNotFound) {
		t.Fatal("a denied (installed-but-ungranted) slug must NOT also satisfy ErrRequestedSkillNotFound — " +
			"the two outcomes must stay distinct (FR-054)")
	}
	if !strings.Contains(err.Error(), "rs-target") || !strings.Contains(err.Error(), "finance-news") {
		t.Fatalf("err = %q, want it to name both the target agent (rs-target) and the skill (finance-news)", err.Error())
	}
	if result != nil {
		t.Fatalf("expected a nil result on dispatch-time denial, got %+v", result)
	}
	if calls, _ := fx.provider.snapshot(); calls != 0 {
		t.Fatalf("mock provider Chat was called %d times — the child must never reach its first model call "+
			"on a requested_skill denial (FR-053: fails BEFORE the child's first model call)", calls)
	}
}

// TestDelegate_RequestedSkillUnresolvableIsDistinct (spec test row 42,
// FR-054): a requested_skill slug that matches nothing on any shelf visible
// to the target at all must be a DISTINCT outcome from denial — never
// conflated — via the new SkillNotFoundCode discriminator (ErrRequestedSkillNotFound).
func TestDelegate_RequestedSkillUnresolvableIsDistinct(t *testing.T) {
	fx := newRSFixture(t, nil, nil)
	// Deliberately never write any skill file anywhere — the slug below
	// matches nothing on any shelf.
	const unresolvableSlug = "totally-unresolvable-skill-zzz-42"

	result, err := rsSpawn(t, fx, "please help with the numbers", unresolvableSlug)
	if err == nil {
		t.Fatalf("expected a dispatch-time error, got result=%+v, err=nil", result)
	}
	if !errors.Is(err, tools.ErrRequestedSkillNotFound) {
		t.Fatalf("err = %v, want it to wrap tools.ErrRequestedSkillNotFound", err)
	}
	if errors.Is(err, tools.ErrRequestedSkillDenied) {
		t.Fatal("an unresolvable slug must NOT also satisfy ErrRequestedSkillDenied — " +
			"the two outcomes must stay distinct (FR-054)")
	}
	if result != nil {
		t.Fatalf("expected a nil result on dispatch-time not-found, got %+v", result)
	}
	if calls, _ := fx.provider.snapshot(); calls != 0 {
		t.Fatalf("mock provider Chat was called %d times — an unresolvable requested_skill must also "+
			"fail before the child's first model call", calls)
	}
}

// TestDelegate_ParentGrantDoesNotAffectOutcome (spec test row 34/43,
// FR-051): the delegating PARENT's own grant is irrelevant — even when the
// parent itself holds (and could resolve) the exact same slug, the outcome
// is decided ENTIRELY by the target's own grant. The skill file exists on
// BOTH workspaces (so this isolates the grant question from an existence
// question), but only the parent's grant list includes it.
func TestDelegate_ParentGrantDoesNotAffectOutcome(t *testing.T) {
	fx := newRSFixture(t, []string{"finance-news"}, nil) // parent granted, target NOT granted
	rsWriteSkill(t, fx.parentWorkspace, "finance-news")
	rsWriteSkill(t, fx.targetWorkspace, "finance-news")

	// Sanity: the PARENT really can resolve this slug on its own — proving
	// the eventual denial below is not an accident of the skill being
	// unresolvable everywhere.
	if fx.parent.ContextBuilder == nil {
		t.Fatal("test setup: parent has no ContextBuilder")
	}
	if _, ok := fx.parent.ContextBuilder.ResolveSkillName("finance-news"); !ok {
		t.Fatal("test setup invariant broken: the parent must be able to resolve finance-news itself")
	}

	result, err := rsSpawn(t, fx, "please help with the numbers", "finance-news")
	if err == nil {
		t.Fatalf("expected a dispatch-time denial despite the PARENT holding the grant, got result=%+v, err=nil", result)
	}
	if !errors.Is(err, tools.ErrRequestedSkillDenied) {
		t.Fatalf("err = %v, want it to wrap tools.ErrRequestedSkillDenied — the TARGET's own (missing) grant "+
			"must decide the outcome, never the parent's", err)
	}
	if calls, _ := fx.provider.snapshot(); calls != 0 {
		t.Fatalf("mock provider Chat was called %d times — the parent's own grant must never let a "+
			"target-ungranted requested_skill through to the child's first model call", calls)
	}
}

// TestResolveRequestedSkillForChild_DirectOutcomes (ADR-072 Finding D) is a
// function-level regression test for resolveRequestedSkillForChild itself,
// covering all three outcomes plus the edge cases the top-level spawnSubTurn
// tests above don't isolate (nil ContextBuilder, no skillsLoader, empty/
// whitespace input). This is the direct-level counterpart to Finding D's
// fix: resolveRequestedSkillForChild used to call the uncached, full-
// directory-scanning SkillsLoader.ListSkills() TWICE on every denied/
// not-found outcome — once implicitly inside cb.ResolveSkillName, once again
// explicitly in the fallback loop. It now fetches the list ONCE
// (cb.skillsLoader.ListSkills()) and reuses it for both the resolution
// attempt (via the extracted cb.resolveSkillNameWithList helper) and the
// fallback membership check — verified here by inspection of the single
// remaining call site in resolveRequestedSkillForChild's body (subturn.go),
// since SkillsLoader has no seam to inject a call-counting double without
// changing production code for the sake of a test. This test instead proves
// the fix did not change behaviour: every outcome the pre-fix double-scan
// implementation produced is still produced identically.
func TestResolveRequestedSkillForChild_DirectOutcomes(t *testing.T) {
	t.Run("granted: slug installed and allowed", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkill(t, workspace, "finance-news")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"finance-news"})

		slug, outcome := resolveRequestedSkillForChild(cb, "finance-news")
		if outcome != requestedSkillGranted {
			t.Fatalf("outcome = %v, want requestedSkillGranted", outcome)
		}
		if slug != "finance-news" {
			t.Fatalf("slug = %q, want %q", slug, "finance-news")
		}
	})

	t.Run("granted: matched by display name, canonical slug returned", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkillWithName(t, workspace, "deploy", "Deploy Helper")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"deploy"})

		slug, outcome := resolveRequestedSkillForChild(cb, "Deploy Helper")
		if outcome != requestedSkillGranted {
			t.Fatalf("outcome = %v, want requestedSkillGranted", outcome)
		}
		if slug != "deploy" {
			t.Fatalf("slug = %q, want canonical slug %q", slug, "deploy")
		}
	})

	t.Run("denied: slug installed but not granted", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkill(t, workspace, "finance-news")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"some-other-skill"})

		slug, outcome := resolveRequestedSkillForChild(cb, "finance-news")
		if outcome != requestedSkillDenied {
			t.Fatalf("outcome = %v, want requestedSkillDenied", outcome)
		}
		if slug != "" {
			t.Fatalf("slug = %q, want empty on denial", slug)
		}
	})

	t.Run("unresolvable: slug matches nothing on any shelf", func(t *testing.T) {
		workspace := t.TempDir()
		cb := NewContextBuilder(workspace).WithSkillAllowlist(nil)

		slug, outcome := resolveRequestedSkillForChild(cb, "totally-unresolvable-zzz")
		if outcome != requestedSkillUnresolvable {
			t.Fatalf("outcome = %v, want requestedSkillUnresolvable", outcome)
		}
		if slug != "" {
			t.Fatalf("slug = %q, want empty when unresolvable", slug)
		}
	})

	t.Run("nil ContextBuilder is unresolvable, not a panic", func(t *testing.T) {
		slug, outcome := resolveRequestedSkillForChild(nil, "anything")
		if outcome != requestedSkillUnresolvable || slug != "" {
			t.Fatalf("got (%q, %v), want (\"\", requestedSkillUnresolvable)", slug, outcome)
		}
	})

	t.Run("empty/whitespace requested is unresolvable", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkill(t, workspace, "finance-news")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"finance-news"})

		for _, in := range []string{"", "   "} {
			slug, outcome := resolveRequestedSkillForChild(cb, in)
			if outcome != requestedSkillUnresolvable || slug != "" {
				t.Fatalf("input %q: got (%q, %v), want (\"\", requestedSkillUnresolvable)", in, slug, outcome)
			}
		}
	})
}

// writeSkillWithName creates workspace/skills/<slug>/SKILL.md with a display
// name distinct from the slug, so a resolution test can prove display-name
// matching returns the canonical slug rather than the free-form name.
func writeSkillWithName(t *testing.T, workspace, slug, displayName string) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + displayName + "\ndescription: A test skill with a sufficiently long description to validate.\n---\n\n# " + slug + "\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDelegate_TaskTextMentionGuaranteesNothing (spec test row 35/44,
// FR-055): naming a skill by slug inside the free-text task prompt — WITHOUT
// using requested_skill at all — must guarantee nothing. Even when the
// target holds a real grant for that exact skill, mentioning it in prose
// alone must NOT force it into the child's ForcedSkills — mechanism 1 has
// zero enforcement by design, distinct from mechanism 2 (requested_skill).
func TestDelegate_TaskTextMentionGuaranteesNothing(t *testing.T) {
	fx := newRSFixture(t, nil, []string{"finance-news"})
	rsWriteSkill(t, fx.targetWorkspace, "finance-news")

	// requested_skill is deliberately empty — only mechanism 1 (naming the
	// skill in the task prompt) is exercised here.
	result, err := rsSpawn(t, fx, "Please use the finance-news skill to help me.", "")
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.IsError || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	calls, forced := fx.provider.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — the child sub-turn did not run")
	}
	if len(forced) != 0 {
		t.Fatalf("child's opts.ForcedSkills = %v, want empty — merely naming a skill inside the free-text "+
			"task prompt must guarantee nothing (FR-055); only the requested_skill parameter forces "+
			"activation", forced)
	}
}
