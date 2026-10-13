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

// sharedStoreEntry is one registered store and the number of loops that own it.
// Registries and instances only look the store up; a loop acquires it and is the
// one that releases it, and the store closes when the last owner releases.
type sharedStoreEntry struct {
	store  *session.UnifiedStore
	owners int
}

var sharedStores = struct {
	mu sync.Mutex
	m  map[string]*sharedStoreEntry
}{m: map[string]*sharedStoreEntry{}}

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
// first use, without taking ownership of it. A failure is returned, never
// replaced by another store.
func openSharedSessionStore(homePath string) (*session.UnifiedStore, error) {
	return sharedSessionStoreFor(homePath, false)
}

// acquireSharedSessionStore is openSharedSessionStore for a loop: it also counts
// the caller as an owner, so the store stays open until every owner has released.
func acquireSharedSessionStore(homePath string) (*session.UnifiedStore, error) {
	return sharedSessionStoreFor(homePath, true)
}

func sharedSessionStoreFor(homePath string, own bool) (*session.UnifiedStore, error) {
	key := filepath.Clean(homePath)
	sharedStores.mu.Lock()
	defer sharedStores.mu.Unlock()
	e, ok := sharedStores.m[key]
	if !ok {
		dir := filepath.Join(key, "sessions")
		s, err := session.NewUnifiedStoreWithHome(dir, key)
		if err != nil {
			return nil, fmt.Errorf("shared session store at %s: %w", dir, err)
		}
		e = &sharedStoreEntry{store: s}
		sharedStores.m[key] = e
	}
	if own {
		e.owners++
	}
	return e.store, nil
}

// releaseSharedSessionStore gives up one ownership of store. The store is
// forgotten and closed when the last owner releases; a store that is no longer
// registered was already released and is not closed again.
func releaseSharedSessionStore(store *session.UnifiedStore) error {
	sharedStores.mu.Lock()
	for key, e := range sharedStores.m {
		if e.store != store {
			continue
		}
		if e.owners--; e.owners > 0 {
			sharedStores.mu.Unlock()
			return nil
		}
		delete(sharedStores.m, key)
		sharedStores.mu.Unlock()
		return store.Close()
	}
	sharedStores.mu.Unlock()
	return nil
}
