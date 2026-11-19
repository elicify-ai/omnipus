// stub_for_red.go — compile-only RED stubs for epic #1094 slice S1a task T-11
// (spec revision 7: the pending agent record).
//
// DELETE when GREEN lands. Every method here returns errors.New("not
// implemented") or a zero value so the RED tests in pending_record_red_test.go
// fail on an assertion or a not-implemented error — never on an undefined
// symbol. Shapes are the spec's Rollback contract (revision 7), mechanism
// steps 1–3, C2, and the effect inventory rows E4/E7:
//   - CreatePendingState(id, agent, soul, agentMD): CreateState with a
//     different entity file name (<id>.pending) and AGENT.md staged in the
//     same call. Same locks (skills lock, then the entity lock for id), same
//     skill binding, same staging and verify.
//   - PublishPending(id): refuse if <id>.json exists, then rename
//     <id>.pending -> <id>.json (the commit point), then sync the
//     entities/agents/ directory. No entity lock for id.
//   - DiscardPending(id): delete <id>.pending, then delete agents/<id>/ with
//     everything in it. Refuses if <id>.json exists. Idempotent.
//
// The two package vars below are the seams the spec names for the fault rows:
// syncDirFn is N2's directory-sync seam (nil in production: GREEN syncs
// directly); publishRenameFn is D7 row 22's rename seam (nil in production:
// os.Rename). Tests swap them and must restore nil.
package agentstore

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// syncDirFn is PublishPending's directory-sync seam (spec "Not guaranteed"
// N2). nil in production — GREEN's PublishPending then syncs the directory
// directly. Never set outside a _test.go file.
var syncDirFn func(dir string) error //nolint:unused // RED seam: GREEN's PublishPending calls it; tests set it

// publishRenameFn is PublishPending's rename seam (D7 row 22 "seam in
// PublishPending"). nil in production — GREEN then uses os.Rename. Never set
// outside a _test.go file.
var publishRenameFn func(from, to string) error //nolint:unused // RED seam: GREEN's PublishPending calls it; tests set it

// CreatePendingState writes the agent record as <id>.pending together with
// SOUL.md and AGENT.md, invisible to every store reader (entity.Store lists
// and reads only <id>.json).
func (s *Store) CreatePendingState(id string, agent *config.AgentConfig, soul, agentMD string) (MutationResult, error) {
	return MutationResult{PersistenceStatus: PersistenceNone, ActivationStatus: ActivationNotAttempted},
		errors.New("not implemented")
}

// PublishPending is the commit point: one rename of <id>.pending to <id>.json
// followed by a directory sync of the records dir, before the caller answers.
func (s *Store) PublishPending(id string) error {
	return errors.New("not implemented")
}

// DiscardPending removes <id>.pending and the agents/<id>/ folder. It refuses
// a published record (<id>.json exists) and is idempotent.
func (s *Store) DiscardPending(id string) error {
	return errors.New("not implemented")
}
