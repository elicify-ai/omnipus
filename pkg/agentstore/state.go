package agentstore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

var (
	ErrInvalidRevision  = errors.New("agentstore: invalid revision")
	ErrRevisionConflict = errors.New("agentstore: revision conflict")
	revisionPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidateRevision(revision string) error {
	if !revisionPattern.MatchString(revision) {
		return fmt.Errorf("%w: expected 64 lowercase hex characters", ErrInvalidRevision)
	}
	return nil
}

type PersistenceStatus string
type ActivationStatus string

const (
	PersistenceComplete    PersistenceStatus = "complete"
	PersistencePartial     PersistenceStatus = "partial"
	PersistenceNone        PersistenceStatus = "none"
	ActivationActive       ActivationStatus  = "active"
	ActivationFailed       ActivationStatus  = "failed"
	ActivationNotAttempted ActivationStatus  = "not_attempted"
)

type State struct {
	Agent    *config.AgentConfig
	Soul     string
	Revision string
}

type MutationResult struct {
	PersistenceStatus PersistenceStatus
	ActivationStatus  ActivationStatus
	Revision          string
	ChangedFields     []string
	ErrorStage        string
	Message           string
}

func revisionFor(entityBytes, soulBytes []byte) string {
	h := sha256.New()
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(entityBytes)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(entityBytes)
	binary.BigEndian.PutUint64(size[:], uint64(len(soulBytes)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(soulBytes)
	return hex.EncodeToString(h.Sum(nil))
}

func readSoul(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentstore: read soul: %w", err)
	}
	return b, nil
}

func readStateFiles(entityPath, soulPath string) (*State, []byte, []byte, error) {
	entityBytes, err := os.ReadFile(entityPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil, entity.ErrNotFound
		}
		return nil, nil, nil, fmt.Errorf("agentstore: read entity: %w", err)
	}
	var agent config.AgentConfig
	if err := json.Unmarshal(entityBytes, &agent); err != nil {
		return nil, nil, nil, fmt.Errorf("agentstore: parse entity: %w", err)
	}
	soulBytes, err := readSoul(soulPath)
	if err != nil {
		return nil, nil, nil, err
	}
	return &State{Agent: &agent, Soul: string(soulBytes), Revision: revisionFor(entityBytes, soulBytes)}, entityBytes, soulBytes, nil
}

func (s *Store) soulPath(id string) string {
	return filepath.Join(s.home, "agents", id, "SOUL.md")
}

func (s *Store) ReadState(id string) (state *State, err error) {
	err = s.inner.WithLock(id, func(entityPath string) error {
		var readErr error
		state, _, _, readErr = readStateFiles(entityPath, s.soulPath(id))
		return readErr
	})
	if err != nil {
		return nil, fmt.Errorf("agentstore: read state %q: %w", id, err)
	}
	return state, nil
}

func stage(path string, data []byte, perm os.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create stage dir: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".adr090-stage-*")
	if err != nil {
		return "", fmt.Errorf("create stage: %w", err)
	}
	name := f.Name()
	cleanup := func(e error) (string, error) { _ = f.Close(); _ = os.Remove(name); return "", e }
	if err := f.Chmod(perm); err != nil {
		return cleanup(fmt.Errorf("chmod stage: %w", err))
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(fmt.Errorf("write stage: %w", err))
	}
	if err := f.Sync(); err != nil {
		return cleanup(fmt.Errorf("sync stage: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("close stage: %w", err)
	}
	return name, nil
}

func replace(staged, target string) error {
	if err := os.Rename(staged, target); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(target), err)
	}
	return nil
}

func (s *Store) MutateState(id, expectedRevision string, mutate func(*config.AgentConfig) error, soul *string) (result MutationResult, err error) {
	result.ActivationStatus = ActivationNotAttempted
	if validateErr := ValidateRevision(expectedRevision); validateErr != nil {
		return result, validateErr
	}
	var updated *config.AgentConfig
	err = s.inner.WithLock(id, func(entityPath string) error {
		current, oldEntity, oldSoul, readErr := readStateFiles(entityPath, s.soulPath(id))
		if readErr != nil {
			return readErr
		}
		if current.Revision != expectedRevision {
			return fmt.Errorf("%w: expected %s, current %s", ErrRevisionConflict, expectedRevision, current.Revision)
		}
		result.Revision = current.Revision
		if mutate != nil {
			if mutateErr := mutate(current.Agent); mutateErr != nil {
				return mutateErr
			}
		}
		newEntity, marshalErr := json.MarshalIndent(current.Agent, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("marshal candidate entity: %w", marshalErr)
		}
		newSoul := oldSoul
		if soul != nil {
			newSoul = []byte(*soul)
		}
		entityChanged := string(newEntity) != string(oldEntity)
		soulChanged := string(newSoul) != string(oldSoul)
		var entityStage, soulStage string
		if entityChanged {
			entityStage, err = s.stageFile(entityPath, newEntity, 0o600)
			if err != nil {
				result.PersistenceStatus = PersistenceNone
				result.ErrorStage = "stage_entity"
				return err
			}
			defer os.Remove(entityStage)
		}
		if soulChanged {
			soulStage, err = s.stageFile(s.soulPath(id), newSoul, 0o600)
			if err != nil {
				result.PersistenceStatus = PersistenceNone
				result.ErrorStage = "stage_soul"
				return err
			}
			defer os.Remove(soulStage)
		}
		if entityChanged {
			if err = s.replaceFile(entityStage, entityPath); err != nil {
				result.PersistenceStatus = PersistenceNone
				result.ErrorStage = "replace_entity"
				return err
			}
			result.ChangedFields = append(result.ChangedFields, "entity")
		}
		if soulChanged {
			if err = s.replaceFile(soulStage, s.soulPath(id)); err != nil {
				if entityChanged {
					result.PersistenceStatus = PersistencePartial
				} else {
					result.PersistenceStatus = PersistenceNone
				}
				result.ErrorStage = "replace_soul"
				actual, _, _, actualErr := readStateFiles(entityPath, s.soulPath(id))
				if actualErr == nil {
					result.Revision = actual.Revision
				}
				return err
			}
			result.ChangedFields = append(result.ChangedFields, "soul")
		}
		actual, _, _, actualErr := readStateFiles(entityPath, s.soulPath(id))
		if actualErr != nil {
			result.PersistenceStatus = PersistencePartial
			result.ErrorStage = "verify"
			return actualErr
		}
		result.PersistenceStatus = PersistenceComplete
		result.Revision = actual.Revision
		updated = actual.Agent
		if s.notifier != nil {
			s.notifier.AgentUpserted(id, updated)
		}
		return nil
	})
	if err != nil {
		result.Message = err.Error()
		return result, fmt.Errorf("agentstore: mutate state %q: %w", id, err)
	}
	return result, nil
}

// CreateState stages the entity and SOUL before publishing either component.
// If the second replacement fails, the returned state explicitly reports the
// durable partial write and its resulting revision.
func (s *Store) CreateState(id string, agent *config.AgentConfig, soul string) (result MutationResult, err error) {
	result.ActivationStatus = ActivationNotAttempted
	if id == "" || agent == nil {
		return result, fmt.Errorf("agentstore: create state: id and agent are required")
	}
	if agent.ID != "" && agent.ID != id {
		return result, fmt.Errorf("agentstore: create state %q: cfg.ID %q does not match", id, agent.ID)
	}
	err = s.inner.WithLock(id, func(entityPath string) error {
		if _, statErr := os.Stat(entityPath); statErr == nil {
			return entity.ErrAlreadyExists
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		agent.ID = id
		if agent.CreatedAt == nil {
			now := time.Now().UTC()
			agent.CreatedAt = &now
		}
		entityBytes, marshalErr := json.MarshalIndent(agent, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		entityStage, stageErr := s.stageFile(entityPath, entityBytes, 0o600)
		if stageErr != nil {
			result.PersistenceStatus, result.ErrorStage = PersistenceNone, "stage_entity"
			return stageErr
		}
		defer os.Remove(entityStage)
		soulStage, stageErr := s.stageFile(s.soulPath(id), []byte(soul), 0o600)
		if stageErr != nil {
			result.PersistenceStatus, result.ErrorStage = PersistenceNone, "stage_soul"
			return stageErr
		}
		defer os.Remove(soulStage)
		if replaceErr := s.replaceFile(entityStage, entityPath); replaceErr != nil {
			result.PersistenceStatus, result.ErrorStage = PersistenceNone, "replace_entity"
			return replaceErr
		}
		result.ChangedFields = append(result.ChangedFields, "entity")
		if replaceErr := s.replaceFile(soulStage, s.soulPath(id)); replaceErr != nil {
			result.PersistenceStatus, result.ErrorStage = PersistencePartial, "replace_soul"
			if actual, _, _, readErr := readStateFiles(entityPath, s.soulPath(id)); readErr == nil {
				result.Revision = actual.Revision
			}
			return replaceErr
		}
		result.ChangedFields = append(result.ChangedFields, "soul")
		actual, _, _, readErr := readStateFiles(entityPath, s.soulPath(id))
		if readErr != nil {
			result.PersistenceStatus, result.ErrorStage = PersistencePartial, "verify"
			return readErr
		}
		result.PersistenceStatus, result.Revision = PersistenceComplete, actual.Revision
		if s.notifier != nil {
			s.notifier.AgentUpserted(id, agent)
		}
		return nil
	})
	if err != nil {
		result.Message = err.Error()
		return result, fmt.Errorf("agentstore: create state %q: %w", id, err)
	}
	return result, nil
}

// DeleteState deletes an agent only if the caller's revision still names the
// persisted entity+SOUL state. The comparison and delete share one lock, so a
// stale caller cannot delete a newer record between validation and removal.
func (s *Store) DeleteState(id, expectedRevision string) (result MutationResult, err error) {
	result.PersistenceStatus = PersistenceNone
	result.ActivationStatus = ActivationNotAttempted
	if err := ValidateRevision(expectedRevision); err != nil {
		return result, err
	}
	err = s.inner.WithLock(id, func(entityPath string) error {
		soulPath := s.soulPath(id)
		current, _, oldSoul, err := readStateFiles(entityPath, soulPath)
		if err != nil {
			return err
		}
		_, soulStatErr := os.Stat(soulPath)
		soulExists := soulStatErr == nil
		if soulStatErr != nil && !os.IsNotExist(soulStatErr) {
			return fmt.Errorf("stat soul: %w", soulStatErr)
		}
		result.Revision = current.Revision
		if current.Revision != expectedRevision {
			return fmt.Errorf("%w: expected %s, current %s", ErrRevisionConflict, expectedRevision, current.Revision)
		}
		if err := s.removeFile(entityPath); err != nil {
			result.PersistenceStatus = PersistenceNone
			result.ErrorStage = "remove_entity"
			return fmt.Errorf("delete entity: %w", err)
		}
		result.ChangedFields = append(result.ChangedFields, "entity")
		if soulExists {
			if err := s.removeFile(soulPath); err != nil {
				result.PersistenceStatus = PersistencePartial
				result.ErrorStage = "remove_soul"
				result.Revision = revisionFor(nil, oldSoul)
				return fmt.Errorf("delete soul: %w", err)
			}
			result.ChangedFields = append(result.ChangedFields, "soul")
		}
		result.PersistenceStatus = PersistenceComplete
		result.Revision = revisionFor(nil, nil)
		if s.notifier != nil {
			s.notifier.AgentDeleted(id)
		}
		return nil
	})
	if err != nil {
		result.Message = err.Error()
		return result, fmt.Errorf("agentstore: delete state %q: %w", id, err)
	}
	return result, nil
}
