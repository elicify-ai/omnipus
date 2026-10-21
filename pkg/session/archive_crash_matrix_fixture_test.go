package session

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

const archiveMatrixInputID = "saved-input-matrix-parent"
const archiveMatrixBody = "old helper report consumed exactly once"
const archiveMatrixProbeID = "matrix-new-probe"

type archiveMatrixFixture struct {
	store      *UnifiedStore
	archive    *ArchiveDayStore
	sid        string
	day        time.Time
	oldDay     string
	nextDay    string
	source     ArchiveAddress
	placement  ArchiveAddress
	sourceRaw  []byte
	placedRaw  []byte
	oldArchive []byte
}

func newArchiveMatrixFixture(t *testing.T) *archiveMatrixFixture {
	t.Helper()
	f := &archiveMatrixFixture{store: newTestStore(t), day: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	setArchiveDay(f.store, &f.day)
	f.oldDay, f.nextDay = f.day.Format(transcriptDayLayout), f.day.AddDate(0, 0, 1).Format(transcriptDayLayout)
	meta, err := f.store.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	f.sid = meta.ID
	f.source, err = f.store.SaveChatInput(f.sid, archiveMatrixInputID, "agent", archiveMatrixBody)
	require.NoError(t, err)
	_, _, err = f.store.PlaceSavedInput(context.Background(), f.sid, f.source)
	require.NoError(t, err)
	f.archive, err = f.store.archiveStore(f.sid)
	require.NoError(t, err)
	rows, err := f.archive.readOrdinalRows(0, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	f.placement = rows[0].Slot
	f.sourceRaw, _, err = f.archive.ReadAtRaw(f.source)
	require.NoError(t, err)
	f.placedRaw, _, err = f.archive.ReadAtRaw(f.placement)
	require.NoError(t, err)
	f.oldArchive, err = os.ReadFile(filepath.Join(f.dir(), archiveCurrentFile))
	require.NoError(t, err)
	_, err = os.Stat(f.store.savedInputIndexPath(f.sid))
	require.ErrorIs(t, err, fs.ErrNotExist, "SETUP: old report really was consumed and retired")
	return f
}

func (f *archiveMatrixFixture) dir() string { return filepath.Join(f.store.BaseDir(), f.sid) }

func (f *archiveMatrixFixture) runCut(t *testing.T, cut archiveMatrixCut, sameDay bool) []string {
	t.Helper()
	if !sameDay {
		f.day = f.day.AddDate(0, 0, 1)
	}
	message := providers.Message{Role: "assistant", Content: "new probe retained across interruption"}
	payload, err := EncodeModelPayload(message)
	require.NoError(t, err)
	rec := ArchiveRecord{TranscriptEntry: TranscriptEntry{ID: archiveMatrixProbeID, Role: message.Role,
		Content: message.Content, Timestamp: f.day, ViewMembership: ViewMembershipModel}, ModelMessage: &payload}
	crash := &struct{ step string }{cut.step}
	var trace []string
	stopped := false
	f.archive.onWriteStep = func(step string) {
		trace = append(trace, step)
		if cut.step != "" && step == cut.step && !stopped {
			stopped = true
			panic(crash)
		}
	}
	func() {
		defer func() {
			if caught := recover(); caught != nil && caught != crash {
				panic(caught)
			}
		}()
		_, _, err = f.archive.AppendIndexed(rec) // the real writer; no substitute I/O
	}()
	f.archive.onWriteStep = nil
	require.NoError(t, err)
	require.Equal(t, cut.step != "", stopped, "instrument: requested real writer step was reached")
	return trace
}

func (f *archiveMatrixFixture) assertDiskPhase(t *testing.T, phase string) {
	t.Helper()
	mark, err := os.ReadFile(filepath.Join(f.dir(), archiveDayMarkFile))
	require.NoError(t, err)
	wantMark := f.nextDay
	if phase == "old" || phase == "renamed" {
		wantMark = f.oldDay
	}
	require.Equal(t, wantMark+"\n", string(mark))
	current, currentErr := os.ReadFile(filepath.Join(f.dir(), archiveCurrentFile))
	if phase == "renamed" || phase == "marked" {
		require.ErrorIs(t, currentErr, fs.ErrNotExist)
	} else {
		require.NoError(t, currentErr)
		if phase == "old" {
			require.Equal(t, f.oldArchive, current)
		} else if phase == "created" {
			require.Empty(t, current)
		} else {
			require.NotEmpty(t, current)
		}
	}
	rolled, rolledErr := os.ReadFile(filepath.Join(f.dir(), f.oldDay+".jsonl"))
	if phase == "old" {
		require.ErrorIs(t, rolledErr, fs.ErrNotExist)
	} else {
		require.NoError(t, rolledErr)
		require.Equal(t, f.oldArchive, rolled)
	}
}

func (f *archiveMatrixFixture) constructAtomicEquivalent(t *testing.T, state string) string {
	t.Helper()
	if state == "old_marker_with_stray_temp" {
		// Constructed equivalent of the shared atomic helper's pre-rename
		// internals, NOT a claim of injection inside WriteFileAtomic.
		require.NoError(t, os.WriteFile(filepath.Join(f.dir(), ".tmp-matrix-marker"), []byte(f.nextDay+"\n"), 0o600))
		return "renamed"
	}
	require.Equal(t, "new_marker_without_current", state)
	// Constructed equivalent of the atomic helper's post-rename result.
	require.NoError(t, os.WriteFile(filepath.Join(f.dir(), archiveDayMarkFile), []byte(f.nextDay+"\n"), 0o600))
	return "marked"
}

func (f *archiveMatrixFixture) constructLoss(t *testing.T, loss string) {
	t.Helper()
	rolled := filepath.Join(f.dir(), f.oldDay+".jsonl")
	switch loss {
	case "missing_renamed_partition":
		require.NoError(t, os.Remove(rolled))
	case "renamed_record_identity_changed":
		broken := bytes.Replace(f.oldArchive, []byte(archiveMatrixInputID), []byte("saved-input-matrix-parenX"), 1)
		require.Len(t, broken, len(f.oldArchive), "keep every published offset stable while damaging identity")
		require.NotEqual(t, f.oldArchive, broken)
		require.NoError(t, os.WriteFile(rolled, broken, 0o600))
	case "ambiguous_current_and_dated":
		require.NoError(t, os.WriteFile(filepath.Join(f.dir(), archiveCurrentFile), f.oldArchive, 0o600))
	default:
		t.Fatalf("unknown deliberate loss case %q", loss)
	}
}

func archiveMatrixSnapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := make(map[string][]byte)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") && name != ordinalRowsFile && name != archiveDayMarkFile &&
			name != savedInputIndexFile && !strings.HasPrefix(name, ".tmp-") {
			continue
		}
		out[name], err = os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
	}
	return out
}
