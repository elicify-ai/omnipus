// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// #1516 F2: fetch_url honours the operator's SSRF allow list
// (sandbox.ssrf.allow_internal), host names included, while keeping its
// always-on block for everything not listed. The tool's own
// tools.web.private_host_whitelist stays IP/CIDR-only.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newIssue1516Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("issue1516-ok"))
	}))
	t.Cleanup(srv.Close)
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Fatalf("fixture: expected server on 127.0.0.1, got %s", srv.URL)
	}
	return srv
}

func TestWebFetch_OperatorAllowList_HostName(t *testing.T) {
	srv := newIssue1516Server(t)
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	tool, err := NewWebFetchToolWithAllowLists(50000, "", format, testFetchLimit, nil, []string{"localhost"})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	res := tool.Execute(context.Background(), map[string]any{"url": "http://localhost:" + port + "/"})
	if res.IsError {
		t.Fatalf("operator-listed host name localhost must be fetchable, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "issue1516-ok") {
		t.Fatalf("expected body, got: %s", res.ForLLM)
	}
}

func TestWebFetch_OperatorAllowList_IP(t *testing.T) {
	srv := newIssue1516Server(t)

	tool, err := NewWebFetchToolWithAllowLists(50000, "", format, testFetchLimit, nil, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	res := tool.Execute(context.Background(), map[string]any{"url": srv.URL + "/"})
	if res.IsError {
		t.Fatalf("operator-listed 127.0.0.1 must be fetchable, got: %s", res.ForLLM)
	}
}

func TestWebFetch_OperatorAllowList_NotListedStaysBlocked(t *testing.T) {
	srv := newIssue1516Server(t)
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	tool, err := NewWebFetchToolWithAllowLists(50000, "", format, testFetchLimit, nil, []string{"127.0.0.2"})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	for _, u := range []string{srv.URL + "/", "http://localhost:" + port + "/"} {
		res := tool.Execute(context.Background(), map[string]any{"url": u})
		if !res.IsError {
			t.Fatalf("%s is not on the operator list and must stay refused, got: %s", u, res.ForLLM)
		}
	}
}

// The tool's own private_host_whitelist keeps rejecting host names: the
// operator list does not loosen that validation.
func TestWebFetch_PrivateHostWhitelist_StillRejectsHostNames(t *testing.T) {
	if _, err := NewWebFetchToolWithAllowLists(50000, "", format, testFetchLimit, []string{"localhost"}, nil); err == nil {
		t.Fatal("private_host_whitelist must still reject host names")
	}
}
