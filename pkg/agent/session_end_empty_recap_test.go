// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_end_empty_recap_test.go — regression tests for persistResponse
// (session_end.go) when the recap model returns a JSON object that parses but
// carries no usable recap text. Before the fix the empty string was written
// verbatim to last-session.md, silently erasing the agent's continuity file.
//
// Expected behaviour (spec: the heuristic fallback path): a blank recap is a
// degraded recap — last-session.md receives the non-empty fallback recap
// (status line + carry-forward of recent user turns) and the retro is recorded
// with Fallback=true and the reason "empty_recap" in its recap body.
//
// session-core U12 port: this test originally drove the recap via
// CloseSession(sid, "explicit"). FR-036 / DEL-08 retired the explicit trigger
// (recap now fires only for idle/bootstrap/joined), so it is ported to the
// still-valid "idle" trigger with every assertion unchanged — spec
// "Regression protection": port safety assertions to the canonical
// replacement, never weaken them.

package agent

import (
	"strings"
	"testing"
	"time"
)

func TestPersistResponse_EmptyRecap_FallsBackToHeuristic(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty string recap", `{"recap":""}`},
		{"whitespace-only recap", "{\"recap\":\"  \\n\\t \"}"},
		{"no recap field", `{"went_well":["x"]}`},
		{"empty object", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &recapTransientProvider{successBody: tc.body}
			al, sessionID, ag := buildRecapTestLoop(t, provider)
			al.CloseSession(sessionID, "idle")

			memory := ag.ContextBuilder.Memory()
			if memory == nil {
				t.Fatal("agent has no memory store")
			}

			// The retro is appended after last-session.md, so wait for it.
			var retros []Retro
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				got, err := memory.ReadRetros(1)
				if err == nil && len(got) > 0 {
					retros = got
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(retros) != 1 {
				t.Fatalf("retros = %d, want exactly 1", len(retros))
			}
			r := retros[0]
			// The on-disk retro format persists only the fallback flag; the reason
			// travels inside the recap body.
			if !r.Fallback {
				t.Errorf("retro Fallback=false, want true")
			}
			if !strings.Contains(r.Recap, "Fallback reason: empty_recap.") {
				t.Errorf("retro recap missing fallback reason; recap:\n%s", r.Recap)
			}

			content := string(pollLastSession(t, ag, false))
			if strings.TrimSpace(content) == "" {
				t.Fatal("last-session.md is empty: continuity file was overwritten with nothing")
			}
			if !strings.Contains(content, "Fallback reason: empty_recap.") {
				t.Errorf("last-session.md missing fallback status line; content:\n%s", content)
			}
			if !strings.Contains(content, "transient retry test conversation") {
				t.Errorf("last-session.md missing carry-forward of recent user turn; content:\n%s", content)
			}
		})
	}
}
