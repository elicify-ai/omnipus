// model_placement.go: the consumption-fence placement of a saved input into
// model order — session-core C-ARCHIVE / U2 Decision B (spec FR-005/FR-008/
// FR-009/FR-023/FR-024).
//
// An input saved ahead of a safe boundary is CHAT-only and already carries its
// prepared, lossless model_message and trusted source under its admitted entry
// id (the ordinary append path). At the moment it is actually consumed, the
// fence calls PlaceModelRef: a model-only `model_ref` effect is appended that
// names the saved record's exact disk address and copies NO body. The saved
// payload is therefore placed once, in model order, without a second
// model-content store.
package session

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// PlaceModelRef appends the body-free model placement for a saved input at the
// consumption fence and returns the placement record's own address.
//
// It resolves and validates the source record first: the source must exist, be a
// same-session accepted payload (the store is bound to one session, so a
// cross-session address simply fails to resolve here) and must NOT itself be a
// model_ref — a reference targets a payload record, never another reference.
// The appended record has view_membership=model, so it never produces a chat
// bubble, and carries only the address triple. The caller applies the existing
// consumption/Stop fence around it; once-ness and ordering are the fence's
// contract (AddressedWindow.ConsumeOnce is the persisted-mark guard that
// enforces one placement per source).
func PlaceModelRef(store *ArchiveDayStore, source ArchiveAddress) (ArchiveAddress, error) {
	src, err := store.ReadAt(source)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("model placement: resolve source: %w", err)
	}
	if src.Type == EntryTypeModelRef {
		return ArchiveAddress{}, fmt.Errorf("model placement: source %s is itself a model_ref; references target payload records", source.EntryID)
	}
	effectID, err := newModelRefID()
	if err != nil {
		return ArchiveAddress{}, err
	}
	rec := ArchiveRecord{
		TranscriptEntry: TranscriptEntry{
			ID:             effectID,
			Type:           EntryTypeModelRef,
			ViewMembership: ViewMembershipModel,
			Timestamp:      store.now().UTC(),
			AgentID:        src.AgentID,
		},
		ModelRef: &ModelRef{
			EntryID:      source.EntryID,
			PartitionKey: source.PartitionKey,
			ByteOffset:   source.ByteOffset,
		},
	}
	return store.Append(rec)
}

// newModelRefID mints the server-assigned effect id of a placement record.
// A ULID keeps it unique and time-ordered like the rest of the store's ids.
func newModelRefID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("session: generate model_ref id: %w", err)
	}
	return "model_ref_" + id.String(), nil
}
