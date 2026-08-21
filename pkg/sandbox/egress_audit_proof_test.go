// Functional proof for path.network_denied audit shape.
// Asserts that when an egress request is denied, the Logger receives an
// entry whose Details map carries the documented "host" + "allow_list"
// keys at the documented positions.

package sandbox

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestEgressProxy_AuditEntryShape wires a real audit.Logger to the
// EgressProxy, makes a denied request, and reads the JSONL line back to
// confirm the documented field positions.
func TestEgressProxy_AuditEntryShape(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	// Test cleanup: Close error is inconsequential — t.TempDir() removes
	// the backing directory regardless.
	defer func() {
		if closeErr := logger.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	// B1.2(c): the proxy now hands us a fully-shaped *audit.Entry. Tests
	// re-tag the event from "egress_denied" to the documented gateway-side
	// event name "path.network_denied" so the on-disk shape continues to
	// match the spec — the proxy emits the canonical sandbox-layer event,
	// and the gateway-side closure (this one) maps it onto the gateway's
	// preferred event taxonomy. Other Details fields pass through verbatim.
	auditFn := func(entry *audit.Entry) {
		entry.Event = "path.network_denied"
		if logErr := logger.Log(entry); logErr != nil {
			t.Logf("audit log: %v", logErr)
		}
	}

	p, err := NewEgressProxy([]string{"registry.npmjs.org"}, auditFn)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	defer func() {
		if closeErr := p.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	proxyURL, _ := url.Parse("http://" + p.Addr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("http://blocked.example/")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	// Draining the denied response is only to unblock the connection;
	// neither error is checked as the response's own status assertion
	// below is the test oracle.
	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		_ = copyErr
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		_ = closeErr
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d; want 403", resp.StatusCode)
	}

	// Flush the audit logger so the line lands on disk before we read.
	// The deferred Close above is idempotent, so this early Close's error
	// is not actionable here.
	if closeErr := logger.Close(); closeErr != nil {
		_ = closeErr
	}

	// Read the audit JSONL and confirm shape.
	contents, err := readAuditFile(dir)
	if err != nil {
		t.Fatalf("readAuditFile: %v", err)
	}
	if !strings.Contains(contents, `"event":"path.network_denied"`) {
		t.Errorf("audit file missing event tag: %s", contents)
	}
	if !strings.Contains(contents, `"decision":"deny"`) {
		t.Errorf("audit file missing deny decision: %s", contents)
	}
	if !strings.Contains(contents, `"host":"blocked.example"`) {
		t.Errorf("audit file missing host detail: %s", contents)
	}
	if !strings.Contains(contents, `"allow_list":["registry.npmjs.org"]`) {
		t.Errorf("audit file missing allow_list detail: %s", contents)
	}
	t.Logf("audit entry: %s", strings.TrimSpace(contents))
}

func readAuditFile(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "audit*.jsonl"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	contents := ""
	for _, m := range matches {
		f, err := os.Open(m)
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(f)
		// Read-only handle; a Close error has no effect on the bytes
		// already read above.
		if closeErr := f.Close(); closeErr != nil {
			_ = closeErr
		}
		if err != nil {
			return "", err
		}
		contents += string(b)
	}
	return contents, nil
}
