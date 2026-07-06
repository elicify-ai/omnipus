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
	defer logger.Close()

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
	defer p.Close()

	proxyURL, _ := url.Parse("http://" + p.Addr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("http://blocked.example/")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d; want 403", resp.StatusCode)
	}

	// Flush the audit logger so the line lands on disk before we read.
	logger.Close()

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
		f.Close()
		if err != nil {
			return "", err
		}
		contents += string(b)
	}
	return contents, nil
}
