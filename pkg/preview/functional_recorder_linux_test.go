//go:build linux

package preview

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/stretchr/testify/require"
)

const functionalReceiptName = "functional-office-starts.jsonl"

// This is telemetry at the external-program edge, NOT a fake converter. The
// unchanged real soffice.bin in recorded/program makes every PDF. Keeping a
// parent-held receipt FD open lets us read its actual exit codes after the
// product has removed work. No production observation hook is added.
type functionalOfficeExit struct {
	PID           int    `json:"pid"`
	PIDStart      string `json:"pid_start,omitempty"`
	Recorder      int    `json:"recorder_pid"`
	RecorderStart string `json:"recorder_start,omitempty"`
	Executable    string `json:"executable"`
	Exit          int    `json:"exit"`
}

func init() {
	if len(os.Args) < 2 {
		return
	}
	if os.Args[1] == extracttext.ModeArg {
		os.Exit(extracttext.Main())
	}
	if filepath.Base(os.Args[0]) == "soffice.bin" && os.Args[1] == "--headless" {
		os.Exit(functionalRecordRealOffice())
	}
}

func functionalRecordRealOffice() int {
	self, err := os.Executable()
	if err != nil {
		return 70
	}
	realProgram := filepath.Join(filepath.Dir(filepath.Dir(self)), "recorded", "program", "soffice.bin")
	cmd := exec.Command(realProgram, os.Args[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Start(); err != nil {
		return 71
	}
	// The kernel start identity, read as early as possible and stable
	// across execve: the launch-side proof that a pid seen alive later is
	// (or is not) the same task. The executable and the command line prove
	// neither — a leaked task can exec away (CHECK F1).
	pidStart, _ := processStart(cmd.Process.Pid)
	recorderStart, _ := processStart(os.Getpid())
	waitErr := cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	if waitErr != nil && code < 0 {
		code = 72
	}
	f, err := os.OpenFile(functionalReceiptName, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 73
	}
	receipt := functionalOfficeExit{
		PID:           cmd.Process.Pid,
		PIDStart:      pidStart,
		Recorder:      os.Getpid(),
		RecorderStart: recorderStart,
		Executable:    realProgram,
		Exit:          code,
	}
	encodeErr := json.NewEncoder(f).Encode(receipt)
	closeErr := f.Close()
	if encodeErr != nil || closeErr != nil {
		return 74
	}
	return code // In particular, the REAL exit 81 is relayed unchanged.
}

// functionalCopyTree lives in own_folder_staging_test.go, untagged (D17):
// the turn-repeat row, darwin || linux, stages its own-folder registrations
// through it on both GOOS.

func functionalRecordedOffice(t *testing.T, source string) string {
	t.Helper()
	// §3.9 fixture move (sanctioned): the recorded Office is registered and
	// used through the live Linux own-folder check, so it stages inside the
	// default program path's own folder. Assertions are untouched; only the
	// fixture root relocates (the source install is still never edited).
	root := filepath.Join(functionalOwnFolder(t), "recorded-office")
	functionalCopyTree(t, source, root)
	// Preserve the ENTIRE unchanged real distribution's relative layout,
	// including its original program/ folder name. Only the outer program
	// entry becomes telemetry; the real distribution is never edited.
	functionalCopyTree(t, source, filepath.Join(root, "recorded"))
	realProgram := filepath.Join(root, "recorded", "program", "soffice.bin")
	original, err := os.ReadFile(filepath.Join(source, "program", "soffice.bin"))
	require.NoError(t, err)
	copied, err := os.ReadFile(realProgram)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(original), sha256.Sum256(copied), "instrument: the real LibreOffice executable is byte-for-byte unchanged")
	self, err := os.Executable()
	require.NoError(t, err)
	in, err := os.Open(self)
	require.NoError(t, err)
	defer func() { _ = in.Close() }()
	entry := filepath.Join(root, "program", "soffice.bin")
	require.NoError(t, os.Remove(entry), "replace only the isolated copy, never the original install")
	out, err := os.OpenFile(entry, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	require.NoError(t, err)
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	require.NoError(t, copyErr)
	require.NoError(t, closeErr)
	t.Logf("real LibreOffice executable sha256=%x", sha256.Sum256(copied))
	return root
}
