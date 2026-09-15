// Tests for the secret carve-out on POST /api/v1/library/{ws}/files/search.
//
// The engine (pkg/filegrep) confines a walk to the roots its caller hands it
// and says nothing about WHICH files inside them a caller may see —
// "confinement is the CALLER's job" per that package's own doc. pkg/tools'
// grep tool subtracts the $OMNIPUS_HOME secret set from every root it builds
// (pkg/tools/grep_carveout_test.go). This file holds the HUMAN search bar to
// the same contract, in the same installation shape: a non-dotted
// $OMNIPUS_HOME with a workspace mount on its PARENT, which
// workspace.CheckMountTarget warns about and allows — its warning promising
// that "the installation's own secrets remain protected independently of this
// mount".
//
// Expected values come from that promise and from fspolicy's secret set
// (pkg/fspolicy/secretset.go's SecretEntriesAlways/PerTurn), never from what
// the handler happens to return.

package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// seedFilesSearchCarveOutAPI builds a restAPI whose $OMNIPUS_HOME is a
// non-dotted directory inside a parent, seeds the secret set inside that home,
// and mounts the PARENT into the workspace — the one supported installation
// shape in which a Library search root legitimately contains $OMNIPUS_HOME.
//
// Returns the api, the workspace id, and the mount's parent directory.
func seedFilesSearchCarveOutAPI(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	parent := t.TempDir()
	home := filepath.Join(parent, "omnipus")
	require.NoError(t, os.MkdirAll(home, 0o700))
	// Set before mustAgentLoop: ensureIsolatedTestOmnipusHome only substitutes
	// its own temp dir when OMNIPUS_HOME is unset, so this keeps every layer
	// (agent loop, workspace store, this API) pointed at the same home.
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore}},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: home}

	wsID := ulidLikeID(t)
	require.NoError(t, writeWorkspaceFile(home, storedWorkspace{
		ID:        wsID,
		Name:      "Carve-out WS",
		Status:    string(gen.WorkspaceStatusActive),
		CoreTeam:  []string{"mia"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}))
	work, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "own-notes.txt"),
		[]byte("{\"api_key\":\"sk-WORKSPACE-OWN-FILE\"}\n"), 0o600))

	// The secret set, seeded with content AND names a search can match. Every
	// one of these is refused to read_file by fspolicy.IsCarveOut.
	for path, body := range map[string]string{
		filepath.Join(home, "credentials.json"):        "{\"api_key\":\"sk-LIBSEARCH-CREDENTIAL\"}\n",
		filepath.Join(home, "master.key"):              "deadbeefLIBSEARCHMASTERKEY\n",
		filepath.Join(home, "config.json"):             "{\"api_key\":\"sk-LIBSEARCH-CONFIG\"}\n",
		filepath.Join(home, "cli.token"):               "sk-LIBSEARCH-CLITOKEN\n",
		filepath.Join(home, "config.json.bak-1700000"): "{\"api_key\":\"sk-LIBSEARCH-BACKUP\"}\n",
		filepath.Join(home, "auth.json"):               "{\"api_key\":\"sk-LIBSEARCH-AUTHJSON\"}\n",
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	for dir, name := range map[string]string{
		filepath.Join(home, "system"):          "audit.jsonl",
		filepath.Join(home, "entities/agents"): "mia.json",
		filepath.Join(home, "backups"):         "backup.json",
	} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name),
			[]byte("{\"api_key\":\"sk-LIBSEARCH-"+strings.ToUpper(filepath.Base(dir))+"\"}\n"), 0o600))
	}

	// A sibling that is NOT a secret: the control proving the guard suppresses
	// the secret set specifically, not the mount.
	project := filepath.Join(parent, "project")
	require.NoError(t, os.MkdirAll(project, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project, "notes.txt"),
		[]byte("{\"api_key\":\"sk-ORDINARY-PROJECT-FILE\"}\n"), 0o600))

	_, warn, err := workspace.CreateMount(home, wsID, "host", parent)
	require.NoError(t, err)
	require.NotEmpty(t, warn, "mounting $OMNIPUS_HOME's parent must warn (FR-7.6)")

	return api, wsID, parent
}

// libSearchLeaks are the seeded secret CONTENTS. A hit excerpt carrying any of
// these is a disclosure of the secret itself.
var libSearchLeaks = []string{
	"sk-LIBSEARCH-CREDENTIAL", "sk-LIBSEARCH-CONFIG", "sk-LIBSEARCH-CLITOKEN",
	"sk-LIBSEARCH-BACKUP", "sk-LIBSEARCH-AUTHJSON",
	"sk-LIBSEARCH-SYSTEM", "sk-LIBSEARCH-AGENTS", "sk-LIBSEARCH-BACKUPS",
}

// libSearchDeniedPaths are the seeded secret PATHS, as the response addresses
// them ("<mount>/<rel>"). A name hit discloses that the file exists and where.
var libSearchDeniedPaths = []string{
	"omnipus/credentials.json", "omnipus/config.json", "omnipus/cli.token",
	"omnipus/auth.json", "omnipus/master.key",
	"omnipus/system/", "omnipus/entities/", "omnipus/backups/",
}

// TestLibraryFilesSearch_SecretCarveOutIsHonoured is the security contract for
// the human search bar: it must refuse the same secret set every filesystem-
// reading tool refuses. Both halves of a hit are covered — an excerpt
// discloses CONTENT, a path discloses EXISTENCE.
func TestLibraryFilesSearch_SecretCarveOutIsHonoured(t *testing.T) {
	api, ws, _ := seedFilesSearchCarveOutAPI(t)

	t.Run("content of every carved-out file stays unreachable", func(t *testing.T) {
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"api_key"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		body := w.Body.String()
		for _, leak := range libSearchLeaks {
			if strings.Contains(body, leak) {
				t.Errorf("files search disclosed carved-out secret content %q:\n%s", leak, body)
			}
		}
		for _, p := range libSearchDeniedPaths {
			if strings.Contains(body, p) {
				t.Errorf("files search disclosed a carved-out path %q:\n%s", p, body)
			}
		}
	})

	t.Run("a NAME match on a carved-out file is suppressed too", func(t *testing.T) {
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"master.key"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		if strings.Contains(w.Body.String(), "master.key") {
			t.Errorf("files search disclosed the existence of the master key file:\n%s", w.Body.String())
		}
	})

	t.Run("a scope aimed straight at the home directory finds nothing", func(t *testing.T) {
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search",
			`{"query":"api_key","path":"host/omnipus"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		body := w.Body.String()
		for _, leak := range libSearchLeaks {
			if strings.Contains(body, leak) {
				t.Errorf("a `path` aimed at $OMNIPUS_HOME disclosed %q:\n%s", leak, body)
			}
		}
	})

	t.Run("the guard subtracts the secret set, not the mount", func(t *testing.T) {
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search",
			`{"query":"sk-ORDINARY-PROJECT-FILE"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.FileSearchResponse](t, w)
		require.Len(t, resp.Hits, 1, "an ordinary file under the same mount must still be searchable: %s", w.Body.String())
		require.Equal(t, "host/project/notes.txt", resp.Hits[0].Path)
	})

	t.Run("the workspace's own work tree is still searchable", func(t *testing.T) {
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search",
			`{"query":"sk-WORKSPACE-OWN-FILE"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.FileSearchResponse](t, w)
		require.NotEmpty(t, resp.Hits,
			"the carve-out must not swallow the workspace's own files: %s", w.Body.String())
	})
}
