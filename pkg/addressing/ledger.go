package addressing

import (
	"bufio"
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
	return fileutil.AppendJSONLSync(p, c)
}

func readLatest(path, requestID string) (Capture, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Capture{}, false, nil
	}
	if err != nil {
		return Capture{}, false, fmt.Errorf("addressing: open ledger: %w", err)
	}
	defer f.Close()
	var latest Capture
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c Capture
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			// A torn or corrupt line fails closed: a reply must never be
			// routed from a ledger that cannot be read faithfully.
			return Capture{}, false, fmt.Errorf("addressing: ledger %s is unreadable: %w", path, err)
		}
		if c.RequestID == requestID {
			latest, found = c, true
		}
	}
	if err := sc.Err(); err != nil {
		return Capture{}, false, fmt.Errorf("addressing: read ledger: %w", err)
	}
	return latest, found, nil
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
