// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/cron"
)

// Oracle: session-core FR-017 — no user-facing session-mode chooser; the mode
// is derived: run_isolated forces ISOLATED for either role, an eligible owner
// main means MAIN (MAIN beats CONTINUE), a recurring non-main owner CONTINUEs,
// anything else is ISOLATED. Expected values come from the spec text.
func TestDeriveScheduleSessionMode_Table(t *testing.T) {
	cases := []struct {
		name                                string
		runIsolated, ownerMainOK, recurring bool
		want                                cron.SessionMode
	}{
		{"run_isolated beats an eligible main", true, true, true, cron.SessionModeIsolated},
		{"run_isolated on a recurring non-main", true, false, true, cron.SessionModeIsolated},
		{"eligible main beats continue", false, true, true, cron.SessionModeMain},
		{"eligible main, one-time", false, true, false, cron.SessionModeMain},
		{"recurring non-main continues", false, false, true, cron.SessionModeContinue},
		{"one-time non-main is isolated", false, false, false, cron.SessionModeIsolated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, deriveScheduleSessionMode(tc.runIsolated, tc.ownerMainOK, tc.recurring))
		})
	}
}

func postSchedule(t *testing.T, api *restAPI, body gen.ScheduleCreate) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	r := withUser(httptest.NewRequest(http.MethodPost, "/api/v1/schedules", bytes.NewBuffer(raw)), "alice")
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return w, out
}

// Oracle: FR-017 through the real create path — the wire carries run_isolated
// and never a session_mode; the stored mode is the derived one.
func TestSchedulesAPI_Create_DerivesModeAndHasNoModeOnTheWire(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	every := int64(60000)
	newBody := func(isolated *bool) gen.ScheduleCreate {
		b := gen.ScheduleCreate{Name: "n", Message: "m", OwnerAgentId: "mia", RunIsolated: isolated}
		b.Trigger.Kind = "every"
		b.Trigger.EveryMs = &every
		return b
	}

	_, plain := postSchedule(t, api, newBody(nil))
	_, hasMode := plain["session_mode"]
	assert.False(t, hasMode, "the wire must not carry a session_mode (FR-017)")
	_, hasIso := plain["run_isolated"]
	assert.False(t, hasIso, "run_isolated is omitted when false")

	yes := true
	_, isolated := postSchedule(t, api, newBody(&yes))
	assert.Equal(t, true, isolated["run_isolated"])

	jobs := cs.ListJobs(true)
	require.Len(t, jobs, 2)
	modes := map[bool]cron.SessionMode{}
	for _, j := range jobs {
		modes[j.RunIsolated] = j.SessionMode
	}
	assert.Equal(t, cron.SessionModeContinue, modes[false], "recurring owner without an eligible main continues")
	assert.Equal(t, cron.SessionModeIsolated, modes[true], "run_isolated forces isolated")
}
