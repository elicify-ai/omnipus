// sessionmode_test.go: tests for the ADR-092 D1 mode types, the
// global/per-agent presentation derivation (FR-001), and the session-scoped
// per-chat modifier store (FR-004).

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------
// GlobalShellMode — presentation over the EXISTING bash policy + GodMode
// ---------------------------------------------------------------------

func TestGlobalShellMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want ShellMode
	}{
		{
			name: "nil cfg fails closed to Ask",
			cfg:  nil,
			want: ShellModeAsk,
		},
		{
			name: "no bash entry at all -> Ask (fail closed)",
			cfg: &config.Config{
				Sandbox: config.OmnipusSandboxConfig{ToolPolicies: map[string]string{}},
			},
			want: ShellModeAsk,
		},
		{
			name: "bash=ask -> Ask",
			cfg: &config.Config{
				Sandbox: config.OmnipusSandboxConfig{ToolPolicies: map[string]string{"bash": "ask"}},
			},
			want: ShellModeAsk,
		},
		{
			name: "bash=deny -> Ask (nearest named mode, fail closed)",
			cfg: &config.Config{
				Sandbox: config.OmnipusSandboxConfig{ToolPolicies: map[string]string{"bash": "deny"}},
			},
			want: ShellModeAsk,
		},
		{
			name: "bash=allow, GodMode=false -> Auto",
			cfg: &config.Config{
				Sandbox: config.OmnipusSandboxConfig{
					ToolPolicies: map[string]string{"bash": "allow"},
					GodMode:      false,
				},
			},
			want: ShellModeAuto,
		},
		{
			name: "bash=allow, GodMode=true -> God",
			cfg: &config.Config{
				Sandbox: config.OmnipusSandboxConfig{
					ToolPolicies: map[string]string{"bash": "allow"},
					GodMode:      true,
				},
			},
			want: ShellModeGod,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GlobalShellMode(tc.cfg))
		})
	}
}

// ---------------------------------------------------------------------
// AgentShellModeOverride — presentation over an agent's EXISTING per-agent
// bash tool-policy override
// ---------------------------------------------------------------------

func agentCfgWithBashPolicy(id string, p config.ToolPolicy) config.AgentConfig {
	return config.AgentConfig{
		ID: id,
		Tools: &config.AgentToolsCfg{
			Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{"bash": p},
			},
		},
	}
}

func TestAgentShellModeOverride(t *testing.T) {
	t.Run("nil cfg -> no override", func(t *testing.T) {
		mode, has := AgentShellModeOverride(nil, "agent-1")
		assert.False(t, has)
		assert.Equal(t, ShellMode(""), mode)
	})

	t.Run("empty agentID -> no override", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			agentCfgWithBashPolicy("agent-1", config.ToolPolicyAllow),
		}}}
		mode, has := AgentShellModeOverride(cfg, "")
		assert.False(t, has)
		assert.Equal(t, ShellMode(""), mode)
	})

	t.Run("agent not found -> no override", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			agentCfgWithBashPolicy("agent-1", config.ToolPolicyAllow),
		}}}
		mode, has := AgentShellModeOverride(cfg, "agent-2")
		assert.False(t, has)
		assert.Equal(t, ShellMode(""), mode)
	})

	t.Run("agent with nil Tools -> no override (rides ceiling)", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			{ID: "agent-1"},
		}}}
		mode, has := AgentShellModeOverride(cfg, "agent-1")
		assert.False(t, has)
		assert.Equal(t, ShellMode(""), mode)
	})

	t.Run("agent with no bash entry -> no override", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			{ID: "agent-1", Tools: &config.AgentToolsCfg{}},
		}}}
		mode, has := AgentShellModeOverride(cfg, "agent-1")
		assert.False(t, has)
		assert.Equal(t, ShellMode(""), mode)
	})

	t.Run("agent bash=allow -> Auto, never God (D1: God is global-only)", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			agentCfgWithBashPolicy("agent-1", config.ToolPolicyAllow),
		}}}
		mode, has := AgentShellModeOverride(cfg, "agent-1")
		assert.True(t, has)
		assert.Equal(t, ShellModeAuto, mode)
	})

	t.Run("agent bash=ask -> Ask", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			agentCfgWithBashPolicy("agent-1", config.ToolPolicyAsk),
		}}}
		mode, has := AgentShellModeOverride(cfg, "agent-1")
		assert.True(t, has)
		assert.Equal(t, ShellModeAsk, mode)
	})

	t.Run("ADR-090 Jim-shaped agent bash=deny -> nearest named mode Ask", func(t *testing.T) {
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
			agentCfgWithBashPolicy("jim", config.ToolPolicyDeny),
		}}}
		mode, has := AgentShellModeOverride(cfg, "jim")
		assert.True(t, has)
		assert.Equal(t, ShellModeAsk, mode)
	})
}

// ---------------------------------------------------------------------
// SessionModeStore — the one genuinely new piece of state (FR-004)
// ---------------------------------------------------------------------

func TestSessionModeStore_GetSetRoundTrip(t *testing.T) {
	s := NewSessionModeStore()
	_, ok := s.Get("sess-1")
	assert.False(t, ok, "unset session must report no modifier")

	assert.True(t, s.Set("sess-1", ShellModeAsk))
	mode, ok := s.Get("sess-1")
	assert.True(t, ok)
	assert.Equal(t, ShellModeAsk, mode)

	// A second Set replaces the prior value outright (this method does not
	// itself enforce tighten-only — that is the write-time caller's job,
	// FR-003; ResolveEffectiveShellMode is the resolution-time backstop).
	assert.True(t, s.Set("sess-1", ShellModeAuto))
	mode, ok = s.Get("sess-1")
	assert.True(t, ok)
	assert.Equal(t, ShellModeAuto, mode)
}

func TestSessionModeStore_NilReceiverSafe(t *testing.T) {
	var s *SessionModeStore
	_, ok := s.Get("sess-1")
	assert.False(t, ok)
	assert.False(t, s.Set("sess-1", ShellModeAsk))
	assert.NotPanics(t, func() { s.InheritFrom("src", "dst") })
	assert.NotPanics(t, func() { s.ClearSession("sess-1") })
}

func TestSessionModeStore_SetRejectsEmptyKeyOrMode(t *testing.T) {
	s := NewSessionModeStore()
	assert.False(t, s.Set("", ShellModeAsk), "empty sessionID must never key a grant")
	assert.False(t, s.Set("sess-1", ""), "empty mode must never be recorded")
	_, ok := s.Get("sess-1")
	assert.False(t, ok)
}

func TestSessionModeStore_ClearSession(t *testing.T) {
	s := NewSessionModeStore()
	s.Set("sess-1", ShellModeAsk)
	s.Set("sess-2", ShellModeAuto)

	s.ClearSession("sess-1")

	_, ok := s.Get("sess-1")
	assert.False(t, ok, "cleared session must have no modifier")
	mode, ok := s.Get("sess-2")
	assert.True(t, ok, "clearing one session must not affect another")
	assert.Equal(t, ShellModeAuto, mode)
}

// TestSessionModeStore_InheritFrom_CopiesToDelegate proves the delegation
// half of FR-005: a delegate's OWN session (dst) picks up the parent's (src)
// chat modifier, copy-at-spawn, exactly as ApprovalGrantStore.InheritFrom
// does for tool grants (ADR-057).
func TestSessionModeStore_InheritFrom_CopiesToDelegate(t *testing.T) {
	s := NewSessionModeStore()
	s.Set("parent-session", ShellModeAsk)

	// Before inheritance: no proof required, but assert the negative first
	// so the "after" assertion below is meaningful.
	_, ok := s.Get("child-session")
	assert.False(t, ok, "delegate session must start with no modifier")

	s.InheritFrom("parent-session", "child-session")

	mode, ok := s.Get("child-session")
	assert.True(t, ok, "delegate session must inherit the parent's modifier")
	assert.Equal(t, ShellModeAsk, mode)

	// Copy-at-spawn, not live: a later change to the parent's modifier must
	// NOT retroactively change the already-spawned delegate's copy.
	s.Set("parent-session", ShellModeAuto)
	mode, ok = s.Get("child-session")
	assert.True(t, ok)
	assert.Equal(t, ShellModeAsk, mode, "delegate's copy must not follow a later parent change")
}

// TestSessionModeStore_InheritFrom_NoSourceModifier_NoOp proves the "nothing
// to inherit" branch is a true no-op, mirroring
// ApprovalGrantStore.InheritFrom's source-miss behaviour.
func TestSessionModeStore_InheritFrom_NoSourceModifier_NoOp(t *testing.T) {
	s := NewSessionModeStore()
	s.InheritFrom("parent-with-no-modifier", "child-session")
	_, ok := s.Get("child-session")
	assert.False(t, ok)
}

// TestSessionModeStore_InheritFrom_TightensExistingDestination proves
// InheritFrom never LOOSENS an existing destination value — it is a union
// via tighterShellMode, not a blind overwrite (sessionmode.go's InheritFrom
// doc comment).
func TestSessionModeStore_InheritFrom_TightensExistingDestination(t *testing.T) {
	s := NewSessionModeStore()

	t.Run("existing dst tighter than incoming src -> dst wins", func(t *testing.T) {
		s.Set("src-a", ShellModeAuto)
		s.Set("dst-a", ShellModeAsk)
		s.InheritFrom("src-a", "dst-a")
		mode, ok := s.Get("dst-a")
		assert.True(t, ok)
		assert.Equal(t, ShellModeAsk, mode, "the stricter existing destination value must survive")
	})

	t.Run("incoming src tighter than existing dst -> src wins", func(t *testing.T) {
		s.Set("src-b", ShellModeAsk)
		s.Set("dst-b", ShellModeAuto)
		s.InheritFrom("src-b", "dst-b")
		mode, ok := s.Get("dst-b")
		assert.True(t, ok)
		assert.Equal(t, ShellModeAsk, mode, "the stricter incoming value must win over a looser existing one")
	})
}

func TestSessionModeStore_InheritFrom_EmptyKeys_NoOp(t *testing.T) {
	s := NewSessionModeStore()
	s.Set("src", ShellModeAsk)
	s.InheritFrom("", "dst")
	s.InheritFrom("src", "")
	_, ok := s.Get("dst")
	assert.False(t, ok)
	_, ok = s.Get("")
	assert.False(t, ok)
}

// ---------------------------------------------------------------------
// tighterShellMode / modeRank
// ---------------------------------------------------------------------

func TestTighterShellMode(t *testing.T) {
	assert.Equal(t, ShellModeAsk, tighterShellMode(ShellModeAsk, ShellModeAuto))
	assert.Equal(t, ShellModeAsk, tighterShellMode(ShellModeAuto, ShellModeAsk))
	assert.Equal(t, ShellModeAsk, tighterShellMode(ShellModeAsk, ShellModeGod))
	assert.Equal(t, ShellModeAuto, tighterShellMode(ShellModeAuto, ShellModeGod))
	assert.Equal(t, ShellModeGod, tighterShellMode(ShellModeGod, ShellModeGod))
	// Tie returns a.
	assert.Equal(t, ShellModeAuto, tighterShellMode(ShellModeAuto, ShellModeAuto))
}

func TestModeRank_UnknownValueFailsClosedToTightest(t *testing.T) {
	assert.Equal(t, 0, modeRank(ShellMode("bogus")))
	assert.Equal(t, modeRank(ShellModeAsk), modeRank(ShellMode("bogus")))
}
