package agentrecord_test

// Instrument tests for the stored-record reader (CHECK round 4, N19). The reader
// exists because two durable-storage observations each carried their own decoder and
// each decoded a record shape the store never writes: S-70 read agent.tools.skills.policies
// (an outer wrapper) and D16 read tools_cfg.skills.policies (a request name). A reader is
// trusted only if it (1) reads a record the REAL store wrote, (2) reads the spec carrier
// from a flat record, and (3) reads nothing from the wrong wrappers and fields. Nothing
// here depends on whether the product already carries tools.skills (spec C-4).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
)

// writeRecord plants a literal entity file where the store keeps it.
func writeRecord(t *testing.T, home, id, body string) {
	t.Helper()
	dir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600))
}

func builtinOverrides() *config.AgentToolsCfg {
	return &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{
		"read_file": config.ToolPolicyAsk,
		"bash":      config.ToolPolicyDeny,
	}}}
}

// assertFlatNativeRecord checks a record the real store wrote: flat (own id at the top
// level, tools beside it, no wrapper, no request-name key) and read exactly.
func assertFlatNativeRecord(t *testing.T, home, id string, want map[string]any) {
	t.Helper()
	record, err := agentrecord.Read(home, id)
	require.NoError(t, err)
	require.Equal(t, id, record["id"], "a flat record carries its own id at the top level")
	require.Contains(t, record, "tools", "the agent's tool settings are the top-level tools member")
	require.NotContains(t, record, "agent", "no outer agent wrapper (S-70's old reader assumed one)")
	require.NotContains(t, record, "tools_cfg", "tools_cfg is a request name, never a stored key (D16's old reader assumed it)")

	got, err := agentrecord.ReadPolicies(home, id, agentrecord.Builtin)
	require.NoError(t, err)
	require.Equal(t, want, got, "the builtin carrier of the record the real store wrote")

	none, err := agentrecord.ReadPolicies(home, id, agentrecord.Skills)
	require.NoError(t, err)
	require.Empty(t, none, "the sibling carrier of the same real record holds no skill override and never echoes the builtin map")
}

func TestReadPolicies_ReadsTheFlatRecordTheRealStoreWrites(t *testing.T) {
	want := map[string]any{"read_file": "ask", "bash": "deny"}

	t.Run("Store.Create", func(t *testing.T) {
		home := t.TempDir()
		record := config.AgentConfig{ID: "legacy", Name: "Legacy", Type: config.AgentTypeCustom, Tools: builtinOverrides()}
		require.NoError(t, agentstore.New(home).Create("legacy", &record))
		assertFlatNativeRecord(t, home, "legacy", want)
	})
	t.Run("Store.CreateState", func(t *testing.T) {
		home := t.TempDir()
		record := config.AgentConfig{ID: "created", Name: "Created", Type: config.AgentTypeCustom, Tools: builtinOverrides()}
		_, err := agentstore.New(home).CreateState("created", &record, "soul")
		require.NoError(t, err)
		assertFlatNativeRecord(t, home, "created", want)
	})
	t.Run("Store.MutateState adds the override to a record that had none", func(t *testing.T) {
		home := t.TempDir()
		store := agentstore.New(home)
		record := config.AgentConfig{ID: "mutated", Name: "Mutated", Type: config.AgentTypeCustom}
		_, err := store.CreateState("mutated", &record, "soul")
		require.NoError(t, err)
		before, err := agentrecord.ReadPolicies(home, "mutated", agentrecord.Builtin)
		require.NoError(t, err)
		require.Empty(t, before, "precondition: nothing stored before the mutation")

		state, err := store.ReadState("mutated")
		require.NoError(t, err)
		_, err = store.MutateState("mutated", state.Revision, func(agent *config.AgentConfig) error {
			agent.Tools = builtinOverrides()
			return nil
		}, nil)
		require.NoError(t, err)
		assertFlatNativeRecord(t, home, "mutated", want)
	})
}

func TestReadPolicies_ReadsEachCarrierOfAFlatRecordAndNeverMixesThem(t *testing.T) {
	home := t.TempDir()
	writeRecord(t, home, "flat", `{"id":"flat","name":"Flat","tools":{`+
		`"builtin":{"policies":{"plan":"deny","bash":"allow"}},`+
		`"skills":{"policies":{"plan":"ask","project:w1/notes":"allow"}}}}`)

	skills, err := agentrecord.ReadPolicies(home, "flat", agentrecord.Skills)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"plan": "ask", "project:w1/notes": "allow"}, skills)

	builtin, err := agentrecord.ReadPolicies(home, "flat", agentrecord.Builtin)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"plan": "deny", "bash": "allow"}, builtin)
}

// Each decoy carries "deny" for plan at a place that is NOT the carrier, and nothing at
// the carrier. A reader that accepted any of these wrappers or fields would report a
// stored override that the product never wrote.
func TestReadPolicies_WrongWrappersAndFieldsAreNotTheCarrier(t *testing.T) {
	for _, decoy := range []struct {
		name    string
		carrier agentrecord.Carrier
		record  string
	}{
		{"S-70's old outer wrapper: agent.tools.skills.policies", agentrecord.Skills,
			`{"id":"d","agent":{"tools":{"skills":{"policies":{"plan":"deny"}}}}}`},
		{"D16's old request name: tools_cfg.skills.policies", agentrecord.Skills,
			`{"id":"d","tools_cfg":{"skills":{"policies":{"plan":"deny"}}}}`},
		{"outer wrapper on the builtin carrier: agent.tools.builtin.policies", agentrecord.Builtin,
			`{"id":"d","agent":{"tools":{"builtin":{"policies":{"plan":"deny"}}}}}`},
		{"request name on the builtin carrier: tools_cfg.builtin.policies", agentrecord.Builtin,
			`{"id":"d","tools_cfg":{"builtin":{"policies":{"plan":"deny"}}}}`},
		{"a tool override named plan is not a skill override", agentrecord.Skills,
			`{"id":"d","tools":{"builtin":{"policies":{"plan":"deny"}}}}`},
		{"a skill override is not a tool override", agentrecord.Builtin,
			`{"id":"d","tools":{"skills":{"policies":{"plan":"deny"}}}}`},
		{"singular member: tools.skill.policies", agentrecord.Skills,
			`{"id":"d","tools":{"skill":{"policies":{"plan":"deny"}}}}`},
		{"singular leaf: tools.skills.policy", agentrecord.Skills,
			`{"id":"d","tools":{"skills":{"policy":{"plan":"deny"}}}}`},
		{"another leaf name: tools.skills.overrides", agentrecord.Skills,
			`{"id":"d","tools":{"skills":{"overrides":{"plan":"deny"}}}}`},
		{"top level skills.policies, outside tools", agentrecord.Skills,
			`{"id":"d","skills":{"policies":{"plan":"deny"}}}`},
		{"top level policies, outside tools and carrier", agentrecord.Skills,
			`{"id":"d","policies":{"plan":"deny"}}`},
		{"the real grants list (AgentConfig.Skills) is not a policy map", agentrecord.Skills,
			`{"id":"d","skills":["plan"]}`},
		{"the real grant bindings (AgentConfig.SkillGrants) are not a policy map", agentrecord.Skills,
			`{"id":"d","skill_grants":{"plan":"deny"}}`},
	} {
		t.Run(decoy.name, func(t *testing.T) {
			home := t.TempDir()
			writeRecord(t, home, "d", decoy.record)
			got, err := agentrecord.ReadPolicies(home, "d", decoy.carrier)
			require.NoError(t, err)
			require.Empty(t, got, "a value at the wrong place is no stored override: %s", decoy.record)
		})
	}

	t.Run("a decoy beside the carrier does not change what the carrier says", func(t *testing.T) {
		home := t.TempDir()
		writeRecord(t, home, "mixed", `{"id":"mixed",`+
			`"agent":{"tools":{"skills":{"policies":{"plan":"deny"}}}},`+
			`"tools_cfg":{"skills":{"policies":{"plan":"deny"}}},`+
			`"tools":{"builtin":{"policies":{"plan":"allow"}},"skills":{"policies":{"plan":"ask"}}}}`)
		got, err := agentrecord.ReadPolicies(home, "mixed", agentrecord.Skills)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"plan": "ask"}, got)
	})
}

func TestReadPolicies_AbsentMembersAreNoOverridesButMisshapenOnesAreErrors(t *testing.T) {
	for _, absent := range []string{
		`{"id":"x"}`,
		`{"id":"x","tools":{}}`,
		`{"id":"x","tools":{"skills":{}}}`,
		`{"id":"x","tools":{"skills":{"policies":{}}}}`,
	} {
		home := t.TempDir()
		writeRecord(t, home, "x", absent)
		got, err := agentrecord.ReadPolicies(home, "x", agentrecord.Skills)
		require.NoError(t, err, absent)
		require.Empty(t, got, absent)
	}

	for _, misshapen := range []string{
		`{"id":"x","tools":[]}`,
		`{"id":"x","tools":null}`,
		`{"id":"x","tools":"all"}`,
		`{"id":"x","tools":{"skills":"all"}}`,
		`{"id":"x","tools":{"skills":null}}`,
		`{"id":"x","tools":{"skills":{"policies":[]}}}`,
		`{"id":"x","tools":{"skills":{"policies":null}}}`,
		`{"id":"x","tools":{"skills":{"policies":"deny"}}}`,
		`[]`,
		`not json`,
	} {
		home := t.TempDir()
		writeRecord(t, home, "x", misshapen)
		_, err := agentrecord.ReadPolicies(home, "x", agentrecord.Skills)
		require.ErrorIs(t, err, agentrecord.ErrShape, misshapen)
	}
}

func TestReadPolicies_AMissingRecordAndAnUnknownCarrierAreErrorsOfTheirOwnKind(t *testing.T) {
	home := t.TempDir()
	_, err := agentrecord.ReadPolicies(home, "nobody", agentrecord.Skills)
	require.ErrorIs(t, err, agentrecord.ErrNoRecord)
	require.NotErrorIs(t, err, agentrecord.ErrShape, "a missing file is not a shape error")

	writeRecord(t, home, "x", `{"id":"x","tools":{"skills":{"policies":{"plan":"ask"}}}}`)
	_, err = agentrecord.ReadPolicies(home, "x", agentrecord.Carrier("skill"))
	require.ErrorIs(t, err, agentrecord.ErrCarrier)
	_, err = agentrecord.ReadPolicies(home, "x", agentrecord.Carrier(""))
	require.ErrorIs(t, err, agentrecord.ErrCarrier)
}

// Whether or not the product already carries tools.skills (spec C-4), a skills record the
// real store wrote must be read exactly, and a product that has not got the member yet
// must read as "no overrides" - never as an instrument failure. The written config is
// decoded into the product's own type first, so the store marshals what the product keeps.
func TestReadPolicies_ASkillsRecordTheRealStoreWritesIsReadWhetherOrNotTheProductCarriesIt(t *testing.T) {
	home := t.TempDir()
	var record config.AgentConfig
	require.NoError(t, json.Unmarshal([]byte(
		`{"id":"carried","name":"Carried","type":"custom","tools":{"skills":{"policies":{"plan":"ask"}}}}`), &record))
	_, err := agentstore.New(home).CreateState("carried", &record, "soul")
	require.NoError(t, err)

	stored, err := agentrecord.Read(home, "carried")
	require.NoError(t, err)
	tools, _ := stored["tools"].(map[string]any)
	got, err := agentrecord.ReadPolicies(home, "carried", agentrecord.Skills)
	require.NoError(t, err)
	if _, carried := tools["skills"]; carried {
		require.Equal(t, map[string]any{"plan": "ask"}, got, "the product keeps tools.skills: the reader reads exactly what the store wrote")
		require.NotContains(t, stored, "agent")
		require.NotContains(t, stored, "tools_cfg")
		return
	}
	require.Empty(t, got, "the product has no tools.skills member yet: the store dropped it, so there is nothing to read")
	t.Log("the product does not carry tools.skills yet (spec C-4); the real-store positive control above is the builtin carrier")
}
