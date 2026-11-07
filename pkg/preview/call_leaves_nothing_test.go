//go:build darwin || linux

package preview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// cwdReceiptOfficeProcess is the fake converter for T-101: it converts and
// records its own working folder, so the test can prove WHERE the call tree
// lived while the call was live (§3.4 D1: <home>/tmp/preview-calls/<id>/work).
func cwdReceiptOfficeProcess(withPDF bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		return 70
	}
	receipt, err := json.Marshal(map[string]any{"pid": os.Getpid(), "cwd": cwd})
	if err != nil {
		return 71
	}
	if os.WriteFile("cwd-receipt.json", receipt, 0600) != nil {
		return 72
	}
	if withPDF {
		if os.MkdirAll("out", 0o700) != nil {
			return 73
		}
		if os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600) != nil {
			return 74
		}
	}
	return 0
}

// T-101 / §3.4 D2 (B-21, B-22): a normal call leaves NOTHING behind — after
// success, failure and Stop, the call root holds no <id>. The oracle checks
// the call folder itself, not only work/, so a leaked record or folder is
// seen. The live tree's location is proven by the converter's own recorded
// working folder (success/failure) and by the live folder during the Stop
// window — on the pre-amendment layout the call tree lives at the old root,
// which is this test's RED.
func TestPreviewCall_LeavesNothingBehind(t *testing.T) {
	home := t.TempDir()
	callRoot := filepath.Join(home, "tmp", "preview-calls")

	t.Run("success", func(t *testing.T) {
		conv, c := fakeOfficeCall(t, home, "cwd_valid")
		t.Cleanup(c.finish)
		pdf, _, err := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
		require.NoError(t, err)
		require.NotNil(t, pdf, "instrument: the success call converted")
		assertCallRanInCallTree(t, c)
		require.NoDirExists(t, callRoot, "T-101: success leaves no call folder")
	})

	t.Run("failure", func(t *testing.T) {
		conv, c := fakeOfficeCall(t, home, "cwd_invalid")
		t.Cleanup(c.finish)
		_, _, err := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
		require.Error(t, err, "instrument: the failure call really failed")
		assertCallRanInCallTree(t, c)
		require.NoDirExists(t, callRoot, "T-101: failure leaves no call folder")
	})

	t.Run("stop", func(t *testing.T) {
		conv, c := fakeOfficeCall(t, home, "retry_81_block")
		t.Cleanup(c.finish)
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan struct{})
		var conversionErr error
		go func() {
			defer close(finished)
			_, _, conversionErr = conv.run(ctx, []byte("same authorized input"), ".docx")
		}()
		defer func() { cancel(); <-finished }()

		// While the call is live, its folder exists under the call root —
		// this is where the next startup's recovery would look for it.
		var liveDirs []string
		require.Eventually(t, func() bool {
			liveDirs, _ = filepath.Glob(filepath.Join(callRoot, "*"))
			return len(liveDirs) == 1
		}, 20*time.Second, 10*time.Millisecond,
			"T-101 §3.4 D1: the live call's folder exists at <home>/tmp/preview-calls/<id>")

		cancel()
		select {
		case <-finished:
			require.Error(t, conversionErr, "the stop ends the conversion")
		case <-time.After(15 * time.Second):
			t.Fatal("T-101: the stop did not end the conversion")
		}
		require.NoDirExists(t, callRoot, "T-101: a stopped call leaves no call folder behind")
		leftovers, _ := filepath.Glob(filepath.Join(callRoot, "*", "*"))
		require.Empty(t, leftovers, "T-101: no record and no work folder survive under a leftover id")
	})
}

// assertCallRanInCallTree reads the fake converter's recorded working folder
// and requires it to be <home>/tmp/preview-calls/<id>/work.
func assertCallRanInCallTree(t *testing.T, c *call) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.dir, "cwd-receipt.json"))
	require.NoError(t, err, "instrument: the fake converter recorded its working folder")
	var receipt struct {
		Cwd string `json:"cwd"`
	}
	require.NoError(t, json.Unmarshal(raw, &receipt))
	inCallTree := strings.Contains(receipt.Cwd, filepath.Join("tmp", "preview-calls")+string(filepath.Separator)) &&
		strings.HasSuffix(receipt.Cwd, filepath.Join("work"))
	require.True(t, inCallTree,
		"T-101 §3.4 D1: the call tree lives at <home>/tmp/preview-calls/<id>/work (converter cwd was %s)", receipt.Cwd)
}
