//go:build !windows

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R1/R2: same executable first succeeds through the real person routes, then
// becomes forbidden solely because a newly configured agent home contains it.
func t11cHomeRefusal(t *testing.T, useDefault bool) {
	t.Helper()
	f := t8SettingsAPI(t)
	marker := filepath.Join(t11cCanonicalDir(t), "starts")
	source := t11BuildMarkerProgram(t, marker)
	program := t11cProgramAt(t, source, t11cCanonicalDir(t))
	t11cSaveAllowed(t, f, source) // a DIFFERENT saved path makes accidental writes observable
	spawns, launched := t11CountSpawns(t)
	resp := t11cCLIResponse(t, f, program)
	require.True(t, resp.Ok, "control: actual version handshake: %+v", resp)
	require.NotNil(t, resp.Version)
	require.Equal(t, "1.2.3", *resp.Version)
	require.Equal(t, 1, *spawns)
	require.Equal(t, []string{program}, *launched)
	require.FileExists(t, marker, "instrument: real process wrote its own marker")
	home := filepath.Dir(program)
	require.NotEqual(t, f.api.homePath, home, "fixture: home outside the data folder")
	t11cSetConfig(t, f, func(cfg *config.Config) {
		if useDefault {
			cfg.Agents.Defaults.Home = home
		} else {
			cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "combined-home", Home: home})
		}
	})
	// Run the stateless check first: an incorrectly accepted Save must not
	// reload the roster/defaults and poison the next surface's input.
	assertHome := func(t *testing.T) {
		t.Helper()
		cfg := f.api.agentLoop.GetConfig()
		if useDefault {
			require.Equal(t, home, cfg.Agents.Defaults.Home, "instrument: configured default home reaches this surface")
		} else {
			require.Contains(t, cfg.Agents.List, config.AgentConfig{ID: "combined-home", Home: home},
				"instrument: configured agent home reaches this surface")
		}
	}
	t.Run("cli_validate", func(t *testing.T) {
		assertHome(t)
		t11cCLIRefused(t, f, program, marker, "agent_writable_location", spawns)
	})
	t.Run("save", func(t *testing.T) {
		assertHome(t)
		t11cSaveRefused(t, f, program, "agent_writable_location", program)
	})
}

func TestCombinedProgramLocation_PerAgentHome_SaveAndCLI_R1(t *testing.T) {
	t11cHomeRefusal(t, false)
}

func TestCombinedProgramLocation_DefaultAgentHome_SaveAndCLI_R2(t *testing.T) {
	t11cHomeRefusal(t, true)
}

func TestCombinedProgramLocation_AllowedPath_CLI_R3(t *testing.T) {
	f := t8SettingsAPI(t)
	marker := filepath.Join(t11cCanonicalDir(t), "starts")
	program := t11cProgramAt(t, t11BuildMarkerProgram(t, marker), t11cCanonicalDir(t))
	spawns, _ := t11CountSpawns(t)
	control := t11cCLIResponse(t, f, program)
	require.True(t, control.Ok, "instrument: this actual executable runs before the grant: %+v", control)
	require.Equal(t, 1, *spawns)
	require.FileExists(t, marker)
	t11cSetConfig(t, f, func(cfg *config.Config) { cfg.Sandbox.AllowedPaths = []string{filepath.Dir(program)} })
	t11cCLIRefused(t, f, program, marker, "agent_writable_location", spawns)
}

func TestCombinedProgramLocation_UnsafeWorkspace_SaveAndCLI_R6(t *testing.T) {
	f := t8SettingsAPI(t)
	marker := filepath.Join(t11cCanonicalDir(t), "starts")
	source := t11BuildMarkerProgram(t, marker)
	program := t11cProgramAt(t, source, t11cCanonicalDir(t))
	t11cSaveAllowed(t, f, source) // a DIFFERENT saved path makes accidental writes observable
	spawns, _ := t11CountSpawns(t)
	control := t11cCLIResponse(t, f, program)
	require.True(t, control.Ok, "control: no corrupt workspace yet: %+v", control)
	require.Equal(t, 1, *spawns)
	require.FileExists(t, marker)
	t11cUnsafeWorkspace(t, f.api.homePath)
	t.Run("save_Q37", func(t *testing.T) {
		t11cSaveRefused(t, f, program, "location_unverifiable", program)
	})
	t.Run("cli_validate", func(t *testing.T) {
		t11cCLIRefused(t, f, program, marker, "location_unverifiable", spawns)
	})
}

func TestCombinedProgramLocation_BlankGatewayHomeFallback_R9(t *testing.T) {
	home := t11cCanonicalDir(t)
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{ID: "outside-home", Home: t11cCanonicalDir(t)}}
	cfg.Sandbox.AllowedPaths = []string{t11cCanonicalDir(t)}
	explicit, err := thirdPartyAgentWritable(config.OmnipusHomeDir(), cfg)
	require.NoError(t, err, "control: explicit configured home")
	require.NotEmpty(t, explicit)
	blank, err := thirdPartyAgentWritable("", cfg)
	require.NoError(t, err, "R9: fallback belongs at the gateway boundary")
	require.Equal(t, explicit, blank, "R9: exact roots, not a partial fallback list")
}

func TestCombinedProgramLocation_CLIDetailNamesCheck_R10(t *testing.T) {
	f := t8SettingsAPI(t)
	marker := filepath.Join(t11cCanonicalDir(t), "starts")
	source := t11BuildMarkerProgram(t, marker)
	outside := t11cProgramAt(t, source, t11cCanonicalDir(t))
	inside := t11cProgramAt(t, source, filepath.Join(f.api.homePath, "bin"))
	spawns, _ := t11CountSpawns(t)
	control := t11cCLIResponse(t, f, outside)
	require.True(t, control.Ok, "instrument: actual handshake before refusals: %+v", control)
	require.Equal(t, 1, *spawns)
	require.FileExists(t, marker)
	t.Run("known_writable", func(t *testing.T) {
		t11cCLIRefused(t, f, inside, marker, "agent_writable_location", spawns)
	})
	t11cUnsafeWorkspace(t, f.api.homePath)
	t.Run("incomplete_list", func(t *testing.T) {
		t11cCLIRefused(t, f, outside, marker, "location_unverifiable", spawns)
	})
}

func TestCombinedProgramLocation_AgreementAcrossThreeSurfaces_R7(t *testing.T) {
	f := t8SettingsAPI(t)
	marker := filepath.Join(t11cCanonicalDir(t), "starts")
	source := t11BuildMarkerProgram(t, marker)
	outside := t11cProgramAt(t, source, t11cCanonicalDir(t))
	agentHome, allowed, mountHost := t11cCanonicalDir(t), t11cCanonicalDir(t), t11cCanonicalDir(t)
	ws := seedLibraryWorkspace(t, f.api, "Combined shared-check agreement")
	mount, _, err := workspace.CreateMount(f.api.homePath, ws, "agreement", mountHost)
	require.NoError(t, err)
	mounted := t11cProgramAt(t, source, mount.HostPath)
	link := filepath.Join(t11cCanonicalDir(t), "mount-link")
	require.NoError(t, os.Symlink(mounted, link))
	setUnion := func(t *testing.T) {
		t.Helper()
		t11cSetConfig(t, f, func(cfg *config.Config) {
			cfg.Agents.List = []config.AgentConfig{{ID: "agreement-home", Home: agentHome}}
			cfg.Sandbox.AllowedPaths = []string{allowed}
		})
		cfg := f.api.agentLoop.GetConfig()
		require.Equal(t, []config.AgentConfig{{ID: "agreement-home", Home: agentHome}}, cfg.Agents.List)
		require.Equal(t, []string{allowed}, cfg.Sandbox.AllowedPaths)
	}
	cases := []struct { // not-wire-format: test inputs/expected classifications only
		name, path, check, at string
	}{
		{"outside", outside, "", outside},
		{"data_home", t11cProgramAt(t, source, filepath.Join(f.api.homePath, "bin")), "agent_writable_location", ""},
		{"agent_home", t11cProgramAt(t, source, agentHome), "agent_writable_location", ""},
		{"allowed_path", t11cProgramAt(t, source, allowed), "agent_writable_location", ""},
		{"mount", mounted, "agent_writable_location", mounted},
		{"link_into_mount", link, "agent_writable_location", mounted},
		{"relative", "combined-cli", "not_absolute", "combined-cli"},
		{"missing", filepath.Join(t11cCanonicalDir(t), "missing-cli"), "not_found", ""},
	}
	spawns, launched := t11CountSpawns(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every candidate starts with the SAME intended union. Save runs last,
			// so even a bad save/reload cannot alter this row's run or CLI inputs.
			setUnion(t)
			at := tc.at
			if at == "" {
				at = tc.path
			}
			var saveCheck, runCheck, cliCheck string
			t.Run("run_program", func(t *testing.T) { runCheck = t11cRunDecision(t, f, tc.path, tc.check) })
			t.Run("cli_validate", func(t *testing.T) {
				if tc.check != "" {
					cliCheck = t11cCLIRefused(t, f, tc.path, marker, tc.check, spawns)
					return
				}
				t11ClearMarker(t, marker)
				*spawns, *launched = 0, nil
				resp := t11cCLIResponse(t, f, tc.path)
				require.True(t, resp.Ok, "R7 outside program must run: %+v", resp)
				require.NotNil(t, resp.Version)
				assert.Equal(t, "1.2.3", *resp.Version)
				require.NotNil(t, resp.ResolvedPath)
				assert.Equal(t, outside, *resp.ResolvedPath)
				assert.Equal(t, []string{outside}, *launched)
				assert.Equal(t, 1, *spawns)
				require.FileExists(t, marker)
			})
			t.Run("save", func(t *testing.T) {
				if tc.check == "" {
					t11cSaveAllowed(t, f, tc.path)
				} else {
					saveCheck = t11cSaveRefused(t, f, tc.path, tc.check, at)
				}
			})
			assert.Equal(t, tc.check, saveCheck, "independent expected Save classification")
			assert.Equal(t, tc.check, runCheck, "independent expected RunProgram classification")
			assert.Equal(t, tc.check, cliCheck, "independent expected CLI classification")
			assert.Equal(t, saveCheck, runCheck, "R7: same actual check on Save and run")
			assert.Equal(t, saveCheck, cliCheck, "R7: same actual check on Save and CLI")
		})
	}
}
