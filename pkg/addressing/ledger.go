package addressing

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

// ledgerFileName lives in the RECEIVER's session directory, so deleting the
// session deletes its captures with it (no second store to clean).
const ledgerFileName = "requests.jsonl"

// Ledger persists captures beside each receiver session. Records are
// append-only; a later record for the same request id supersedes an earlier
// one (that is how a discard is recorded).
type Ledger struct {
	sessionsDir string
	mu          sync.Mutex
}

// NewLedger roots a ledger at the sessions directory (UnifiedStore.BaseDir()).
func NewLedger(sessionsDir string) *Ledger { return &Ledger{sessionsDir: sessionsDir} }

func (l *Ledger) path(sessionID string) (string, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || strings.Contains(sessionID, "..") || sessionID == "." {
		return "", fmt.Errorf("addressing: invalid session id %q", sessionID)
	}
	return filepath.Join(l.sessionsDir, sessionID, ledgerFileName), nil
}

// Put records a capture. The receiver session's directory must already exist:
// a capture never creates (or resurrects) a session.
func (l *Ledger) Put(c Capture) error {
	if err := c.Validate(); err != nil {
		return err
	}
	p, err := l.path(c.ReceiverSessionID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, statErr := os.Stat(filepath.Dir(p)); statErr != nil {
		return fmt.Errorf("addressing: receiver session %q is not available: %w", c.ReceiverSessionID, statErr)
	}
	if err := repairTornTail(p); err != nil {
		return err
	}
	return fileutil.AppendJSONLSync(p, c)
}

// Get returns the latest record for requestID in sessionID's ledger.
func (l *Ledger) Get(sessionID, requestID string) (Capture, bool, error) {
	p, err := l.path(sessionID)
	if err != nil {
		return Capture{}, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return readLatest(p, requestID)
}

// Discard marks a request as discarded so it can no longer be answered. A
// request that was never captured is not an error (nothing to discard).
func (l *Ledger) Discard(sessionID, requestID string) error {
	p, err := l.path(sessionID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok, err := readLatest(p, requestID)
	if err != nil || !ok {
		return err
	}
	if c.Discarded {
		return nil
	}
	c.Discarded = true
	if err := repairTornTail(p); err != nil {
		return err
	}
	return fileutil.AppendJSONLSync(p, c)
}

// repairTornTail makes the ledger end on a record boundary before an append. A
// final line with no newline is the footprint of an append that was cut short:
// if it is still a complete JSON record only the newline is missing and it is
// completed; otherwise the partial bytes were never acknowledged and are cut
// off. Without this the next record would glue onto the partial line and the
// ledger would fail closed for the session forever.
func repairTornTail(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("addressing: read ledger: %w", err)
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("addressing: open ledger: %w", err)
	}
	defer f.Close()
	cut := bytes.LastIndexByte(data, '\n') + 1
	if json.Valid(data[cut:]) {
		if _, err := f.WriteAt([]byte{'\n'}, int64(len(data))); err != nil {
			return fmt.Errorf("addressing: complete ledger record: %w", err)
		}
	} else if err := f.Truncate(int64(cut)); err != nil {
		return fmt.Errorf("addressing: cut torn ledger tail: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("addressing: sync ledger: %w", err)
	}
	return nil
}

func readLatest(path, requestID string) (Capture, bool, error) {
	all, err := readAllLatest(path)
	if err != nil {
		return Capture{}, false, err
	}
	for _, c := range all {
		if c.RequestID == requestID {
			return c, true, nil
		}
	}
	return Capture{}, false, nil
}

// readAllLatest returns the latest record of every request id, ordered by the
// position of each id's first record (admission order).
func readAllLatest(path string) ([]Capture, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("addressing: read ledger: %w", err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	var order []string
	latest := map[string]Capture{}
	for i, raw := range lines {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var c Capture
		if err := json.Unmarshal(line, &c); err != nil {
			if i == len(lines)-1 && !json.Valid(line) {
				// The unterminated last line is a torn append that was never
				// acknowledged: it is not a record. Only a newline-terminated line
				// that cannot be read is corruption.
				continue
			}
			// A corrupt line fails closed: a reply must never be routed from a
			// ledger that cannot be read faithfully.
			return nil, fmt.Errorf("addressing: ledger %s is unreadable: %w", path, err)
		}
		if _, seen := latest[c.RequestID]; !seen {
			order = append(order, c.RequestID)
		}
		latest[c.RequestID] = c
	}
	out := make([]Capture, 0, len(order))
	for _, id := range order {
		out = append(out, latest[id])
	}
	return out, nil
}

// Resolve returns the capture the responding agent may answer. The request is
// looked up ONLY in the responder's own session ledger and must be addressed
// to the responder's pair; anything else is ErrUnknownRequest.
func (l *Ledger) Resolve(responderSessionID string, responder Pair, requestID string) (Capture, error) {
	if strings.TrimSpace(requestID) == "" {
		return Capture{}, ErrUnknownRequest
	}
	c, ok, err := l.Get(responderSessionID, requestID)
	if err != nil {
		return Capture{}, err
	}
	if !ok || c.ReceiverSessionID != responderSessionID || c.Receiver != responder {
		return Capture{}, ErrUnknownRequest
	}
	if c.Discarded {
		return Capture{}, ErrDiscarded
	}
	if err := c.Validate(); err != nil {
		return Capture{}, fmt.Errorf("%w: %w", ErrUnusableCorrelation, err)
	}
	return c, nil
}

// MarkDelivered records, durably, that a turn of the receiver has been given the
// request. A request that was never captured, or is already delivered, is not an
// error.
func (l *Ledger) MarkDelivered(sessionID, requestID string) error {
	p, err := l.path(sessionID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok, err := readLatest(p, requestID)
	if err != nil || !ok || c.Delivered {
		return err
	}
	c.Delivered = true
	if err := repairTornTail(p); err != nil {
		return err
	}
	return fileutil.AppendJSONLSync(p, c)
}

// MarkDeliveredFirst is MarkDelivered that reads and marks in ONE operation and
// reports the capture as it was BEFORE the mark and whether this call performed
// the not-delivered-to-delivered transition. A caller that must act once on the
// first delivery (moving a held request's sender row on) uses it so a failed read
// can never be followed by a successful mark that loses the transition: on any
// error nothing is marked and the transition stays available to the next try.
func (l *Ledger) MarkDeliveredFirst(sessionID, requestID string) (prior Capture, first bool, err error) {
	p, err := l.path(sessionID)
	if err != nil {
		return Capture{}, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok, err := readLatest(p, requestID)
	if err != nil || !ok || c.Delivered {
		return c, false, err
	}
	if c.Source.Kind == SourceRoom && c.ModelInput == nil {
		return c, false, nil // the addressed room-input bind is the delivery commit
	}
	prior = c
	c.Delivered = true
	if err := repairTornTail(p); err != nil {
		return Capture{}, false, err
	}
	if err := fileutil.AppendJSONLSync(p, c); err != nil {
		return Capture{}, false, err
	}
	return prior, true, nil
}

// MarkHeld records that the request's sender was told it is waiting, for a
// request admitted to a receiver that was not stopped but whose wake could not
// be published: it is delivered by the receiver's next turn like any held
// request. It reports whether the request IS held afterwards - held, not yet
// delivered and not discarded - which is what the sender may be told. A request
// that was never captured, or is already delivered or discarded, is left alone
// and reported not held: a turn admitted before this call may already have
// delivered it, with no tick owed, so a "waiting" row would never be cleared.
// On an error the answer is false and nothing was marked.
func (l *Ledger) MarkHeld(sessionID, requestID string) (held bool, err error) {
	p, err := l.path(sessionID)
	if err != nil {
		return false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok, err := readLatest(p, requestID)
	if err != nil || !ok || c.Delivered || c.Discarded {
		return false, err
	}
	if c.Held {
		return true, nil
	}
	c.Held = true
	if err := repairTornTail(p); err != nil {
		return false, err
	}
	if err := fileutil.AppendJSONLSync(p, c); err != nil {
		return false, err
	}
	return true, nil
}

// Undelivered lists the session's held requests - captured, not yet given to a
// running turn, not discarded - in admission order.
func (l *Ledger) Undelivered(sessionID string) ([]Capture, error) {
	p, err := l.path(sessionID)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	all, err := readAllLatest(p)
	if err != nil {
		return nil, err
	}
	var held []Capture
	for _, c := range all {
		if !c.Delivered && !c.Discarded {
			held = append(held, c)
		}
	}
	return held, nil
}
