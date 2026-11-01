package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

type capRedGatewayConfigSource interface {
	ConfigSnapshot() (*config.Config, uint64)
}
type capRedGatewayCapacityApplier interface{ ApplyConfiguredCapacity(uint64, int) }

func TestCapRED_SettingsPUT_AppliesPublishedGeneration(t *testing.T) {
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { return false, true }, func() (uint64, bool) { return 1 << 30, true })
	t.Cleanup(restore)
	api := newTestRestAPIWithHome(t)
	te := agent.GetTaskExecutor(api.agentLoop)
	if te == nil {
		t.Fatal("real settings fixture has no executor")
	}
	source, coherent := any(api.agentLoop).(capRedGatewayConfigSource)
	var oldGen uint64
	oldValue := api.agentLoop.GetConfig().Performance.MaxParallelAgents
	if coherent {
		cfg, gen := source.ConfigSnapshot()
		oldGen = gen
		oldValue, _ = cfg.Performance.EffectiveMaxParallelAgents()
	} else {
		t.Error("CAP-B12 RED: Settings has no coherent ConfigSnapshot source")
	}
	value := 2
	body, err := json.Marshal(gen.PerformanceSettingsUpdate{MaxParallelAgents: &value})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("actual Settings PUT: HTTP %d: %s", w.Code, w.Body.String())
	}
	if got := te.DispatchSemaCap(); got != 2 {
		t.Errorf("published settings not applied immediately: cap=%d, want 2", got)
	}
	if got := api.agentLoop.GetConfig().Performance.MaxParallelAgents; got != 2 {
		t.Errorf("live configured value=%d, want 2", got)
	}
	applier, versioned := any(te).(capRedGatewayCapacityApplier)
	if !versioned {
		t.Error("CAP-B12 RED: Settings executor has no generation-aware applier")
	}
	if coherent && versioned {
		cfg, newGen := source.ConfigSnapshot()
		if newGen <= oldGen || cfg.Performance.MaxParallelAgents != 2 {
			t.Fatalf("Settings did not publish a newer coherent pair: old=%d new=%d value=%d", oldGen, newGen, cfg.Performance.MaxParallelAgents)
		}
		// If the handler merely called Resize, its applier has not recorded G+1:
		// replaying an old read (or the same generation) could restore capacity.
		applier.ApplyConfiguredCapacity(oldGen, oldValue)
		applier.ApplyConfiguredCapacity(newGen, 8)
		if got := te.DispatchSemaCap(); got != 2 {
			t.Errorf("Settings did not record its applied generation: stale/equal replay restored %d, want 2", got)
		}
	}
	if _, retired := any(te).(interface{ ResizeDispatchSema(int) }); retired {
		t.Error("CAP-B12 RED: retired unversioned ResizeDispatchSema is still callable")
	}
}

func TestCapRED_FastUpsert_RealErrorFallsBackToFullReload(t *testing.T) {
	for _, failRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload_succeeds", true: "reload_read_fails"}[failRead], func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			cfg, err := api.agentLoop.GetConfig().Clone()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Performance.MaxParallelAgents = 2
			cfg.Planning.GlobalActiveLoopCap = 4
			if err := config.SaveConfig(api.configPath(), cfg); err != nil {
				t.Fatal(err)
			}
			reloads := 0
			api.agentLoop.SetReloadFunc(func() error {
				reloads++
				read, err := config.LoadConfig(api.configPath())
				if err != nil {
					return err
				}
				return api.agentLoop.ReloadProviderAndConfig(context.Background(), &restMockProvider{}, read)
			})
			if failRead {
				// Preserve and inspect before replacing this test-owned file with
				// a directory, an actual filesystem error even for a root process.
				bytes, err := os.ReadFile(api.configPath())
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(api.configPath()); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(api.configPath(), 0o700); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := os.Remove(api.configPath()); err != nil {
						t.Error(err)
					}
					if err := os.WriteFile(api.configPath(), bytes, 0o600); err != nil {
						t.Error(err)
					}
				}()
			}
			// Absent requested agent forces the REAL upsert to reject its input.
			// testForceFastUpsertErr is deliberately not used: neither the upsert
			// implementation nor full reload is replaced with a canned result.
			warning := api.fastAgentUpsert("cap-no-such-agent")
			if reloads != 1 {
				t.Errorf("real upsert error must trigger one full reload, got %d", reloads)
			}
			if failRead {
				if !strings.HasPrefix(warning, "config reload failed:") {
					t.Errorf("actual reload read failure hidden: %q", warning)
				}
			} else {
				if warning != "" {
					t.Errorf("successful full reload reported warning: %q", warning)
				}
				live := api.agentLoop.GetConfig()
				if live.Performance.MaxParallelAgents != 2 || live.Planning.GlobalActiveLoopCap != 4 {
					t.Errorf("full reload did not apply saved capacities: agent=%d planning=%d", live.Performance.MaxParallelAgents, live.Planning.GlobalActiveLoopCap)
				}
			}
		})
	}
}
