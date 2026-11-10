package agentstore

// Oracle: merge CHECK finding F1 (/workspace/logs/t8-merge/check-report.md) and
// tool-parity r17 section 7.5: a nil-mutate write (AGENT.md only or SOUL.md
// only) runs the skills binding on every persist, but when the binding leaves
// the logical record unchanged the existing entity bytes are kept and
// changed_fields must not list "entity", even when the stored JSON is a valid
// compact (non-canonical) encoding. When the binding really changes the record
// (a skill folder was replaced, so the grant is rebound), the entity IS
// published. Real store, real files, no mocks.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	t8F1OldGeneration = "11111111-2222-3333-4444-555555555555\n"
	t8F1NewGeneration = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\n"
)

// t8F1Fixture is an agent bound to one file-only skill, written by the real
// store so its grant matches the skill folder identity.
type t8F1Fixture struct {
	store      *Store
	entityPath string
	generation string
}

func newT8F1Fixture(t *testing.T) *t8F1Fixture {
	t.Helper()
	home := t.TempDir()
	folder := filepath.Join(home, "skills", "foo")
	require.NoError(t, os.MkdirAll(folder, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "SKILL.md"),
		[]byte("---\nname: foo\ndescription: file-only binding diagnostic\n---\nbody\n"), 0o600))
	generation := filepath.Join(folder, ".omnipus-identity")
	require.NoError(t, os.WriteFile(generation, []byte(t8F1OldGeneration), 0o600))
	store := New(home)
	_, err := store.CreateState("probe", &config.AgentConfig{ID: "probe", Name: "Probe", Type: config.AgentTypeCustom, Skills: []string{"foo"}}, "old soul")
	require.NoError(t, err)
	return &t8F1Fixture{store: store, entityPath: filepath.Join(home, "entities", "agents", "probe.json"), generation: generation}
}

// compact rewrites the stored entity as valid compact JSON with the same
// logical record.
func (f *t8F1Fixture) compact(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(f.entityPath)
	require.NoError(t, err)
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, raw))
	require.NotEqual(t, raw, compact.Bytes(), "instrument: the compact form differs from the canonical bytes")
	require.NoError(t, os.WriteFile(f.entityPath, compact.Bytes(), 0o600))
}

// drift replaces the skill folder identity so the stored grant no longer
// matches and the binding must rebind it.
func (f *t8F1Fixture) drift(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.generation, []byte(t8F1NewGeneration), 0o600))
}

type t8F1Write struct {
	name  string
	write func(f *t8F1Fixture, revision string) (MutationResult, error)
	field string
}

func t8F1Writes() []t8F1Write {
	content := "new instructions"
	return []t8F1Write{
		{"AGENT.md only", func(f *t8F1Fixture, rev string) (MutationResult, error) {
			return f.store.MutateStateWithAgentMD("probe", rev, nil, nil, &content)
		}, "agent_md"},
		{"SOUL.md only", func(f *t8F1Fixture, rev string) (MutationResult, error) {
			return f.store.MutateState("probe", rev, nil, &content)
		}, "soul"},
	}
}

// Cases 1 and 2: compact entity, no binding drift. The entity bytes stay as
// they were and only the written file is reported changed.
func TestAgentWrite_NilMutateNoDrift_CompactEntityKept(t *testing.T) {
	for _, w := range t8F1Writes() {
		t.Run(w.name, func(t *testing.T) {
			f := newT8F1Fixture(t)
			f.compact(t)
			before, err := f.store.ReadState("probe")
			require.NoError(t, err)
			rawBefore, err := os.ReadFile(f.entityPath)
			require.NoError(t, err)

			result, err := w.write(f, before.Revision)
			require.NoError(t, err)

			rawAfter, err := os.ReadFile(f.entityPath)
			require.NoError(t, err)
			assert.Equal(t, string(rawBefore), string(rawAfter), "the entity bytes are kept when the logical record is unchanged")
			assert.Equal(t, []string{w.field}, result.ChangedFields, "changed_fields lists only the written file, never entity")
			assert.Equal(t, PersistenceComplete, result.PersistenceStatus)
			after, err := f.store.ReadState("probe")
			require.NoError(t, err)
			assert.Equal(t, before.Agent.SkillGrants, after.Agent.SkillGrants, "no drift, grants unchanged")
		})
	}
}

// Case 3, the controls: with real drift the binding still runs on a nil-mutate
// write, the grant is rebound and the entity is published. These must stay green
// when the no-drift cases are fixed.
func TestAgentWrite_NilMutateWithDrift_EntityRewrittenAndGrantsRebound(t *testing.T) {
	for _, w := range t8F1Writes() {
		t.Run(w.name, func(t *testing.T) {
			f := newT8F1Fixture(t)
			before, err := f.store.ReadState("probe")
			require.NoError(t, err)
			require.NotEmpty(t, before.Agent.SkillGrants, "instrument: the agent starts with a grant")
			rawBefore, err := os.ReadFile(f.entityPath)
			require.NoError(t, err)
			f.drift(t)

			result, err := w.write(f, before.Revision)
			require.NoError(t, err)

			rawAfter, err := os.ReadFile(f.entityPath)
			require.NoError(t, err)
			assert.NotEqual(t, string(rawBefore), string(rawAfter), "a genuine rebinding publishes the entity")
			assert.Contains(t, result.ChangedFields, "entity")
			assert.Contains(t, result.ChangedFields, w.field)
			after, err := f.store.ReadState("probe")
			require.NoError(t, err)
			assert.NotEqual(t, before.Agent.SkillGrants, after.Agent.SkillGrants, "the grant was rebound to the new skill identity")
		})
	}
}
