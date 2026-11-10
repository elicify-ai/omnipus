package agentstore

// Oracle: r17 §7.10, 7.5–7.6, D-12. Entity, SOUL and AGENT.md faults hit the
// real stage/replace filesystem edges. Store mutation, publication order,
// bytes and revision remain real. Tool/REST formatting is a separate gap.
import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentWrite_D12_FaultInjectionPrerequisites(t *testing.T) {
	for _, at := range []string{"stage_entity", "stage_soul", "stage_agent_md", "replace_entity", "replace_soul", "replace_agent_md"} {
		t.Run(at, func(t *testing.T) {
			withMD := strings.HasSuffix(at, "agent_md")
			home := t.TempDir()
			s := New(home)
			_, err := s.CreateState("r1", &config.AgentConfig{ID: "r1", Name: "Before", Type: config.AgentTypeCustom}, "before soul")
			require.NoError(t, err)
			before, err := s.ReadState("r1")
			require.NoError(t, err)
			entityPath := filepath.Join(home, "entities", "agents", "r1.json")
			soulPath := filepath.Join(home, "agents", "r1", "SOUL.md")
			mdPath := filepath.Join(home, "agents", "r1", "AGENT.md")
			entityBytes, err := os.ReadFile(entityPath)
			require.NoError(t, err)
			soulBytes, err := os.ReadFile(soulPath)
			require.NoError(t, err)
			var mdBytes []byte
			if withMD {
				// AGENT.md already exists, so a fault must leave these exact bytes.
				mdBytes = []byte("before agent md")
				seeded, seedErr := s.MutateStateWithAgentMD("r1", before.Revision, nil, nil, ptrStr("before agent md"))
				require.NoError(t, seedErr)
				require.Equal(t, PersistenceComplete, seeded.PersistenceStatus)
				before, err = s.ReadState("r1")
				require.NoError(t, err)
				entityBytes, err = os.ReadFile(entityPath)
				require.NoError(t, err)
				onDisk, readErr := os.ReadFile(mdPath)
				require.NoError(t, readErr)
				require.Equal(t, mdBytes, onDisk, "setup: AGENT.md written through the staged API")
			}
			fault := errors.New("T8 injected " + at)
			hit := 0
			matches := func(path string) bool {
				return (strings.HasSuffix(at, "entity") && path == entityPath) || (strings.HasSuffix(at, "soul") && path == soulPath) || (withMD && path == mdPath)
			}
			stage, replace := s.stageFile, s.replaceFile
			s.stageFile = func(path string, data []byte, mode os.FileMode) (string, error) {
				if strings.HasPrefix(at, "stage_") && matches(path) {
					hit++
					return "", fault
				}
				return stage(path, data, mode)
			}
			s.replaceFile = func(staged, path string) error {
				if strings.HasPrefix(at, "replace_") && matches(path) {
					hit++
					return fault
				}
				return replace(staged, path)
			}
			newSoul := "after soul"
			var result MutationResult
			if withMD {
				// SOUL.md is left alone so the only publication after the entity is AGENT.md.
				result, err = s.MutateStateWithAgentMD("r1", before.Revision, func(a *config.AgentConfig) error { a.Name = "After"; return nil }, nil, ptrStr("after agent md"))
			} else {
				result, err = s.MutateState("r1", before.Revision, func(a *config.AgentConfig) error { a.Name = "After"; return nil }, &newSoul)
			}
			require.ErrorIs(t, err, fault)
			assert.Equal(t, 1, hit, "instrument: exact real fault boundary reached once")
			assert.Equal(t, at, result.ErrorStage)
			after, err := s.ReadState("r1")
			require.NoError(t, err, "fresh read remains possible after failure")
			assert.Equal(t, after.Revision, result.Revision, "reported revision equals an independent fresh read")
			entityAfter, err := os.ReadFile(entityPath)
			require.NoError(t, err)
			soulAfter, err := os.ReadFile(soulPath)
			require.NoError(t, err)
			assert.Equal(t, soulBytes, soulAfter, "no SOUL publication on any injected failure")
			if withMD {
				mdAfter, mdErr := os.ReadFile(mdPath)
				require.NoError(t, mdErr)
				assert.Equal(t, mdBytes, mdAfter, "no AGENT.md publication on any injected failure")
			}
			if at == "replace_soul" || at == "replace_agent_md" {
				assert.Equal(t, PersistencePartial, result.PersistenceStatus)
				assert.Equal(t, "After", after.Agent.Name)
				assert.NotEqual(t, entityBytes, entityAfter, "entity publication precedes the later failed replacement")
				assert.NotEqual(t, before.Revision, after.Revision)
			} else {
				assert.Equal(t, PersistenceNone, result.PersistenceStatus)
				assert.Equal(t, entityBytes, entityAfter, "staging/first replacement failure writes nothing")
				assert.Equal(t, before.Revision, after.Revision)
			}
		})
	}
}

func ptrStr(v string) *string { return &v }
