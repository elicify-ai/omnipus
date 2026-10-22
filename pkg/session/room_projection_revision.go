package session

import (
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Recall spans are process-local caches. The durable archive and retirement
// records remain authoritative; no cache revision is a grant or persisted state.
var roomProjectionRevisions sync.Map

func roomProjectionCounter(base string) *atomic.Uint64 {
	counter, _ := roomProjectionRevisions.LoadOrStore(filepath.Clean(base), new(atomic.Uint64))
	return counter.(*atomic.Uint64)
}

// RoomProjectionRevision invalidates cached recall text when room work retires.
// A new process has no old spans; fresh reads still apply durable projection.
func (us *UnifiedStore) RoomProjectionRevision() uint64 {
	return roomProjectionCounter(us.BaseDir()).Load()
}
