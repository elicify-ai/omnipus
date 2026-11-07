// /api/v1/notifications REST tests (#264, FR-012/013): notification list and
// read. The /api/v1/schedules tests that shared this file were removed with the
// API (T10 FR-12).

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/notifications"
)

func newSchedulesTestAPI(t *testing.T) (*restAPI, *cron.CronService) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCustom},
		{ID: "max", Type: config.AgentTypeCustom},
	}
	msgBus := bus.NewMessageBus()
	loop := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	cronPath := filepath.Join(t.TempDir(), "jobs.json")
	cs := cron.NewCronService(cronPath)
	require.NoError(t, cs.Start())
	t.Cleanup(cs.Stop)

	notifs := notifications.NewStore(t.TempDir())
	api := &restAPI{agentLoop: loop, notifStore: notifs}
	api.cronService.Store(cs)
	return api, cs
}

// withUser attaches an authenticated user to the request context.
func withUser(r *http.Request, username string) *http.Request {
	u := &config.UserConfig{Username: username}
	return r.WithContext(context.WithValue(r.Context(), UserContextKey{}, u))
}

// Note: ptr() helper is provided by rest_tasks.go as ptr[T any](v T) *T — use that.

// ---- notifications ----

func TestNotificationsAPI_ListAndRead(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	_, _ = api.notifStore.Create(notifications.Notification{
		Recipient: "alice", Type: notifications.TypeScheduleFailed, Title: "x",
		Severity: notifications.SeverityError, ScheduleID: "s1",
	})

	// List.
	rl := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil), "alice")
	wl := httptest.NewRecorder()
	api.HandleNotifications(wl, rl)
	require.Equal(t, http.StatusOK, wl.Code, wl.Body.String())

	var list gen.NotificationList
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &list))
	require.Len(t, list.Notifications, 1)
	assert.Equal(t, 1, list.UnreadCount)
	id := list.Notifications[0].Id

	// Mark read.
	rr := withUser(
		httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+id+"/read", nil),
		"alice",
	)
	wr := httptest.NewRecorder()
	api.HandleNotifications(wr, rr)
	assert.Equal(t, http.StatusNoContent, wr.Code)

	c, _ := api.notifStore.UnreadCount("alice")
	assert.Equal(t, 0, c)
}

func TestNotificationsAPI_ReadAll(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	_, _ = api.notifStore.Create(notifications.Notification{Recipient: "alice", ScheduleID: "a", Title: "1"})
	_, _ = api.notifStore.Create(notifications.Notification{Recipient: "alice", ScheduleID: "b", Title: "2"})

	rr := withUser(
		httptest.NewRequest(http.MethodPost, "/api/v1/notifications/read-all", nil),
		"alice",
	)
	wr := httptest.NewRecorder()
	api.HandleNotifications(wr, rr)
	assert.Equal(t, http.StatusNoContent, wr.Code)

	c, _ := api.notifStore.UnreadCount("alice")
	assert.Equal(t, 0, c)
}

func i64p(v int64) *int64 { return &v }
