//go:build goolm && stdjson

package gateway

// Team-lead round after handoff-9, item 3 (Skills & Tools #1963, C-19 "When
// the new rules apply"): while the one-time upgrade has not reached an agent,
// that agent is resolved from its planned values. Every agentstore read
// already serves them (Get, List); the registry the running loop uses is fed
// by the store's notifier, so every write path must hand it the same record
// Get returns, not the raw record on disk. dfecf876b did this for
// RemoveSkillGrant and AddSkillBindings; this test covers Store.Create,
// Store.Update, Store.MutateState, Store.MutateStateWithAgentMD and
// Store.CreateState.
//
// Oracle: the record Store.Get returns right after the write (the C-19
// overlay) — the registry must never hold a different value than a read.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/skillperm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type plannedValuesNotifier struct {
	mu   sync.Mutex
	last map[string]*config.AgentConfig
}

func (n *plannedValuesNotifier) AgentUpserted(id string, cfg *config.AgentConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last == nil {
		n.last = map[string]*config.AgentConfig{}
	}
	n.last[id] = cfg
}

func (n *plannedValuesNotifier) AgentDeleted(string) {}

func (n *plannedValuesNotifier) got(t *testing.T, id string) *config.AgentConfig {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	rec, ok := n.last[id]
	require.True(t, ok, "the write notified the registry for %q", id)
	return rec
}

// waitingAgentHome is an old-shape home whose upgrade wrote config.json but
// not test-agent's record: test-agent stays pending (C-19 failed agent write).
func waitingAgentHome(t *testing.T) string {
	t.Helper()
	home := seedOldShapeHome(t, "deny")
	err := skillperm.Run(home, skillperm.Options{BeforeAgentWrite: func(agent string) error {
		if agent == waitingAgentID {
			return errors.New(waitingWriteFault)
		}
		return nil
	}})
	_ = err // a failed agent write is reported by the upgrade; the agent stays pending
	unfinished, err := skillperm.UnfinishedAgents(home)
	require.NoError(t, err)
	require.NotEmpty(t, unfinished, "setup: the upgrade has not reached %s", waitingAgentID)
	return home
}

func skillOverrides(a *config.AgentConfig) map[string]config.ToolPolicy {
	if a == nil || a.Tools == nil || a.Tools.Skills == nil {
		return nil
	}
	return a.Tools.Skills.Policies
}

func builtinSkillEntry(a *config.AgentConfig) (config.ToolPolicy, bool) {
	if a == nil || a.Tools == nil {
		return "", false
	}
	v, ok := a.Tools.Builtin.Policies["Skill"]
	return v, ok
}

// assertNotifiedAsRead compares the registry's record with Store.Get.
func assertNotifiedAsRead(t *testing.T, store *agentstore.Store, n *plannedValuesNotifier, id string) {
	t.Helper()
	read, err := store.Get(id)
	require.NoError(t, err)
	require.NotEmpty(t, skillOverrides(read), "instrument: Get serves the planned skill overrides of a waiting agent")
	_, readHasOld := builtinSkillEntry(read)
	require.False(t, readHasOld, "instrument: Get drops the old Skill tool entry of a waiting agent")
	notified := n.got(t, id)
	assert.Equal(t, skillOverrides(read), skillOverrides(notified),
		"C-19: the registry receives the planned skill overrides, as Get returns them")
	_, notifiedHasOld := builtinSkillEntry(notified)
	assert.False(t, notifiedHasOld, "C-19: the registry does not receive the old Skill tool entry of a waiting agent")
}

func oldShapeRecord() config.AgentConfig {
	record := config.AgentConfig{
		ID: waitingAgentID, Name: waitingAgentID, Type: config.AgentTypeCustom,
		Skills: []string{waitingSkillKey}, Tools: coreagent.NewCustomAgentToolsCfg(),
	}
	record.Tools.Builtin.Policies["Skill"] = config.ToolPolicyDeny
	return record
}

func TestAgentStoreWritePaths_NotifyPlannedValues(t *testing.T) {
	t.Run("Update", func(t *testing.T) {
		home := waitingAgentHome(t)
		store, n := agentstore.New(home), &plannedValuesNotifier{}
		store.SetNotifier(n)
		_, err := store.Update(waitingAgentID, func(a *config.AgentConfig) error {
			a.Description = "changed while the upgrade waits"
			return nil
		})
		require.NoError(t, err)
		assertNotifiedAsRead(t, store, n, waitingAgentID)
	})
	t.Run("MutateState", func(t *testing.T) {
		home := waitingAgentHome(t)
		store, n := agentstore.New(home), &plannedValuesNotifier{}
		store.SetNotifier(n)
		state, err := store.ReadState(waitingAgentID)
		require.NoError(t, err)
		_, err = store.MutateState(waitingAgentID, state.Revision, func(a *config.AgentConfig) error {
			a.Description = "changed while the upgrade waits"
			return nil
		}, nil)
		require.NoError(t, err)
		assertNotifiedAsRead(t, store, n, waitingAgentID)
	})
	t.Run("MutateStateWithAgentMD", func(t *testing.T) {
		home := waitingAgentHome(t)
		store, n := agentstore.New(home), &plannedValuesNotifier{}
		store.SetNotifier(n)
		state, err := store.ReadState(waitingAgentID)
		require.NoError(t, err)
		md := "# Agent notes\n"
		_, err = store.MutateStateWithAgentMD(waitingAgentID, state.Revision, func(a *config.AgentConfig) error {
			a.Description = "changed while the upgrade waits"
			return nil
		}, nil, &md)
		require.NoError(t, err)
		assertNotifiedAsRead(t, store, n, waitingAgentID)
	})
	for _, path := range []string{"Create", "CreateState"} {
		t.Run(path, func(t *testing.T) {
			home := waitingAgentHome(t)
			// The waiting agent's record is gone and written again through
			// the create path while the upgrade still waits for it.
			require.NoError(t, os.Remove(filepath.Join(home, "entities", "agents", waitingAgentID+".json")))
			store, n := agentstore.New(home), &plannedValuesNotifier{}
			store.SetNotifier(n)
			record := oldShapeRecord()
			if path == "Create" {
				require.NoError(t, store.Create(waitingAgentID, &record))
			} else {
				_, err := store.CreateState(waitingAgentID, &record, "")
				require.NoError(t, err)
			}
			assertNotifiedAsRead(t, store, n, waitingAgentID)
			v, ok := builtinSkillEntry(&record)
			assert.True(t, ok && v == config.ToolPolicyDeny, "the caller's own record is not changed by the overlay")
		})
	}
}
