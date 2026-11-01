package gateway

// Oracle: r17 §7.5 AG-04 and scenarios 12.1–12.2. Real REST handler
// and protected store. No native-agent SOUL requirement is invented for an
// external worker, and no forbidden SOUL is added to make the fixture pass.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestExternalCreate_SoulContract(t *testing.T) {
	for _, withSoul := range []bool{false, true} {
		t.Run(map[bool]string{false: "12.2 no SOUL accepted", true: "12.1 SOUL refused"}[withSoul], func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			pattern := filepath.Join(api.homePath, "entities", "agents", "*.json")
			before, err := filepath.Glob(pattern)
			require.NoError(t, err)
			body := map[string]any{"type": "subagent_3p", "name": "T8 external worker", "description": "external create parity", "executor": map[string]any{"cli": "codex"}}
			if withSoul {
				body["soul"] = "SECRET-PERSONA"
			}
			data, err := json.Marshal(body)
			require.NoError(t, err)
			w := httptest.NewRecorder()
			api.HandleAgents(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(data)))
			if withSoul {
				require.Equal(t, http.StatusBadRequest, w.Code, "12.1: %s", w.Body.String())
				assert.Contains(t, w.Body.String(), "soul")
				after, err := filepath.Glob(pattern)
				require.NoError(t, err)
				assert.Equal(t, before, after, "a refused create leaves the protected agent inventory unchanged")
				return
			}
			require.Equal(t, http.StatusCreated, w.Code, "12.2 external worker must not require SOUL: %s", w.Body.String())
			var created map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
			assert.Equal(t, "subagent_3p", created["type"])
			assert.NotContains(t, created, "soul")
		})
	}
}
