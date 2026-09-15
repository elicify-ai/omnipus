package gateway

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newRegistrarTestStore builds an unlocked credential store in a temp dir.
func newRegistrarTestStore(t *testing.T) *credentials.Store {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("UnlockWithKey: %v", err)
	}
	return store
}

func storeOAuthEntry(t *testing.T, store *credentials.Store, vendor, access, refresh string) {
	t.Helper()
	payload := map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Set(credentials.OAuthEntryName(vendor), string(data)); err != nil {
		t.Fatalf("Set: %v", err)
	}
}
