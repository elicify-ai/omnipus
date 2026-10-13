package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Amendment §3.2(6): workspace-owned pair files cascade through the REAL REST
// delete, while another workspace's same-agent file survives. This is the
// existing directory-ownership positive control, not a claim of new RED code.
func TestWU21_WorkspaceDeleteOwnsPairSummaries(t *testing.T) {
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("USERPROFILE", isolated)
	t.Setenv("OMNIPUS_HOME", isolated)
	api := newTestRestAPIWithHome(t)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	a := createWorkspaceViaAPI(t, api, "WU21 Alpha", "")
	b := createWorkspaceViaAPI(t, api, "WU21 Beta", "")
	pairPath := func(ws string) string {
		return filepath.Join(api.homePath, "workspaces", ws, ".omnipus", "carry-forward", "self.md")
	}
	for _, ws := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(pairPath(ws)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pairPath(ws), []byte("pair:"+ws), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, a), nil)
	api.HandleWorkspaces(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("workspace delete failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(pairPath(a)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("§3.2(6): deleted workspace's pair summary survives: %v", err)
	}
	data, err := os.ReadFile(pairPath(b))
	if err != nil || string(data) != "pair:"+b {
		t.Fatalf("other workspace's pair summary changed: bytes=%q err=%v", data, err)
	}
}
