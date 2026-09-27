// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// D-13 security-review fix (2026-09-24): the D8 classifier now extracts the
// literal hosts a command names (founder decision B, point 1) and
// EvaluateNetworkPreflight only treats a command as Contained (no re-prompt)
// when every one of ITS hosts is already approved for the session — not
// merely "some network grant exists" (point 4's per-host scoping).
package tools

import "testing"

func TestExtractNetworkHosts(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"curl https URL", "curl -sI https://example.com", []string{"example.com"}},
		{"curl https URL with path", "curl https://example.com/api/v1?x=1", []string{"example.com"}},
		{"wget http URL", "wget http://example.com/file.tar.gz", []string{"example.com"}},
		{"two distinct URLs, de-duplicated", "curl https://a.example && curl https://a.example && curl https://b.example",
			[]string{"a.example", "b.example"}},
		{"ssh user@host", "ssh git@example.com", []string{"example.com"}},
		{"scp remote source", "scp user@example.com:/remote/file /tmp/local", []string{"example.com"}},
		{"rsync remote dest, local source ignored", "rsync -av ./local/ user@example.com:/remote/", []string{"example.com"}},
		{"blind network need — npm install has no literal host", "npm install", nil},
		{"blind network need — plain git push", "git push", nil},
		{"scp with two local paths has no host", "scp ./a ./b", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractNetworkHosts(tc.command)
			if len(got) != len(tc.want) {
				t.Fatalf("ExtractNetworkHosts(%q) = %v, want %v", tc.command, got, tc.want)
			}
			for i, h := range tc.want {
				if got[i] != h {
					t.Errorf("ExtractNetworkHosts(%q)[%d] = %q, want %q", tc.command, i, got[i], h)
				}
			}
		})
	}
}

func TestEvaluateNetworkPreflight_HostScoping(t *testing.T) {
	// R4-style setup: an unflagged command never escalates, host-aware or not.
	t.Run("not network-capable never escalates", func(t *testing.T) {
		v := EvaluateNetworkPreflight("ls -la", false, nil)
		if v.NeedsEscalation() {
			t.Error("a non-network command must never need escalation")
		}
	})

	// The literal example from the security review: an approved
	// "curl -sI https://example.com" must extract exactly ["example.com"].
	t.Run("first curl to example.com needs escalation and lists the host", func(t *testing.T) {
		v := EvaluateNetworkPreflight("curl -sI https://example.com", false, nil)
		if !v.NeedsEscalation() {
			t.Fatal("a fresh session with no grant must need escalation")
		}
		if len(v.Hosts) != 1 || v.Hosts[0] != "example.com" {
			t.Errorf("Hosts = %v, want [example.com]", v.Hosts)
		}
		if v.BlindHostNeed() {
			t.Error("a command with an extractable host must not report BlindHostNeed")
		}
	})

	// D-13's central fix: a session already holding the PORT-level grant
	// (granted=true) for a DIFFERENT host must still escalate for a NEW
	// host — "https://other.test is still refused" (approved only for
	// example.com).
	t.Run("session approved for example.com still escalates for other.test", func(t *testing.T) {
		v := EvaluateNetworkPreflight("curl https://other.test", true, []string{"example.com"})
		if !v.NeedsEscalation() {
			t.Fatal("a host outside the session's approved set must still need escalation, even with granted=true")
		}
		if len(v.Hosts) != 1 || v.Hosts[0] != "other.test" {
			t.Errorf("Hosts = %v, want [other.test]", v.Hosts)
		}
	})

	// Once example.com IS in the approved set, a repeat call runs silently.
	t.Run("repeat call to an already-approved host is contained", func(t *testing.T) {
		v := EvaluateNetworkPreflight("curl https://example.com", true, []string{"example.com"})
		if v.NeedsEscalation() {
			t.Error("a host already in the session's approved set must not re-escalate")
		}
		if !v.Contained {
			t.Error("Contained must be true")
		}
	})

	// A blind network need (no extractable host) keeps the ORIGINAL
	// ports-only behaviour: granted=true alone is enough, same as before
	// this fix (founder decision B, point 3).
	t.Run("blind network need falls back to ports-only gating", func(t *testing.T) {
		blind := EvaluateNetworkPreflight("npm install", false, nil)
		if !blind.NeedsEscalation() {
			t.Fatal("a blind network need with no grant must still escalate")
		}
		if !blind.BlindHostNeed() {
			t.Error("BlindHostNeed must be true when Flagged and Hosts is empty")
		}
		grantedBlind := EvaluateNetworkPreflight("npm install", true, nil)
		if grantedBlind.NeedsEscalation() {
			t.Error("a blind network need must be Contained once the port-level grant exists, unchanged from before D-13")
		}
	})
}
