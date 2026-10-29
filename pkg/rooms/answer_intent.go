// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// An agent answer with files is a durable answer intent (#1449 FR-078,
// FR-084, FR-085; TD-3). Its lifecycle:
//
//	accepted   validation passed and every original source was read once,
//	           hashed and copied into private, immutable staging owned by
//	           the intent; the intent records text and, per file, the media
//	           id and Library file name allocated at acceptance.
//	           (publishing: each file is published into both representations
//	           under those identities, create-or-adopt; the per-file outcome
//	           is recorded durably on the same record.)
//	committed  the entry (carrying the intent id) is appended, ownership
//	           passed to the entry, then mirror and mention admission run.
//	rejected   a definitive failure, recorded before the agent is told
//	           "nothing posted"; staged bytes and unreferenced published
//	           files are removed and nothing is ever recreated.
//
// A retryable failure leaves the intent accepted: the agent is told the
// answer is accepted and completing. After a restart every accepted intent is
// completed from the recorded data only; the model is never asked again and
// the room, membership and request are rechecked first.

// StreamIntents is the answer-intent journal stream (latest line per intent
// id wins).
const StreamIntents = "intents"

// Intent states.
const (
	intentAccepted  = "accepted"
	intentCommitted = "committed"
	intentRejected  = "rejected"
)

// ErrAnswerCompleting: the answer was accepted and will be posted; a step
// could not finish now and is completed automatically (FR-078 (5)).
var ErrAnswerCompleting = errors.New("your answer with its files was accepted and is being completed; " +
	"it will appear in the group without another call. Calling answer_request again with the same text " +
	"and the same files completes it now and never posts it twice")

// ErrAnswerFilesRefused: a definitive refusal of an answer's files; nothing
// was posted, copied or passed on.
var ErrAnswerFilesRefused = errors.New("the answer was not posted")

type intentFile struct { // not-wire-format: private answer-intent journal line
	// Ref is a workspace reference posted as it is (no staging).
	Ref string `json:"ref,omitempty"`
	// Identity, SHA256 and Size describe the original source at acceptance.
	Identity string `json:"identity,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Filename string `json:"filename"`
	// MediaID and LibraryName are the identities allocated at acceptance.
	MediaID     string `json:"media_id,omitempty"`
	LibraryName string `json:"library_name,omitempty"`
	// Part is the published (or resolved) part; set once published.
	Part *generated.MediaPart `json:"part,omitempty"`
}

type intentRecord struct { // not-wire-format: private answer-intent journal line
	IntentID  string       `json:"intent_id"`
	RequestID string       `json:"request_id"`
	AgentID   string       `json:"agent_id"`
	SessionID string       `json:"session_id,omitempty"`
	Key       string       `json:"key"`
	Text      string       `json:"text"`
	Files     []intentFile `json:"files"`
	State     string       `json:"state"`
	EntryID   string       `json:"entry_id,omitempty"`
	Reason    string       `json:"reason,omitempty"`
	At        time.Time    `json:"at"`
}

func (in *intentRecord) clone() *intentRecord {
	c := *in
	c.Files = slices.Clone(in.Files)
	return &c
}

// SetStagingDir sets the room service's private staging root for answer
// files (TD-3: never an agent-reachable tree). Empty disables answers with
// files.
func (s *Service) SetStagingDir(dir string) { s.stagingDir = dir }

// DefaultStagingDir is the staging root under an Omnipus home.
func DefaultStagingDir(home string) string { return filepath.Join(home, "entities", "room-staging") }

func (s *Service) intentDir(roomID, intentID string) string {
	return filepath.Join(s.stagingDir, roomID, intentID)
}

// deliverAnswerWithFiles is DeliverAnswer for an answer that carries files.
func (s *Service) deliverAnswerWithFiles(ctx context.Context, a Answer) (AnswerReceipt, error) {
	if len(a.Files) > MaxAttachments {
		return AnswerReceipt{}, fmt.Errorf("%w: an answer carries at most %d files; this one has %d", ErrAnswerFilesRefused, MaxAttachments, len(a.Files))
	}
	if s.stagingDir == "" || s.deps.Media == nil || s.deps.Publisher == nil {
		return AnswerReceipt{}, fmt.Errorf("%w: files cannot be attached to a group answer here", ErrAnswerFilesRefused)
	}
	r, ws, err := s.openRoom(a.RoomID)
	if err != nil {
		if StatusOf(err) == 404 {
			return AnswerReceipt{}, ErrRoomUnavailable
		}
		return AnswerReceipt{}, err
	}
	r.mu.Lock()
	err = s.answerAllowed(r, a)
	name := r.rec.Name
	r.mu.Unlock()
	if err != nil {
		return AnswerReceipt{}, err
	}
	files, tmp, err := s.stageSources(r, a)
	if err != nil {
		return AnswerReceipt{}, err
	}
	r.mu.Lock()
	in, err := s.acceptIntent(r, a, files, tmp)
	r.mu.Unlock()
	removeAll(tmp)
	if err != nil {
		return AnswerReceipt{}, err
	}
	entryID, err := s.completeIntent(ctx, r, ws, in.IntentID)
	if err != nil {
		return AnswerReceipt{AnswerID: entryID, RoomName: name}, err
	}
	return AnswerReceipt{AnswerID: entryID, RoomName: name}, nil
}

// answerAllowed applies recordAnswer's checks. The caller holds r.mu.
func (s *Service) answerAllowed(r *room, a Answer) error {
	d := r.deliveries[a.RequestID]
	if r.deleted || d == nil || d.AgentID != a.Responder.AgentID || a.Responder.WorkspaceID != r.rec.WorkspaceID {
		return ErrRoomUnavailable
	}
	if !slices.Contains(r.rec.Members, d.AgentID) {
		return errors.New(textMemberLeftRoom)
	}
	if d.State == StateCancelled {
		return ErrRoomUnavailable
	}
	return nil
}

// stageSources resolves direct references and reads every other source
// once into a temporary staging directory, hashing it (FR-078 (1), TD-3).
// Any failure is definitive: nothing is staged and nothing is posted.
func (s *Service) stageSources(r *room, a Answer) ([]intentFile, string, error) {
	tmp := filepath.Join(s.stagingDir, r.rec.ID, "tmp-"+uuid.NewString())
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, "", storageError("the answer's files could not be prepared", err)
	}
	out := make([]intentFile, 0, len(a.Files))
	for i, f := range a.Files {
		n := i + 1
		if f.Ref != "" {
			if !isWorkspaceRefOf(f.Ref, r.rec.WorkspaceID) {
				removeAll(tmp)
				return nil, "", fmt.Errorf("%w: file %d is a file of another workspace; only files of this group's workspace can be attached", ErrAnswerFilesRefused, n)
			}
			part, err := s.deps.Media.ResolveAttachment(r.rec.WorkspaceID, f.Ref)
			if err != nil {
				removeAll(tmp)
				return nil, "", fmt.Errorf("%w: file %d: %v", ErrAnswerFilesRefused, n, err)
			}
			out = append(out, intentFile{Ref: f.Ref, Filename: part.Filename, Part: &part})
			continue
		}
		staged, err := stageOne(tmp, strconv.Itoa(n), f)
		if err != nil {
			removeAll(tmp)
			return nil, "", fmt.Errorf("%w: file %d (%s): %v", ErrAnswerFilesRefused, n, f.Filename, err)
		}
		out = append(out, staged)
	}
	return out, tmp, nil
}

func stageOne(dir, name string, f AnswerFile) (intentFile, error) {
	if f.Open == nil || strings.TrimSpace(f.Identity) == "" {
		return intentFile{}, errors.New("the file could not be read")
	}
	if f.Size > MaxAnswerFileBytes {
		return intentFile{}, fmt.Errorf("the file is %d bytes; a file can be at most %d bytes", f.Size, MaxAnswerFileBytes)
	}
	src, err := f.Open()
	if err != nil {
		return intentFile{}, fmt.Errorf("the file could not be read: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return intentFile{}, fmt.Errorf("the file could not be staged: %w", err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, MaxAnswerFileBytes+1))
	syncErr := dst.Sync()
	closeErr := dst.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return intentFile{}, fmt.Errorf("the file could not be staged: %w", err)
	}
	if n > MaxAnswerFileBytes {
		return intentFile{}, fmt.Errorf("the file is larger than %d bytes", MaxAnswerFileBytes)
	}
	filename := strings.TrimSpace(f.Filename)
	if filename == "" {
		filename = filepath.Base(f.Identity)
	}
	return intentFile{Identity: f.Identity, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n, Filename: filename}, nil
}

// answerKey is the attempt identity (FR-022): request, text and the ordered
// original source identities.
func answerKey(requestID, text string, files []intentFile) string {
	h := sha256.New()
	h.Write([]byte(requestID + "\x00" + text))
	for _, f := range files {
		if f.Ref != "" {
			h.Write([]byte("\x00ref:" + f.Ref))
		} else {
			h.Write([]byte("\x00src:" + f.Identity + ":" + f.SHA256))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// acceptIntent records a new intent, or returns the request's latest intent
// with the same key while it is not fully completed (FR-084). The caller
// holds r.mu; tmp is the staging of this call (moved into the new intent).
func (s *Service) acceptIntent(r *room, a Answer, files []intentFile, tmp string) (*intentRecord, error) {
	if err := s.answerAllowed(r, a); err != nil {
		return nil, err
	}
	key := answerKey(a.RequestID, a.Content, files)
	if prior := s.latestIntent(r, a.RequestID); prior != nil && prior.Key == key && !s.intentDone(r, prior) {
		return prior, nil
	}
	in := &intentRecord{
		IntentID: uuid.NewString(), RequestID: a.RequestID, AgentID: a.Responder.AgentID,
		SessionID: a.ResponderSessionID, Key: key, Text: a.Content, State: intentAccepted, At: s.now(),
	}
	for i, f := range files {
		if f.Ref == "" {
			f.MediaID = uuid.NewString()
			f.LibraryName = f.Filename
			files[i] = f
		}
	}
	in.Files = files
	if err := os.Rename(tmp, s.intentDir(r.rec.ID, in.IntentID)); err != nil {
		return nil, storageError("the answer's files could not be kept", err)
	}
	// The intent owns its direct references from acceptance on (FR-078 (1)).
	if err := s.retainRefs(r.rec.WorkspaceID, intentOwner(r.rec.ID, in.IntentID), directRefs(in)); err != nil {
		removeAll(s.intentDir(r.rec.ID, in.IntentID))
		return nil, storageError("the answer's files could not be kept", err)
	}
	if err := s.putIntent(r, in); err != nil {
		s.releaseRefs(r.rec.WorkspaceID, intentOwner(r.rec.ID, in.IntentID), directRefs(in))
		removeAll(s.intentDir(r.rec.ID, in.IntentID))
		return nil, err
	}
	return in, nil
}

func directRefs(in *intentRecord) []string {
	var out []string
	for _, f := range in.Files {
		if f.Ref != "" && !contains(out, f.Ref) {
			out = append(out, f.Ref)
		}
	}
	return out
}

func (s *Service) retainRefs(workspaceID, owner string, refs []string) error {
	for i, ref := range refs {
		if err := s.deps.Media.Retain(workspaceID, ref, owner); err != nil {
			s.releaseRefs(workspaceID, owner, refs[:i])
			return err
		}
	}
	return nil
}

// latestIntent is the request's most recent intent. The caller holds r.mu.
func (s *Service) latestIntent(r *room, requestID string) *intentRecord {
	for i := len(r.intentOrder) - 1; i >= 0; i-- {
		if in := r.intents[r.intentOrder[i]]; in.RequestID == requestID {
			return in
		}
	}
	return nil
}

// intentDone: rejected, or committed with its mirror written (mention
// admission follows the mirror and is resumed by the room on its own).
func (s *Service) intentDone(r *room, in *intentRecord) bool {
	switch in.State {
	case intentRejected:
		return true
	case intentCommitted:
		return r.mirrored[in.EntryID]
	}
	return false
}

// putIntent journals one intent line and applies it. The caller holds r.mu.
func (s *Service) putIntent(r *room, in *intentRecord) error {
	in.At = s.now()
	if err := s.journal.Append(r.rec.ID, StreamIntents, in); err != nil {
		return storageError("the answer could not be recorded", err)
	}
	r.addIntent(in.clone())
	return nil
}

func (r *room) addIntent(in *intentRecord) {
	if _, known := r.intents[in.IntentID]; !known {
		r.intentOrder = append(r.intentOrder, in.IntentID)
	}
	r.intents[in.IntentID] = in
}

// completeIntent drives an accepted intent to committed (or rejected) and
// returns the entry id. A retryable failure returns ErrAnswerCompleting.
func (s *Service) completeIntent(ctx context.Context, r *room, ws WorkspaceInfo, intentID string) (string, error) {
	r.mu.Lock()
	in := r.intents[intentID]
	if in == nil {
		r.mu.Unlock()
		return "", ErrRoomUnavailable
	}
	in = in.clone()
	if in.State == intentRejected {
		r.mu.Unlock()
		return "", fmt.Errorf("%w: %s", ErrAnswerFilesRefused, in.Reason)
	}
	if in.State == intentAccepted {
		if reason := s.intentStale(r, in); reason != "" {
			err := s.rejectIntent(r, in, reason)
			r.mu.Unlock()
			return "", err
		}
	}
	r.mu.Unlock()
	if in.State == intentAccepted {
		if err := s.publishIntentFiles(r, in); err != nil {
			return "", err
		}
	}
	r.mu.Lock()
	se, mentions, err := s.commitIntent(r, ws, in.IntentID)
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := s.mirrorAnswer(r, se); err != nil {
		return se.Entry.Id, fmt.Errorf("%w: %w", ErrMirrorFailed, err)
	}
	s.admitAll(r, ws, mentions)
	removeAll(s.intentDir(r.rec.ID, in.IntentID))
	_ = ctx
	return se.Entry.Id, nil
}

// intentStale is the recovery recheck (FR-078 (6)): the room, the member and
// the request must still be there. The caller holds r.mu.
func (s *Service) intentStale(r *room, in *intentRecord) string {
	d := r.deliveries[in.RequestID]
	switch {
	case r.deleted:
		return "the group is no longer available"
	case d == nil || d.AgentID != in.AgentID:
		return "the request is no longer available"
	case !slices.Contains(r.rec.Members, in.AgentID):
		return "you are no longer a member of this group"
	case d.State == StateCancelled:
		return "the request was cancelled"
	}
	return ""
}

// publishIntentFiles publishes each staged file not yet published, records
// each outcome, and acquires the intent's ownership of it.
func (s *Service) publishIntentFiles(r *room, in *intentRecord) error {
	for i := range in.Files {
		f := in.Files[i]
		if f.Ref != "" || f.Part != nil {
			continue
		}
		part, used, err := s.publishWithNames(r, in, i)
		switch {
		case errors.Is(err, ErrPublishDeleted):
			// D26: the person deleted it after publication; it is never
			// recreated. The part still names it; chats show it unavailable.
			part = generated.MediaPart{
				Type: "file", Url: "/api/v1/media/workspace/" + r.rec.WorkspaceID + "/" + f.MediaID,
				Filename: f.Filename, ContentType: "application/octet-stream",
			}
		case err != nil:
			logRoomError("an answer file could not be published; the answer completes on the next attempt", r.rec.ID, err)
			return ErrAnswerCompleting
		}
		ref := refOfPart(part)
		if err := s.deps.Media.Retain(r.rec.WorkspaceID, ref, intentOwner(r.rec.ID, in.IntentID)); err != nil &&
			!errors.Is(err, ErrPublishDeleted) && !errors.Is(err, ErrAttachmentMissing) {
			logRoomError("a published answer file could not be marked as kept; the answer completes on the next attempt", r.rec.ID, err)
			return ErrAnswerCompleting
		}
		f.Part, f.LibraryName = &part, used
		in.Files[i] = f
		r.mu.Lock()
		err = s.putIntent(r, in)
		r.mu.Unlock()
		if err != nil {
			logRoomError("an answer file's publication could not be recorded; the answer completes on the next attempt", r.rec.ID, err)
			return ErrAnswerCompleting
		}
	}
	return nil
}

// publishWithNames publishes file i, allocating a new Library name (recorded
// before it is written) when the allocated one holds another file.
func (s *Service) publishWithNames(r *room, in *intentRecord, i int) (generated.MediaPart, string, error) {
	const maxNames = 50
	for attempt := 1; attempt <= maxNames; attempt++ {
		f := in.Files[i]
		pf := PublishFile{
			MediaID: f.MediaID, LibraryName: f.LibraryName, Filename: f.Filename,
			StagedPath: filepath.Join(s.intentDir(r.rec.ID, in.IntentID), strconv.Itoa(i+1)), SHA256: f.SHA256,
		}
		part, used, err := s.deps.Publisher.PublishAnswerFile(r.rec.WorkspaceID, pf)
		if !errors.Is(err, ErrLibraryNameTaken) {
			return part, used, err
		}
		ext := filepath.Ext(f.Filename)
		f.LibraryName = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(f.Filename, ext), attempt, ext)
		in.Files[i] = f
		r.mu.Lock()
		perr := s.putIntent(r, in)
		r.mu.Unlock()
		if perr != nil {
			return generated.MediaPart{}, "", perr
		}
	}
	return generated.MediaPart{}, "", fmt.Errorf("no free Library file name for %q", in.Files[i].Filename)
}

// commitIntent appends the intent's entry (once: its id derives from the
// intent), passes ownership to the entry, and records the commit. The caller
// holds r.mu.
func (s *Service) commitIntent(r *room, ws WorkspaceInfo, intentID string) (storedEntry, []string, error) {
	in := r.intents[intentID].clone()
	entryID := deriveID("answer-intent", intentID)
	if idx, ok := r.byEntryID[entryID]; ok {
		se := r.entries[idx]
		if in.State != intentCommitted {
			in.State, in.EntryID = intentCommitted, entryID
			if err := s.putIntent(r, in); err != nil {
				logRoomError("an answer's commit could not be recorded; its entry is already posted", r.rec.ID, err)
			}
		}
		return se, r.byEntry[entryID], nil
	}
	if reason := s.intentStale(r, in); reason != "" {
		return storedEntry{}, nil, s.rejectIntent(r, in, reason)
	}
	d := r.deliveries[in.RequestID]
	parts := make([]generated.MediaPart, 0, len(in.Files))
	for _, f := range in.Files {
		if f.Part == nil {
			return storedEntry{}, nil, ErrAnswerCompleting
		}
		parts = append(parts, *f.Part)
	}
	afterPause := d.Generation < r.rec.Generation
	var addressees []string
	if !afterPause && !r.rec.Paused {
		addressees = agentAddressees(r, ws, in.Text, d.AgentID)
	}
	author := d.AgentID
	se := storedEntry{
		Entry: generated.RoomEntry{
			Id: entryID, RoomId: r.rec.ID, Text: in.Text, Addressees: nonNil(addressees), AfterPause: afterPause,
			Author: generated.RoomEntryAuthor{Kind: "agent", AgentId: &author}, Attachments: parts,
			Timestamp: s.now().Format(time.RFC3339Nano),
		},
		AnswersRequest: in.RequestID, SenderAgentID: d.AgentID, IntentID: intentID,
	}
	if in.SessionID != "" {
		d.SessionID = in.SessionID
	}
	// Acquire before release (FR-078 (3)): the entry owns the files first.
	if err := s.retainParts(r.rec.WorkspaceID, entryOwner(r.rec.ID, entryID), parts); err != nil {
		logRoomError("an answer's files could not be passed to its entry; the answer completes on the next attempt", r.rec.ID, err)
		return storedEntry{}, nil, ErrAnswerCompleting
	}
	ds := s.newDeliveries(r, entryID, addressees, d.AgentID)
	if err := s.appendEntry(r, se, ds); err != nil {
		s.releaseParts(r.rec.WorkspaceID, entryOwner(r.rec.ID, entryID), parts)
		logRoomError("an answer could not be appended; it stays accepted and completes on the next attempt", r.rec.ID, err)
		return storedEntry{}, nil, ErrAnswerCompleting
	}
	s.releaseParts(r.rec.WorkspaceID, intentOwner(r.rec.ID, intentID), parts)
	in.State, in.EntryID = intentCommitted, entryID
	if err := s.putIntent(r, in); err != nil {
		logRoomError("an answer's commit could not be recorded; its entry is posted and the next load completes it", r.rec.ID, err)
	}
	if !terminal(d.State) {
		s.updateState(r, d, StateAnswered, "")
	}
	s.forgetWorking(d.SessionID, d.RequestID)
	return r.entries[r.byEntryID[entryID]], deliveryIDs(ds), nil
}

// rejectIntent records a definitive rejection BEFORE the agent is told, then
// removes staging and the published files nothing else references. The
// caller holds r.mu.
func (s *Service) rejectIntent(r *room, in *intentRecord, reason string) error {
	in = in.clone()
	in.State, in.Reason = intentRejected, reason
	if !r.deleted {
		if err := s.putIntent(r, in); err != nil {
			return err
		}
	}
	owner := intentOwner(r.rec.ID, in.IntentID)
	for _, f := range in.Files {
		switch {
		case f.Ref != "":
			s.releaseRefs(r.rec.WorkspaceID, owner, []string{f.Ref})
		case f.Part != nil:
			s.releaseRefs(r.rec.WorkspaceID, owner, []string{refOfPart(*f.Part)})
			pf := PublishFile{MediaID: f.MediaID, LibraryName: f.LibraryName, Filename: f.Filename, SHA256: f.SHA256}
			if err := s.deps.Publisher.UnpublishAnswerFile(r.rec.WorkspaceID, pf); err != nil {
				logRoomError("a rejected answer's published file could not be removed", r.rec.ID, err)
			}
		}
	}
	removeAll(s.intentDir(r.rec.ID, in.IntentID))
	return fmt.Errorf("%w: %s", ErrAnswerFilesRefused, reason)
}

// resumeIntents completes every accepted intent of a loaded room in the
// background (FR-078 (6)). Nothing here calls the model.
func (s *Service) resumeIntents(r *room, ws WorkspaceInfo) {
	r.mu.Lock()
	var pending []string
	for _, id := range r.intentOrder {
		in := r.intents[id]
		if in.State == intentAccepted || (in.State == intentCommitted && !r.mirrored[in.EntryID]) {
			pending = append(pending, id)
		}
	}
	r.mu.Unlock()
	for _, id := range pending {
		if _, err := s.completeIntent(s.ctx, r, ws, id); err != nil && !errors.Is(err, ErrAnswerFilesRefused) {
			logRoomError("a pending answer could not be completed yet; the next load retries", r.rec.ID, err)
		}
	}
}

// repairIntentEntries lets the text-only repair path skip answers that came
// from an intent (they are matched by intent, not by text, FR-022).
func (e storedEntry) fromIntent() bool { return e.IntentID != "" }

func removeAll(dir string) {
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		logRoomError("an answer staging directory could not be removed", dir, err)
	}
}
