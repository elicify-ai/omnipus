package preview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// readPreviewOwner reads the trusted owner record. §3.4 D1 puts it in the call
// folder, one level above the work folder (c.dir). Reading c.dir/owner.json
// would accept a file the converter can replace.
func readPreviewOwner(t *testing.T, c *call) ownerRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(c.dir), ownerFile))
	require.NoError(t, err, "§3.4 D1: owner.json lives in the call folder, one level above the work folder")
	var record ownerRecord
	require.NoError(t, json.Unmarshal(data, &record))
	return record
}

func TestPreviewScratch_PublishesPreparingRunningAndCleansDone(t *testing.T) {
	home := t.TempDir()
	c, err := newCall(home, 42, "gateway-start")
	require.NoError(t, err)
	defer c.finish()
	require.Equal(t, ownerRecord{State: "preparing", GatewayPID: 42, GatewayStart: "gateway-start"}, readPreviewOwner(t, c))
	info, err := os.Stat(c.dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	id := procIdentity{PID: 43, Start: "launcher-start", Executable: "/trusted/omnipus"}
	// §3.4 D4: running publishes the launcher identity and the allowed
	// executables. There is no stored process-group number.
	require.NoError(t, c.running(id, []string{"/trusted/omnipus", "/trusted/omnipus-pdfium"}))
	require.Equal(t, ownerRecord{State: "running", GatewayPID: 42, GatewayStart: "gateway-start", Launcher: &id, Executables: []string{"/trusted/omnipus", "/trusted/omnipus-pdfium"}}, readPreviewOwner(t, c))
	require.NoError(t, c.idle())
	require.Equal(t, ownerRecord{State: "preparing", GatewayPID: 42, GatewayStart: "gateway-start"}, readPreviewOwner(t, c))
	c.finish()
	_, err = os.Stat(c.dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// T-14 / AD-8: foreign or uncertain identity is kept, never killed.
func TestPreviewRecovery_DeadLiveForeignAndUnreadableOwners(t *testing.T) {
	home := t.TempDir()
	start, err := processStart(os.Getpid())
	require.NoError(t, err)
	cases := []struct {
		name       string
		record     *ownerRecord
		raw        string
		keep, warn bool
	}{
		{name: "dead_preparing", record: &ownerRecord{State: "preparing", GatewayPID: os.Getpid(), GatewayStart: "not-current-start"}},
		{name: "dead_done", record: &ownerRecord{State: "done", GatewayPID: os.Getpid(), GatewayStart: "not-current-start"}},
		{name: "ours", record: &ownerRecord{State: "preparing", GatewayPID: os.Getpid(), GatewayStart: start}, keep: true},
		{name: "foreign_launcher", record: &ownerRecord{State: "running", GatewayPID: os.Getpid(), GatewayStart: "not-current-start", Launcher: &procIdentity{PID: os.Getpid(), Start: "not-current-start", Executable: "/not-the-test-process"}}, keep: true, warn: true},
		{name: "invalid_json", raw: "{broken", keep: true, warn: true},
		{name: "missing_owner", keep: true, warn: true},
		{name: "unknown_state", record: &ownerRecord{State: "unknown", GatewayPID: os.Getpid(), GatewayStart: "not-current-start"}, keep: true, warn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(home, "tmp", "preview", tc.name)
			require.NoError(t, os.MkdirAll(dir, 0700))
			if tc.record != nil {
				data, merr := json.Marshal(tc.record)
				require.NoError(t, merr)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "owner.json"), data, 0600))
			} else if tc.raw != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "owner.json"), []byte(tc.raw), 0600))
			}
			var report RecoveryReport
			recoverOne(dir, os.Getpid(), start, &report)
			_, statErr := os.Stat(dir)
			if tc.keep {
				require.NoError(t, statErr)
				require.Zero(t, report.Removed)
			} else {
				require.ErrorIs(t, statErr, os.ErrNotExist)
				require.Equal(t, 1, report.Removed)
			}
			require.Zero(t, report.Killed, "no positively owned live child is in this fixture")
			if tc.warn {
				require.Len(t, report.Warnings, 1)
			} else {
				require.Empty(t, report.Warnings)
			}
		})
	}
	// The same real owner, viewed by another gateway, is kept and diagnosed.
	dir := filepath.Join(home, "tmp", "preview", "live-other-gateway")
	require.NoError(t, os.MkdirAll(dir, 0700))
	data, err := json.Marshal(ownerRecord{State: "preparing", GatewayPID: os.Getpid(), GatewayStart: start})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "owner.json"), data, 0600))
	var report RecoveryReport
	recoverOne(dir, -1, "other", &report)
	require.Zero(t, report.Removed)
	require.Zero(t, report.Killed)
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "still running")
}

func TestPreviewRecovery_RunningWithoutChildIdentityIsKeptAndWarned(t *testing.T) {
	// AD-8 explicitly keeps unconfirmed ownership. A running record lacking
	// its launcher identity cannot prove that a child has terminated.
	home := t.TempDir()
	dir := filepath.Join(home, "tmp", "preview", "unconfirmed")
	require.NoError(t, os.MkdirAll(dir, 0700))
	data, err := json.Marshal(ownerRecord{State: "running", GatewayPID: os.Getpid(), GatewayStart: "dead-owner-start"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "owner.json"), data, 0600))
	var report RecoveryReport
	recoverOne(dir, -1, "other", &report)
	_, err = os.Stat(dir)
	require.NoError(t, err, "AD-8: unconfirmed running state must remain for diagnosis")
	require.Zero(t, report.Removed)
	require.NotEmpty(t, report.Warnings, "AD-8: unconfirmed ownership needs a diagnostic")
}
