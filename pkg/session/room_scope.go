package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// not-wire-format: server-owned room causality and retirement metadata.
type RoomRequestRef struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
}

// RoomScopeRef points to an immutable, content-free request-ID set.
// not-wire-format: never a gateway or model-authored permission.
type RoomScopeRef struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
}

// not-wire-format: immutable source-to-execution correlation.
type RoomRequestRetirement struct {
	Request   RoomRequestRef    `json:"request"`
	Input     ArchiveAddress    `json:"input"`
	Execution ExecutionIdentity `json:"execution"`
}

func safeRoomMetadataPart(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 255 && !strings.ContainsAny(id, `/\\`)
}

func roomMetadataPath(base, owner, kind, id string) (string, error) {
	if !safeRoomMetadataPart(owner) || !safeRoomMetadataPart(id) {
		return "", fmt.Errorf("room context: invalid metadata identity")
	}
	return filepath.Join(base, owner, kind, id+".json"), nil
}

func SaveRoomRequestScope(base, owner string, refs []RoomRequestRef) (*RoomScopeRef, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	refs = append([]RoomRequestRef(nil), refs...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].SessionID == refs[j].SessionID {
			return refs[i].RequestID < refs[j].RequestID
		}
		return refs[i].SessionID < refs[j].SessionID
	})
	unique := refs[:0]
	for _, r := range refs {
		if !safeRoomMetadataPart(r.SessionID) || !safeRoomMetadataPart(r.RequestID) {
			return nil, fmt.Errorf("room context: invalid request correlation")
		}
		if len(unique) == 0 || unique[len(unique)-1] != r {
			unique = append(unique, r)
		}
	}
	data, err := json.Marshal(unique)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	id := hex.EncodeToString(hash[:])
	path, err := roomMetadataPath(base, owner, "room_scopes", id)
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(path)
	if err == nil {
		if string(old) != string(data) {
			return nil, fmt.Errorf("room context: scope identity conflicts")
		}
		return &RoomScopeRef{SessionID: owner, ID: id}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := fileutil.WriteFileAtomicSyncDir(path, data, 0o600); err != nil {
		return nil, err
	}
	return &RoomScopeRef{SessionID: owner, ID: id}, nil
}

func ReadRoomRequestScope(base string, ref *RoomScopeRef) ([]RoomRequestRef, error) {
	if ref == nil {
		return nil, nil
	}
	path, err := roomMetadataPath(base, ref.SessionID, "room_scopes", ref.ID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != ref.ID {
		return nil, fmt.Errorf("room context: corrupt request scope")
	}
	var refs []RoomRequestRef
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, err
	}
	for _, r := range refs {
		if !safeRoomMetadataPart(r.SessionID) || !safeRoomMetadataPart(r.RequestID) {
			return nil, fmt.Errorf("room context: invalid scope reference")
		}
	}
	return refs, nil
}

func RetireRoomRequest(base string, retirement RoomRequestRetirement) error {
	if retirement.Input.EntryID == "" || retirement.Execution.RunID == "" || retirement.Execution.BootSeq == 0 {
		return fmt.Errorf("context_repair_required: exact consumed room input correlation is missing")
	}
	path, err := roomMetadataPath(base, retirement.Request.SessionID, "room_retirements", retirement.Request.RequestID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(retirement)
	if err != nil {
		return err
	}
	if old, readErr := os.ReadFile(path); readErr == nil {
		if string(old) != string(data) {
			return fmt.Errorf("context_repair_required: retirement identity conflicts")
		}
		return nil
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return readErr
	}
	return fileutil.WriteFileAtomicSyncDir(path, data, 0o600)
}

func RoomRequestRetired(base string, ref RoomRequestRef) (bool, error) {
	path, err := roomMetadataPath(base, ref.SessionID, "room_retirements", ref.RequestID)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var retired RoomRequestRetirement
	if err := json.Unmarshal(data, &retired); err != nil {
		return false, err
	}
	if retired.Request != ref || retired.Input.EntryID == "" || retired.Execution.RunID == "" {
		return false, fmt.Errorf("context_repair_required: corrupt room retirement")
	}
	return true, nil
}
