package gateway

// TestMain registers the real gateway.RunContext into pkg/agent/testutil so
// that StartTestGateway can boot the full gateway without creating an import
// cycle (testutil does not import pkg/gateway).

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// isolatedProcessHome is the throwaway user home this test binary runs under (set
// in TestMain). It exists so no test can resolve, read or write the developer's
// real ~/.omnipus: every "$HOME/.omnipus" fallback lands here, and TestMain fails
// the run if anything was created there.
var isolatedProcessHome *testutil.IsolatedHome

func TestMain(m *testing.M) {
	home, err := testutil.IsolateProcessHome()
	if err != nil {
		panic("pkg/gateway TestMain: " + err.Error())
	}
	isolatedProcessHome = home
	testutil.RegisterGatewayRunner(RunContext)
	// No pre-run sweep of leaked files (R3-QA-05): a lock a run leaves behind must
	// stay visible as the leak it is. The CI worker's persistent checkout is made
	// clean by runci.sh (`git clean -fdx` after checkout), which is where stale
	// files from an older revision belong.
	code := m.Run()
	// A test that never gave itself a home (or built its config before the home
	// was isolated) falls back to "$HOME/.omnipus". Under the real HOME that reads
	// and writes the developer's own data; here it lands in the throwaway home and
	// fails the run, naming what leaked.
	if leaks := home.Leaks(); len(leaks) > 0 {
		reportTestMain("GUARD FAILURE: gateway TestMain: a test used the user-home fallback ($HOME/.omnipus) instead of its own temp home; created:", strings.Join(leaks, ", "))
		if code == 0 {
			code = 1
		}
	}
	home.Remove()
	if leaked := leakedPackageDirFiles(); len(leaked) > 0 {
		reportTestMain("GUARD FAILURE: gateway TestMain: a test wrote into the package directory instead of its temp home:", strings.Join(leaked, ", "))
		if code == 0 {
			code = 1
		}
	}
	// Hygiene guard (2026-09-14, after a CI debug session where the real
	// failure text never reached the log): a test that silences the console
	// via logger.DisableConsole and never runs its restore func leaves EVERY
	// later test in this binary unable to log. Fail the package loudly rather
	// than letting the next failure hide its own evidence.
	if logger.ConsoleDisabled() {
		reportTestMain("GUARD FAILURE: gateway TestMain: console logging is still disabled after the test run — a test called logger.DisableConsole() without t.Cleanup on its restore func")
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// reportTestMain writes to stdout. println goes to fd 2, and a gateway boot
// permanently dup2s that fd onto the panic log (logger.initPanicFile). The
// returned cleanup closes the file and does not restore the fd, so a
// println here never reaches go test: the package exits 1 with no --- FAIL
// line and no message. stdout (fd 1) is not what that dup2 replaces.
func reportTestMain(parts ...string) {
	fmt.Fprintln(os.Stdout, strings.Join(parts, " "))
}
