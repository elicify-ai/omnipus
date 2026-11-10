package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func ownerWiringRunner(t *testing.T, accounts bool) (*scheduledRunner, *session.UnifiedStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := baseConfig()
	cfg.Agents.Defaults.Home = home
	if accounts {
		cfg.Gateway.Users = ownerWiringAccounts()
	} else {
		cfg.Gateway.Users = []config.UserConfig{}
	}
	runner, exec, msgBus, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	runner.home = home
	t.Cleanup(msgBus.Close)
	t.Cleanup(func() { require.NoError(t, exec.store.Close()) })
	require.NoError(t, writeWorkspaceFile(home, storedWorkspace{
		ID: "default", Name: "Owner schedule workspace", Status: "active",
		IsDefault: true, CoreTeam: []string{"mia"},
		CreatedAt: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z",
	}))
	return runner, exec.store
}

func TestOwnerWiringI1_ScheduleAdoptsOnceWithoutChangingRecency(t *testing.T) {
	// I1(1,2,5): an ordinary main-mode schedule, not a heartbeat exemption.
	runner, store := ownerWiringRunner(t, true)
	main, err := store.GetOrCreateMainSession("default", "mia")
	require.NoError(t, err)
	require.Equal(t, "", main.Owner, "fixture predates any human owner")
	updated := ownerWiringHistoricalRecency(t, store, main.ID)
	job := &cron.CronJob{ID: "owner-schedule-alice", AgentID: "mia", CreatedBy: "alice", SessionMode: cron.SessionModeMain}

	id, err := runner.pickSession(job, "mia")
	require.NoError(t, err, "resolve the main through the real scheduled path")
	require.Equal(t, main.ID, id, "the existing main is reused, not replaced")
	adopted := ownerWiringFreshMeta(t, store, id)
	require.Equal(t, "alice", adopted.Owner, "the schedule's human creator adopts the existing main")
	require.Equal(t, updated, adopted.UpdatedAt, "schedule owner adoption is not session activity")
	beforeBytes := ownerWiringIdentityBytes(t, store, id)

	later := &cron.CronJob{ID: "owner-schedule-bob", AgentID: "mia", CreatedBy: "bob", SessionMode: cron.SessionModeMain}
	laterID, err := runner.pickSession(later, "mia")
	require.NoError(t, err)
	require.Equal(t, id, laterID)
	require.Equal(t, adopted, ownerWiringFreshMeta(t, store, laterID), "a later person's job changes no existing metadata")
	require.Equal(t, beforeBytes, ownerWiringIdentityBytes(t, store, id), "a later job does not rewrite the owned identity")
}

func TestOwnerWiringI1_NewScheduledMainUsesCreatorOrOperator(t *testing.T) {
	for _, tc := range []struct {
		name, creator, want string
		accounts            bool
	}{
		{name: "human_creator", creator: "alice", accounts: true, want: "alice"},
		{name: "machine_creator", creator: "cli", accounts: true, want: "operator"},
		{name: "system_creator", creator: "", accounts: true, want: "operator"},
		{name: "no_accounts_system_creator", creator: "", accounts: false, want: ""},
		{name: "no_accounts_machine_creator", creator: "cli", accounts: false, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store := ownerWiringRunner(t, tc.accounts)
			job := &cron.CronJob{ID: "owner-new-main", AgentID: "mia", CreatedBy: tc.creator, SessionMode: cron.SessionModeMain}
			id, err := runner.pickSession(job, "mia")
			require.NoError(t, err)
			persisted := ownerWiringFreshMeta(t, store, id)
			require.Equal(t, tc.want, persisted.Owner, "new main uses the person's identity or first eligible operator, never cli")
			require.Equal(t, session.SessionTypeMain, persisted.Type)
			require.Equal(t, "default", persisted.WorkspaceID)
			require.Equal(t, "mia", persisted.AgentID, "human attribution never replaces the agent")
		})
	}
}
