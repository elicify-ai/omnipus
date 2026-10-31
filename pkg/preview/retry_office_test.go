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
// The child snapshots its owner record immediately on entry: this proves
// publication before target entry, not the stricter before-go-byte ordering.
// The latter needs a separate pre-go observation seam; it is not claimed here.
type officeRetryReceipt struct {
	fakeOfficeReceipt
	Owner         ownerRecord `json:"owner"`
	Start         string      `json:"start"`
	LauncherPID   int         `json:"launcher_pid"`
	LauncherStart string      `json:"launcher_start"`
	Profile       string      `json:"profile"`
	Input         string      `json:"input"`
}

func officeRetryProcess(behavior string) int {
	starts, err := filepath.Glob("retry-start-*.json")
	if err != nil {
		return 70
	}
	n := len(starts) + 1
	raw, err := os.ReadFile(ownerFile)
	if err != nil {
		return 71
	}
	var owner ownerRecord
	if json.Unmarshal(raw, &owner) != nil {
		return 72
	}
	profile, err := os.ReadFile(filepath.Join("profile", "user", "registrymodifications.xcu"))
	if err != nil {
		return 73
	}
	input, err := os.ReadFile(filepath.Join("in", "document.docx"))
	if err != nil {
		return 74
	}
	start, err := processStart(os.Getpid())
	if err != nil {
		return 75
	}
	launcherPID := os.Getpid()
	if runtime.GOOS == "windows" {
		launcherPID = os.Getppid() // Windows relays through the job-owning parent.
	}
	launcherStart, err := processStart(launcherPID)
	if err != nil {
		return 75
	}
	receipt := officeRetryReceipt{fakeOfficeReceipt: fakeOfficeReceipt{Args: os.Args[1:], Env: os.Environ(), PID: os.Getpid()}, Owner: owner, Start: start, LauncherPID: launcherPID, LauncherStart: launcherStart, Profile: string(profile), Input: string(input)}
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

func assertOfficeRetryRecord(t *testing.T, r officeRetryReceipt, executable string) {
	t.Helper()
	require.Equal(t, "running", r.Owner.State)
	require.NotNil(t, r.Owner.Launcher)
	require.Equal(t, r.LauncherPID, r.Owner.Launcher.PID)
	require.Equal(t, r.LauncherStart, r.Owner.Launcher.Start)
	if runtime.GOOS != "windows" {
		require.Equal(t, r.PID, r.LauncherPID, "Unix target keeps the launcher PID across execve")
		require.Equal(t, r.Start, r.LauncherStart)
	}
	require.Equal(t, executable, r.Owner.Launcher.Executable)
	require.Contains(t, r.Owner.Executables, executable)
	require.Equal(t, r.LauncherPID, r.Owner.PGID)
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
			pdf, _, err := conv.run(context.Background(), []byte("same authorized input"), ".docx")
			receipts := officeRetryReceipts(t, c)
			assert.Equal(t, tc.starts, len(receipts), "T-78: exact number of ACTUAL converter entries")
			for _, receipt := range receipts {
				assertOfficeRetryRecord(t, receipt, conv.self)
			}
			if len(receipts) == 2 {
				require.NotEqual(t, receipts[0].PID, receipts[1].PID)
				require.NotEqual(t, receipts[0].Owner.Launcher.PID, receipts[1].Owner.Launcher.PID, "T-78: a NEW launcher, not only a new target")
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
