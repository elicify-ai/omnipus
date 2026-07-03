//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestHandleUserChangeRole_ConcurrentDemotion_OneWins used to prove that the
// last-admin guard in HandleUserChangeRole is evaluated INSIDE the
// safeUpdateConfigJSON callback (post-configMu-acquire), not against a
// pre-lock snapshot, by racing two concurrent demotions that would otherwise
// leave the deployment admin-less.
//
// Single-user model (operator directive, 2026-07): a role-change request to
// "user" is now always a no-op with respect to actual authority —
// rest_users.go HandleUserChangeRole always persists "admin" regardless of
// body.Role (see TestHandleUserChangeRole_AdminToUser /
// TestHandleUserChangeRole_LastAdminDemotion409). Neither concurrent
// "demotion" in this scenario can ever actually remove an admin anymore, so
// the ErrLastAdmin race this test exercised can no longer occur via this
// handler — countAdmins can never drop below its starting value as a result
// of HandleUserChangeRole. This test is deliberately flipped (not deleted)
// to prove the new outcome: BOTH concurrent PATCH requests succeed (200),
// and BOTH alice and bob remain admin on disk afterward — no data
// corruption, no spurious conflict.
//
// Invariant across all 100 iterations:
//
//	success200 == 200  (both requests succeed every iteration)
//	adminCount == 2    (both users remain admin on disk every iteration)
func TestHandleUserChangeRole_ConcurrentDemotion_OneWins(t *testing.T) {
	const iterations = 100

	var success200 int

	for i := 0; i < iterations; i++ {
		// Per-iteration fresh harness so each run starts from a clean
		// two-admin config on disk.
		hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
		require.NoError(t, err)
		users := []any{
			map[string]any{"username": "alice", "password_hash": string(hash), "token_hash": "", "role": "admin"},
			map[string]any{"username": "bob", "password_hash": string(hash), "token_hash": "", "role": "admin"},
		}
		api, _ := newUserMgmtAPI(t, users)

		codes := make(chan int, 2)
		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine A: request to demote alice to user (now a no-op).
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := adminRequest(http.MethodPatch, "/api/v1/users/alice/role", `{"role":"user"}`)
			api.HandleUserChangeRole(w, r)
			codes <- w.Code
		}()

		// Goroutine B: request to demote bob to user (now a no-op).
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := adminRequest(http.MethodPatch, "/api/v1/users/bob/role", `{"role":"user"}`)
			api.HandleUserChangeRole(w, r)
			codes <- w.Code
		}()

		wg.Wait()
		close(codes)

		codeA := <-codes
		codeB := <-codes

		got200 := 0
		for _, c := range []int{codeA, codeB} {
			if c == http.StatusOK {
				got200++
			}
		}

		if got200 != 2 {
			t.Fatalf("iteration %d: expected both requests to succeed (200), got codes %d and %d",
				i+1, codeA, codeB)
		}

		// On-disk config must still have both admins — single-user model
		// means the "demotion" request never actually demotes anyone.
		disk := readDiskUsers(t, api)
		adminCount := 0
		for _, u := range disk {
			if u["role"] == "admin" {
				adminCount++
			}
		}
		if adminCount != 2 {
			t.Fatalf("iteration %d: expected both admins to remain admin on disk (no-op demotion), got %d (users: %+v)",
				i+1, adminCount, disk)
		}

		success200 += got200
	}

	// Aggregate assertion: every iteration must have contributed two
	// successes. Deviations would have caused t.Fatalf above, but this final
	// check documents the invariant explicitly.
	require.Equal(t, iterations*2, success200, "success200 count must equal 2x iteration count")

	t.Logf("ConcurrentDemotion: success200=%d (all %d iterations correct, no-op under single-user model)",
		success200, iterations)
}

// TestHandleUserChangeRole_ConcurrentDemotion_ConflictBodyMessage used to
// verify that a 409 response body from the concurrent-demotion race carried
// the canonical ErrLastAdmin message. Single-user model (operator directive,
// 2026-07): since a role-change request to "user" now always persists
// "admin" (no actual demotion occurs — see
// TestHandleUserChangeRole_ConcurrentDemotion_OneWins above), the
// ErrLastAdmin/countAdmins guard this test exercised is unreachable dead
// code for HandleUserChangeRole; ErrLastAdmin itself is retained
// (config.json's countAdmins helper is still used by HandleUserDelete, where
// the guard remains live). This test is deliberately flipped (not deleted)
// to prove the new outcome: NEITHER concurrent request returns 409 — both
// succeed and both response bodies report role "admin".
func TestHandleUserChangeRole_ConcurrentDemotion_ConflictBodyMessage(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	require.NoError(t, err)
	users := []any{
		map[string]any{"username": "alice", "password_hash": string(hash), "token_hash": "", "role": "admin"},
		map[string]any{"username": "bob", "password_hash": string(hash), "token_hash": "", "role": "admin"},
	}
	api, _ := newUserMgmtAPI(t, users)

	codes := make(chan int, 2)
	bodies := make(chan string, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		api.HandleUserChangeRole(w, adminRequest(http.MethodPatch, "/api/v1/users/alice/role", `{"role":"user"}`))
		codes <- w.Code
		bodies <- w.Body.String()
	}()

	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		api.HandleUserChangeRole(w, adminRequest(http.MethodPatch, "/api/v1/users/bob/role", `{"role":"user"}`))
		codes <- w.Code
		bodies <- w.Body.String()
	}()

	wg.Wait()
	close(codes)
	close(bodies)

	var bodySlice []string
	for b := range bodies {
		bodySlice = append(bodySlice, b)
	}

	var codeSlice []int
	for c := range codes {
		codeSlice = append(codeSlice, c)
	}

	for i, c := range codeSlice {
		require.Equal(t, http.StatusOK, c, "single-user model: no-op demotion must always succeed; body: %s", bodySlice[i])
		var resp map[string]any
		require.NoError(t, json.Unmarshal([]byte(bodySlice[i]), &resp),
			"200 body must be valid JSON: %s", bodySlice[i])
		assert.Equal(t, "admin", resp["role"], "single-user model: role must remain admin; body: %s", bodySlice[i])
	}
}
