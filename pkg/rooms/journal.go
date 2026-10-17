// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Journal streams of one room.
const (
	StreamEntries     = "entries"
	StreamDeliveries  = "deliveries"
	StreamMirrors     = "mirrors"
	StreamTurns       = "turns"
	StreamCostGaps    = "cost_gaps"
	StreamSettlements = "settlements"
)

// Journal is every durable write and read of the rooms (the "post-back"
// seam of FR-216, BDD-17.32): the room acknowledgement of an admission, the
// answer append, the mirror marker and each state change are Journal calls,
// so a tester can fault-inject any of them by wrapping the file journal.
//
// Append writes one JSON line durably (fsync) to a stream; WriteRecord
// replaces the room record atomically. Read returns a stream's complete
// lines in order (a torn last line - an append cut short - is not a record);
// an unreadable middle line is an error, never skipped.
type Journal interface {
	Create(roomID string) error
	WriteRecord(roomID string, record any) error
	ReadRecord(roomID string, dst any) error
	Append(roomID, stream string, record any) error
	Read(roomID, stream string) ([][]byte, error)
	List() ([]string, error)
	Remove(roomID string) error
}

// FileJournal is the production Journal rooted at a directory
// ($OMNIPUS_HOME/entities/rooms).
type FileJournal struct {
	dir string
	mu  sync.Mutex
}

// NewFileJournal roots the journal at dir.
func NewFileJournal(dir string) *FileJournal { return &FileJournal{dir: dir} }

// DefaultDir is the rooms directory under an Omnipus home.
func DefaultDir(home string) string { return filepath.Join(home, "entities", "rooms") }

func (j *FileJournal) roomDir(roomID string) (string, error) {
	if !validID(roomID) {
		return "", fmt.Errorf("rooms: invalid room id %q", roomID)
	}
	return filepath.Join(j.dir, roomID), nil
}

// validID accepts the ids this package mints (uuids) and refuses anything
// that could escape the directory.
func validID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

// Create makes the room directory.
func (j *FileJournal) Create(roomID string) error {
	d, err := j.roomDir(roomID)
	if err != nil {
		return err
	}
	return os.MkdirAll(d, 0o700)
}

// WriteRecord replaces room.json atomically.
func (j *FileJournal) WriteRecord(roomID string, record any) error {
	d, err := j.roomDir(roomID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("rooms: marshal record: %w", err)
	}
	return fileutil.WriteFileAtomic(filepath.Join(d, "room.json"), data, 0o600)
}

// ReadRecord reads room.json into dst.
func (j *FileJournal) ReadRecord(roomID string, dst any) error {
	d, err := j.roomDir(roomID)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(d, "room.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("rooms: room %s record is unreadable: %w", roomID, err)
	}
	return nil
}

// Append writes one durable JSON line, first completing or cutting a torn
// tail so the new line never glues onto a partial one.
func (j *FileJournal) Append(roomID, stream string, record any) error {
	d, err := j.roomDir(roomID)
	if err != nil {
		return err
	}
	p := filepath.Join(d, stream+".jsonl")
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := repairTornTail(p); err != nil {
		return err
	}
	return fileutil.AppendJSONLSync(p, record)
}

// Read returns the complete lines of a stream (nil when it does not exist).
func (j *FileJournal) Read(roomID, stream string) ([][]byte, error) {
	d, err := j.roomDir(roomID)
	if err != nil {
		return nil, err
	}
	j.mu.Lock()
	data, err := os.ReadFile(filepath.Join(d, stream+".jsonl"))
	j.mu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: read %s: %w", stream, err)
	}
	raw := bytes.Split(data, []byte{'\n'})
	out := make([][]byte, 0, len(raw))
	for i, line := range raw {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			if i == len(raw)-1 {
				continue // a torn append that was never acknowledged
			}
			return nil, fmt.Errorf("rooms: room %s %s journal line %d is unreadable", roomID, stream, i+1)
		}
		out = append(out, line)
	}
	return out, nil
}

// List returns the ids of the stored rooms.
func (j *FileJournal) List() ([]string, error) {
	entries, err := os.ReadDir(j.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: list: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// Remove deletes a room's directory.
func (j *FileJournal) Remove(roomID string) error {
	d, err := j.roomDir(roomID)
	if err != nil {
		return err
	}
	return os.RemoveAll(d)
}

func repairTornTail(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("rooms: read journal: %w", err)
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("rooms: open journal: %w", err)
	}
	return repairJournalTail(f, data)
}

// journalRepairFile is the file edge of tail repair; tests can force a close
// failure without mocking the repair algorithm or the journal.
type journalRepairFile interface {
	WriteAt(data []byte, offset int64) (int, error)
	Truncate(size int64) error
	Sync() error
	Close() error
}

func repairJournalTail(f journalRepairFile, data []byte) (repairErr error) {
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			repairErr = errors.Join(repairErr, fmt.Errorf("rooms: close repaired journal: %w", closeErr))
		}
	}()
	cut := bytes.LastIndexByte(data, '\n') + 1
	if json.Valid(data[cut:]) {
		if _, err := f.WriteAt([]byte{'\n'}, int64(len(data))); err != nil {
			return fmt.Errorf("rooms: complete journal line: %w", err)
		}
	} else if err := f.Truncate(int64(cut)); err != nil {
		return fmt.Errorf("rooms: cut torn journal line: %w", err)
	}
	return f.Sync()
}
