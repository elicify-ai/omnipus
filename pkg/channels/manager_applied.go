package channels

// The applied/current restart predicate and the pending-application marker
// (channels-spec-r8, FR-050 legs 1-5, T-6, S-88).
//
// Configure decides whether an unchanged save must still restart a
// connection from the manager's lifecycle records, not from its own diff:
// an enabled connection is current only when an adapter is installed, its
// applied adapter hash equals the desired one, it has no start-failure
// record, no unfinished disposal and no pending-application marker.
//
// The marker records that a credential was stored under a reference the
// instance's saved configuration selects, which hashes and credential
// signatures cannot see (they record presence, never values). It carries a
// non-secret version from one monotonic counter. A reload stamps the counter
// value it observed before resolving its credential bundle; a rebuild that
// completes clears the marker only when that stamp is at least the marker's
// version, i.e. only when the bundle the rebuild consumed was captured after
// the marked write (snapshot-aware acknowledgment, R7-MAJ-002). A later
// write stays pending.

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// NoCredentialCapture is the capture version of a reload that did not stamp
// its bundle. It acknowledges no marker (every marker version is >= 1).
const NoCredentialCapture uint64 = 0

// NotCurrentLeg names the FR-050 predicate leg that failed.
type NotCurrentLeg string

const (
	// LegCurrent: all five legs hold.
	LegCurrent NotCurrentLeg = ""
	// LegNotInstalled: no constructed and registered adapter (leg 1).
	LegNotInstalled NotCurrentLeg = "no installed adapter"
	// LegHashDiffers: the applied adapter hash is absent or differs (leg 2).
	LegHashDiffers NotCurrentLeg = "applied settings differ from the saved settings"
	// LegStartFailed: a start-failure record exists (leg 3).
	LegStartFailed NotCurrentLeg = "the last start failed"
	// LegDisposalPending: an unfinished teardown is retained (leg 4).
	LegDisposalPending NotCurrentLeg = "an earlier shutdown did not finish"
	// LegCredentialPending: a stored credential was never applied (leg 5).
	LegCredentialPending NotCurrentLeg = "a stored credential was not applied yet"
)

// applicationMarkers is the pending-application marker state. It has its own
// lock so Configure never waits on m.mu, which a reload holds for long
// stretches; it is never held while taking m.mu.
type applicationMarkers struct {
	mu      sync.Mutex
	version uint64            // last version issued
	pending map[string]uint64 // instance id -> version of its newest unapplied write
}

// MarkCredentialPending advances the marker version and records it as id's
// newest unapplied credential write. Configure calls it right after the
// credential store accepted a value under a reference the instance's saved
// configuration selects — never before the store, so a failed store never
// leaves a marker. Returns the new version (non-secret).
func (m *Manager) MarkCredentialPending(id string) uint64 {
	m.applied.mu.Lock()
	defer m.applied.mu.Unlock()
	m.applied.version++
	if m.applied.pending == nil {
		m.applied.pending = make(map[string]uint64)
	}
	m.applied.pending[id] = m.applied.version
	return m.applied.version
}

// CredentialWriteVersion is the capture stamp a reload records immediately
// before it resolves its credential bundle: every write marked at or below
// this version was stored before the bundle was read.
func (m *Manager) CredentialWriteVersion() uint64 {
	m.applied.mu.Lock()
	defer m.applied.mu.Unlock()
	return m.applied.version
}

// PendingApplication reports id's pending-application marker version, if any.
func (m *Manager) PendingApplication(id string) (version uint64, pending bool) {
	m.applied.mu.Lock()
	defer m.applied.mu.Unlock()
	version, pending = m.applied.pending[id]
	return version, pending
}

// acknowledgeApplication clears id's marker after a completed rebuild whose
// credential bundle was captured at version captured — only when the marked
// write is included in that capture (FR-050 (iii)).
func (m *Manager) acknowledgeApplication(id string, captured uint64) {
	m.applied.mu.Lock()
	defer m.applied.mu.Unlock()
	if v, ok := m.applied.pending[id]; ok && v <= captured {
		delete(m.applied.pending, id)
	}
}

// ApplicationCurrent evaluates FR-050's applied/current predicate for an
// enabled instance id against the desired configuration: it returns
// LegCurrent when all five legs hold, else the first failing leg. It reads
// lifecycle records only (never connectivity), takes m.mu.RLock, and is for
// Configure's restart decision only — never the dispatch hook or a worker.
func (m *Manager) ApplicationCurrent(id string, desired *config.Config) NotCurrentLeg {
	m.mu.RLock()
	_, installed := m.channels[id]
	applied, hashKnown := m.channelHashes[id]
	startErr := m.startErrors[id]
	disposing := m.pendingDisposals[id] != nil
	m.mu.RUnlock()

	switch {
	case !installed:
		return LegNotInstalled
	case !hashKnown || applied != ChannelRevision(desired, id):
		return LegHashDiffers
	case startErr != nil:
		return LegStartFailed
	case disposing:
		return LegDisposalPending
	}
	if _, pending := m.PendingApplication(id); pending {
		return LegCredentialPending
	}
	return LegCurrent
}
