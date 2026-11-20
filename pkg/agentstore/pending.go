package agentstore

// pending.go: the pending agent record of the package import (epic #1094,
// spec export-import-s1 revision 7, "Rollback contract", mechanism steps 1
// and 3, compensation C2). An import writes the new agent as
// entities/agents/<id>.pending, which no reader of this store sees
// (entity.Store lists and reads only <id>.json), and makes it visible with one
// rename to <id>.json: that rename is the import's commit point.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// pendingSuffix is the file suffix of a pending record. It must never be
// ".json": every reader keys on that suffix, which is what keeps a pending
// record invisible.
const pendingSuffix = ".pending"

// ErrPublishNotDurable is returned by PublishPending when the commit rename
// succeeded but the directory sync after it failed: the record IS published
// (visible to every reader), but the rename may not survive a power cut.
var ErrPublishNotDurable = errors.New("agentstore: pending record published, but the directory sync failed")

// syncDirFn and publishRenameFn are PublishPending's count and fault seams
// (spec "Not guaranteed" N2: count the directory syncs; D7 row 22: fail the
// commit rename). Each is called just before the real step with the same
// arguments: a non-nil error stops PublishPending before that step runs, and
// a nil error lets the real os.Rename or directory sync run. Both are nil in
// production. Never set outside a _test.go file.
var (
	syncDirFn       func(dir string) error
	publishRenameFn func(from, to string) error
)

func (s *Store) pendingPath(id string) string {
	return filepath.Join(s.inner.Dir(), id+pendingSuffix)
}

func (s *Store) publishedPath(id string) string {
	return filepath.Join(s.inner.Dir(), id+".json")
}

// existsOnDisk reports whether path exists; any stat error other than "does
// not exist" is returned.
func existsOnDisk(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// CreatePendingState writes the agent record as <id>.pending together with
// SOUL.md and AGENT.md in one call (mechanism step 1). It is CreateState with a
// different entity file name and AGENT.md as a third staged file: the same
// locks (skills lock, then the entity lock for id), the same skill binding,
// and the same stage-everything-then-replace discipline (publishStaged) and
// verify. An empty agentMD writes no AGENT.md, as CreateState writes none.
// It refuses when either <id>.json or <id>.pending already exists. No
// notifier is called: the record is invisible until PublishPending.
func (s *Store) CreatePendingState(id string, agent *config.AgentConfig, soul, agentMD string) (result MutationResult, err error) {
	result.PersistenceStatus = PersistenceNone
	result.ActivationStatus = ActivationNotAttempted
	if validateErr := ValidateAgentID(id); validateErr != nil {
		return result, validateErr
	}
	if agent == nil {
		return result, fmt.Errorf("agentstore: create pending state %q: agent is required", id)
	}
	if agent.ID != "" && agent.ID != id {
		return result, fmt.Errorf("agentstore: create pending state %q: cfg.ID %q does not match", id, agent.ID)
	}
	err = s.withSkillsLock(func() error {
		return s.inner.WithLock(id, func(entityPath string) error {
			pendingPath := s.pendingPath(id)
			for _, path := range []string{entityPath, pendingPath} {
				exists, statErr := existsOnDisk(path)
				if statErr != nil {
					return statErr
				}
				if exists {
					return fmt.Errorf("%w: %s", entity.ErrAlreadyExists, filepath.Base(path))
				}
			}
			agent.ID = id
			if bindErr := s.bindSkillGrants(agent); bindErr != nil {
				return bindErr
			}
			if agent.CreatedAt == nil {
				now := time.Now().UTC()
				agent.CreatedAt = &now
			}
			entityBytes, marshalErr := json.MarshalIndent(agent, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			files := []*stagedFile{
				{field: "entity", path: pendingPath, data: entityBytes},
				{field: "soul", path: s.soulPath(id), data: []byte(soul)},
			}
			if agentMD != "" {
				files = append(files, &stagedFile{field: "agent_md", path: s.agentMDPath(id), data: []byte(agentMD)})
			}
			if publishErr := s.publishStaged(pendingPath, id, files, &result); publishErr != nil {
				return publishErr
			}
			actual, _, _, readErr := readStateFiles(pendingPath, s.soulPath(id), id)
			if readErr != nil {
				result.PersistenceStatus, result.ErrorStage = PersistencePartial, "verify"
				return readErr
			}
			result.PersistenceStatus, result.Revision = PersistenceComplete, actual.Revision
			return nil
		})
	})
	if err != nil {
		result.Message = err.Error()
		return result, fmt.Errorf("agentstore: create pending state %q: %w", id, err)
	}
	return result, nil
}

// PublishPending is the import's commit point (mechanism step 3): it refuses
// when <id>.json exists, renames <id>.pending to <id>.json, then syncs the
// records directory before it returns. It does not take the entity lock for
// id (the caller holds the workspace lock; see the spec's lock rule). A failed
// rename publishes nothing and syncs nothing. A failed sync after a
// successful rename returns ErrPublishNotDurable: the record is published.
func (s *Store) PublishPending(id string) error {
	if err := ValidateAgentID(id); err != nil {
		return err
	}
	published, pending := s.publishedPath(id), s.pendingPath(id)
	exists, err := existsOnDisk(published)
	if err != nil {
		return fmt.Errorf("agentstore: publish pending %q: %w", id, err)
	}
	if exists {
		return fmt.Errorf("agentstore: publish pending %q: agent is already published", id)
	}
	if exists, err = existsOnDisk(pending); err != nil {
		return fmt.Errorf("agentstore: publish pending %q: %w", id, err)
	}
	if !exists {
		return fmt.Errorf("agentstore: publish pending %q: no pending record", id)
	}
	if publishRenameFn != nil {
		if err := publishRenameFn(pending, published); err != nil {
			return fmt.Errorf("agentstore: publish pending %q: rename: %w", id, err)
		}
	}
	if err := os.Rename(pending, published); err != nil {
		return fmt.Errorf("agentstore: publish pending %q: rename: %w", id, err)
	}
	dir := s.inner.Dir()
	if syncDirFn != nil {
		if err := syncDirFn(dir); err != nil {
			return fmt.Errorf("%w: %q: %w", ErrPublishNotDurable, id, err)
		}
	}
	if err := syncRecordsDir(dir); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrPublishNotDurable, id, err)
	}
	return nil
}

// syncRecordsDir makes a rename inside dir durable on Linux and macOS. Go
// cannot sync a directory on Windows (spec N2): there the call does nothing.
func syncRecordsDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", dir, err)
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil || closeErr != nil {
		return fmt.Errorf("sync %s: %w", dir, errors.Join(syncErr, closeErr))
	}
	return nil
}

// DiscardPending is compensation C2: it deletes <id>.pending, then the
// agents/<id>/ folder with everything in it. It refuses when <id>.json exists,
// so it never deletes a published agent, and it is idempotent: an absent
// record or folder is not an error. It runs under the entity lock for id, so
// it cannot interleave with CreatePendingState for the same id.
func (s *Store) DiscardPending(id string) error {
	if err := ValidateAgentID(id); err != nil {
		return err
	}
	err := s.inner.WithLock(id, func(entityPath string) error {
		exists, statErr := existsOnDisk(entityPath)
		if statErr != nil {
			return statErr
		}
		if exists {
			return errors.New("agent is already published; a published agent is never discarded")
		}
		pending := s.pendingPath(id)
		if removeErr := s.removeFile(pending); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("remove pending record %s: %w", filepath.Base(pending), removeErr)
		}
		folder := filepath.Dir(s.soulPath(id))
		if removeErr := os.RemoveAll(folder); removeErr != nil {
			return fmt.Errorf("pending record removed, but the agent folder %s remains: %w", folder, removeErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("agentstore: discard pending %q: %w", id, err)
	}
	return nil
}
