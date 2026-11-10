// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// "SF-2 (stale connection result) is fixed on the SERVER", 2026-10-01,
// and the team-lead's added removal case. Key mutation during a held check
// returns 409 with a fixed, non-secret error and no provider outcome; the
// unchanged-key control returns the normal 200 success outcome. The example
// conflict message in that ruling is illustrative, not a mandated literal.
// The registered routes, client, encrypted store, saves/removal and audit stay
// real. Only the external provider response is held at the HTTP boundary.
// GREEN and mutation proof belong to independent CHECK, not this RED author.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestSearchConnectionCheck_KeyMutationDuringHeldCallReturnsConflictWithNoChangeControl(t *testing.T) {
	const replacementKey = "replacement-key-held-check-do-not-expose"
	conflictMessages := make(map[string]string)
	for _, tc := range []struct {
		name, mutation string
	}{
		{"no_key_change_control", ""},
		{"key_replaced_through_real_save", `{"kind":"search","api_key":"replacement-key-held-check-do-not-expose"}`},
		{"key_removed_through_real_removal", removeSearchKeyJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			t.Cleanup(api.credStore.Close)
			// The design's total 15-second budget is a harness backstop, not
			// an elapsed-time oracle. Channels prove the required ordering.
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			t.Cleanup(cancel)
			started := make(chan []byte, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			var calls atomic.Int32
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				data, err := io.ReadAll(r.Body)
				if !assert.NoError(t, err, "the real provider request body must be consumed") {
					return
				}
				select {
				case started <- data:
				case <-r.Context().Done():
					return
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				// Decision #1105: a valid empty result is a successful check.
				_, err = io.WriteString(w, `{"results":[]}`)
				assert.NoError(t, err)
			}))
			t.Cleanup(edge.Close)
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
			require.Len(t, cfg.Gateway.Users, 1, "fixture must provide the real authenticated settings user")
			user := cfg.Gateway.Users[0]
			finished := make(chan *httptest.ResponseRecorder, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				finished <- postSearchCheck(ctx, mux, "tavily", `{}`, diagnosticBearer)
			}()
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					t.Error("check goroutine did not finish after provider release and cancellation")
				}
			})

			var requestData []byte
			select {
			case requestData = <-started:
			case early := <-finished:
				t.Fatalf("fixture never admitted the held old-key check: HTTP %d: %s", early.Code, early.Body.String())
			case <-ctx.Done():
				t.Fatalf("held check did not reach the provider within its design budget: %v", ctx.Err())
			}
			var providerRequest map[string]any
			require.NoError(t, json.Unmarshal(requestData, &providerRequest))
			require.Equal(t, searchSettingsSecret, providerRequest["api_key"], "the held real client call must be pinned to key A before mutation")
			require.Equal(t, int32(1), calls.Load())

			if tc.mutation != "" {
				token := reauthFor(t, api, &user)
				r := httptest.NewRequest(http.MethodPut, "/api/v1/integrations/providers/tavily", strings.NewReader(tc.mutation)).WithContext(ctx)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+diagnosticBearer)
				r.Header.Set("X-Reauth-Token", token)
				mutated := httptest.NewRecorder()
				mux.ServeHTTP(mutated, r)
				require.Equal(t, http.StatusOK, mutated.Code, "mutation must complete through the real authenticated save/removal route while the provider call is held; body=%s", mutated.Body.String())
				key, err := api.credStore.Get("TAVILY_API_KEY")
				if tc.name == "key_replaced_through_real_save" {
					require.NoError(t, err)
					require.Equal(t, replacementKey, key, "the real save route must actually persist key B")
				} else {
					var missing *credentials.NotFoundError
					require.ErrorAs(t, err, &missing, "the real removal route must actually delete key A")
					require.Equal(t, "TAVILY_API_KEY", missing.Name)
				}
				t.Logf("held key-A provider call; real %s mutation completed with HTTP %d", tc.name, mutated.Code)
			}
			select {
			case early := <-finished:
				t.Fatalf("check answered before the held provider response was released: HTTP %d: %s", early.Code, early.Body.String())
			default:
			}
			releaseOnce.Do(func() { close(release) })
			var response *httptest.ResponseRecorder
			select {
			case response = <-finished:
			case <-ctx.Done():
				t.Fatalf("released provider response did not finish the check: %v", ctx.Err())
			}
			assert.Equal(t, int32(1), calls.Load(), "no retry or rotation to the new key")
			if tc.mutation == "" {
				requireSearchCheckResult(t, response, "tavily", "success")
				return
			}

			assert.Equal(t, http.StatusConflict, response.Code, "an old-key outcome must be discarded after a completed key mutation; body=%s", response.Body.String())
			var failure gen.ErrorResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
			message := strings.TrimSpace(failure.Error)
			assert.NotEmpty(t, message, "stale completion must return the fixed, actionable error message, not a provider outcome")
			conflictMessages[tc.name] = message
			assert.NotContains(t, response.Body.String(), searchSettingsSecret)
			assert.NotContains(t, response.Body.String(), replacementKey)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
			for field := range fields {
				assert.Contains(t, []string{"error", "code", "field", "details"}, field, "stale check must use the existing ErrorResponse shape, never expose the provider outcome field %q", field)
			}
		})
	}
	require.Len(t, conflictMessages, 2, "both real mutation cases must reach the stale-result assertions")
	assert.Equal(t, conflictMessages["key_replaced_through_real_save"], conflictMessages["key_removed_through_real_removal"], "the conflict message must be fixed, not depend on the key or mutation")
}
