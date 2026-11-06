package skills

// The "seen" record (skills spec r15 §7.3): an in-memory record per
// (session, skill key) → the revision that session last observed through a
// managed read or its own committed change. Used only by the agent adapters
// to supply the pipeline's expected revision; the pipeline never looks at
// sessions. Lost on gateway restart (the agent reads again).

import "sync"

type seenKey struct{ session, key string }

var seenRecords = struct {
	sync.Mutex
	m map[seenKey]string
}{m: map[seenKey]string{}}

// RecordSeen records that session observed revision rev of slug. An empty
// session or revision records nothing (a read with no session cannot vouch
// for a later write).
func RecordSeen(session, slug, rev string) {
	if session == "" || rev == "" {
		return
	}
	seenRecords.Lock()
	defer seenRecords.Unlock()
	seenRecords.m[seenKey{session, SkillKey(slug)}] = rev
}

// SeenRevision returns the revision session last observed for slug.
func SeenRevision(session, slug string) (string, bool) {
	if session == "" {
		return "", false
	}
	seenRecords.Lock()
	defer seenRecords.Unlock()
	rev, ok := seenRecords.m[seenKey{session, SkillKey(slug)}]
	return rev, ok
}

// ForgetSeen removes session's record for slug (a partial save, §7.6 class
// 2, or a read that could not observe the state, §9.5).
func ForgetSeen(session, slug string) {
	seenRecords.Lock()
	defer seenRecords.Unlock()
	delete(seenRecords.m, seenKey{session, SkillKey(slug)})
}
