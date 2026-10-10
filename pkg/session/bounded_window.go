// bounded_window.go: the addressed, content-free model window — session-core
// C-ARCHIVE / U2 Decision C (spec FR-005/FR-006/FR-007).
//
// The window persists ADDRESSES, not content: stable
// (partition_key, byte_offset, entry_id) marks for the start and exclusive end
// tail, the optional original-user anchor, and the exact active model slots and
// their sources. It contains no provider content and is not a second history.
// Reading the model messages seeks directly to the active slots (and, for a
// model_ref, to its referenced source) with ArchiveDayStore.ReadAt, so the work
// is proportional to the active window — never to the evicted prefix, the total
// retained content or the number of archived messages. A record the reader
// cannot resolve is an error, never a silently short history.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ErrAlreadyConsumed is returned when a source address is placed a second time.
// One accepted input gets at most one committed model placement (Decision B).
var ErrAlreadyConsumed = errors.New("archive: source already has a committed model placement")

// WindowProjectionLimit records one retained-but-limited model slot's projection
// state (FR-006): the slot's address, the tool_call_id it belongs to and the
// retained source-run length. It is content-free.
type WindowProjectionLimit struct {
	Slot        ArchiveAddress `json:"slot"`
	ToolCallID  string         `json:"tool_call_id"`
	State       string         `json:"state"`
	SourceRunes int            `json:"source_runes"`
}

// AddressedWindow is the persisted, content-free state of one session's active
// model window over the addressed archive.
type AddressedWindow struct {
	// Start is the first retained record; records before it are evicted and are
	// never read or counted. HasStart gates an empty window.
	Start    ArchiveAddress `json:"start"`
	HasStart bool           `json:"has_start"`
	// End is the exclusive tail: the address of the next record to be appended.
	End    ArchiveAddress `json:"end"`
	HasEnd bool           `json:"has_end"`
	// Anchor is the optional original-user message the window is pinned to,
	// when it lies before Start.
	Anchor *ArchiveAddress `json:"anchor,omitempty"`
	// Model lists the active model slots in MODEL ORDER. A slot is either a
	// payload record carrying a model_message, or a model_ref record naming a
	// saved source placed here at consumption.
	Model []ArchiveAddress `json:"model"`
	// Source lists active source payload addresses the model window draws on.
	Source []ArchiveAddress `json:"source,omitempty"`
	// Excluded lists retained-but-excluded marks (FR-006 rollback effects): the
	// bytes stay on disk and stay recallable, they are only omitted from the
	// model view.
	Excluded []ArchiveAddress `json:"excluded,omitempty"`
	// Limits carries the per-slot projection limits (FR-006).
	Limits []WindowProjectionLimit `json:"limits,omitempty"`
}

// ModelMessages reconstructs the ordered provider messages the model window
// represents, reading ONLY the active model slots (and, for a placement, its
// referenced source). It never decodes the evicted prefix or scans excluded
// stretches. A model slot that resolves no payload, or that resolves to a
// placement whose source is missing, is an error.
func (w AddressedWindow) ModelMessages(store *ArchiveDayStore) ([]providers.Message, error) {
	out := make([]providers.Message, 0, len(w.Model))
	for i, slot := range w.Model {
		rec, err := store.ReadAt(slot)
		if err != nil {
			return nil, fmt.Errorf("window: read model slot %d: %w", i, err)
		}
		msg, err := w.messageForSlot(store, rec, slot)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func (w AddressedWindow) messageForSlot(store *ArchiveDayStore, rec ArchiveRecord, slot ArchiveAddress) (providers.Message, error) {
	if refAddr, ok := rec.ReferenceAddress(); ok {
		src, err := store.ReadAt(refAddr)
		if err != nil {
			return providers.Message{}, fmt.Errorf("window: resolve placement %s: %w", rec.ID, err)
		}
		if src.ModelMessage == nil {
			return providers.Message{}, fmt.Errorf("window: placement %s references %s, which carries no model payload", rec.ID, src.ID)
		}
		return DecodeModelPayload(*src.ModelMessage)
	}
	if rec.ModelMessage == nil {
		return providers.Message{}, fmt.Errorf("window: model slot %s@%d resolves no payload", slot.PartitionKey, slot.ByteOffset)
	}
	return DecodeModelPayload(*rec.ModelMessage)
}

// ConsumeOnce places a saved source into model order exactly once. If the source
// is already among the window's placed sources it refuses with
// ErrAlreadyConsumed; otherwise it appends the body-free placement and returns
// the updated window (Model extended by the placement's address). The
// already-placed set is derived from the persisted marks — no separate counter
// or queue.
func (w AddressedWindow) ConsumeOnce(store *ArchiveDayStore, source ArchiveAddress) (ArchiveAddress, AddressedWindow, error) {
	placed, err := w.placedSources(store)
	if err != nil {
		return ArchiveAddress{}, w, err
	}
	if _, ok := placed[source]; ok {
		return ArchiveAddress{}, w, ErrAlreadyConsumed
	}
	addr, err := PlaceModelRef(store, source)
	if err != nil {
		return ArchiveAddress{}, w, err
	}
	out := w
	out.Model = append(append([]ArchiveAddress(nil), w.Model...), addr)
	return addr, out, nil
}

// placedSources resolves the window's model slots to the source addresses their
// placements reference.
func (w AddressedWindow) placedSources(store *ArchiveDayStore) (map[ArchiveAddress]bool, error) {
	placed := make(map[ArchiveAddress]bool)
	for _, slot := range w.Model {
		rec, err := store.ReadAt(slot)
		if err != nil {
			return nil, fmt.Errorf("window: read model slot %s@%d: %w", slot.PartitionKey, slot.ByteOffset, err)
		}
		if ref, ok := rec.ReferenceAddress(); ok {
			placed[ref] = true
		}
	}
	return placed, nil
}

// ModelSources returns the source addresses the window's placements reference,
// in model order (a payload slot is itself a source).
func (w AddressedWindow) ModelSources(store *ArchiveDayStore) ([]ArchiveAddress, error) {
	out := make([]ArchiveAddress, 0, len(w.Model))
	for _, slot := range w.Model {
		rec, err := store.ReadAt(slot)
		if err != nil {
			return nil, fmt.Errorf("window: read model slot: %w", err)
		}
		if ref, ok := rec.ReferenceAddress(); ok {
			out = append(out, ref)
			continue
		}
		out = append(out, slot)
	}
	return out, nil
}

// archiveWindowFile holds the persisted, content-free window state next to the
// archive partitions (Decision C: the window state lives under the session
// directory and carries addresses only).
const archiveWindowFile = "window.json"

// SaveWindow persists the window state atomically. The state is addresses only,
// so the file contains no provider content.
func (s *ArchiveDayStore) SaveWindow(w AddressedWindow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return fmt.Errorf("archive: create dir: %w", err)
	}
	data, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("archive: encode window: %w", err)
	}
	tmp := filepath.Join(s.dir(), archiveWindowFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("archive: write window: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir(), archiveWindowFile)); err != nil {
		return fmt.Errorf("archive: rename window: %w", err)
	}
	return nil
}

// LoadWindow reads the persisted window state. found is false when no window has
// been saved yet (a fresh session); a present-but-unreadable window is an error,
// never a silent empty window.
func (s *ArchiveDayStore) LoadWindow() (w AddressedWindow, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.dir(), archiveWindowFile))
	if errors.Is(err, os.ErrNotExist) {
		return AddressedWindow{}, false, nil
	}
	if err != nil {
		return AddressedWindow{}, false, fmt.Errorf("archive: read window: %w", err)
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return AddressedWindow{}, false, fmt.Errorf("archive: decode window: %w", err)
	}
	return w, true, nil
}
