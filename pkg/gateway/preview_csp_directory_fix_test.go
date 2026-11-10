// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — Mode 2 CSP directory-source controls (review finding F2;
// spec docs/internal/specs/adr-094-preview-isolation-spec.md "Mode 2
// response headers": ${PREFIX} = /preview/{agent}/{token}/, and child
// modules, stylesheets and fetch('./data.json') must work).
//
// Test plan (elicify-test-writing):
//
//   - Behaviour under test: every origin+prefix source of the served Mode 2
//     CSP admits the preview's own child resources and nothing outside the
//     preview directory.
//   - Oracle: the W3C CSP Level 3 "path-part matching" algorithm
//     (https://www.w3.org/TR/CSP3/#match-paths), transcribed below
//     independently of production: a source path without a trailing "/" is
//     an EXACT match; with one it is a directory-prefix match, compared
//     segment by segment after percent-decoding.
//   - Unit boundary: real served responses — a static registration through
//     the real HandlePreview, and a dev registration through the real proxy
//     on the production middleware chain.
//   - Positive controls: stylesheet, nested module script, relative data
//     fetch and a WebSocket under the prefix. Negative controls: gateway API,
//     a sibling token that extends the token string, another agent's prefix,
//     a foreign origin. (Per CSP3 a directory source also matches its own
//     bare path — the same registration — so that is not a negative control.)
//   - Mutation: dropping the trailing "/" in buildPreviewCSP turns every
//     positive control red (exact-match source).

package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cspPathSplit splits a URL path the way CSP3 does ("strictly split on /").
func cspPathSplit(p string) []string {
	return strings.Split(p, "/")
}

// cspSchemeMatches implements the CSP3 scheme-part rule for the cases used
// here: same scheme, or the secure upgrade (http→https, ws→wss).
func cspSchemeMatches(src, target string) bool {
	if src == target {
		return true
	}
	return (src == "http" && target == "https") || (src == "ws" && target == "wss")
}

// cspSourceMatchesURL reports whether a host-source expression (scheme +
// host + optional port + path) matches target per CSP3 §6.7.2.8/§6.7.2.12.
func cspSourceMatchesURL(t *testing.T, source, target string) bool {
	t.Helper()
	s, err := url.Parse(source)
	require.NoError(t, err, "source %q", source)
	u, err := url.Parse(target)
	require.NoError(t, err, "target %q", target)
	if !cspSchemeMatches(s.Scheme, u.Scheme) || !strings.EqualFold(s.Hostname(), u.Hostname()) || s.Port() != u.Port() {
		return false
	}
	srcPath := s.EscapedPath()
	urlPath := u.EscapedPath()
	if srcPath == "" {
		return true
	}
	if srcPath == "/" && urlPath == "" {
		return true
	}
	exact := !strings.HasSuffix(srcPath, "/")
	pathList := cspPathSplit(srcPath)
	urlList := cspPathSplit(urlPath)
	if exact && len(pathList) != len(urlList) {
		return false
	}
	if !exact {
		pathList = pathList[:len(pathList)-1] // the final empty item
	}
	if len(pathList) > len(urlList) {
		return false
	}
	for i, piece := range pathList {
		a, errA := url.PathUnescape(piece)
		b, errB := url.PathUnescape(urlList[i])
		if errA != nil || errB != nil || a != b {
			return false
		}
	}
	return true
}

// cspOriginPrefixSource returns the directive's single origin+path source
// (the one that is not a keyword or a scheme-only source).
func cspOriginPrefixSource(t *testing.T, policy, directive, scheme string) string {
	t.Helper()
	for _, src := range cspDirectiveSources(t, policy, directive) {
		if strings.HasPrefix(src, scheme+"://") {
			return src
		}
	}
	t.Fatalf("directive %q carries no %s:// source in %q", directive, scheme, policy)
	return ""
}

// cspAssertDirectoryControls asserts the positive and negative controls for
// the policy served at origin under prefix (no trailing slash).
func cspAssertDirectoryControls(t *testing.T, policy, origin, wsOrigin, prefix string) {
	t.Helper()
	positives := map[string]string{
		"style-src":   origin + prefix + "/style.css",
		"script-src":  origin + prefix + "/assets/app.module.js",
		"img-src":     origin + prefix + "/img/logo.png",
		"font-src":    origin + prefix + "/fonts/preview.woff2",
		"media-src":   origin + prefix + "/clip.mp4",
		"connect-src": origin + prefix + "/data.json",
		"form-action": origin + prefix + "/submit",
		"worker-src":  origin + prefix + "/worker.js",
	}
	for directive, child := range positives {
		src := cspOriginPrefixSource(t, policy, directive, "http")
		assert.True(t, cspSourceMatchesURL(t, src, child),
			"F2 (spec ${PREFIX} = /preview/{agent}/{token}/): %s source %q must admit the preview's own child %q",
			directive, src, child)
		assert.True(t, strings.HasSuffix(src, "/"),
			"F2: %s source %q must be a directory source (trailing slash, CSP3 path matching)", directive, src)

		for _, outside := range []string{
			origin + "/api/v1/config",
			origin + prefix + "x/app.js", // sibling token extending the token string
			origin + "/preview/other-agent/tok/app.js",
			"http://evil.example" + prefix + "/app.js",
		} {
			assert.False(t, cspSourceMatchesURL(t, src, outside),
				"%s source %q must NOT admit out-of-prefix %q", directive, src, outside)
		}
	}

	wsSrc := cspOriginPrefixSource(t, policy, "connect-src", "ws")
	assert.True(t, cspSourceMatchesURL(t, wsSrc, wsOrigin+prefix+"/socket"),
		"F2: the ws connect-src source %q must admit the preview's own WebSocket", wsSrc)
	assert.False(t, cspSourceMatchesURL(t, wsSrc, wsOrigin+"/api/v1/ws"),
		"the ws connect-src source %q must NOT admit the gateway WebSocket", wsSrc)
}

// TestPreviewCSPDirectory_StaticChildAssets: a real static Mode 2 response.
func TestPreviewCSPDirectory_StaticChildAssets(t *testing.T) {
	api, ss := newServeWebTestAPI(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte(`<!doctype html><link rel="stylesheet" href="style.css">`), 0o644))
	token, _, err := ss.Register("csp-dir-agent", dir, time.Hour)
	require.NoError(t, err)

	prefix := "/preview/csp-dir-agent/" + token
	rec := getFromAPI(api, prefix+"/")
	require.Equal(t, http.StatusOK, rec.Code)
	cspAssertDirectoryControls(t, rec.Header().Get("Content-Security-Policy"),
		"http://127.0.0.1:8080", "ws://127.0.0.1:8080", prefix)
}

// TestPreviewCSPDirectory_DevProxiedChildAssets: a real proxied Mode 2
// response from a dev registration.
func TestPreviewCSPDirectory_DevProxiedChildAssets(t *testing.T) {
	h := lbNewHarness(t)
	prefix := "/preview/" + lbAgent + "/" + h.devToken
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+prefix+"/", nil)
	require.NoError(t, err)
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	cspAssertDirectoryControls(t, resp.Header.Get("Content-Security-Policy"),
		"http://localhost:"+h.port, "ws://localhost:"+h.port, prefix)
}

// TestPreviewCSPDirectory_OracleSelfCheck pins the transcribed CSP3 matcher
// on the two rules F2 depends on, so a broken oracle cannot pass vacuously.
func TestPreviewCSPDirectory_OracleSelfCheck(t *testing.T) {
	assert.False(t, cspSourceMatchesURL(t, "http://h/preview/a/t", "http://h/preview/a/t/app.js"),
		"CSP3: a source path without a trailing slash is an exact match")
	assert.True(t, cspSourceMatchesURL(t, "http://h/preview/a/t", "http://h/preview/a/t"),
		"CSP3: an exact source matches its own path")
	assert.True(t, cspSourceMatchesURL(t, "http://h/preview/a/t/", "http://h/preview/a/t/app.js"),
		"CSP3: a trailing-slash source matches children")
	assert.False(t, cspSourceMatchesURL(t, "http://h/preview/a/t/", "http://h/preview/a/tx/app.js"),
		"CSP3: matching is segment-wise, not string-prefix")
}
