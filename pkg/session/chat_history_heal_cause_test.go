package session

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// historyHealCause is the sanitizer; its oracle is the H1 brief: errno and
// check text survive, every path and file name is replaced, bounded length.
func TestHistoryHealCause_Sanitizes(t *testing.T) {
	long := strings.Repeat("é", chatHistoryHealCauseMax)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "unknown"},
		{"unix_path", &fs.PathError{Op: "open", Path: "/home/u/.omnipus/sessions/s1/transcript.jsonl", Err: fs.ErrPermission}, "open <path>: permission denied"},
		{"windows_path", &fs.PathError{Op: "remove", Path: `C:\Users\u\.omnipus\sessions\s1\.chat-history\generations\g.chi`, Err: errors.New("Access is denied.")}, "remove <path>: Access is denied."},
		{"bare_file_name", errors.New("rename checkpoint.json failed"), "rename <file> failed"},
		{"build_wrapper_unwraps", historyBuildFailure("storage_error", errors.New("no space left on device")), "no space left on device"},
		{"unavailable_keeps_reason", historyUnavailable("index_inconsistent", errors.New("canonical archive truncated")), "index_inconsistent: canonical archive truncated"},
		{"joined_and_newlines", errors.Join(historyBuildFailure("storage_error", errors.New("a\nb")), errors.New("c")), "a b; c"},
		{"class_only_wrapper", &ChatHistoryIndexBuildError{Class: "corrupt_archive"}, "corrupt_archive"},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, historyHealCause(tc.err)) })
	}
	got := historyHealCause(errors.New(long))
	require.True(t, strings.HasSuffix(got, "..."))
	require.LessOrEqual(t, len(got), chatHistoryHealCauseMax+3)
	require.True(t, strings.HasPrefix(long, strings.TrimSuffix(got, "...")), "the cut keeps whole UTF-8 characters")
}
