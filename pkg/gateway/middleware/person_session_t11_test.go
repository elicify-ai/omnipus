// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T11 slice A, RequirePersonSession (slim spec §3.2, §8 FR-4).
//
// Direct matrix (person / CLI token / bypass / no user). The route-level
// proof is pkg/gateway.TestCliValidate_NonPersonRefusedZeroSpawn. The
// assertions below are the spec.
package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// loadRequirePersonSession is the direct call. The matrix below is the spec.
func loadRequirePersonSession(t *testing.T) func(http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return RequirePersonSession
}

type t11BodyCounter struct {
	r *bytes.Reader
	n int
}

func (c *t11BodyCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func t11CallGate(t *testing.T, gate func(http.HandlerFunc) http.HandlerFunc, ctx context.Context) (code, reads int, nextCalled bool) {
	t.Helper()
	body := &t11BodyCounter{r: bytes.NewReader([]byte(`{"cli":"claude-code","cli_path":"/usr/bin/true"}`))}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-validate", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)
	called := false
	h := gate(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	h(w, req)
	return w.Code, body.n, called
}

func t11Ctx(bypass bool, user *config.UserConfig, cliToken bool) context.Context {
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = bypass
	ctx := context.WithValue(context.Background(), ctxkey.ConfigContextKey{}, cfg)
	if user != nil {
		ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, user)
	}
	if cliToken {
		ctx = context.WithValue(ctx, ctxkey.CLITokenContextKey{}, true)
	}
	return ctx
}

// TestPersonSessionMiddleware_Matrix is FR-4 / full spec T-A43.
// 403, body unread, next not called: development-bypass, CLI token, no user.
// A signed-in person with bypass off and no CLI token reaches next.
func TestPersonSessionMiddleware_Matrix(t *testing.T) {
	gate := loadRequirePersonSession(t)
	person := &config.UserConfig{Username: "ada"}

	t.Run("development bypass is 403 before the body", func(t *testing.T) {
		code, reads, called := t11CallGate(t, gate, t11Ctx(true, nil, false))
		if code != http.StatusForbidden || reads != 0 || called {
			t.Fatalf("spec §3.2: bypass must be 403 before the body and before next; code=%d reads=%d next=%v", code, reads, called)
		}
	})

	t.Run("bypass wins even when a user is also present", func(t *testing.T) {
		code, reads, called := t11CallGate(t, gate, t11Ctx(true, person, false))
		if code != http.StatusForbidden || reads != 0 || called {
			t.Fatalf("spec §3.2: a development-bypass request is refused even if a user is attached; code=%d reads=%d next=%v", code, reads, called)
		}
	})

	t.Run("cli token is 403 before the body", func(t *testing.T) {
		// The CLI-token branch also attaches a synthetic user. The key, not the
		// absence of a user, is what the spec refuses.
		code, reads, called := t11CallGate(t, gate, t11Ctx(false, &config.UserConfig{Username: "cli"}, true))
		if code != http.StatusForbidden || reads != 0 || called {
			t.Fatalf("spec §3.2: CLITokenContextKey must be 403 before the body; code=%d reads=%d next=%v", code, reads, called)
		}
	})

	t.Run("no user is 403 before the body", func(t *testing.T) {
		code, reads, called := t11CallGate(t, gate, t11Ctx(false, nil, false))
		if code != http.StatusForbidden || reads != 0 || called {
			t.Fatalf("spec §3.2: a request with no user must be 403 before the body; code=%d reads=%d next=%v", code, reads, called)
		}
	})

	t.Run("signed-in person reaches the handler", func(t *testing.T) {
		code, _, called := t11CallGate(t, gate, t11Ctx(false, person, false))
		if !called || code != http.StatusNoContent {
			t.Fatalf("spec §3.2: a signed-in person is allowed through; code=%d next=%v", code, called)
		}
	})
}
