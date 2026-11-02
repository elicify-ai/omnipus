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
	agentIDPattern      = regexp.MustCompile(`^[A-Za-z0-9]+(?:[-_.][A-Za-z0-9]+)*$`)

	ErrInvalidAgentID = errors.New("agentstore: invalid agent id")
)

// ValidateAgentID allowlists the externally supplied ID before callers derive
// entity and SOUL paths from it. The bounded single-component form also rejects
// traversal and separators by construction.
func ValidateAgentID(id string) error {
	if len(id) == 0 || len(id) > 128 || !agentIDPattern.MatchString(id) {
		return fmt.Errorf("%w: must be 1-128 alphanumeric path-component characters", ErrInvalidAgentID)
	}
	return nil
}

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

// State is an agent's stored definition: the entity record, SOUL.md and
// AGENT.md (r17 §7.5 AG-05, #1321), and the revision that covers all three.
type State struct {
	Agent    *config.AgentConfig
	Soul     string
	AgentMD  string
	Revision string
}

type MutationResult struct {
	PersistenceStatus PersistenceStatus
	ActivationStatus  ActivationStatus
	Revision          string
	ChangedFields     []string
	// NotSaved names the files a failed write did not publish, in
	// publication order (r17 §7.10).
	NotSaved   []string
	ErrorStage string
	Message    string
}

// revisionFor hashes the entity record, SOUL.md and AGENT.md, each length
// prefixed. AGENT.md is part of the revision (r17 §7.5 AG-05, §7.10, #1321):
// an AGENT.md-only change by another writer makes an older revision stale.
// An absent or empty AGENT.md adds nothing, so an agent without one keeps the
// revision it had before AGENT.md joined the hash (reads show both as "").
func revisionFor(entityBytes, soulBytes []byte, agentMDBytes ...[]byte) string {
	h := sha256.New()
	var size [8]byte
	parts := [][]byte{entityBytes, soulBytes}
	for _, md := range agentMDBytes {
		if len(md) > 0 {
			parts = append(parts, md)
		}
	}
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readOptional reads a file beside the record; nil when it does not exist.
func readOptional(path, what string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentstore: read %s: %w", what, err)
	}
	return b, nil
}

// agentMDPathBeside is AGENT.md's path: the same agent directory as SOUL.md.
func agentMDPathBeside(soulPath string) string {
	return filepath.Join(filepath.Dir(soulPath), "AGENT.md")
}

// readStateFiles reads the record stored for id. Agent ids match exactly
// (chief ruling 2026-10-14, #1032): a differently cased spelling is an unknown
// id on every filesystem, so the record's own stored id must equal id — a
// case-insensitive filesystem (macOS) would otherwise open omnipus.json for
// "OMNIPUS". A record without a stored id must sit under exactly id's file name.
func readStateFiles(entityPath, soulPath, id string) (*State, []byte, []byte, error) {
	entityBytes, err := os.ReadFile(entityPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, entity.ErrNotFound
		}
		return nil, nil, nil, fmt.Errorf("agentstore: read entity: %w", err)
	}
	var agent config.AgentConfig
	if err = json.Unmarshal(entityBytes, &agent); err != nil {
		return nil, nil, nil, fmt.Errorf("agentstore: parse entity: %w", err)
	}
	if exactErr := confirmExactAgentID(entityPath, id, agent.ID); exactErr != nil {
		return nil, nil, nil, exactErr
	}
	soulBytes, err := readOptional(soulPath, "soul")
	if err != nil {
		return nil, nil, nil, err
	}
	agentMDBytes, err := readOptional(agentMDPathBeside(soulPath), "AGENT.md")
	if err != nil {
		return nil, nil, nil, err
	}
	return &State{
		Agent: &agent, Soul: string(soulBytes), AgentMD: string(agentMDBytes),
		Revision: revisionFor(entityBytes, soulBytes, agentMDBytes),
	}, entityBytes, soulBytes, nil
}

// readStoreDir lists the agent entity directory for confirmExactAgentID. A
// variable only so a test can present the listing a case-insensitive volume
// would give on any filesystem; production never reassigns it.
var readStoreDir = os.ReadDir

// confirmExactAgentID returns entity.ErrNotFound unless the record at
// entityPath is id's under exact comparison: its stored id equals id, or — for
// a record that stores none — its file name is exactly id + ".json".
func confirmExactAgentID(entityPath, id, storedID string) error {
	if storedID != "" {
		if storedID != id {
			return entity.ErrNotFound
		}
		return nil
	}
	entries, err := readStoreDir(filepath.Dir(entityPath))
	if err != nil {
		return fmt.Errorf("agentstore: read store directory: %w", err)
	}
	want := filepath.Base(entityPath)
	for _, e := range entries {
		if e.Name() == want {
			return nil
		}
	}
	return entity.ErrNotFound
}

func (s *Store) soulPath(id string) string {
	return filepath.Join(s.home, "agents", id, "SOUL.md")
}

func (s *Store) agentMDPath(id string) string {
	return agentMDPathBeside(s.soulPath(id))
}

// PeekRevision returns id's current revision WITHOUT taking the entity lock - a
// bare read, like entity.Store.Get. It is for advisory pre-checks that must not
// depend on the lock (delete_agent's read-only stale-revision check before any
// cleanup); the authoritative revision comparison is still DeleteState's, under
// the lock. entity.ErrNotFound when the record is absent; any other error means
// the record exists but could not be read.
func (s *Store) PeekRevision(id string) (string, error) {
	if err := ValidateAgentID(id); err != nil {
		return "", err
	}
	entityPath := filepath.Join(s.inner.Dir(), id+".json")
	state, _, _, err := readStateFiles(entityPath, s.soulPath(id), id)
	if err != nil {
		return "", err
	}
	return state.Revision, nil
}

func (s *Store) ReadState(id string) (state *State, err error) {
	if validateErr := ValidateAgentID(id); validateErr != nil {
		return nil, validateErr
	}
	err = s.inner.WithLock(id, func(entityPath string) error {
		var readErr error
		state, _, _, readErr = readStateFiles(entityPath, s.soulPath(id), id)
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

// MutateState applies mutate to the record and replaces SOUL.md when soul is
// non-nil, under the record's lock and revision check. See
// MutateStateWithAgentMD.
func (s *Store) MutateState(id, expectedRevision string, mutate func(*config.AgentConfig) error, soul *string) (result MutationResult, err error) {
	return s.MutateStateWithAgentMD(id, expectedRevision, mutate, soul, nil)
}

// stagedFile is one file of an agent write: its result field name, target,
// new bytes, and the staged copy once staged.
type stagedFile struct {
	field, path, staged string
	data                []byte
}

// MutateStateWithAgentMD is MutateState plus AGENT.md as the third staged file
// (r17 §7.5 AG-05, §7.10, #1321): under one lock it checks the revision (which
// covers all three files), stages every changed file, then publishes them in
// the order entity, SOUL.md, AGENT.md. A nil mutate leaves the record bytes
// untouched; a nil soul or agentMD leaves that file untouched. A replace that
// fails after an earlier one succeeded is a partial write; the result names
// what was and was not saved and the revision now readable.
func (s *Store) MutateStateWithAgentMD(id, expectedRevision string, mutate func(*config.AgentConfig) error,
	soul, agentMD *string,
) (result MutationResult, err error) {
	// Nothing is written until a replace succeeds: a refusal (invalid input,
	// stale revision, mutate error) reports persistence "none" (r17 §7.10).
	result.PersistenceStatus = PersistenceNone
	result.ActivationStatus = ActivationNotAttempted
	if validateErr := ValidateAgentID(id); validateErr != nil {
		return result, validateErr
	}
	if validateErr := ValidateRevision(expectedRevision); validateErr != nil {
		return result, validateErr
	}
	err = s.inner.WithLock(id, func(entityPath string) error {
		current, oldEntity, oldSoul, readErr := readStateFiles(entityPath, s.soulPath(id), id)
		if readErr != nil {
			return readErr
		}
		if current.Revision != expectedRevision {
			return fmt.Errorf("%w: expected %s, current %s", ErrRevisionConflict, expectedRevision, current.Revision)
		}
		result.Revision = current.Revision
		newEntity := oldEntity
		if mutate != nil {
			if mutateErr := mutate(current.Agent); mutateErr != nil {
				return mutateErr
			}
			var marshalErr error
			if newEntity, marshalErr = json.MarshalIndent(current.Agent, "", "  "); marshalErr != nil {
				return fmt.Errorf("marshal candidate entity: %w", marshalErr)
			}
		}
		var files []*stagedFile
		add := func(field, path string, old, data []byte) {
			if string(old) != string(data) {
				files = append(files, &stagedFile{field: field, path: path, data: data})
			}
		}
		add("entity", entityPath, oldEntity, newEntity)
		if soul != nil {
			add("soul", s.soulPath(id), oldSoul, []byte(*soul))
		}
		if agentMD != nil {
			add("agent_md", s.agentMDPath(id), []byte(current.AgentMD), []byte(*agentMD))
		}
		if publishErr := s.publishStaged(entityPath, id, files, &result); publishErr != nil {
			return publishErr
		}
		actual, _, _, actualErr := readStateFiles(entityPath, s.soulPath(id), id)
		if actualErr != nil {
			result.PersistenceStatus = PersistencePartial
			result.ErrorStage = "verify"
			return actualErr
		}
		result.PersistenceStatus = PersistenceComplete
		result.Revision = actual.Revision
		if s.notifier != nil {
			s.notifier.AgentUpserted(id, actual.Agent)
		}
		return nil
	})
	if err != nil {
		result.Message = err.Error()
		return result, fmt.Errorf("agentstore: mutate state %q: %w", id, err)
	}
	return result, nil
}

// publishStaged stages every file, then replaces them in order. A stage
// failure publishes nothing. A replace failure is partial when an earlier
// file was already published; the readable revision is then re-read.
func (s *Store) publishStaged(entityPath, id string, files []*stagedFile, result *MutationResult) error {
	defer func() {
		for _, f := range files {
			if f.staged != "" {
				_ = os.Remove(f.staged)
			}
		}
	}()
	notSaved := func(from int) []string {
		names := make([]string, 0, len(files)-from)
		for _, f := range files[from:] {
			names = append(names, f.field)
		}
		return names
	}
	for _, f := range files {
		staged, err := s.stageFile(f.path, f.data, 0o600)
		if err != nil {
			result.PersistenceStatus = PersistenceNone
			result.ErrorStage = "stage_" + f.field
			result.NotSaved = notSaved(0)
			return err
		}
		f.staged = staged
	}
	for i, f := range files {
		if err := s.replaceFile(f.staged, f.path); err != nil {
			result.PersistenceStatus = PersistenceNone
			if i > 0 {
				result.PersistenceStatus = PersistencePartial
			}
			result.ErrorStage = "replace_" + f.field
			result.NotSaved = notSaved(i)
			if actual, _, _, actualErr := readStateFiles(entityPath, s.soulPath(id), id); actualErr == nil {
				result.Revision = actual.Revision
			}
			return err
		}
		f.staged = ""
		result.ChangedFields = append(result.ChangedFields, f.field)
	}
	return nil
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
		} else if !errors.Is(statErr, os.ErrNotExist) {
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
			if actual, _, _, readErr := readStateFiles(entityPath, s.soulPath(id), id); readErr == nil {
				result.Revision = actual.Revision
			}
			return replaceErr
		}
		result.ChangedFields = append(result.ChangedFields, "soul")
		actual, _, _, readErr := readStateFiles(entityPath, s.soulPath(id), id)
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
	if err = ValidateAgentID(id); err != nil {
		return result, err
	}
	if err = ValidateRevision(expectedRevision); err != nil {
		return result, err
	}
	err = s.inner.WithLock(id, func(entityPath string) error {
		soulPath := s.soulPath(id)
		current, _, _, readErr := readStateFiles(entityPath, soulPath, id)
		if readErr != nil {
			return readErr
		}
		_, soulStatErr := os.Stat(soulPath)
		soulExists := soulStatErr == nil
		if soulStatErr != nil && !errors.Is(soulStatErr, os.ErrNotExist) {
			return fmt.Errorf("stat soul: %w", soulStatErr)
		}
		result.Revision = current.Revision
		if current.Revision != expectedRevision {
			return fmt.Errorf("%w: expected %s, current %s", ErrRevisionConflict, expectedRevision, current.Revision)
		}
		if removeErr := s.removeFile(entityPath); removeErr != nil {
			result.PersistenceStatus = PersistenceNone
			result.ErrorStage = "remove_entity"
			return fmt.Errorf("delete entity: %w", removeErr)
		}
		result.ChangedFields = append(result.ChangedFields, "entity")
		if soulExists {
			if removeErr := s.removeFile(soulPath); removeErr != nil {
				result.PersistenceStatus = PersistencePartial
				result.ErrorStage = "remove_soul"
				// Entity is gone: ReadState/get_agent return not-found, so a
				// digest over leftover soul is not a readable revision (SE-C).
				result.Revision = ""
				return fmt.Errorf("delete soul: %w; entity is gone so reads report not-found and no readable revision exists", removeErr)
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
