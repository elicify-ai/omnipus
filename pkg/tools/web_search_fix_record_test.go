package tools

// web_search_fix_record_test.go — gate round 1 finding K5: ADR-096 D20's
// observability. (a) Every search call emits ONE structured record naming
// the resolved default, resolved fallback, the provider that answered, its
// role, the depth ask, and whether a hop/refusal/skip occurred (spec test
// 50: "each search emits one structured record naming provider, role and
// depth"). (b) A run of consecutive empty DuckDuckGo results warns (D20's
// consecutive-empty warning: "the cheap version of the block-page
// fingerprint"). Records are observed through logger.EnableFileLogging —
// the real sink — not a mock.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// captureSearchLogFile runs fn with logger file logging pointed at a fresh
// temp file (console silenced) and returns the file's contents.
func captureSearchLogFile(t *testing.T, fn func()) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("enable file logging: %v", err)
	}
	defer logger.DisableFileLogging()
	restore := logger.DisableConsole()
	defer restore()
	fn()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	return string(b)
}

// countLines counts lines containing substr.
func countLines(log, substr string) int {
	n := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// A successful default-path call emits the record: tavily answered as the
// default, the auto-DDG fallback resolved, no hop.
func TestFixK5_PerCallRecordDefaultSuccess(t *testing.T) {
	var log string
	f := newRolesSearchFixture(t, nil, nil)
	log = captureSearchLogFile(t, func() {
		res := f.run(map[string]any{"query": "golang"})
		if res.IsError {
			t.Fatalf("expected success: %s", res.ForLLM)
		}
	})
	if !strings.Contains(log, `"message":"web search call"`) {
		t.Fatalf("want the D20 per-call record, log:\n%s", log)
	}
	for _, want := range []string{
		`"default":"tavily"`,
		`"fallback":"duckduckgo"`,
		`"served":"tavily"`,
		`"role":"default"`,
		`"depth":""`,
		`"hop":false`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("record missing %s, log:\n%s", want, log)
		}
	}
}

// A hop records the fallback as the server, with the hop flag set and both
// roles on the record.
func TestFixK5_PerCallRecordHopNamesFallback(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	var log string
	log = captureSearchLogFile(t, func() {
		res := f.run(map[string]any{"query": "golang"})
		if res.IsError {
			t.Fatalf("expected the fallback to answer: %s", res.ForLLM)
		}
	})
	for _, want := range []string{
		`"message":"web search call"`,
		`"default":"tavily"`,
		`"fallback":"duckduckgo"`,
		`"served":"duckduckgo"`,
		`"role":"fallback"`,
		`"hop":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("record missing %s, log:\n%s", want, log)
		}
	}
}

// D20's consecutive-empty warning: after three DuckDuckGo runs in a row
// returning zero results, the warning appears (the threshold constant is a
// lane decision — the spec fixes the warning, not a number — documented in
// web_search.go where it is defined).
func TestFixK5_ConsecutiveEmptyDDGWarning(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
	}, nil)
	f.setHandler("ddg", jsonBody(`<html><body><p>no anchors here</p></body></html>`))
	var log string
	log = captureSearchLogFile(t, func() {
		for i := 0; i < 4; i++ {
			f.run(map[string]any{"query": "golang"})
		}
	})
	if got := countLines(log, "duckduckgo returned no results repeatedly"); got < 2 {
		t.Fatalf("want the consecutive-empty warning from run 3 onward (>=2 occurrences in 4 runs), got %d in:\n%s", got, log)
	}
}
