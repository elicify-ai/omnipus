//go:build darwin || linux

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-87 / §3.2 (FR-049, B-61, MV-8): a config save or reload whose ADDED or
// CHANGED write-access entry overlaps — or cannot be reduced below — a
// preview program folder is refused before any byte is written, with the
// exact spec sentence. Entries already in force stay accepted (§3.2 point 4)
// and surface as doctor WARN-PREVIEW-002 (covered in the doctor package).
// Every writer backend-lead is charged with is driven through its real
// entry point: the REST save path (safeUpdateConfigJSON →
// writeConfigJSONLocked), the pairing candidate builder
// (prepareConfigJSONLocked) and the reload funnel (prepareReloadConfig, the
// documented single entry of both the file watcher and the manual reload).
//
// Every subtest builds its OWN fixture: at RED the refusals do not exist and
// a shared config.json would carry one subtest's accepted entry into the
// next subtest's save.

type officeConfigFixture struct {
	api  *restAPI
	home string
	root string // resolved LibreOffice root
}

func newOfficeConfigFixture(t *testing.T) *officeConfigFixture {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	t.Setenv(config.EnvHome, api.homePath)
	root := filepath.Join(t.TempDir(), "office")
	officepreview.TrustedOffice(t, api.homePath, root)
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return &officeConfigFixture{api: api, home: api.homePath, root: resolved}
}

// officeAllowancePattern is the reducible operator form covering folder.
func officeAllowancePattern(folder string) string {
	return "^" + folder + "(?:/|$)"
}

// officeConfigBytes reads the fixture's config.json exactly as persisted.
func (f *officeConfigFixture) configBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(f.api.configPath())
	require.NoError(t, err)
	return raw
}

// saveAllowWriteEntry drives the REAL REST save path with one entry appended
// to tools.allow_write_paths.
func (f *officeConfigFixture) saveAllowWriteEntry(t *testing.T, entry string) error {
	t.Helper()
	return f.api.safeUpdateConfigJSON(func(m map[string]any) error {
		return officeAllowWriteMutate(m, entry)
	})
}

// officeAllowWriteMutate appends one entry to tools.allow_write_paths in the
// raw config map.
func officeAllowWriteMutate(m map[string]any, entry string) error {
	raw, ok := m["tools"].(map[string]any)
	if !ok {
		raw = map[string]any{}
		m["tools"] = raw
	}
	list, _ := raw["allow_write_paths"].([]any)
	raw["allow_write_paths"] = append(list, entry)
	return nil
}

func TestOfficePreviewConfigWritersRefuseProgramFolderAccess(t *testing.T) {
	t.Run("allow_write_paths_overlap_shapes", func(t *testing.T) {
		for _, shape := range []struct{ name string }{
			{"root"}, {"inside"}, {"contains"},
		} {
			t.Run(shape.name, func(t *testing.T) {
				f := newOfficeConfigFixture(t)
				entry := officeAllowancePattern(f.root)
				if shape.name == "inside" {
					require.NoError(t, os.MkdirAll(filepath.Join(f.root, "sub"), 0700))
					entry = officeAllowancePattern(filepath.Join(f.root, "sub"))
				} else if shape.name == "contains" {
					entry = officeAllowancePattern(filepath.Dir(f.root))
				}
				before := f.configBytes(t)
				err := f.saveAllowWriteEntry(t, entry)
				require.Error(t, err, "T-87: the overlapping allowance must be refused")
				assert.Equal(t, officepreview.ConfigEntryRefusal("tools.allow_write_paths", entry, "LibreOffice", f.root), err.Error())
				assert.Equal(t, before, f.configBytes(t), "a refused save must not write config.json")
			})
		}
	})

	t.Run("allow_write_paths_renderer_overlap", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		rendererRoot := filepath.Join(t.TempDir(), "pdfium")
		officepreview.RegisteredRendererFixture(t, f.home, rendererRoot, true)
		resolved, err := filepath.EvalSymlinks(rendererRoot)
		require.NoError(t, err)
		entry := officeAllowancePattern(resolved)
		before := f.configBytes(t)
		err = f.saveAllowWriteEntry(t, entry)
		require.Error(t, err, "T-87: the renderer's registered root is protected the same way")
		assert.Equal(t, officepreview.ConfigEntryRefusal("tools.allow_write_paths", entry, "PDF renderer", resolved), err.Error())
		assert.Equal(t, before, f.configBytes(t), "a refused save must not write config.json")
	})

	t.Run("sandbox_allowed_paths_overlap", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		before := f.configBytes(t)
		entry := f.root // a plain absolute path: the kernel sandbox grants it read-write
		err := f.api.safeUpdateConfigJSON(func(m map[string]any) error {
			raw, ok := m["sandbox"].(map[string]any)
			if !ok {
				raw = map[string]any{}
				m["sandbox"] = raw
			}
			list, _ := raw["allowed_paths"].([]any)
			raw["allowed_paths"] = append(list, entry)
			return nil
		})
		require.Error(t, err, "T-87: a sandbox allowed path over the component must be refused")
		assert.Equal(t, officepreview.ConfigEntryRefusal("sandbox.allowed_paths", entry, "LibreOffice", f.root), err.Error())
		assert.Equal(t, before, f.configBytes(t), "a refused save must not write config.json")
	})

	t.Run("unreducible_pattern_refused", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		before := f.configBytes(t)
		entry := f.root // no ^ anchor: Omnipus cannot tell which folders it covers
		err := f.saveAllowWriteEntry(t, entry)
		require.Error(t, err, "T-87: an entry Omnipus cannot reduce must be refused at the save, not left to the loader")
		assert.Equal(t, officepreview.ConfigUnreducibleRefusal("tools.allow_write_paths", entry), err.Error(),
			"the §3.2 save-time refusal, not a post-write load rejection")
		assert.Equal(t, before, f.configBytes(t), "a refused save must not write config.json")
	})

	t.Run("changed_entry_now_overlaps_refused", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		unrelatedEntry := "^/srv/data(?:/|$)"
		require.NoError(t, f.saveAllowWriteEntry(t, unrelatedEntry), "the unrelated first save must go through")
		before := f.configBytes(t)
		overlapping := officeAllowancePattern(f.root)
		err := f.api.safeUpdateConfigJSON(func(m map[string]any) error {
			raw, ok := m["tools"].(map[string]any)
			if !ok {
				raw = map[string]any{}
				m["tools"] = raw
			}
			raw["allow_write_paths"] = []any{overlapping} // replaces the unrelated entry
			return nil
		})
		require.Error(t, err, "T-87: an entry CHANGED to overlap must be refused")
		assert.Equal(t, officepreview.ConfigEntryRefusal("tools.allow_write_paths", overlapping, "LibreOffice", f.root), err.Error())
		assert.Equal(t, before, f.configBytes(t), "the refused change must leave the unrelated entry on disk")
	})

	t.Run("pairing_candidate_builder_refuses", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		before := f.configBytes(t)
		entry := officeAllowancePattern(f.root)
		_, _, _, err := f.api.prepareConfigJSONLocked(func(m map[string]any) error {
			return officeAllowWriteMutate(m, entry)
		})
		require.Error(t, err, "T-87: the pairing candidate builder must refuse the overlap before any write")
		assert.Equal(t, officepreview.ConfigEntryRefusal("tools.allow_write_paths", entry, "LibreOffice", f.root), err.Error())
		assert.Equal(t, before, f.configBytes(t))
	})

	t.Run("reload_refuses_new_overlap", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		candidate := config.DefaultConfig()
		candidate.Sandbox.AllowedPaths = []string{f.root}
		err := prepareReloadConfig(candidate, &services{})
		require.Error(t, err, "T-87: the reload funnel must reject a config that would newly grant the component folder")
		assert.Contains(t, err.Error(), officepreview.ConfigEntryRefusal("sandbox.allowed_paths", f.root, "LibreOffice", f.root),
			"a rejected reload must carry the same §3.2 text; the previous configuration stays in force")
	})

	t.Run("unrelated_entry_control", func(t *testing.T) {
		f := newOfficeConfigFixture(t)
		unrelated := "^" + filepath.Dir(filepath.Dir(f.root)) + "/somewhere-else(?:/|$)"
		require.NoError(t, f.saveAllowWriteEntry(t, unrelated), "T-87 control: an unrelated new entry is accepted")
		assert.Contains(t, string(f.configBytes(t)), "somewhere-else", "the accepted entry really reached config.json")
	})
}

// §3.2 point 4: an entry present at gateway start and left unchanged is
// accepted by later saves and reloads; the component is reported "unsafe
// location" by doctor (its row lives in the doctor package's own test).
func TestOfficePreviewConfigEntryInForceAtStartIsAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	entry := "^" + filepath.Join(tmpDir, "office") + "(?:/|$)"
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
		}},
		Tools: config.ToolsConfig{AllowWritePaths: []string{entry}},
	}
	root := filepath.Join(tmpDir, "office")
	officepreview.TrustedOffice(t, tmpDir, root)
	configPath := filepath.Join(tmpDir, "config.json")
	require.NoError(t, config.SaveConfig(configPath, cfg), "the in-force entry is persisted before start")

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
	require.Contains(t, al.GetConfig().Tools.AllowWritePaths, entry,
		"instrument: the entry really is in force at gateway start")

	// A later UNRELATED save is accepted and keeps the in-force entry.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			gw = map[string]any{}
			m["gateway"] = gw
		}
		gw["log_level"] = "debug"
		return nil
	}), "T-87 (e): an unrelated save must not be refused by the in-force overlap")

	// A reload that leaves the entry unchanged is accepted (both reload
	// triggers funnel through prepareReloadConfig).
	candidate, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	require.Contains(t, candidate.Tools.AllowWritePaths, entry, "instrument: the reloaded candidate still carries the entry")
	require.NoError(t, prepareReloadConfig(candidate, &services{}),
		"T-87 (e): a reload leaving the entry unchanged must be accepted")
}
