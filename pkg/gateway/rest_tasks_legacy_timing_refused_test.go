// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// session-core DEL-19 / C-TIMING: the legacy `every` trigger type and the
// `every_ms` / `cron_expr` config keys are removed. The server must refuse them
// with a 400 and a clear message — a 201 for a task that can never fire is a
// silent failure.
func TestRestTasks_LegacyTimingTriggersRefused(t *testing.T) {
	cases := []struct {
		name    string
		trigger string
		want    string
	}{
		{"every type", `{"type":"every","config":{"every_ms":60000}}`, "every"},
		{"recurring with cron_expr", `{"type":"recurring","config":{"cron_expr":"0 9 * * MON"}}`, "cron_expr"},
		{"cron_expr beside a valid rrule", `{"type":"recurring","config":{"cron_expr":"0 9 * * MON","rrule":"FREQ=WEEKLY","dtstart_ms":1784624400000,"tz":"UTC"}}`, "cron_expr"},
		{"every_ms on a once trigger", `{"type":"once","config":{"at_ms":4102444800000,"every_ms":60000}}`, "every_ms"},
	}
	for _, c := range cases {
		t.Run("create: "+c.name, func(t *testing.T) {
			api := newTestRestAPIAlignedStores(t)
			wsID := ensureTestWorkspace(t, api)
			setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
			body := fmt.Sprintf(`{"title":"Legacy","action":"llm","workspace_id":%q,"agent_id":"mia","trigger":%s,`+
				singleAttemptJSON+`,`+minimalCriteriaDodJSON+`}`, wsID, c.trigger)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/tasks"
			api.HandleTasks(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			require.Contains(t, w.Body.String(), c.want, "the error must name what is refused")
		})
	}

	t.Run("update: a PATCH cannot introduce a legacy trigger", func(t *testing.T) {
		api := newTestRestAPIAlignedStores(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
		body := fmt.Sprintf(`{"title":"Plain","action":"llm","workspace_id":%q,"agent_id":"mia",`+
			singleAttemptJSON+`,`+minimalCriteriaDodJSON+`}`, wsID)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "control create: body=%s", w.Body.String())
		var created gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
		id := created.Id
		for _, trig := range []string{
			`{"type":"every","config":{"every_ms":60000}}`,
			`{"type":"recurring","config":{"cron_expr":"0 9 * * MON"}}`,
		} {
			wp := patchTask(t, api, id, `{"trigger":`+trig+`}`)
			require.Equal(t, http.StatusBadRequest, wp.Code, "trigger %s: body=%s", trig, wp.Body.String())
		}
	})
}
