package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig_RejectsAwaitMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"version":1,"agents":{"list":[{"id":"worker","delegation_policy":{"modes":["await"]}}]}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), `delegation mode "await"`) {
		t.Fatalf("LoadConfig(await) error = %v, want explicit retired-mode rejection", err)
	}
}
