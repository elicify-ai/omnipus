//go:build darwin || linux

package preview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// §3.4 D1/D5 (FR-051, FR-050, founder Q18/Q19): the owner record lives at the
// call folder, one level above the scratch folder the converter can write, so
// the converter cannot alter it and the gateway never writes into scratch by
// name after the first start. The fake converter here runs through the REAL
// launcher and the REAL confinement hooks (hooksFor), like every process-edge
// test in this package; the only substitution is the converter binary itself.

// recordAttackOfficeProcess is the fake converter for T-92. It writes one
// marker line per attempt into attempts-log.txt BEFORE each attempt and the
// attempt's own outcome after it, then a control write inside scratch that
// must succeed — so a fake that ended early, or one running unconfined, can
// never produce a false pass.
func recordAttackOfficeProcess() int {
	recordDir := "."
	if _, err := os.Stat("owner.json"); err != nil {
		recordDir = ".."
	}
	appendMark := func(line string) error {
		f, err := os.OpenFile("attempts-log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(line + "\n")
		return err
	}
	attacks := []struct {
		name    string
		attempt func() error
	}{
		{"overwrite", func() error {
			return os.WriteFile(filepath.Join(recordDir, "owner.json"), []byte("TAMPERED-BY-CONVERTER"), 0600)
		}},
		{"unlink", func() error { return os.Remove(filepath.Join(recordDir, "owner.json")) }},
		{"rename", func() error {
			return os.Rename(filepath.Join(recordDir, "owner.json"), filepath.Join(recordDir, "owner.json.moved"))
		}},
		{"link", func() error { return os.Link(filepath.Join(recordDir, "owner.json"), "record-hardlink") }},
		{"create_beside", func() error {
			return os.WriteFile(filepath.Join(recordDir, "beside-record.txt"), []byte("planted"), 0600)
		}},
	}
	for _, attack := range attacks {
		if err := appendMark("attempt " + attack.name + " start"); err != nil {
			return 70
		}
		outcome := "denied"
		if err := attack.attempt(); err == nil {
			outcome = "ok"
		}
		if err := appendMark("attempt " + attack.name + " " + outcome); err != nil {
			return 71
		}
	}
	// Control: the same process CAN still write inside its own scratch.
	if err := appendMark("control write ok"); err != nil {
		return 72
	}
	return 0
}

// T-92 / §3.4 D1, FR-051 (B-64): through the real launcher and real
// confinement hooks, the converter tries to overwrite, delete, rename and
// hard-link the record and to create a file beside it. Every attempt must be
// denied, the control write inside scratch must succeed, and afterwards the
// record is byte-identical to what the gateway published. Confinement being
// off fails the test — it never skips.
func TestPreviewRecord_IsOutOfConverterReach(t *testing.T) {
	primeKernelSandbox(t)
	home := t.TempDir()
	conv, c := fakeOfficeCall(t, home, "record_attack")
	t.Cleanup(c.finish)
	// The office run returns the confinement disclosure directly (the office
	// path never calls noteUnconfined, so c.unconfinedReason() is always
	// empty and would make this check vacuous).
	pdf, unconfined, runErr := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	require.Nil(t, pdf, "T-92: the attack converter produces no PDF")
	require.Error(t, runErr, "instrument: the fake writes no document.pdf, so conversion reports it")

	require.Equal(t, "", unconfined,
		"T-92: the kernel sandbox must be active for this run; an unconfined converter proves nothing (spec §3.4 D1)")

	log, err := os.ReadFile(filepath.Join(c.dir, "attempts-log.txt"))
	require.NoError(t, err, "instrument: the fake converter really ran and marked every attempt")
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	var control bool
	denied := map[string]int{}
	for _, line := range lines {
		if line == "control write ok" {
			control = true
		}
		for _, name := range []string{"overwrite", "unlink", "rename", "link", "create_beside"} {
			if line == "attempt "+name+" denied" {
				denied[name]++
			}
			require.NotEqual(t, "attempt "+name+" ok", line,
				"T-92: the converter must not be able to %s the owner record", name)
		}
	}
	require.True(t, control, "instrument: the fake's control write inside scratch succeeded — it had write access and really tried")
	for _, name := range []string{"overwrite", "unlink", "rename", "link", "create_beside"} {
		require.Equal(t, 1, denied[name], "T-92: attempt %s ran exactly once and was denied", name)
	}

	// The record is byte-identical to the gateway's publication (idle state:
	// preparing, launcher cleared, socket folder recorded). Any tampering that
	// survived would change these bytes.
	data, err := os.ReadFile(filepath.Join(recordPathFor(t, c), ownerFile))
	require.NoError(t, err, "T-92: the trusted record still exists at the call folder")
	var record ownerRecord
	require.NoError(t, json.Unmarshal(data, &record))
	require.Equal(t, statePreparing, record.State)
	require.Nil(t, record.Launcher, "T-92: the record is the gateway's own publication")
	require.Equal(t, os.Getpid(), record.GatewayPID)
}

// recordPathFor is the §3.4 D1 call folder: one level above work (c.dir).
// There is no fallback into scratch. A record inside work is the converter's
// file, and accepting it would let a correct layout fail to be required.
func recordPathFor(t *testing.T, c *call) string {
	t.Helper()
	return filepath.Dir(c.dir)
}

// guardSwapEvidence reads the fake's launch counter from the root it moved
// aside (§3.4 D6 fixtures): the fake renames its scratch root into the
// recorded socket folder, where its markers survive the replacement.
func guardSwapEvidence(t *testing.T, c *call) (string, string) {
	t.Helper()
	record, err := readPreviewOwnerAt(t, recordPathFor(t, c))
	require.NoError(t, err, "§3.4 D1: the trusted record is the call folder, one level above work")
	moved := filepath.Join(record.SocketDir, "moved-root")
	starts, err := os.ReadFile(filepath.Join(moved, "guard-starts"))
	require.NoError(t, err, "instrument: the fake's own launch counter survived inside the recorded socket folder")
	return moved, string(starts)
}

func readPreviewOwnerAt(t *testing.T, dir string) (ownerRecord, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		return ownerRecord{}, err
	}
	var record ownerRecord
	err = json.Unmarshal(data, &record)
	return record, err
}

// T-98 / §3.4 D5, FR-050 (B-65): after the scratch folder is replaced by a
// link to an outside folder E, the gateway's record publication (idle) and
// the end-of-call cleanup must not touch E and must update the REAL record.
// The primary variant performs the replacement gateway-side, at a barrier;
// every OS runs it.
func TestPreviewPublish_ReplacedScratchNeverRedirectsGatewayWrites(t *testing.T) {
	outside := t.TempDir()
	E := filepath.Join(outside, "E")
	require.NoError(t, os.MkdirAll(E, 0o700))
	sentinel := filepath.Join(E, "owner.json")
	require.NoError(t, os.WriteFile(sentinel, []byte("E-SENTINEL: outside original\n"), 0o600))

	home := t.TempDir()
	conv, c := fakeOfficeCall(t, home, "valid")
	var finished bool
	t.Cleanup(func() {
		if !finished {
			c.finish()
		}
	})
	_, _, err := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	require.NoError(t, err, "instrument: the control call converts")

	// The barrier: replace scratch with a link to E, then idle and finish run.
	realDir := c.dir + "-real"
	require.NoError(t, os.Rename(c.dir, realDir))
	require.NoError(t, os.Symlink(E, c.dir))
	before := scratchOutsideSnapshot(t, outside)
	require.NoError(t, c.idle(), "instrument: idle publishes after the barrier")
	finished = true
	c.finish()
	require.Equal(t, before, scratchOutsideSnapshot(t, outside),
		"T-98: the replaced scratch folder must not redirect a gateway write — E keeps its sentinel and gains no .tmp-* file")
	record, err := readPreviewOwnerAt(t, realRecordDir(t, c))
	require.NoError(t, err, "T-98: the REAL record (not one written through the link) is updated")
	require.Equal(t, stateDone, record.State, "T-98: the real record reaches done")
	require.NoDirExists(t, c.dir, "end-of-call cleanup unlinks the replacement")
}

// realRecordDir is the §3.4 D1 call folder, one level above work. A copy
// inside the moved scratch is not the trusted record.
func realRecordDir(t *testing.T, c *call) string {
	t.Helper()
	callDir := filepath.Dir(c.dir)
	require.FileExists(t, filepath.Join(callDir, ownerFile),
		"T-98 §3.4 D1: the trusted record is the call folder, one level above work, not a file inside scratch")
	return callDir
}

// T-98 macOS variant: a real confined child performs the replacement
// (class-verified on macOS: a confined child can replace its own scratch root
// with a link; it cannot write through the replacement). The child renames
// its scratch root into the recorded socket folder and installs a link to E
// at the old path, then exits 0 having written the PDF first — the gateway
// reads the output through its anchored handle and then publishes by name.
func TestPreviewPublish_ConfinedChildReplacesScratch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only variant: no other platform has both a confined child that can replace its root and a socket folder to keep the moved root in")
	}
	primeKernelSandbox(t)
	outside := t.TempDir()
	E := filepath.Join(outside, "E")
	require.NoError(t, os.MkdirAll(E, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(E, "owner.json"), []byte("E-SENTINEL\n"), 0o600))
	rootAbs := filepath.Join(E, "target-root")
	require.NoError(t, os.MkdirAll(rootAbs, 0o700))

	home := t.TempDir()
	conv, c := fakeOfficeCall(t, home, "rootswap_confined")
	t.Cleanup(c.finish)
	plan, err := json.Marshal(map[string]string{"Target": rootAbs})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "rootswap-plan.json"), plan, 0o600))

	_, unconfined, runErr := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	require.Equal(t, "", unconfined, "T-98 macOS variant: the child runs confined")
	require.NoError(t, runErr, "instrument: the confined child converted, then replaced the root (child exit %v)", runErr)

	record, err := readPreviewOwnerAt(t, recordPathFor(t, c))
	require.NoError(t, err)
	moved := filepath.Join(record.SocketDir, "moved-root")
	starts, err := os.ReadFile(filepath.Join(moved, "rootswap-starts"))
	require.NoError(t, err, "instrument: the confined child's markers survive inside the recorded socket folder")
	require.Equal(t, "x", string(starts), "instrument: exactly one start did the replacement")

	require.Equal(t, map[string]string{
		"./":             "directory",
		"E/":             "directory",
		"E/owner.json":   "E-SENTINEL\n",
		"E/target-root/": "directory",
	}, scratchOutsideSnapshot(t, outside),
		"T-98: the confined child's replacement must not write anything into E")
	_, lstatErr := os.Lstat(c.dir)
	require.NoError(t, lstatErr, "the replacement (a link) sits at the scratch path when the call ends")
	c.finish()
	require.Equal(t, map[string]string{
		"./":             "directory",
		"E/":             "directory",
		"E/owner.json":   "E-SENTINEL\n",
		"E/target-root/": "directory",
	}, scratchOutsideSnapshot(t, outside), "T-98: end-of-call cleanup unlinks the replacement, never traverses it")
}

// guardSwapOfficeProcess is the fake converter for the §3.4 D6 guard fixtures:
// it converts, then replaces its own scratch root with a link to the planned
// target, keeping its markers inside the recorded socket folder. Behavior
// suffix decides whether it exits 81 (T-100) or 0 with a valid PDF (T-98).
func guardLikeOfficeProcess(planFile, marker string, exitCode int, withPDF bool) int {
	raw, err := os.ReadFile(planFile)
	if err != nil {
		return 70
	}
	var plan struct {
		Target string `json:"Target"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return 71
	}
	if mkdirErr := os.MkdirAll("out", 0o700); mkdirErr != nil {
		return 72
	}
	if withPDF {
		if pdfErr := os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600); pdfErr != nil {
			return 73
		}
	}
	// Append, never overwrite: a second start (the defect T-100 counts) must
	// be visible in the marker, so one start reads "x" and two read "xx".
	markerFile, markerErr := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if markerErr != nil {
		return 74
	}
	if _, writeErr := markerFile.WriteString("x"); writeErr != nil {
		_ = markerFile.Close()
		return 74
	}
	if closeErr := markerFile.Close(); closeErr != nil {
		return 74
	}
	// The recorded socket folder survives the replacement and is readable by
	// the gateway's recovery, so the fake parks its markers there.
	socket := os.Getenv("OSL_SOCKET_PATH")
	if socket == "" {
		return 75
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return 76
	}
	moved := filepath.Join(socket, "moved-root")
	if renameErr := os.Rename(cwd, moved); renameErr != nil {
		return 77
	}
	if linkErr := os.Symlink(plan.Target, cwd); linkErr != nil {
		return 78
	}
	return exitCode
}

func confinedRootSwapOfficeProcess() int {
	return guardLikeOfficeProcess("rootswap-plan.json", "rootswap-starts", 0, true)
}

func guardSwapOfficeProcess() int {
	return guardLikeOfficeProcess("guard-plan.json", "guard-starts", 81, false)
}

// T-100 / §3.4 D6, FR-052 (B-66), founder Q23 = A: the first start replaces
// its scratch folder and exits 81. The call must end with the existing text
// "conversion failed: scratch unavailable" and EXACTLY ONE launch — counting
// launches is the oracle, because asserting only the outside folder's
// survival passes on a cached sandbox profile with no guard at all.
//
// macOS-gated: the confined child can replace its scratch root only there
// (class-verified: recreate its own root as a link). On Linux a confined
// converter cannot rename or remove its scratch root at all — rename and
// removal need write rights on the parent folder, and only work is granted —
// so macOS is the platform where the guard has a real end-to-end input.
func TestPreviewSecondStart_RefusesReplacedScratchFolder(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only: on Linux a confined converter cannot replace its scratch root (no parent-folder rights), so the guard has no end-to-end input there")
	}
	primeKernelSandbox(t)
	outside := t.TempDir()
	E := filepath.Join(outside, "E")
	require.NoError(t, os.MkdirAll(E, 0o700))
	before := scratchOutsideSnapshot(t, outside)

	home := t.TempDir()
	conv, c := fakeOfficeCall(t, home, "guard_swap81")
	t.Cleanup(c.finish)
	plan, err := json.Marshal(map[string]string{"Target": E})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "guard-plan.json"), plan, 0o600))

	_, unconfined, runErr := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	require.Equal(t, "", unconfined, "T-100: the child runs confined")
	require.EqualError(t, runErr, "conversion failed: scratch unavailable",
		"T-100 §3.4 D6: a replaced scratch folder ends the call with the existing text and no second start")

	// Exactly ONE launch, counted from the fake's own append markers in both
	// folders a start can land in: the first start's markers survive inside
	// the recorded socket folder (moved-root), a second start's land in the
	// replacement target through the replaced path. Counting both is the
	// oracle, because asserting only E's survival passes on a cached sandbox
	// profile with no guard at all.
	_, firstStarts := guardSwapEvidence(t, c)
	retriedStarts, _ := os.ReadFile(filepath.Join(E, "guard-starts"))
	starts := strings.Count(strings.TrimSpace(firstStarts), "x") +
		strings.Count(string(retriedStarts), "x")
	require.Equal(t, 1, starts, "T-100: exactly one converter start — the guard must not retry onto the replaced folder")
	_, statErr := os.Lstat(filepath.Join(E, "owner.json"))
	require.ErrorIs(t, statErr, os.ErrNotExist,
		"T-100: a second start would publish the running record through the replacement into E")
	require.Equal(t, before, scratchOutsideSnapshot(t, outside),
		"T-100: the replaced-onto folder gains nothing from the call")
}
