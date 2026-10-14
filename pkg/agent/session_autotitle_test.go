package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder ruling (UI feedback 4): every session other than a main is titled
// after its FIRST reply, from the person's first message; a title set by hand
// is never overwritten; a main is never auto-titled.
func TestAutoTitleAfterFirstReply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	provider := testutil.NewScenario().WithText("Sure, here is a plan.").WithUsage(10, 5).
		WithText("Sure, here is a plan.").WithUsage(10, 5).
		WithText("Sure, here is a plan.").WithUsage(10, 5).
		// Fourth turn: the non-chat session-type case below.
		WithText("Sure, here is a plan.").WithUsage(10, 5)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })
	registerAgent(t, al, home, "jim", provider)

	store := al.GetSessionStore()
	run := func(t *testing.T, id, msg string) {
		t.Helper()
		_, err := al.ProcessScheduled(context.Background(), "jim", id, msg, "web", id)
		require.NoError(t, err)
	}
	title := func(t *testing.T, id string) string {
		t.Helper()
		m, err := store.GetMeta(id)
		require.NoError(t, err)
		return m.Title
	}

	t.Run("an extra chat is titled after its first reply", func(t *testing.T) {
		chat, err := store.NewSession(session.SessionTypeChat, "web", "jim")
		require.NoError(t, err)
		require.Empty(t, title(t, chat.ID), "instrument: a fresh chat starts untitled")
		run(t, chat.ID, "Plan my week please")
		require.Equal(t, "Plan my week please", title(t, chat.ID))
	})

	t.Run("a hand-set title survives the first reply", func(t *testing.T) {
		chat, err := store.NewSession(session.SessionTypeChat, "web", "jim")
		require.NoError(t, err)
		mine := "Budget review"
		require.NoError(t, store.SetMeta(chat.ID, session.MetaPatch{Title: &mine}))
		run(t, chat.ID, "Plan my week please")
		require.Equal(t, mine, title(t, chat.ID))
	})

	t.Run("a main is never auto-titled", func(t *testing.T) {
		main, err := store.GetOrCreateMainSession("ws-1", "jim")
		require.NoError(t, err)
		run(t, main.ID, "Plan my week please")
		require.Empty(t, title(t, main.ID), "a main's title is derived on read, never stored")
	})

	// S1-16: the empty-reply guard. autoTitleAfterFirstReply returns early when
	// the reply's final content is empty, so a turn that produced no reply text
	// leaves the session untitled. Driven through the guard directly: an empty
	// scripted reply is retried by the provider (it consumes the next scripted
	// turn), so the run path cannot construct the state this guard exists for.
	// The control call on a second session proves the check can see the
	// difference rather than passing for an unrelated reason.
	t.Run("an empty first reply leaves the session untitled", func(t *testing.T) {
		const msg = "Plan my week please"
		empty, err := store.NewSession(session.SessionTypeChat, "web", "jim")
		require.NoError(t, err)
		control, err := store.NewSession(session.SessionTypeChat, "web", "jim")
		require.NoError(t, err)

		(&turnState{
			transcriptStore:     store,
			transcriptSessionID: empty.ID,
			opts:                processOptions{UserMessage: msg},
		}).autoTitleAfterFirstReply("")

		require.Empty(t, title(t, empty.ID),
			"a session whose first reply was empty must not be titled")

		(&turnState{
			transcriptStore:     store,
			transcriptSessionID: control.ID,
			opts:                processOptions{UserMessage: msg},
		}).autoTitleAfterFirstReply("Sure, here is a plan.")
		require.Equal(t, msg, title(t, control.ID),
			"instrument: the same fixture DOES title a session when the reply was non-empty")
	})

	// S1-16: the ruling covers EVERY session other than a main — including a
	// non-chat type. The guard excludes only a main, and SetTitleIfEmpty is what
	// enforces that, so a task session IS titled from its first message.
	t.Run("a non-chat, non-main session is titled too (the ruling names only mains as exempt)", func(t *testing.T) {
		tk, err := store.NewSession(session.SessionTypeTask, "web", "jim")
		require.NoError(t, err)
		require.Empty(t, title(t, tk.ID), "instrument: a fresh task session starts untitled")
		run(t, tk.ID, "Plan my week please")
		require.Equal(t, "Plan my week please", title(t, tk.ID),
			"the ruling exempts a MAIN only; a task session is titled from its first reply like any other")
	})
}
