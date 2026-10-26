//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seatbeltTrustdHelperFlag marks a re-exec of this test binary as the
// certificate-verification child rather than a normal test run.
const seatbeltTrustdHelperFlag = "--seatbelt-trustd-verify"

// seatbeltTrustdFixture is a real, public certificate chain (leaf, intermediate,
// cross-signed root) captured from api.github.com on 2026-10-26. It holds no
// private key. Verification is pinned to seatbeltTrustdVerifyTime, inside the
// leaf's validity window (2026-08-30 .. 2026-11-27), so the leaf's expiry does
// not age the test out.
var seatbeltTrustdFixture = filepath.Join("testdata", "seatbelt-trustd", "api-github-com-chain.crt")

var seatbeltTrustdVerifyTime = time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

// TestSeatbelt_RealChild_PlatformCertificateVerification is the regression test
// for Go programs (gh, and any other Go binary an agent runs) failing TLS inside
// the sandbox with "x509: OSStatus -26276" (errSecInternalComponent).
//
// On darwin, crypto/x509 verifies against the system roots by calling
// Security.framework (SecTrustEvaluateWithError), which hands the evaluation to
// the trust daemon over a Mach service. If the profile does not allow looking
// that service up, the evaluation fails with -26276 before any certificate is
// examined. Programs that read /private/etc/ssl/cert.pem themselves (curl,
// Python) never notice, which is why the gap went unseen.
//
// The child is this test binary re-executed under the REAL rendered profile
// (SeatbeltBackend.ApplyToCmd). It verifies the fixture chain against the
// system roots without opening any network connection of its own. It also
// proves the profile is active by failing to read a file the policy does not
// grant — without that, an unconfined child would pass trivially.
func TestSeatbelt_RealChild_PlatformCertificateVerification(t *testing.T) {
	if i := slices.Index(os.Args, seatbeltTrustdHelperFlag); i >= 0 {
		os.Exit(seatbeltTrustdHelperMain(os.Args[i+1:]))
	}

	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	exe, err := os.Executable()
	require.NoError(t, err)
	fixture, err := filepath.Abs(seatbeltTrustdFixture)
	require.NoError(t, err)
	require.FileExists(t, fixture)

	// A readable file outside the policy: the child must be DENIED this read,
	// which shows the verification below ran under an enforcing profile.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("not-granted"), 0o600))

	childArgv := func(outsideFile string) []string {
		return []string{
			exe, "-test.run=^TestSeatbelt_RealChild_PlatformCertificateVerification$", "--",
			seatbeltTrustdHelperFlag, fixture, outsideFile,
		}
	}

	// Control, unsandboxed: the fixture must verify on this host at all. If it
	// does not, the sandboxed result below would be meaningless, so stop with
	// the reason instead of reporting a sandbox defect. The control skips the
	// confinement probe ("-"): it is not confined, by design.
	controlArgv := childArgv("-")
	control := exec.Command(controlArgv[0], controlArgv[1:]...)
	controlOut, controlErr := control.CombinedOutput()
	require.NoError(t, controlErr,
		"unsandboxed control failed, so the fixture cannot judge the sandbox "+
			"(refresh %s if Apple no longer accepts it); output=%s", seatbeltTrustdFixture, controlOut)
	require.Contains(t, string(controlOut), "TRUSTD-VERIFY-OK",
		"unsandboxed control did not verify the fixture; output=%s", controlOut)

	policy := SandboxPolicy{
		FilesystemRules: []PathRule{
			{Path: filepath.Dir(exe), Access: AccessRead | AccessExecute},
			{Path: filepath.Dir(fixture), Access: AccessRead},
		},
	}
	argv := childArgv(outside)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = filepath.Dir(fixture)
	require.NoError(t, backend.ApplyToCmd(cmd, policy))
	require.Equal(t, seatbeltExecPath, cmd.Path, "child must be wrapped in sandbox-exec")

	out, err := cmd.CombinedOutput()
	text := string(out)
	require.NotContains(t, text, "-26276",
		"sandboxed Go program could not reach the trust daemon (errSecInternalComponent); output=%s", text)
	require.NoError(t, err, "sandboxed certificate verification failed; output=%s", text)
	require.Contains(t, text, "TRUSTD-CONFINED", "child was not confined by the profile; output=%s", text)
	require.Contains(t, text, "TRUSTD-VERIFY-OK", "sandboxed child did not verify the chain; output=%s", text)
}

// seatbeltTrustdHelperMain runs in the re-executed child. args are the fixture
// path and either a file the policy does not grant (the child must be denied
// reading it) or "-" for the unsandboxed control, which skips that probe.
// Returns the process exit code.
func seatbeltTrustdHelperMain(args []string) int {
	if len(args) != 2 {
		fmt.Println("TRUSTD-USAGE want <fixture> <outside-file|->")
		return 2
	}
	if args[1] != "-" {
		if _, err := os.ReadFile(args[1]); err == nil {
			fmt.Println("TRUSTD-UNCONFINED read a file outside the policy")
			return 3
		}
		fmt.Println("TRUSTD-CONFINED")
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Println("TRUSTD-READ-ERR", err)
		return 4
	}
	var certs []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		cert, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			fmt.Println("TRUSTD-PARSE-ERR", parseErr)
			return 5
		}
		certs = append(certs, cert)
	}
	if len(certs) < 2 {
		fmt.Println("TRUSTD-FIXTURE-ERR want a leaf plus intermediates, got", len(certs))
		return 6
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	// Roots left nil: this is the platform verifier path (Security.framework),
	// the one that needs the trust daemon.
	chains, err := certs[0].Verify(x509.VerifyOptions{
		DNSName:       "api.github.com",
		Intermediates: intermediates,
		CurrentTime:   seatbeltTrustdVerifyTime,
	})
	if err != nil {
		fmt.Println("TRUSTD-VERIFY-ERR", err)
		return 1
	}
	root := chains[0][len(chains[0])-1]
	fmt.Println("TRUSTD-VERIFY-OK", strings.TrimSpace(root.Subject.CommonName))
	return 0
}
