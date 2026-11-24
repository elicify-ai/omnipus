package gateway

// Spec rev8 FR009/028, S45/S81: alternate generic writers cannot widen Programs.
// Real authenticated router, live config and persisted file; no guard mock.
import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func pgConfigRegressionFixture(t *testing.T) *pgGateFixture {
	t.Helper()
	f := pgBoundChannel(t)
	// Gateway boot, not registerAdditionalEndpoints, registers generic config.
	// Add its actual withAuth/HandleConfig route and full middleware unchanged.
	base := f.handler
	mux := http.NewServeMux()
	mux.Handle("/api/v1/config", f.api.withAuth(f.api.HandleConfig))
	configHandler := buildProductionMiddlewareChain(f.api, mux)
	f.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/config" {
			configHandler.ServeHTTP(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
	return f
}

func pgRegressionObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "instrument: persisted config member is a JSON object")
	return object
}

func TestProgramsConfig_GenericWriterRegression(t *testing.T) {
	for _, row := range []struct {
		name string
		body func(*testing.T, map[string]any) map[string]any
	}{
		{"F1_bearer_cannot_enable_web", func(_ *testing.T, _ map[string]any) map[string]any {
			return map[string]any{"programs": map[string]any{"web_chat_allowed": true}}
		}},
		{"F1_bearer_cannot_replace_whole_channel_to_enable", func(t *testing.T, doc map[string]any) map[string]any {
			channels := pgRegressionObject(t, doc["channels"])
			channel := pgRegressionObject(t, channels["telegram"])
			channel["programs_allowed"] = true
			return map[string]any{"channels": channels}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := pgConfigRegressionFixture(t)
			// Positive instrument: this same bearer passes real auth and may save an
			// unrelated setting. A generic auth failure cannot explain a refusal.
			control, _ := pgServe(t, f, http.MethodPut, "/api/v1/config", []byte(`{"tools":{"filter_min_length":37}}`), f.token, false, false)
			require.Equal(t, http.StatusOK, control.Code, "valid unrelated bearer write: %s", control.Body.String())
			before, err := os.ReadFile(f.api.configPath())
			require.NoError(t, err)
			var doc map[string]any
			require.NoError(t, json.Unmarshal(before, &doc))
			require.Equal(t, float64(37), pgRegressionObject(t, doc["tools"])["filter_min_length"])
			require.Equal(t, false, pgChannelSetting(t, f)["allowed"])
			body, err := json.Marshal(row.body(t, doc))
			require.NoError(t, err)
			refused, _ := pgServe(t, f, http.MethodPut, "/api/v1/config", body, f.token, false, false)
			after, err := os.ReadFile(f.api.configPath())
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, refused.Code, "FR009/028: bearer cannot widen Programs through an alternate writer")
			require.Equal(t, before, after, "a refused generic write must persist nothing")
			require.Equal(t, false, pgChannelSetting(t, f)["allowed"])
		})
	}
	t.Run("F1_whole_channel_rebind_clears_permission", func(t *testing.T) {
		f := pgConfigRegressionFixture(t)
		pgEnableChannelPrograms(t, f)
		raw, err := os.ReadFile(f.api.configPath())
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		channel := pgRegressionObject(t, pgRegressionObject(t, doc["channels"])["telegram"])
		require.Equal(t, true, channel["programs_allowed"], "instrument: real switch is on before this rebind")
		pgRegressionObject(t, channel["identity"])["id"] = "ray"
		body, err := json.Marshal(map[string]any{"channels": map[string]any{"telegram": channel}})
		require.NoError(t, err)
		updated, _ := pgServe(t, f, http.MethodPut, "/api/v1/config", body, f.token, false, false)
		require.Equal(t, http.StatusOK, updated.Code, "ordinary rebind remains available: %s", updated.Body.String())
		raw, err = os.ReadFile(f.api.configPath())
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &doc))
		channel = pgRegressionObject(t, pgRegressionObject(t, doc["channels"])["telegram"])
		require.Equal(t, "ray", pgRegressionObject(t, channel["identity"])["id"], "instrument: actual persisted new main agent")
		allowed, exists := channel["programs_allowed"]
		require.True(t, !exists || allowed == false, "S45: the same write must clear Programs, never carry authority to the new agent")
		require.Equal(t, false, pgChannelSetting(t, f)["allowed"])
	})
}
