// shared_session_store.go: the ONE session store of a running loop (session-core
// U2, DEL-10 step 2; ARCHITECT-ANSWER-DEL10.md).
//
// Every chat, task, plan and verifier session lives in the shared store at
// $OMNIPUS_HOME/sessions, and so does every agent's model window (it is keyed by
// the owning session id, not by agent). There are no per-agent stores. The store
// is opened once per home directory and every consumer - the loop, each agent
// instance, a reloaded registry, an upserted agent - receives that same pointer,
// so a reload can never put two lock-shard sets over one directory, and the store
// is closed exactly once, by the loop that owns it.
package agent

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

var sharedStores = struct {
	mu sync.Mutex
	m  map[string]*session.UnifiedStore
}{m: map[string]*session.UnifiedStore{}}

// sessionsHomeFor is the omnipus home directory the shared store belongs to: the
// parent of the default workspace, exactly where the loop puts its task store.
func sessionsHomeFor(cfg *config.Config) string {
	if cfg != nil {
		if base := cfg.AgentHomeBasePath(); base != "" {
			return filepath.Dir(base)
		}
	}
	return omnipusHome()
}

// openSharedSessionStore returns the shared store for homePath, opening it on
// first use. A failure is returned, never replaced by another store.
func openSharedSessionStore(homePath string) (*session.UnifiedStore, error) {
	key := filepath.Clean(homePath)
	sharedStores.mu.Lock()
	defer sharedStores.mu.Unlock()
	if s, ok := sharedStores.m[key]; ok {
		return s, nil
	}
	dir := filepath.Join(key, "sessions")
	s, err := session.NewUnifiedStoreWithHome(dir, key)
	if err != nil {
		return nil, fmt.Errorf("shared session store at %s: %w", dir, err)
	}
	sharedStores.m[key] = s
	return s, nil
}

// releaseSharedSessionStore forgets store as the shared store of homePath (when
// it is the registered one) and closes it. It is called once, by the loop that
// owns the store.
func releaseSharedSessionStore(homePath string, store *session.UnifiedStore) error {
	key := filepath.Clean(homePath)
	sharedStores.mu.Lock()
	if sharedStores.m[key] == store {
		delete(sharedStores.m, key)
	}
	sharedStores.mu.Unlock()
	return store.Close()
}
