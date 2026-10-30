package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a delta review I1 (code) / finding 1 (tests): the heal cause sanitizer
// replaced only whitespace-delimited tokens holding a slash, so a data folder
// whose name holds spaces left its words in the warning
// ("open <path> Highly Confidential <path>: permission denied"), and the raw
// text of a decoder error carried the persisted value it failed on. Oracle: a
// path is removed whole, keeping the operation and the OS mechanism; a decoder
// or parse error is logged as its class only.

const (
	healCauseUnixDir    = "/tmp/Acme Highly Confidential Matter/sessions/s1"
	healCauseWindowsDir = `C:\Users\Jane Doe Private Matter\.omnipus\sessions\s1`
	healCauseCanary     = "PRIVATE_RECORD_CANARY_6A"
)

// healCausePrivateWords are the words of the private folder names above; none
// may reach a logged cause.
var healCausePrivateWords = []string{"Acme", "Highly", "Confidential", "Matter", "Jane", "Doe", "Private", "sessions", "s1"}

func requireNoPrivateCauseWords(t *testing.T, cause string) {
	t.Helper()
	for _, word := range healCausePrivateWords {
		require.NotContains(t, cause, word, "a private path word leaked into the cause")
	}
	require.NotContains(t, cause, healCauseCanary, "a persisted value leaked into the cause")
}

func TestHistoryHealCause_RemovesWholePathsWithSpaces(t *testing.T) {
	unixOpen := &fs.PathError{Op: "open", Path: healCauseUnixDir + "/transcript.jsonl", Err: fs.ErrPermission}
	winRemove := &fs.PathError{Op: "remove", Path: healCauseWindowsDir + `\.chat-history\generations\g 1.chi`, Err: errors.New("Access is denied.")}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unix_spaced_path", unixOpen, "open <path>: permission denied"},
		{"windows_spaced_path", winRemove, "remove <path>: Access is denied."},
		{"spaced_rename_names_both_paths", &os.LinkError{Op: "rename", Old: healCauseUnixDir + "/a b.tmp", New: healCauseUnixDir + "/checkpoint.json", Err: fs.ErrExist}, "rename <path> <path>: file already exists"},
		{"windows_spaced_rename", &os.LinkError{Op: "rename", Old: healCauseWindowsDir + `\a b.tmp`, New: healCauseWindowsDir + `\checkpoint.json`, Err: errors.New("Access is denied.")}, "rename <path> <path>: Access is denied."},
		{"wrapped_path_error_keeps_the_wrapper_text", fmt.Errorf("persist checkpoint: %w", unixOpen), "persist checkpoint: open <path>: permission denied"},
		{"build_wrapper_unwraps_to_the_path_error", historyBuildFailure("storage_error", winRemove), "remove <path>: Access is denied."},
		{"joined_path_errors", errors.Join(unixOpen, winRemove), "open <path>: permission denied; remove <path>: Access is denied."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := historyHealCause(tc.err)
			require.Equal(t, tc.want, got)
			requireNoPrivateCauseWords(t, got)
		})
	}
}

func TestHistoryHealCause_DecoderErrorsLogOnlyTheirClass(t *testing.T) {
	var record ArchiveRecord
	timeFieldErr := json.Unmarshal([]byte(`{"timestamp":"`+healCauseCanary+`"}`), &record)
	require.Error(t, timeFieldErr)
	require.Contains(t, timeFieldErr.Error(), healCauseCanary, "SETUP: the real decoder error carries the persisted value")
	_, parseErr := time.Parse(time.RFC3339, healCauseCanary)
	require.Error(t, parseErr)
	syntaxErr := json.Unmarshal([]byte(`{"id": `+healCauseCanary+`}`), &record)
	require.Error(t, syntaxErr)
	typeErr := json.Unmarshal([]byte(`{"tokens":"`+healCauseCanary+`"}`), &record)
	require.Error(t, typeErr)
	_, numErr := strconv.ParseInt(healCauseCanary, 10, 64)
	require.Error(t, numErr)
	require.Contains(t, numErr.Error(), healCauseCanary, "SETUP: the real parse error carries the persisted value")
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"json_time_field", timeFieldErr, "decode error (*time.ParseError)"},
		{"time_parse", parseErr, "decode error (*time.ParseError)"},
		{"json_syntax", syntaxErr, "decode error (*json.SyntaxError)"},
		{"json_type", typeErr, "decode error (*json.UnmarshalTypeError)"},
		{"number_parse", numErr, "decode error (*strconv.NumError)"},
		{"build_wrapper_unwraps_to_the_class", historyBuildFailure("corrupt_archive", timeFieldErr), "decode error (*time.ParseError)"},
		{"suffix_wrapper_keeps_its_own_text", fmt.Errorf("archive record 4: %w", parseErr), "archive record 4: decode error (*time.ParseError)"},
		{"embedding_wrapper_is_dropped_whole", fmt.Errorf("bad record (%w) skipped", parseErr), "decode error (*time.ParseError)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := historyHealCause(tc.err)
			require.Equal(t, tc.want, got)
			requireNoPrivateCauseWords(t, got)
		})
	}
}

// The real heal path under a data folder whose name holds spaces: the
// reviewer's reproduction, as a permanent regression.
func TestChatHistoryHeal_FailureWarningRemovesSpacedDataDir(t *testing.T) {
	spaced, err := os.MkdirTemp("", "6a Acme Highly Confidential Matter ")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(spaced) })
	// t.TempDir (the store root below) is created under the process temp
	// directory: TMPDIR on Unix, TMP/TEMP on Windows.
	t.Setenv("TMPDIR", spaced)
	t.Setenv("TMP", spaced)
	t.Setenv("TEMP", spaced)
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	require.Contains(t, f.store.BaseDir(), "Highly Confidential", "SETUP: the store must live under the spaced folder")
	private := filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile)
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if old != nil {
			if err := old(ctx, e); err != nil {
				return err
			}
		}
		if e.SessionID == f.sid && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
			return &fs.PathError{Op: "open", Path: private, Err: fs.ErrPermission}
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryReadHook = old })
	selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
	records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
	require.Len(t, records, 1)
	selfHealRequireLogBase(t, records[0], f.sid, 1)
	require.Equal(t, "storage_error", records[0]["class"])
	require.Equal(t, "open <path>: permission denied", records[0]["cause"])
	cause, _ := records[0]["cause"].(string)
	requireNoPrivateCauseWords(t, cause)
	selfHealRequirePrivateLogs(t, logs, f)
}

// A malformed canonical record beyond the overflow probe, whose timestamp is
// a persisted private string, reaches the real time.Time decoder during the
// full rebuild. The warning names the decoder's class, never the value.
func TestChatHistoryHeal_DecoderFailureLogsOnlyItsClass(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	file, err := os.OpenFile(filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, writeErr := file.WriteString(`{"id":"6a-decoder-canary","timestamp":"` + healCauseCanary + `"}` + "\n")
	require.NoError(t, errors.Join(writeErr, file.Sync(), file.Close()))
	selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
	records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
	require.Len(t, records, 1)
	selfHealRequireLogBase(t, records[0], f.sid, 1)
	require.Equal(t, "corrupt_archive", records[0]["class"])
	require.Equal(t, "decode error (*time.ParseError)", records[0]["cause"])
	require.NotContains(t, logs.text(), healCauseCanary, "the persisted value must not reach any warning")
	selfHealRequirePrivateLogs(t, logs, f)
}

// Free error text (no typed path) that names a spaced store root: the root is
// removed together with the rest of its path, up to the next ": ".
func TestHistoryHealCause_RemovesKnownRootsFromFreeText(t *testing.T) {
	for _, tc := range []struct {
		name, root string
		err        error
		want       string
	}{
		{"unix_root", healCauseUnixDir, errors.New("read index of " + healCauseUnixDir + "/.chat-history/g 2: short read"), "read index of <path>: short read"},
		{"windows_root", healCauseWindowsDir, errors.New(`lock ` + healCauseWindowsDir + `\.chat-history: The process cannot access the file`), "lock <path>: The process cannot access the file"},
		{"root_at_the_end", healCauseUnixDir, errors.New("missing folder " + healCauseUnixDir + "/day one"), "missing folder <path>"},
		{"twice", healCauseUnixDir, errors.New("move " + healCauseUnixDir + "/a: then " + healCauseUnixDir + "/b: failed"), "move <path>: then <path>: failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := historyHealCause(tc.err, tc.root)
			require.Equal(t, tc.want, got)
			requireNoPrivateCauseWords(t, got)
		})
	}
	store := newTestStore(t)
	got := store.historyHealCause(fmt.Errorf("scan %s: interrupted", filepath.Join(store.BaseDir(), "s1", "day one")))
	require.Equal(t, "scan <path>: interrupted", got, "the store passes its own data folder as a known root")
}
