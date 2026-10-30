package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Demand self-heal of a derived chat-history index that is too far behind for
// the bounded catch-up (#1470, selfheal-design.md). Only catchup_limit is a
// trigger: every capture/anchor check already passed in the same owner-locked
// reconcile, so the canonical layout is consistent with the old checkpoint.
// Boot and retention keep the non-healing reconcileChatHistoryIndex.
const (
	chatHistoryHealMaxSourceBytes = int64(32 * 1024 * 1024) // strictly greater is refused
	chatHistoryHealMaxAttempts    = 3
	chatHistoryHealBackoff        = 60 * time.Second
	chatHistoryHealTimeout        = 30 * time.Second
)

// chatHistoryHealSlot is the process-wide, capacity-one rebuild slot: at most
// one full rebuild runs at a time. A heal that cannot take it is a transient
// "busy" attempt. Tests may hold it by sending into the channel.
var chatHistoryHealSlot = make(chan struct{}, 1)

// errHistoryHealSlotBusy is the cause logged for a "busy" attempt.
var errHistoryHealSlotBusy = errors.New("another chat history rebuild holds the process-wide rebuild slot")

// chatHistoryHealState is the per-owner attempt state, guarded by owner.mu.
// It lives for the store's (process's) lifetime; a restart starts afresh.
type chatHistoryHealState struct {
	// attempts counts every real attempt (the warning's attempt field);
	// counted counts only the failures that move toward the latch.
	attempts int
	counted  int
	last     time.Time
	latched  bool
}

func (us *UnifiedStore) historyHealClock() time.Time {
	if us.historyHealNow != nil {
		return us.historyHealNow()
	}
	return time.Now()
}

// historyHealContext bounds one heal attempt by chatHistoryHealTimeout.
func (us *UnifiedStore) historyHealContext(parent context.Context) (context.Context, context.CancelFunc) {
	if us.historyHealWithTimeout != nil {
		return us.historyHealWithTimeout(parent, chatHistoryHealTimeout)
	}
	return context.WithTimeout(parent, chatHistoryHealTimeout)
}

// historyHealTransient reports whether a failure class may be retried after
// the backoff, rather than latching for the process lifetime.
func historyHealTransient(class string) bool {
	return historyHealUncounted(class) || class == "deadline"
}

// historyHealUncounted reports the transient classes that are not failures
// of the heal itself (6a review W1/W2): another chat holds the rebuild slot,
// or the chat was written to during the build. They keep the 60 s spacing
// but never count toward the chatHistoryHealMaxAttempts latch.
func historyHealUncounted(class string) bool {
	return class == "busy" || class == "source_changed"
}

// healChatHistoryOwned runs with owner.mu held, after reconcileHistoryOwned
// returned trigger (a catchup_limit unavailability). It returns the original
// trigger unchanged whenever no heal completes.
func (us *UnifiedStore) healChatHistoryOwned(ctx context.Context, sid string, owner *chatHistoryOwner, trigger error) (chatHistoryReconcileResult, error) {
	state := &owner.heal
	now := us.historyHealClock()
	if state.latched || state.attempts > 0 && now.Sub(state.last) < chatHistoryHealBackoff {
		return chatHistoryReconcileResult{}, trigger
	}
	state.attempts++
	state.last = now
	attempt := state.attempts
	started := time.Now()
	// fail logs one warning for this attempt with its sanitized cause and
	// returns ret, or the original trigger when ret is nil.
	fail := func(class string, cause, ret error) (chatHistoryReconcileResult, error) {
		if !historyHealUncounted(class) {
			state.counted++
		}
		retry := "after_restart"
		if historyHealUncounted(class) || historyHealTransient(class) && state.counted < chatHistoryHealMaxAttempts {
			retry = "after_backoff"
		} else {
			state.latched = true
		}
		slog.Warn("session: chat history index rebuild failed", "session_id", sid, "trigger", "catchup_limit", "class", class,
			"duration_ms", time.Since(started).Milliseconds(), "attempt", attempt, "retry", retry, "cause", us.historyHealCause(cause))
		if ret != nil {
			return chatHistoryReconcileResult{}, ret
		}
		return chatHistoryReconcileResult{}, trigger
	}

	select {
	case chatHistoryHealSlot <- struct{}{}:
		defer func() { <-chatHistoryHealSlot }()
	default:
		return fail("busy", errHistoryHealSlotBusy, nil)
	}
	dir, err := filepath.Abs(filepath.Join(us.baseDir, sid))
	if err != nil {
		return fail("storage_error", err, nil)
	}
	size, err := historyHealSourceSize(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// A listed source file vanished during the walk: a proven move such
		// as a day rollover, not a stable storage failure.
		return fail("source_changed", err, nil)
	}
	if err != nil {
		return fail("storage_error", err, nil)
	}
	if size > chatHistoryHealMaxSourceBytes {
		return fail("too_large", fmt.Errorf("session source is %d bytes, above the %d byte cap", size, chatHistoryHealMaxSourceBytes), nil)
	}
	hctx, cancel := us.historyHealContext(context.WithoutCancel(ctx))
	defer cancel()
	build, err := PrepareChatHistoryIndex(hctx, dir, ChatHistoryIndexBuildOptions{demand: true})
	if err == nil {
		summary := build.Summary()
		// Retire in the store's own path spelling: reader pins (retain) are
		// keyed by filepath.Join(us.baseDir, ...), while the builder works on
		// an absolute, symlink-resolved directory.
		generations := filepath.Join(historyIndexPath(filepath.Join(us.baseDir, sid)), "generations")
		// cleanupErr is a failure to remove old generation files after the
		// new checkpoint was published. The rebuild is complete by then:
		// the old files stay pending and every later reconcile (the proof
		// below included) retries their removal.
		var cleanupErr error
		err = build.publish(hctx, func(_, keep string) error {
			// Retire with the RUNTIME owner so page readers' retained
			// generations survive until their last release.
			h := us.lockSession(sid)
			defer h.Unlock()
			cleanupErr = owner.retire(generations, keep)
			return nil
		})
		err = errors.Join(err, build.Close())
		if err == nil {
			// Fold in appends after the build's captured ends; this bounded
			// reconcile is the proof of readiness with live writers. It runs
			// on the detached heal context: a client leaving mid-heal is not
			// a readiness failure.
			result, readyErr := us.reconcileHistoryOwned(hctx, sid)
			var retirement *historyRetirementError
			cleanupOnly := readyErr != nil && errors.As(readyErr, &retirement)
			if readyErr != nil && !cleanupOnly {
				if errors.Is(hctx.Err(), context.DeadlineExceeded) {
					return fail("deadline", readyErr, nil)
				}
				return fail("readiness", readyErr, readyErr)
			}
			*state = chatHistoryHealState{}
			slog.Warn("session: chat history index rebuilt", "session_id", sid, "trigger", "catchup_limit", "action", summary.Action,
				"records", summary.ArchiveRecordCount, "source_groups", summary.SourceGroupCount, "partitions", summary.PartitionCount,
				"bytes", size, "duration_ms", time.Since(started).Milliseconds(), "attempt", attempt)
			if cleanupErr == nil && cleanupOnly {
				cleanupErr = readyErr
			}
			if cleanupErr != nil {
				slog.Warn("session: chat history index old generations not retired", "session_id", sid, "trigger", "catchup_limit",
					"cause", us.historyHealCause(cleanupErr))
			}
			// A cleanup failure the proof could not clear either is the
			// store's real state (storage_error, retried on every read), not
			// a failed heal: it neither spends an attempt nor latches.
			return result, readyErr
		}
	}
	if errors.Is(hctx.Err(), context.DeadlineExceeded) {
		return fail("deadline", err, nil)
	}
	class := "storage_error"
	var buildErr *ChatHistoryIndexBuildError
	if errors.As(err, &buildErr) {
		class = buildErr.HistoryIndexBuildClass()
	}
	if class == "index_inconsistent" {
		// The existing published index failed its complete proof: real
		// derived damage, refused as such rather than as plain lag.
		return fail(class, err, historyUnavailable("index_inconsistent", err))
	}
	return fail(class, err, nil)
}

// historyHealSourceSize is a stat-only total of the session folder, excluding
// the derived .chat-history subtree. No file content is read.
func historyHealSourceSize(dir string) (int64, error) {
	var total int64
	index := historyIndexPath(dir)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == index {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// chatHistoryHealCauseMax bounds the logged cause text.
const chatHistoryHealCauseMax = 512

var (
	// historyHealPathToken matches any token holding a path separator (with an
	// optional drive letter): absolute and relative paths, session folders.
	// It is the backstop for free text only; typed path errors and the
	// store's known roots are removed whole first (6a delta review I1).
	historyHealPathToken = regexp.MustCompile(`(?:[A-Za-z]:)?[^\s:;,'"()]*[/\\][^\s:;,'"()]*`)
	// historyHealFileToken matches a bare history or canonical file name.
	historyHealFileToken = regexp.MustCompile(`[^\s:;,'"()]+\.(?:jsonl|json|chi|tmp)\b`)
)

// historyHealCause is the operator-facing cause of a heal failure: the
// underlying error with every path and file name removed, newlines folded and
// the length bounded. The session id is logged separately.
//
// Paths are removed whole, never token by token, because a folder name may
// hold spaces (6a delta review I1): the path of an fs.PathError or
// os.LinkError is dropped from the typed error; in free text, each known root
// (roots: the store's data folders) is removed together with everything up to
// the next ": " separator. A decoder or parse error (JSON, time, number) is
// logged as its class only: its text quotes the persisted value it failed on.
// The classifying wrappers expose only their class, so their private cause is
// unwrapped first.
func historyHealCause(err error, roots ...string) string {
	text := historyHealCauseText(err, historyHealRoots(roots))
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > chatHistoryHealCauseMax {
		cut := chatHistoryHealCauseMax
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "..."
	}
	return text
}

// historyHealCause is the store's sanitizer: it also removes every spelling
// of the store's own data folders from free error text.
func (us *UnifiedStore) historyHealCause(err error) string {
	roots := []string{us.baseDir, us.homePath}
	for _, root := range []string{us.baseDir, us.homePath} {
		if root == "" {
			continue
		}
		if abs, absErr := filepath.Abs(root); absErr == nil {
			roots = append(roots, abs)
			if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
				roots = append(roots, resolved)
			}
		}
	}
	return historyHealCause(err, roots...)
}

// historyHealRoots drops empty and root-only spellings and orders the rest
// longest first, so a longer root is never cut short by its own prefix.
func historyHealRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if len(strings.Trim(root, `/\.`)) == 0 {
			continue
		}
		out = append(out, root)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// historyHealDecodeClass reports the class of the first decoder or parse error
// in err's chain; their text carries persisted values, never logged.
func historyHealDecodeClass(err error) (string, bool) {
	var (
		syntaxErr  *json.SyntaxError
		typeErr    *json.UnmarshalTypeError
		invalidErr *json.InvalidUnmarshalError
		timeErr    *time.ParseError
		numErr     *strconv.NumError
		base64Err  base64.CorruptInputError
	)
	switch {
	case errors.As(err, &timeErr):
		return fmt.Sprintf("%T", timeErr), true
	case errors.As(err, &numErr):
		return fmt.Sprintf("%T", numErr), true
	case errors.As(err, &syntaxErr):
		return fmt.Sprintf("%T", syntaxErr), true
	case errors.As(err, &typeErr):
		return fmt.Sprintf("%T", typeErr), true
	case errors.As(err, &invalidErr):
		return fmt.Sprintf("%T", invalidErr), true
	case errors.As(err, &base64Err):
		return fmt.Sprintf("%T", base64Err), true
	}
	return "", false
}

func historyHealCauseText(err error, roots []string) string {
	if err == nil {
		return "unknown"
	}
	// The switch matches the OUTERMOST layer only, on purpose: each layer is
	// rendered by itself and wrappers recurse below, so a wrapper keeps its
	// own words and every member of a joined error is kept. errors.As would
	// jump to the first matching inner error and drop both; it is used below
	// only for wrappers whose text cannot be split.
	switch e := err.(type) { //nolint:errorlint // layer-by-layer rendering; wrapped errors recurse through the branches below
	case *ChatHistoryIndexBuildError:
		if e.cause != nil {
			return historyHealCauseText(e.cause, roots)
		}
		return e.Class
	case *ChatHistoryUnavailableError:
		if e.cause != nil {
			return e.Reason + ": " + historyHealCauseText(e.cause, roots)
		}
		return e.Reason
	case *historyRetirementError:
		return historyHealCauseText(e.err, roots)
	case *fs.PathError:
		return historyHealFreeText(e.Op, roots) + " <path>: " + historyHealCauseText(e.Err, roots)
	case *os.LinkError:
		return historyHealFreeText(e.Op, roots) + " <path> <path>: " + historyHealCauseText(e.Err, roots)
	case *os.SyscallError:
		return historyHealFreeText(e.Syscall, roots) + ": " + historyHealCauseText(e.Err, roots)
	case interface{ Unwrap() []error }:
		var parts []string
		for _, inner := range e.Unwrap() {
			if inner != nil {
				parts = append(parts, historyHealCauseText(inner, roots))
			}
		}
		return strings.Join(parts, "; ")
	}
	if class, ok := historyHealDecodeClass(err); ok {
		inner := errors.Unwrap(err)
		if _, own := historyHealDecodeClass(inner); inner != nil && own {
			// A wrapper over the decoder error: keep the wrapper's own words
			// only when they are a plain prefix of the inner text.
			if prefix, isPrefix := strings.CutSuffix(err.Error(), inner.Error()); isPrefix {
				return historyHealFreeText(prefix, roots) + historyHealCauseText(inner, roots)
			}
		}
		return "decode error (" + class + ")"
	}
	if inner := errors.Unwrap(err); inner != nil {
		if prefix, isPrefix := strings.CutSuffix(err.Error(), inner.Error()); isPrefix {
			return historyHealFreeText(prefix, roots) + historyHealCauseText(inner, roots)
		}
	}
	// A wrapper that embeds a path error mid-text cannot be split into its
	// own words and the inner text: keep only the typed error, whose path is
	// removed whole.
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return historyHealCauseText(linkErr, roots)
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return historyHealCauseText(pathErr, roots)
	}
	return historyHealFreeText(err.Error(), roots)
}

// historyHealFreeText removes paths from text that carries no typed path:
// each known root through the next ": " separator (or the end), then any
// remaining separator-holding token and bare file name.
func historyHealFreeText(text string, roots []string) string {
	for _, root := range roots {
		for {
			at := strings.Index(text, root)
			if at < 0 {
				break
			}
			end := len(text)
			if sep := strings.Index(text[at+len(root):], ": "); sep >= 0 {
				end = at + len(root) + sep
			}
			text = text[:at] + "<path>" + text[end:]
		}
	}
	text = historyHealPathToken.ReplaceAllString(text, "<path>")
	return historyHealFileToken.ReplaceAllString(text, "<file>")
}
