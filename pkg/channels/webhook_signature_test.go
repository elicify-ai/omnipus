// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package channels

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestWebhookHandlers_HaveSignatureVerification is the regression guard for
// issue #162.
//
// Every channel that implements WebhookHandler exposes a publicly reachable
// HTTP endpoint on the gateway's shared mux. Without signature verification
// on the request body, anyone who knows the path can inject fake messages
// claiming to come from the upstream platform — driving agent turns,
// spending LLM budget, or worse.
//
// The current set is line + googlechat; both correctly check an
// HMAC-keyed platform signature on the request body before processing.
// This test enforces the project-wide invariant: any new package under
// pkg/channels/*/ that declares a WebhookPath() method MUST also contain
// either a verifySignature function or an hmac.Equal call somewhere in
// the package. Failing this test loudly tells the author "you added a
// webhook channel without authenticating it" before the code ships.
func TestWebhookHandlers_HaveSignatureVerification(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	channelsRoot := filepath.Dir(thisFile)

	entries, err := os.ReadDir(channelsRoot)
	if err != nil {
		t.Fatalf("read channels dir: %v", err)
	}

	type finding struct {
		channel             string
		declaresWebhookPath bool
		hasSignatureChecker bool
		dir                 string
	}
	var findings []finding

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subDir := filepath.Join(channelsRoot, e.Name())
		hasPath, hasCheck, err := scanChannelPackage(subDir)
		if err != nil {
			t.Fatalf("scan %s: %v", e.Name(), err)
		}
		if hasPath {
			findings = append(findings, finding{
				channel:             e.Name(),
				declaresWebhookPath: true,
				hasSignatureChecker: hasCheck,
				dir:                 subDir,
			})
		}
	}

	if len(findings) == 0 {
		t.Fatal("scan found zero WebhookPath() declarations across pkg/channels/*" +
			" — did AST detection break? expected at least line + googlechat")
	}

	var missing []string
	var present []string
	for _, f := range findings {
		if !f.hasSignatureChecker {
			missing = append(missing, f.channel)
		} else {
			present = append(present, f.channel)
		}
	}
	sort.Strings(missing)
	sort.Strings(present)

	if len(missing) > 0 {
		t.Fatalf(
			"webhook channels missing signature verification"+
				" — found WebhookPath() declarations"+
				" but no verifySignature() function or hmac.Equal() call"+
				" in the same package:\n  %v\n\n"+
				"Every webhook handler MUST verify the platform-issued HMAC"+
				" signature on the request body BEFORE parsing or acting on"+
				" the payload. Compare:\n"+
				"  - pkg/channels/line/line.go:228"+
				" (LINE: X-Line-Signature + hmac-sha256)\n"+
				"  - pkg/channels/googlechat/googlechat.go:342"+
				" (Google Chat: Google-Signature + hmac-sha256)\n\n"+
				"Picked up channels: %v",
			missing, present,
		)
	}
}

// scanChannelPackage parses every *.go file in dir (skipping _test.go) and
// reports two things: (a) does the package declare a method named
// WebhookPath returning a string, and (b) does it have a function named
// verifySignature OR call hmac.Equal anywhere?
//
// The detection deliberately accepts either form so a future channel can
// inline the HMAC compare or factor it into a helper, as long as the
// security primitive is *somewhere* in the package.
func scanChannelPackage(dir string) (hasWebhookPath, hasSignatureChecker bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return false, false, parseErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if node.Name == nil {
					return true
				}
				if node.Name.Name == "WebhookPath" && node.Recv != nil {
					hasWebhookPath = true
				}
				if strings.EqualFold(node.Name.Name, "verifySignature") ||
					strings.EqualFold(node.Name.Name, "verifyWebhookSignature") {
					hasSignatureChecker = true
				}
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil {
					return true
				}
				if sel.Sel.Name == "Equal" {
					if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "hmac" {
						hasSignatureChecker = true
					}
				}
			}
			return true
		})
	}
	return hasWebhookPath, hasSignatureChecker, nil
}
