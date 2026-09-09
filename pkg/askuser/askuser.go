// Omnipus — AskUserQuestion structured clarification: types + validation
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package askuser holds the backend half of the AskUserQuestion structured
// clarification tool (spec: docs/internal/specs/askuserquestion-tool-spec.md
// v3; ADR-074 D4b): the question-set types and full validation table (§2),
// the durable pending registry (§0.4 — UnifiedMeta session-meta persistence
// plus an approvalRegistryV2-shaped in-process registry with a global cap),
// the server-side submission path (§3 validation, first-valid-wins, resume
// dispatch per §0.2), and the 30-minute default-safe timers (§US-3),
// re-armed on boot from persisted timestamps.
//
// The WS card frames and the SPA card are a later, serialized stream — this
// package deliberately exposes narrow interfaces (CardSink, ResumeDispatcher,
// AuditSink) with fail-safe no-op/nil behavior so everything here compiles,
// runs and tests without any wire format existing yet.
package askuser

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Validation caps (spec §2, M-8; header cap C-enforced per r2).
const (
	MaxQuestions     = 10
	MinOptions       = 2
	MaxOptions       = 6
	MaxQuestionChars = 500
	MaxHeaderChars   = 16
	MaxOptionLabel   = 80
	MaxOptionDesc    = 200
	MaxContextChars  = 4000
	MaxFreeTextChars = 2000
	DefaultSafeDelay = 30 * time.Minute
	DefaultGlobalCap = 64 // pending sets across ALL sessions (spec §4 DoS line item)
)

// Option is one selectable option of a question.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Question is one question of a pending set (spec §2).
type Question struct {
	Header      string   `json:"header"`
	Question    string   `json:"question"`
	Options     []Option `json:"options"`
	MultiSelect bool     `json:"multi_select,omitempty"`
	// Recommended names an option BY LABEL (spec §2 — dup labels within a
	// question are therefore a validation error).
	Recommended string `json:"recommended,omitempty"`
	// DefaultSafe requires Recommended (validated): at CreatedAt+30:00 an
	// unanswered such question becomes resolved-pending-submit (US-3 S2).
	DefaultSafe bool `json:"default_safe,omitempty"`
	// Context is raw markdown, display-only, SPA-sanitized at render (US-4).
	Context string `json:"context,omitempty"`
}

// Answer is one question's answer as carried on the RESUME MESSAGE payload
// (spec §2: the result schema is the resume-message payload, not a tool
// return). QuestionText is the o-R2-1 echo: a days-late resume must not
// depend on the question surviving windowTrim's eviction window.
type Answer struct {
	Header       string   `json:"header"`
	QuestionText string   `json:"question"`
	Selected     []string `json:"selected,omitempty"`
	FreeText     *string  `json:"free_text,omitempty"`
	AutoDefault  bool     `json:"auto_default"`
}

// SubmittedAnswer is one question's answer as received by the server-side
// submission path (from the future ask_user_answer frame handler, or a
// test). Presence of FreeText IS the free-text flag (US-2 S1, M-10): a
// non-nil pointer means "free text answer", even an empty-string one is
// distinguishable — though empty free text is rejected by validation.
type SubmittedAnswer struct {
	Header      string   `json:"header"`
	Selected    []string `json:"selected,omitempty"`
	FreeText    *string  `json:"free_text,omitempty"`
	AutoDefault bool     `json:"auto_default,omitempty"`
}

// SetStatus is the lifecycle status of a pending set.
type SetStatus string

const (
	StatusPending   SetStatus = "pending"
	StatusAnswered  SetStatus = "answered"
	StatusCancelled SetStatus = "cancelled"
)

// PendingSet is the durable record of one parked question set. It is
// persisted verbatim (JSON) into the owner session's UnifiedMeta
// (SessionMeta.PendingAskJSON) and mirrored in the in-process Registry.
type PendingSet struct {
	CardID string `json:"card_id"`
	// RoutingSessionKey is the pending-key namespace (§0.9): one set per
	// routing session. For a root (owner) turn this is the turn's session
	// key; delegated children cannot call the tool at all (owner-only).
	RoutingSessionKey string `json:"routing_session_key"`
	// TranscriptSessionID is the owner session's real store-backed id — the
	// key the pending state persists under, and the session the resume turn
	// is dispatched into.
	TranscriptSessionID string     `json:"transcript_session_id"`
	AgentID             string     `json:"agent_id"`
	Channel             string     `json:"channel"`
	ChatID              string     `json:"chat_id"`
	Owner               string     `json:"owner,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	Questions           []Question `json:"questions"`
	Status              SetStatus  `json:"status"`
	// AutoResolved records per-header server-side default-safe resolutions
	// (US-3's "resolved-pending-submit" state), keyed by question header,
	// valued by resolution time. Persisted so a restart re-derives which
	// timers already fired.
	AutoResolved map[string]time.Time `json:"auto_resolved,omitempty"`
	// Answers is populated when Status == answered; the collapsed record
	// (§0.6) renders from THIS record, not the tool result pair.
	Answers []Answer `json:"answers,omitempty"`
}

// Clone returns a deep copy of s.
func (s *PendingSet) Clone() *PendingSet {
	if s == nil {
		return nil
	}
	c := *s
	c.Questions = make([]Question, len(s.Questions))
	copy(c.Questions, s.Questions)
	for i := range c.Questions {
		opts := make([]Option, len(s.Questions[i].Options))
		copy(opts, s.Questions[i].Options)
		c.Questions[i].Options = opts
	}
	if s.AutoResolved != nil {
		c.AutoResolved = make(map[string]time.Time, len(s.AutoResolved))
		for k, v := range s.AutoResolved {
			c.AutoResolved[k] = v
		}
	}
	c.Answers = cloneAnswers(s.Answers)
	return &c
}

func cloneAnswers(in []Answer) []Answer {
	if in == nil {
		return nil
	}
	out := make([]Answer, len(in))
	copy(out, in)
	for i := range out {
		out[i].Selected = append([]string(nil), in[i].Selected...)
		if in[i].FreeText != nil {
			v := *in[i].FreeText
			out[i].FreeText = &v
		}
	}
	return out
}

// NewCardID mints a fresh card id.
func NewCardID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable for id-minting purposes;
		// fall back to a time-derived id rather than panicking a turn.
		return fmt.Sprintf("ask_%d", time.Now().UnixNano())
	}
	return "ask_" + hex.EncodeToString(b[:])
}

// ValidateQuestions applies the full spec §2 validation table. It returns a
// descriptive error for the FIRST violation found (the tool surfaces it
// verbatim to the calling agent), or nil when the set is valid.
func ValidateQuestions(qs []Question) error {
	if len(qs) == 0 {
		return fmt.Errorf("questions must contain at least 1 question")
	}
	if len(qs) > MaxQuestions {
		return fmt.Errorf("questions must contain at most %d questions (got %d)", MaxQuestions, len(qs))
	}
	seenHeaders := make(map[string]bool, len(qs))
	for i, q := range qs {
		if strings.TrimSpace(q.Header) == "" {
			return fmt.Errorf("questions[%d]: header must not be empty", i)
		}
		if len([]rune(q.Header)) > MaxHeaderChars {
			return fmt.Errorf("questions[%d]: header exceeds %d characters", i, MaxHeaderChars)
		}
		if seenHeaders[q.Header] {
			return fmt.Errorf("questions[%d]: duplicate header %q", i, q.Header)
		}
		seenHeaders[q.Header] = true
		if strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("questions[%d]: question text must not be empty", i)
		}
		if len([]rune(q.Question)) > MaxQuestionChars {
			return fmt.Errorf("questions[%d]: question text exceeds %d characters", i, MaxQuestionChars)
		}
		if len([]rune(q.Context)) > MaxContextChars {
			return fmt.Errorf("questions[%d]: context exceeds %d characters", i, MaxContextChars)
		}
		if len(q.Options) < MinOptions {
			return fmt.Errorf("questions[%d]: needs at least %d options (got %d)", i, MinOptions, len(q.Options))
		}
		if len(q.Options) > MaxOptions {
			return fmt.Errorf("questions[%d]: at most %d options allowed (got %d)", i, MaxOptions, len(q.Options))
		}
		seenLabels := make(map[string]bool, len(q.Options))
		for j, o := range q.Options {
			if strings.TrimSpace(o.Label) == "" {
				return fmt.Errorf("questions[%d].options[%d]: label must not be empty", i, j)
			}
			if len([]rune(o.Label)) > MaxOptionLabel {
				return fmt.Errorf("questions[%d].options[%d]: label exceeds %d characters", i, j, MaxOptionLabel)
			}
			if len([]rune(o.Description)) > MaxOptionDesc {
				return fmt.Errorf("questions[%d].options[%d]: description exceeds %d characters", i, j, MaxOptionDesc)
			}
			if seenLabels[o.Label] {
				return fmt.Errorf("questions[%d]: duplicate option label %q (recommended matches by label — labels must be unique within a question)", i, o.Label)
			}
			seenLabels[o.Label] = true
		}
		if q.Recommended != "" && !seenLabels[q.Recommended] {
			return fmt.Errorf("questions[%d]: recommended %q names no option", i, q.Recommended)
		}
		if q.DefaultSafe && q.Recommended == "" {
			return fmt.Errorf("questions[%d]: default_safe requires recommended", i)
		}
	}
	return nil
}

// questionByHeader returns the question with the given header, or nil.
func questionByHeader(qs []Question, header string) *Question {
	for i := range qs {
		if qs[i].Header == header {
			return &qs[i]
		}
	}
	return nil
}

// allDefaultSafe reports whether EVERY question in the set is default-safe —
// the precondition for the server ever auto-submitting on its own (US-3 S3:
// the server submits only when every question is default-safe-resolved and
// no client submission landed).
func allDefaultSafe(qs []Question) bool {
	for i := range qs {
		if !qs[i].DefaultSafe {
			return false
		}
	}
	return len(qs) > 0
}
