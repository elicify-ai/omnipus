package wecom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWeComTestsNeverResolveTheRealHome is Train 2 gate r1 (side finding):
// NewChannel builds its route store at "$HOME/.omnipus/wecom/reqid-store.json"
// and loads it, so every test constructing a channel read the developer's real
// data dir. The package's TestMain must point HOME at a throwaway home.
func TestWeComTestsNeverResolveTheRealHome(t *testing.T) {
	home := os.Getenv("HOME")
	if !strings.HasPrefix(filepath.Base(home), "omnipus-test-home-") {
		t.Fatalf("tests run under the real home %q; the route store would read its .omnipus", home)
	}
	if !strings.HasPrefix(defaultReqIDStorePath(), home+string(filepath.Separator)) {
		t.Fatalf("route store path %q is outside the isolated home %q", defaultReqIDStorePath(), home)
	}
}
