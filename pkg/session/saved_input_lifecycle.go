package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const savedInputLifecycleFile = "saved_input_lifecycle.json"

var ErrSavedInputExpired = errors.New("helper report expired undelivered by retention")

// SavedInputExpiry identifies an authorized terminal loss, not a missing-file
// inference. It is an internal error, never a gateway wire type.
type SavedInputExpiry struct {
	SessionID string
	InputID   string
	ReceiptID string
	Source    ArchiveAddress
	ExpiredAt time.Time
}

func (e *SavedInputExpiry) Error() string {
	return fmt.Sprintf("%s: input %q in session %q at %s", ErrSavedInputExpired, e.InputID, e.SessionID, e.ExpiredAt.Format(time.RFC3339Nano))
}
func (e *SavedInputExpiry) Unwrap() error { return ErrSavedInputExpired }

// savedInputLifecycle is versioned, content-free session control metadata.
// not-wire-format: private persistence, never read by the SPA.
type savedInputLifecycle struct {
	Version        int                             `json:"version"`
	SessionID      string                          `json:"session_id"`
	Deletions      map[string]savedInputDeletion   `json:"deletions"`
	ExpiredReports map[string]savedInputExpiration `json:"expired_reports"`
	PollConsumed   map[string]savedInputPoll       `json:"poll_consumed"`
	Notices        map[string]savedInputNotice     `json:"notices"`
}

// not-wire-format: exact original file incarnation plus content-free row tuples.
type savedInputDeletion struct {
	ID                   string           `json:"id"`
	PartitionKey         string           `json:"partition_key"`
	FileName             string           `json:"file_name"`
	SizeBytes            int64            `json:"size_bytes"`
	MtimeUnixNano        int64            `json:"mtime_unix_nano"`
	SHA256               string           `json:"sha256"`
	RetentionDays        int              `json:"retention_days"`
	CutoffUTC            time.Time        `json:"cutoff_utc"`
	CommittedAt          time.Time        `json:"committed_at"`
	RetiredModelRows     []ordinalRow     `json:"retired_model_rows"`
	RetiredNoticeSources []ArchiveAddress `json:"retired_notice_sources,omitempty"`
	Unlinked             bool             `json:"unlinked"`
}

// not-wire-format: authorized loss of one immutable input, not its contents.
type savedInputExpiration struct {
	ReceiptID  string         `json:"receipt_id"`
	InputID    string         `json:"input_id"`
	Source     ArchiveAddress `json:"source"`
	SavedSeq   int64          `json:"saved_seq"`
	DeletionID string         `json:"deletion_id"`
	ExpiredAt  time.Time      `json:"expired_at"`
	NoticeID   string         `json:"notice_id"`
}

// not-wire-format: the final-read claim survives expiry of its chat marker.
type savedInputPoll struct {
	InputID       string          `json:"input_id"`
	ConsumedAt    time.Time       `json:"consumed_at"`
	MarkerID      string          `json:"marker_id"`
	MarkerAddress *ArchiveAddress `json:"marker_address,omitempty"`
	Source        *ArchiveAddress `json:"source,omitempty"`
}

// not-wire-format: retryable operational notification, never the deleted body.
type savedInputNotice struct {
	ReceiptID     string          `json:"receipt_id"`
	OutcomeID     string          `json:"outcome_id"`
	CopyVersion   int             `json:"copy_version"`
	CreatedAt     time.Time       `json:"created_at"`
	Source        *ArchiveAddress `json:"source,omitempty"`
	ChatSaved     bool            `json:"chat_saved"`
	ModelAdmitted bool            `json:"model_admitted"`
}

func newSavedInputLifecycle(sessionID string) savedInputLifecycle {
	return savedInputLifecycle{Version: 1, SessionID: sessionID,
		Deletions: map[string]savedInputDeletion{}, ExpiredReports: map[string]savedInputExpiration{},
		PollConsumed: map[string]savedInputPoll{}, Notices: map[string]savedInputNotice{}}
}

func loadSavedInputLifecycle(dir, sessionID string) (savedInputLifecycle, error) {
	data, err := os.ReadFile(filepath.Join(dir, savedInputLifecycleFile))
	if errors.Is(err, fs.ErrNotExist) {
		return newSavedInputLifecycle(sessionID), nil
	}
	if err != nil {
		return savedInputLifecycle{}, fmt.Errorf("saved input lifecycle %q: read: %w", sessionID, err)
	}
	var state savedInputLifecycle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, fmt.Errorf("saved input lifecycle %q: decode: %w", sessionID, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return state, fmt.Errorf("saved input lifecycle %q: trailing data", sessionID)
	}
	if err := state.validate(sessionID); err != nil {
		return state, fmt.Errorf("saved input lifecycle %q: %w", sessionID, err)
	}
	return state, nil
}

func saveSavedInputLifecycle(dir string, state savedInputLifecycle) error {
	if err := state.validate(state.SessionID); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomicSyncDir(filepath.Join(dir, savedInputLifecycleFile), data, 0o600); err != nil {
		return fmt.Errorf("saved input lifecycle %q: durable publication: %w", state.SessionID, err)
	}
	return nil
}

func (s savedInputLifecycle) validate(sessionID string) error {
	if s.Version != 1 || s.SessionID != sessionID || sessionID == "" || s.Deletions == nil || s.ExpiredReports == nil || s.PollConsumed == nil || s.Notices == nil {
		return errors.New("invalid version, session identity or null lifecycle map")
	}
	for id, d := range s.Deletions {
		if id == "" || d.ID != id || d.PartitionKey == "" || filepath.Base(d.FileName) != d.FileName || strings.ContainsAny(d.FileName, "/\\") ||
			(d.FileName != archiveCurrentFile && d.FileName != d.PartitionKey+".jsonl") || d.SizeBytes < 0 || d.RetentionDays <= 0 ||
			d.CommittedAt.IsZero() || d.CutoffUTC.IsZero() || time.Unix(0, d.MtimeUnixNano).Compare(d.CutoffUTC) >= 0 || !savedInputDigest(d.SHA256) {
			return fmt.Errorf("invalid deletion intent %q", id)
		}
		for _, a := range d.RetiredNoticeSources {
			if !validSavedInputAddress(a) || a.PartitionKey != d.PartitionKey || a.ByteOffset >= d.SizeBytes {
				return fmt.Errorf("invalid retired notice source in %q", id)
			}
		}
		for _, row := range d.RetiredModelRows {
			if row.Ordinal < 0 || !validSavedInputAddress(row.Slot) || (row.Source != nil && !validSavedInputAddress(*row.Source)) ||
				(row.Slot.PartitionKey != d.PartitionKey && row.content().PartitionKey != d.PartitionKey) {
				return fmt.Errorf("invalid retired publication row in %q", id)
			}
		}
	}
	for id, e := range s.ExpiredReports {
		d, ok := s.Deletions[e.DeletionID]
		n, hasNotice := s.Notices[e.ReceiptID]
		if !ok || !hasNotice || id == "" || e.InputID != id || e.Source.EntryID != id || !validSavedInputAddress(e.Source) || e.Source.PartitionKey != d.PartitionKey ||
			e.ReceiptID != savedInputReceiptID(sessionID, e.Source) || e.ExpiredAt.IsZero() || e.NoticeID != "saved-input-"+n.OutcomeID {
			return fmt.Errorf("invalid expiry receipt %q", id)
		}
		if _, consumed := s.PollConsumed[id]; consumed {
			return fmt.Errorf("input %q is both expired undelivered and poll-consumed", id)
		}
	}
	for id, p := range s.PollConsumed {
		if id == "" || p.InputID != id || p.ConsumedAt.IsZero() || p.MarkerID != "consumed-"+strings.TrimPrefix(id, "saved-input-") ||
			(p.Source != nil && (!validSavedInputAddress(*p.Source) || p.Source.EntryID != id)) || (p.MarkerAddress != nil && !validSavedInputAddress(*p.MarkerAddress)) {
			return fmt.Errorf("invalid poll receipt %q", id)
		}
	}
	for id, n := range s.Notices {
		e, ok := s.ExpiredReportsByReceipt(id)
		if !ok || id == "" || n.ReceiptID != id || n.OutcomeID != "retention-expiry:"+id || n.CopyVersion != 1 || n.CreatedAt.IsZero() ||
			(n.Source != nil && (!validSavedInputAddress(*n.Source) || n.Source.EntryID != e.NoticeID)) || (n.ChatSaved && n.Source == nil) || (n.ModelAdmitted && !n.ChatSaved) {
			return fmt.Errorf("invalid expiry notice outbox %q", id)
		}
	}
	return nil
}

func (s savedInputLifecycle) ExpiredReportsByReceipt(id string) (savedInputExpiration, bool) {
	for _, e := range s.ExpiredReports {
		if e.ReceiptID == id {
			return e, true
		}
	}
	return savedInputExpiration{}, false
}

func (s savedInputLifecycle) expiry(inputID string) error {
	if e, ok := s.ExpiredReports[inputID]; ok {
		return &SavedInputExpiry{SessionID: s.SessionID, InputID: inputID, ReceiptID: e.ReceiptID, Source: e.Source, ExpiredAt: e.ExpiredAt}
	}
	return nil
}

func validSavedInputAddress(a ArchiveAddress) bool {
	return a.EntryID != "" && a.PartitionKey != "" && a.ByteOffset >= 0
}
func savedInputDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}
func savedInputReceiptID(sessionID string, a ArchiveAddress) string {
	data, _ := json.Marshal(a) // This concrete address contains no fallible values.
	sum := sha256.Sum256(append(append([]byte(sessionID), 0), data...))
	return hex.EncodeToString(sum[:])
}

func savedInputExpiryCopy(sessionID string, e savedInputExpiration) string {
	return fmt.Sprintf("A helper report (%s) expired undelivered in this chat (%s) at %s under your conversation-retention setting. The original report will not arrive. Ask the helper to run the work again if you still need the result.",
		e.InputID, sessionID, e.ExpiredAt.UTC().Format(time.RFC3339))
}
