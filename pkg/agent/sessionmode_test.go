// sessionmode_test.go: ADR-092 Auto-approve resolution across the three
// scopes (global default, per-agent off-switch, per-chat modifier) and the
// session-scoped per-chat modifier store. Expected values come from the
// contract text (SandboxConfig.auto_approve, Agent.auto_approve_disabled,
// SessionModeUpdateFrame), not from the implementation.

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestResolveAutoApprove(t *testing.T) {
	cfgWith := func(global bool, agents ...config.AgentConfig) *config.Config {
		c := &config.Config{}
		c.Sandbox.AutoApprove = global
		c.Agents.List = agents
		return c
	}
	disabled := config.AgentConfig{ID: "jim", AutoApproveDisabled: true}
	enabled := config.AgentConfig{ID: "mia"}

	cases := []struct {
		name    string
		cfg     *config.Config
		agentID string
		chat    *bool
		want    bool
	}{
		{"nil config is off", nil, "mia", nil, false},
		{"global off", cfgWith(false, enabled), "mia", nil, false},
		{"global on, agent follows", cfgWith(true, enabled), "mia", nil, true},
		{"global on, agent not in list follows global", cfgWith(true), "ghost", nil, true},
		{"global on, agent off-switch wins", cfgWith(true, disabled), "jim", nil, false},
		{"agent off-switch cannot turn Auto on when global is off", cfgWith(false, enabled), "mia", nil, false},
		{"chat on loosens past global off", cfgWith(false, enabled), "mia", boolPtr(true), true},
		{"chat on loosens past the agent off-switch", cfgWith(true, disabled), "jim", boolPtr(true), true},
		{"chat off tightens global on", cfgWith(true, enabled), "mia", boolPtr(false), false},
		{"chat modifier applies even with nil config", nil, "mia", boolPtr(true), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ResolveAutoApprove(tc.cfg, tc.agentID, tc.chat))
		})
	}
}

func TestSessionModeStore_GetSetRoundTrip(t *testing.T) {
	s := NewSessionModeStore()
	_, ok := s.Get("sess")
	assert.False(t, ok, "an untouched session has no modifier")

	assert.True(t, s.Set("sess", true))
	v, ok := s.Get("sess")
	assert.True(t, ok)
	assert.True(t, v)

	assert.True(t, s.Set("sess", false), "a later Set replaces the value")
	v, ok = s.Get("sess")
	assert.True(t, ok)
	assert.False(t, v)
}

func TestSessionModeStore_NilReceiverAndEmptyKeys(t *testing.T) {
	var nilStore *SessionModeStore
	assert.False(t, nilStore.Set("sess", true))
	_, ok := nilStore.Get("sess")
	assert.False(t, ok)
	nilStore.ClearSession("sess")
	nilStore.InheritFrom("a", "b")

	s := NewSessionModeStore()
	assert.False(t, s.Set("", true), "an empty session id must never become a bucket")
	_, ok = s.Get("")
	assert.False(t, ok)
}

func TestSessionModeStore_ClearSession(t *testing.T) {
	s := NewSessionModeStore()
	s.Set("sess", true)
	s.Set("other", false)
	s.ClearSession("sess")

	_, ok := s.Get("sess")
	assert.False(t, ok, "a cleared chat falls back to the agent and global defaults")
	v, ok := s.Get("other")
	assert.True(t, ok, "clearing one chat must not touch another")
	assert.False(t, v)
}

func TestSessionModeStore_InheritFrom(t *testing.T) {
	t.Run("copies the parent's modifier to the delegate", func(t *testing.T) {
		s := NewSessionModeStore()
		s.Set("parent", true)
		s.InheritFrom("parent", "child")
		v, ok := s.Get("child")
		assert.True(t, ok)
		assert.True(t, v)

		s.Set("parent", false)
		v, _ = s.Get("child")
		assert.True(t, v, "copy at spawn: a later parent change does not reach an already-spawned delegate")
	})
	t.Run("no parent modifier leaves the delegate unset", func(t *testing.T) {
		s := NewSessionModeStore()
		s.InheritFrom("parent", "child")
		_, ok := s.Get("child")
		assert.False(t, ok)
	})
	t.Run("off wins over an existing on at the destination", func(t *testing.T) {
		s := NewSessionModeStore()
		s.Set("parent", false)
		s.Set("child", true)
		s.InheritFrom("parent", "child")
		v, _ := s.Get("child")
		assert.False(t, v)
	})
	t.Run("an inherited on does not overwrite the delegate's own off", func(t *testing.T) {
		s := NewSessionModeStore()
		s.Set("parent", true)
		s.Set("child", false)
		s.InheritFrom("parent", "child")
		v, _ := s.Get("child")
		assert.False(t, v)
	})
}
