package rooms

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type closeFaultFile struct {
	*os.File
	closeErr, syncErr error
	closed            int
}

func (f *closeFaultFile) Close() error {
	f.closed++
	return errors.Join(f.File.Close(), f.closeErr)
}
func (f *closeFaultFile) Sync() error { return errors.Join(f.File.Sync(), f.syncErr) }

// Founder item K / CodeQL #2848: close is part of durable repair, not a
// best-effort cleanup. Both earlier I/O errors and the close error survive.
func TestRoomsFix2_JournalRepairReportsCloseError(t *testing.T) {
	closeErr := errors.New("injected repair close failure")
	syncErr := errors.New("injected repair sync failure")
	for _, tc := range []struct {
		name              string
		data              []byte
		closeErr, syncErr error
	}{
		{"valid_tail_close", []byte(`{"ok":true}`), closeErr, nil},
		{"torn_tail_close", []byte("{}\n{broken"), closeErr, nil},
		{"sync_and_close", []byte(`{}`), closeErr, syncErr},
		{"healthy", []byte(`{}`), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "entries.jsonl")
			require.NoError(t, os.WriteFile(path, tc.data, 0o600))
			file, err := os.OpenFile(path, os.O_RDWR, 0o600)
			require.NoError(t, err)
			f := &closeFaultFile{File: file, closeErr: tc.closeErr, syncErr: tc.syncErr}
			err = repairJournalTail(f, tc.data)
			require.Equal(t, 1, f.closed, "instrument: close must really run exactly once")
			if tc.closeErr != nil {
				require.ErrorIs(t, err, tc.closeErr, "repair may not report success after a failed close")
			} else {
				require.NoError(t, err)
			}
			if tc.syncErr != nil {
				require.ErrorIs(t, err, tc.syncErr, "close must not overwrite an earlier failure")
			}
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			want := append(append([]byte(nil), tc.data...), '\n')
			if tc.name == "torn_tail_close" {
				want = []byte("{}\n")
			}
			require.Equal(t, want, data, "the real file repair still completes or truncates its tail")
		})
	}
}
