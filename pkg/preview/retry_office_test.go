package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	pdfreader "github.com/ledongthuc/pdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-78 uses an external process only for the converter. The actual call,
// launcher, owner publication, environment and conversion decision stay real.
// The child no longer reads the owner record: §3.4 D1 (and §3.4 D8c) keep
// `<id>/owner.json` out of the converter's reach on purpose, so a confined
// fake must not read it (it used to exit 71 whenever an earlier test in the
// same process had primed the sandbox). The publication-before-entry oracle
// moves to the parent, which reads the record while the fake is held at a
// work-folder barrier (officeRetryRunWithOwnerBarriers): the record must then
// name THIS start's launcher. The gateway reads the actual kernel start times
// independently of that record: Linux also denies the fake's /proc reads. The
// fake carries its own PID/launcher PID, environment, profile, and input.
type officeRetryReceipt struct {
	fakeOfficeReceipt
	Start         string `json:"start"`
	LauncherPID   int    `json:"launcher_pid"`
	LauncherStart string `json:"launcher_start"`
	Profile       string `json:"profile"`
	Input         string `json:"input"`
}

func officeRetryProcess(behavior string) int {
	return officeRetryProcessWithStartReader(behavior, processStart)
}

// The reader is a regression tripwire: this confined fake must never call the
// OS process-metadata edge. Linux /proc is outside its allowed folders. The
// gateway-side barrier reads the real kernel identities while the target waits.
func officeRetryProcessWithStartReader(behavior string, _ func(int) (string, error)) int {
	starts, err := filepath.Glob("retry-start-*.json")
	if err != nil {
		return 70
	}
	n := len(starts) + 1
	profile, err := os.ReadFile(filepath.Join("profile", "user", "registrymodifications.xcu"))
	if err != nil {
		return 73
	}
	input, err := os.ReadFile(filepath.Join("in", "document.docx"))
	if err != nil {
		return 74
	}
	launcherPID := os.Getpid()
	if runtime.GOOS == "windows" {
		launcherPID = os.Getppid() // Windows relays through the job-owning parent.
	}
	receipt := officeRetryReceipt{fakeOfficeReceipt: fakeOfficeReceipt{Args: os.Args[1:], Env: os.Environ(), PID: os.Getpid()}, LauncherPID: launcherPID, Profile: string(profile), Input: string(input)}
	if n == 2 && strings.HasSuffix(behavior, "_block") {
		self, executableErr := os.Executable()
		if executableErr != nil {
			return 76
		}
		child := exec.Command(self, "__office-retry-grandchild")
		if child.Start() != nil {
			return 76
		}
		receipt.Child = child.Process.Pid
		defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		poll := time.NewTicker(5 * time.Millisecond)
		defer poll.Stop()
	ready:
		for {
			data, readErr := os.ReadFile("retry-child-ready")
			if readErr == nil && string(data) == strconv.Itoa(receipt.Child) {
				break ready
			}
			select {
			case <-deadline.C:
				return 76
			case <-poll.C:
			}
		}
	}
	data, err := json.Marshal(receipt)
	if err != nil || os.WriteFile(fmt.Sprintf("retry-start-%d.json", n), data, 0600) != nil {
		return 77
	}
	// The owner-record barrier: the parent reads <id>/owner.json while this
	// start is held here — a file barrier in work/, never a timer — then
	// releases it. Without the wait the call could end (and the record reset
	// to idle) before the parent's mid-run read; bounded, so a broken parent
	// fails the call instead of hanging it.
	if !officeRetryOwnerSeen(n) {
		return 76
	}
	if n == 2 && strings.HasSuffix(behavior, "_block") {
		time.Sleep(30 * time.Second) // canceled by the REAL conversion lifetime
		return 78
	}
	if n == 1 && behavior == "retry_81_profile_reset" {
		// Model the first Office start rewriting its profile. The second
		// start must restore the seeded safety settings, not trust that exit
		// 81 left them intact (§3.1 D3.4).
		if os.WriteFile(filepath.Join("profile", "user", "registrymodifications.xcu"), []byte("<oor:items/>"), 0600) != nil {
			return 78
		}
	}
	switch behavior {
	case "retry_79":
		return 79
	case "retry_1":
		return 1
	case "retry_81_twice":
		if n <= 2 {
			return 81
		}
	case "retry_81_pdf":
		if os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600) != nil {
			return 78
		}
		return 81
	default:
		if n == 1 && behavior != "retry_valid" {
			return 81
		}
	}
	if os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600) != nil {
		return 78
	}
	return 0
}

// Independently constructed, one-page PDF with an empty content stream. This
// is a readable PDF, not a marker-only output whose acceptance is another bug.
func officeRetryPDF() []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [3 0 R] >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents 4 0 R >>", "<< /Length 0 >>\nstream\n\nendstream"}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 5\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return []byte(b.String())
}

// officeRetryOwnerSeen polls for the parent's release file for this start,
// bounded by 5 s so a broken parent fails the call visibly instead of
// hanging it.
func officeRetryOwnerSeen(n int) bool {
	want := fmt.Sprintf("retry-owner-seen-%d", n)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.ReadFile(want); err == nil {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

func officeRetryReceipts(t *testing.T, c *call) []officeRetryReceipt {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(c.dir, "retry-start-*.json"))
	require.NoError(t, err)
	sort.Strings(paths)
	receipts := make([]officeRetryReceipt, 0, len(paths))
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		var receipt officeRetryReceipt
		require.NoError(t, json.Unmarshal(raw, &receipt))
		receipts = append(receipts, receipt)
	}
	return receipts
}

// assertOfficeRetryRecord checks the fake's PID/input/profile and the kernel
// start times independently observed by the gateway. The owner-record checks
// live in assertOfficeOwnerRecord, read by the parent from the trusted record
// while the fake is held at its barrier.
func assertOfficeRetryRecord(t *testing.T, r officeRetryReceipt) {
	t.Helper()
	if runtime.GOOS != "windows" {
		require.Equal(t, r.PID, r.LauncherPID, "Unix target keeps the launcher PID across execve")
		require.Equal(t, r.Start, r.LauncherStart)
	}
	// §3.4 D4: no stored process-group number. The group proof is T-94
	// (TestPreviewRecovery_StopsOnlyTheVerifiedProcessGroup), not a field compare.
	require.Equal(t, "same authorized input", r.Input)
	var profile struct {
		Items []struct {
			Path  string `xml:"path,attr"`
			Props []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value"`
			} `xml:"prop"`
		} `xml:"item"`
	}
	require.NoError(t, xml.Unmarshal([]byte(r.Profile), &profile))
	safety := map[string]string{}
	for _, item := range profile.Items {
		for _, prop := range item.Props {
			if item.Path == "/org.openoffice.Office.Common/Security/Scripting" {
				safety[prop.Name] = prop.Value
			}
		}
	}
	require.Equal(t, "true", safety["DisableMacrosExecution"], "AD-7: macros remain disabled on EVERY start")
}

// assertOfficeOwnerRecord is T-78's publication-before-entry oracle, read
// from the parent — the vantage §3.4 D1 allows. While the start is held at
// the fake's work-folder barrier, the trusted record must already name THIS
// start's launcher: state running, launcher PID equal to the fake's own PID
// observation, start equal to the gateway's independent kernel query, and
// executable recorded. Neither the owner record nor Linux /proc is read by
// the confined converter.
func assertOfficeOwnerRecord(t *testing.T, c *call, receipt officeRetryReceipt, executable string) {
	t.Helper()
	owner := readPreviewOwner(t, c)
	require.Equal(t, "running", owner.State, "T-78 §3.1 D3: the owner record is published before the target's go-byte")
	require.NotNil(t, owner.Launcher)
	require.Equal(t, receipt.LauncherPID, owner.Launcher.PID, "T-78 §3.1 D3: the record names THIS start's launcher")
	require.Equal(t, receipt.LauncherStart, owner.Launcher.Start, "T-78 §3.1 D3: the record names THIS start's launcher start")
	require.Equal(t, executable, owner.Launcher.Executable)
	require.Contains(t, owner.Executables, executable)
}

// officeRetryWaitForStart waits for the fake's receipt for start n while the
// conversion is still running; ok is false when the conversion finished
// without reaching that start.
func officeRetryWaitForStart(t *testing.T, c *call, n int, finished <-chan struct{}) (officeRetryReceipt, bool) {
	t.Helper()
	path := filepath.Join(c.dir, fmt.Sprintf("retry-start-%d.json", n))
	deadline := time.NewTimer(10 * time.Second) // instrument failure bound only
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		raw, readErr := os.ReadFile(path)
		var receipt officeRetryReceipt
		if readErr == nil && json.Unmarshal(raw, &receipt) == nil {
			// The target can report its PID without /proc, but Linux denies its
			// start-time lookup. Observe both real kernel identities here in the
			// gateway, NOT from owner.json, while the target waits for release.
			require.Empty(t, receipt.Start, "instrument: the confined fake did not read process metadata")
			require.Empty(t, receipt.LauncherStart)
			receipt.Start, readErr = processStart(receipt.PID)
			require.NoError(t, readErr, "instrument: the live target's kernel identity is readable by the gateway")
			receipt.LauncherStart, readErr = processStart(receipt.LauncherPID)
			require.NoError(t, readErr, "instrument: the live launcher's kernel identity is readable by the gateway")
			updated, encodeErr := json.Marshal(receipt)
			require.NoError(t, encodeErr)
			require.NoError(t, os.WriteFile(path, updated, 0600), "instrument: retain gateway-observed identities with the target receipt")
			return receipt, true
		}
		select {
		case <-finished:
			return officeRetryReceipt{}, false
		case <-deadline.C:
			t.Fatalf("T-78: converter start %d never wrote its receipt", n)
		case <-tick.C:
		}
	}
}

// officeRetryRunWithOwnerBarriers runs one conversion while checking, per
// start, that the owner record names THIS start's launcher before the fake
// proceeds (assertOfficeOwnerRecord), then releases the fake's barrier. The
// fake is held at the barrier until the release, so the record cannot reset
// to idle before the parent reads it. onStart runs after that check, still
// holding the fake, before the release.
func officeRetryRunWithOwnerBarriers(t *testing.T, conv officeConversion, ctx context.Context, c *call, executable string, input []byte, ext string, onStart func(t *testing.T, start int, receipt officeRetryReceipt)) ([]byte, string, error) {
	t.Helper()
	var (
		pdf        []byte
		unconfined string
		runErr     error
	)
	finished := make(chan struct{})
	go func() {
		pdf, unconfined, runErr = conv.run(ctx, input, ext)
		close(finished)
	}()
	for start := 1; ; start++ {
		receipt, ok := officeRetryWaitForStart(t, c, start, finished)
		if !ok {
			break
		}
		assertOfficeOwnerRecord(t, c, receipt, executable)
		if onStart != nil {
			onStart(t, start, receipt)
		}
		require.NoError(t, os.WriteFile(filepath.Join(c.dir, fmt.Sprintf("retry-owner-seen-%d", start)), []byte("ok"), 0600),
			"instrument: release the fake's owner-record barrier")
	}
	<-finished
	return pdf, unconfined, runErr
}

func TestOfficeConversionRetriesExit81OnlyOnce(t *testing.T) {
	fixture := officeRetryPDF()
	reader, fixtureErr := pdfreader.NewReader(bytes.NewReader(fixture), int64(len(fixture)))
	require.NoError(t, fixtureErr, "instrument: the supplied PDF is actually readable")
	require.Equal(t, 1, reader.NumPage())
	cases := []struct {
		mode    string
		starts  int
		success bool
		exit    int
	}{
		{"retry_valid", 1, true, 0},
		{"retry_81_once", 2, true, 0},
		{"retry_81_twice", 2, false, 81},
		{"retry_79", 1, false, 79},
		{"retry_1", 1, false, 1},
		{"retry_81_pdf", 1, true, 0},
		{"retry_81_profile_reset", 2, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			conv, c := fakeOfficeCall(t, t.TempDir(), tc.mode)
			defer c.finish()
			pdf, _, err := officeRetryRunWithOwnerBarriers(t, conv, context.Background(), c, conv.self, []byte("same authorized input"), ".docx", nil)
			receipts := officeRetryReceipts(t, c)
			assert.Equal(t, tc.starts, len(receipts), "T-78: exact number of ACTUAL converter entries")
			for _, receipt := range receipts {
				assertOfficeRetryRecord(t, receipt)
			}
			if len(receipts) == 2 {
				require.NotEqual(t, receipts[0].PID, receipts[1].PID)
				// A NEW launcher, not only a new target: the owner-record
				// oracle above tied each start's record to ITS receipt's
				// launcher PID, and the two launcher PIDs differ below, so
				// the two records named different launchers transitively.
				require.NotEqual(t, receipts[0].LauncherPID, receipts[1].LauncherPID, "T-78: a NEW launcher, not only a new target")
				require.NotEqual(t, fmt.Sprint(receipts[0].LauncherPID, receipts[0].LauncherStart), fmt.Sprint(receipts[1].LauncherPID, receipts[1].LauncherStart))
				require.Equal(t, receipts[0].Args, receipts[1].Args, "same profile argument and verified executable")
				require.Equal(t, receipts[0].Env, receipts[1].Env, "same scrubbed environment")
				require.Equal(t, receipts[0].Profile, receipts[1].Profile, "security settings survive or are restored before retry")
			}
			if tc.success {
				require.NoError(t, err)
				require.Equal(t, officeRetryPDF(), pdf)
			} else {
				require.EqualError(t, err, fmt.Sprintf("conversion failed: LibreOffice produced no readable PDF (exit status %d)", tc.exit))
				require.Nil(t, pdf)
			}
		})
	}
}
