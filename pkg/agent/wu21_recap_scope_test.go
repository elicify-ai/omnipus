package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type wu21Fixture struct {
	home, agentHome, auditDir string
	al                        *AgentLoop
	ag                        *AgentInstance
	store                     *session.UnifiedStore
}

func wu21New(t *testing.T) wu21Fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNIPUS_HOME", home)
	for _, ws := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(home, "workspaces", ws, "work"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agHome := filepath.Join(home, "agents", "self")
	cb := NewContextBuilder(agHome).WithAgentInfo("self", "Self")
	ag := &AgentInstance{ID: "self", Home: agHome, ContextBuilder: cb}
	t.Cleanup(cb.Memory().Close)
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close session store: %v", closeErr)
		}
	})
	auditDir := t.TempDir()
	log, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Errorf("close audit: %v", err)
		}
	})
	al := &AgentLoop{sharedSessionStore: store, auditLogger: log, registry: &AgentRegistry{agents: map[string]*AgentInstance{"self": ag}}}
	return wu21Fixture{home: home, agentHome: agHome, auditDir: auditDir, al: al, ag: ag, store: store}
}

func (f wu21Fixture) recap(t *testing.T, ws, text string) *agentLoopRunRecap {
	t.Helper()
	meta, err := f.store.NewSession(session.SessionTypeChat, "webchat", "self")
	if err != nil {
		t.Fatal(err)
	}
	if setErr := f.store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}); setErr != nil {
		t.Fatal(setErr)
	}
	if appendErr := f.store.AppendTranscript(meta.ID, session.TranscriptEntry{Role: "user", Content: text, AgentID: "self"}); appendErr != nil {
		t.Fatal(appendErr)
	}
	body, err := json.Marshal(map[string]any{"recap": text, "went_well": []string{}, "needs_improvement": []string{}, "worth_remembering": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	rr := &agentLoopRunRecap{al: f.al, sessionID: meta.ID, trigger: "idle", resp: &providers.LLMResponse{Content: string(body)}}
	if rr.loadTranscript() {
		t.Fatal("positive control: real session metadata/transcript failed to load")
	}
	return rr
}

func (f wu21Fixture) path(ws, agent string) string {
	return filepath.Join(f.home, "workspaces", ws, ".omnipus", "carry-forward", agent+".md")
}

// §3 reachability oracle: a real success recap from A must not reach B's session.
func TestWU21_RealRecapInAIsAbsentFromSessionB(t *testing.T) {
	f := wu21New(t)
	rr := f.recap(t, "alpha", "WU21_ALPHA_RECAP")
	f.ag.ContextBuilder.Memory().SharedRoom(f.ag.ContextBuilder.Memory().scopeForWorkspace("beta")) // unrelated lookup must not retarget recap.
	rr.persistResponse()
	if err := f.ag.ContextBuilder.Memory().AppendLongTermToScope("WU21_PRIVATE_NOTE_STAYS_AGENT_WIDE", "lesson_learned", memrooms.RoomScopePrivate, f.ag.ContextBuilder.Memory().scopeForWorkspace("")); err != nil {
		t.Fatalf("FR-219 private-note control could not write: %v", err)
	}
	bSession, err := f.store.GetOrCreateMainSession("beta", "self")
	if err != nil {
		t.Fatal(err)
	}
	promptA := f.ag.ContextBuilder.BuildSystemPromptForWorkspace("alpha")
	promptB := f.ag.ContextBuilder.BuildSystemPromptForWorkspace(bSession.WorkspaceID)
	t.Run("same_workspace_positive", func(t *testing.T) {
		if !strings.Contains(promptA, "WU21_ALPHA_RECAP") {
			t.Fatal("positive control: own workspace prompt lacks its real recap")
		}
	})
	t.Run("deliberate_private_note_agent_wide_positive", func(t *testing.T) {
		if !strings.Contains(promptA, "WU21_PRIVATE_NOTE_STAYS_AGENT_WIDE") || !strings.Contains(promptB, "WU21_PRIVATE_NOTE_STAYS_AGENT_WIDE") {
			t.Fatal("FR-219: deliberate private notes must still be present across workspaces")
		}
	})
	if count := strings.Count(promptA, "## Last Session\n"); count != 1 {
		t.Errorf("§3.2(4): workspace A Last Session heading count=%d want 1", count)
	}
	if strings.Contains(promptB, "## Last Session") {
		t.Error("§3.2(4): workspace B without a pair summary rendered a Last Session block")
	}
	if strings.Contains(promptB, "WU21_ALPHA_RECAP") {
		t.Error("FR-197: workspace A's real recap leaked into workspace B's session prompt")
	}
	b, err := os.ReadFile(f.path("alpha", "self"))
	if err != nil || !strings.Contains(string(b), "WU21_ALPHA_RECAP") {
		t.Errorf("§3.2(2): own pair file missing or wrong: err=%v", err)
	}
	if heading, recap := strings.Index(promptA, "## Last Session\n"), strings.Index(promptA, "WU21_ALPHA_RECAP"); heading < 0 || recap <= heading {
		t.Error("§3.2(4): own recap must appear after the workspace's Last Session heading")
	}
	if _, err := os.Stat(f.path("beta", "self")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("immutable scope: recap A wrote to mutable workspace B; err=%v", err)
	}
}

func TestWU21_ConcurrentSuccessAndFallbackKeepPairScope(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "success"
		if fallback {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			f := wu21New(t)
			a := f.recap(t, "alpha", "WU21_ALPHA_ONLY")
			b := f.recap(t, "beta", "WU21_BETA_ONLY")
			f.ag.ContextBuilder.Memory().SharedRoom(f.ag.ContextBuilder.Memory().scopeForWorkspace("beta"))
			var wg sync.WaitGroup
			var errs [2]error
			for i, rr := range []*agentLoopRunRecap{a, b} {
				wg.Add(1)
				go func(i int, rr *agentLoopRunRecap) {
					defer wg.Done()
					if fallback {
						errs[i] = f.al.writeHeuristicFallbackRetroWithCount(rr.sessionID, "idle", "llm_error", f.ag, len(rr.entries), 0, rr.carryForward)
					} else {
						rr.persistResponse()
					}
				}(i, rr)
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					t.Errorf("recap write failed: %v", err)
				}
			}
			for i, ws := range []string{"alpha", "beta"} {
				own, other := "WU21_ALPHA_ONLY", "WU21_BETA_ONLY"
				if i == 1 {
					own, other = other, own
				}
				content, err := os.ReadFile(f.path(ws, "self"))
				if err != nil || !strings.Contains(string(content), own) || strings.Contains(string(content), other) {
					t.Errorf("FR-197 %s %s pair file: got %q err=%v", name, ws, content, err)
				}
				prompt := f.ag.ContextBuilder.BuildSystemPromptForWorkspace(ws)
				if !strings.Contains(prompt, own) || strings.Contains(prompt, other) {
					t.Errorf("FR-197 %s prompt for %s does not contain only its own recap", name, ws)
				}
			}
		})
	}
}

func TestWU21_NoWorkspaceRecapHasNoAgentWideFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		f := wu21New(t)
		const seeded = "WU21_SEEDED_PAIR_RECAP"
		if err := f.ag.ContextBuilder.Memory().WriteCarryForward(CarryForwardScope{AgentID: "self", WorkspaceID: "alpha"}, seeded); err != nil {
			t.Fatalf("valid pair positive source could not be written: %v", err)
		}
		f.ag.ContextBuilder.Memory().SharedRoom(f.ag.ContextBuilder.Memory().scopeForWorkspace("alpha")) // generic context must not follow this lookup.
		t.Run("valid_workspace_summary_positive", func(t *testing.T) {
			own := f.ag.ContextBuilder.BuildSystemPromptForWorkspace("alpha")
			if !strings.Contains(own, seeded) || strings.Count(own, "## Last Session\n") != 1 {
				t.Fatal("positive source: seeded workspace summary and heading missing from its own prompt")
			}
		})
		rr := f.recap(t, "", "WU21_NO_WORKSPACE")
		if fallback {
			err := f.al.writeHeuristicFallbackRetroWithCount(rr.sessionID, "idle", "llm_error", f.ag, 1, 0, rr.carryForward)
			if !errors.Is(err, ErrFallbackRecapSkipped) {
				t.Errorf("§3.2(3): no-workspace fallback must return ErrFallbackRecapSkipped, got %v", err)
			}
		} else {
			rr.persistResponse()
		}
		if outcome := lastRecapOutcome(t, f.auditDir); outcome != "skipped_no_workspace" {
			t.Errorf("§3.2(1): audit=%q want skipped_no_workspace", outcome)
		}
		old := filepath.Join(f.agentHome, ".omnipus", "last-session.md")
		if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("§3.2(1/7): no-workspace recap created old agent-wide file (err=%v)", err)
		}
		prompt := f.ag.ContextBuilder.BuildSystemPrompt()
		if strings.Contains(prompt, "WU21_NO_WORKSPACE") || strings.Contains(prompt, seeded) || strings.Contains(prompt, "## Last Session") {
			t.Error("§3.2(1/4): no-workspace prompt read a carry-forward fallback or rendered its heading")
		}
		if b, err := os.ReadFile(f.path("alpha", "self")); err != nil || string(b) != seeded {
			t.Errorf("§3.2(1): no-workspace recap changed valid pair source: bytes=%q err=%v", b, err)
		}
		cb := f.ag.ContextBuilder
		cb.BuildSystemPromptWithCacheForWorkspace("")
		if v := cb.workspacePromptCache.variants[""]; v != nil {
			for path := range v.existedAtCache {
				if strings.Contains(filepath.ToSlash(path), "/carry-forward/") || filepath.Base(path) == "last-session.md" {
					t.Errorf("§3.2(1): no-workspace prompt cache tracks carry-forward path %s", path)
				}
			}
		}
	}
}

func TestWU21_OldAgentFileIsIgnoredAndUntouched(t *testing.T) {
	f := wu21New(t)
	old := filepath.Join(f.agentHome, ".omnipus", "last-session.md")
	if err := os.WriteFile(old, []byte("WU21_LEGACY_MUST_BE_IGNORED"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.recap(t, "alpha", "WU21_NEW_PAIR_RECAP").persistResponse()
	t.Run("valid_pair_positive", func(t *testing.T) {
		if !strings.Contains(f.ag.ContextBuilder.BuildSystemPromptForWorkspace("alpha"), "WU21_NEW_PAIR_RECAP") {
			t.Fatal("positive source: new pair recap is not reachable in its own workspace")
		}
	})
	for _, ws := range []string{"", "alpha", "beta"} {
		prompt := f.ag.ContextBuilder.BuildSystemPromptForWorkspace(ws)
		if strings.Contains(prompt, "WU21_LEGACY_MUST_BE_IGNORED") {
			t.Errorf("§3.2(7): legacy agent-wide recap reached workspace %q", ws)
		}
		if ws == "" && (strings.Contains(prompt, "## Last Session") || strings.Contains(prompt, "WU21_NEW_PAIR_RECAP")) {
			t.Error("§3.2(4): no-workspace prompt rendered a recap or Last Session heading")
		}
	}
	generic := f.ag.ContextBuilder.Memory().GetMemoryContext()
	if strings.Contains(generic, "WU21_LEGACY_MUST_BE_IGNORED") || strings.Contains(generic, "WU21_NEW_PAIR_RECAP") || strings.Contains(generic, "## Last Session") {
		t.Error("§3.2(4): generic long-term memory context includes a recap or Last Session heading")
	}
	b, err := os.ReadFile(old)
	if err != nil || string(b) != "WU21_LEGACY_MUST_BE_IGNORED" {
		t.Errorf("§3.2(7): old file overwritten/migrated/deleted: bytes=%q err=%v", b, err)
	}
}
