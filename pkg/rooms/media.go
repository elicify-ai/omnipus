// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Group files (#1449 D3, D19, D25, D26; FR-016 to FR-026, FR-076).
//
// A group file is a workspace media library file, stored exactly like a
// normal chat upload: the chat copy in workspaces/<id>/media/ (the
// media://workspace/<id>/<uuid> reference entries carry) and the Library file
// under the workspace's .library folder. The room keeps only references. It
// never deletes a file (D19): deleting a group removes its journals, and only
// an explicit Library deletion removes a file (D26).

// MaxGroupMessageBytes is the one group message size limit (D24, D27): a
// person's message and an agent's answer are at most this many UTF-8 bytes.
// The operator setting session_messaging.steer_body can lower it, never raise
// it. contracts/components/schemas/RoomMessageRequest.yaml and RoomEntry.yaml
// state the same maximum (T-55).
const MaxGroupMessageBytes = 65536

// MaxAttachments is the most files one message or answer carries (FR-008).
const MaxAttachments = 16

// MaxAnswerFileBytes is the largest file an agent's answer may carry (TD-3):
// the same 100 MB per part as POST /upload.
const MaxAnswerFileBytes = int64(100 << 20)

// Attachment resolution failures. Every one refuses the whole message (422).
var (
	// ErrAttachmentNotWorkspaceFile: the reference is not a
	// media://workspace/<this room's workspace>/<id> reference.
	ErrAttachmentNotWorkspaceFile = errors.New("not a file of this group's workspace")
	// ErrAttachmentMissing: no such file in the workspace library.
	ErrAttachmentMissing = errors.New("the file does not exist")
	// ErrAttachmentStranded: the file's bytes are unreachable.
	ErrAttachmentStranded = errors.New("the file is in an inconsistent state")
	// ErrAttachmentOtherWorkspace: a file of another workspace.
	ErrAttachmentOtherWorkspace = errors.New("the file belongs to another workspace")
	// ErrFilesUnavailable: the workspace file library cannot be reached now
	// (503); nothing was posted.
	ErrFilesUnavailable = errors.New("the workspace file library is not available")
)

// Media is the workspace media library as the rooms use it. It is optional:
// a Service without it refuses every message or answer that carries files,
// and keeps no retention owners.
type Media interface {
	// ResolveAttachment resolves ref as a file of workspaceID and returns the
	// part an entry stores. A failure wraps one of the ErrAttachment errors
	// above, or is a server-side fault.
	ResolveAttachment(workspaceID, ref string) (generated.MediaPart, error)
	// Retain records owner as a persistent retention owner of the file, so
	// the orphan cleanup never removes it (FR-076). Idempotent per (file,
	// owner). Release drops owner (no-op when absent).
	Retain(workspaceID, ref, owner string) error
	Release(workspaceID, ref, owner string) error
}

// AnswerFile is one file an agent's answer carries (FR-020, FR-078).
type AnswerFile struct {
	// Ref is set for a workspace reference of the room's workspace: it is
	// posted as it is. Otherwise Identity names the original source (a path
	// or a reference the agent's own chat can send) and Open reads its bytes
	// once, at acceptance.
	Ref      string
	Identity string
	Filename string
	Size     int64
	Open     func() (io.ReadCloser, error)
}

// PublishFile is one staged answer file to publish under identities that
// were allocated when the answer was accepted (FR-078 (1), (2)).
type PublishFile struct {
	MediaID     string // preallocated chat-copy id (uuid)
	LibraryName string // preallocated Library file name
	Filename    string
	StagedPath  string // immutable staged original bytes
	SHA256      string
}

// Publisher publishes staged answer files into both representations with
// create-or-adopt semantics: repeating a step never creates a second
// artifact, and a file the person deleted explicitly is never recreated.
type Publisher interface {
	// PublishAnswerFile returns the published part and the Library file name
	// actually used (it may differ from f.LibraryName when that name was
	// taken by another file; the caller records it before retrying).
	PublishAnswerFile(workspaceID string, f PublishFile) (generated.MediaPart, string, error)
	// UnpublishAnswerFile removes a published file of a rejected answer when
	// nothing else references it.
	UnpublishAnswerFile(workspaceID string, f PublishFile) error
}

// ErrPublishDeleted: the file was deleted explicitly in the Library after it
// was published; it is not recreated (D26).
var ErrPublishDeleted = errors.New("the file was deleted in the Library")

// ErrLibraryNameTaken: the preallocated Library file name holds another file;
// the caller allocates a new name, records it, and publishes again.
var ErrLibraryNameTaken = errors.New("the Library file name is taken")

// bodyLimit is the effective group text limit: min(steer_body, 65,536)
// bytes, read at submission time (FR-074). An unset or invalid setting is the
// default 65,536.
func (s *Service) bodyLimit() int {
	_, body := s.deps.Limits()
	if body <= 0 || body > MaxGroupMessageBytes {
		return MaxGroupMessageBytes
	}
	return body
}

// resolveMessageMedia resolves every reference of a person's message before
// anything is recorded (FR-018). Any failure refuses the whole message (422).
func (s *Service) resolveMessageMedia(workspaceID string, refs []string) ([]generated.MediaPart, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if s.deps.Media == nil {
		return nil, unprocessable("files cannot be attached here: the workspace file library is not available")
	}
	parts := make([]generated.MediaPart, 0, len(refs))
	for i, ref := range refs {
		if !isWorkspaceRefOf(ref, workspaceID) {
			return nil, unprocessable("attachment %d is %s; nothing was posted", i+1, ErrAttachmentNotWorkspaceFile)
		}
		part, err := s.deps.Media.ResolveAttachment(workspaceID, ref)
		if err != nil {
			return nil, attachmentRefusal(i+1, err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func attachmentRefusal(n int, err error) error {
	if errors.Is(err, ErrFilesUnavailable) {
		return &Error{Status: 503, Msg: "the files cannot be checked now: " + ErrFilesUnavailable.Error() + "; nothing was posted", cause: err}
	}
	for _, known := range []error{ErrAttachmentNotWorkspaceFile, ErrAttachmentMissing, ErrAttachmentStranded, ErrAttachmentOtherWorkspace} {
		if errors.Is(err, known) {
			return unprocessable("attachment %d: %s; nothing was posted", n, known)
		}
	}
	return storageError("the attachments could not be checked", err)
}

// isWorkspaceRefOf reports whether ref is media://workspace/<workspaceID>/<id>
// with a single, non-empty id segment.
func isWorkspaceRefOf(ref, workspaceID string) bool {
	rest, ok := strings.CutPrefix(ref, "media://workspace/"+workspaceID+"/")
	return ok && workspaceID != "" && rest != "" && !strings.ContainsAny(rest, `/\`) && !strings.Contains(rest, "..")
}

// refOfPart recovers the media reference from a stored part's URL
// (/api/v1/media/workspace/<ws>/<id>).
func refOfPart(p generated.MediaPart) string {
	if rest, ok := strings.CutPrefix(p.Url, "/api/v1/media/workspace/"); ok {
		return "media://workspace/" + rest
	}
	return ""
}

// entryOwner is the retention owner of one entry's attachments.
func entryOwner(roomID, entryID string) string {
	return "room:" + roomID + ":entry:" + entryID
}

// intentOwner is the retention owner of a pending answer intent's files.
func intentOwner(roomID, intentID string) string {
	return "room:" + roomID + ":intent:" + intentID
}

// retainParts acquires owner on every part (once per reference, TD-4). On a
// failure it releases what it acquired and returns the error.
func (s *Service) retainParts(workspaceID, owner string, parts []generated.MediaPart) error {
	if s.deps.Media == nil || len(parts) == 0 {
		return nil
	}
	var done []string
	for _, p := range parts {
		ref := refOfPart(p)
		if ref == "" || contains(done, ref) {
			continue
		}
		if err := s.deps.Media.Retain(workspaceID, ref, owner); err != nil {
			if errors.Is(err, ErrAttachmentMissing) {
				continue // deleted explicitly (D26): nothing left to keep
			}
			s.releaseRefs(workspaceID, owner, done)
			return fmt.Errorf("retain %s: %w", ref, err)
		}
		done = append(done, ref)
	}
	return nil
}

func (s *Service) releaseParts(workspaceID, owner string, parts []generated.MediaPart) {
	refs := make([]string, 0, len(parts))
	for _, p := range parts {
		if ref := refOfPart(p); ref != "" && !contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	s.releaseRefs(workspaceID, owner, refs)
}

func (s *Service) releaseRefs(workspaceID, owner string, refs []string) {
	if s.deps.Media == nil {
		return
	}
	for _, ref := range refs {
		if err := s.deps.Media.Release(workspaceID, ref, owner); err != nil {
			logRoomError("a retention owner could not be released; the file is kept longer than needed", owner, err)
		}
	}
}

// repairRetention re-acquires the retention owner of every stored entry with
// attachments (FR-076: a restart repairs a missed acquisition). A failure is
// logged and retried on the next load; it never makes the room unreadable.
// The caller holds r.mu.
func (s *Service) repairRetention(r *room) {
	if s.deps.Media == nil {
		return
	}
	for _, e := range r.entries {
		if len(e.Entry.Attachments) == 0 {
			continue
		}
		if err := s.retainParts(r.rec.WorkspaceID, entryOwner(r.rec.ID, e.Entry.Id), e.Entry.Attachments); err != nil {
			logRoomError("an entry's files could not be marked as kept; the next load retries", r.rec.ID, err)
		}
	}
}

// attachmentNames lists an entry's file names for the quoted group context:
// earlier files are named, never handed to the model again (FR-082).
func attachmentNames(parts []generated.MediaPart) string {
	if len(parts) == 0 {
		return ""
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		names = append(names, p.Filename)
	}
	return " [attached: " + strings.Join(names, ", ") + "]"
}

// partRefs returns the media references of parts, in order (one per part).
func partRefs(parts []generated.MediaPart) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if ref := refOfPart(p); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
