package email

// BuildReplyRecipients — the ONE transport-neutral reply-recipient rule
// (ADR-20261001 "Reply / Reply all context (F5)" row; w4 spec §3.1, US-5).
// The gateway's reply-context operation and the agent reply adapter both call
// this helper; neither keeps a second copy of the algorithm (register R-4:
// one recipient rule).
//
// The rule (founder-set, w4 spec US-5):
//   - Primary recipient: the parsed Reply-To when present, otherwise From.
//     An invalid supplied address is an actionable error, never a silent
//     omission.
//   - reply_all: Cc = the original To + the original Cc, minus the mailbox's
//     own address and the primary, de-duplicated case-insensitively across
//     the whole set (display-name duplicates share one bare address, so the
//     first-seen representation wins). The original Bcc is never copied —
//     the caller's own bcc arguments are the only Bcc source and are the
//     adapter's concern, not this rule's.
//   - reply: the primary only — the other recipients are deliberately NOT
//     kept (a plain Reply must stay distinguishable from Reply all; a
//     private response to one sender must not reach the group by one
//     reflexive click). The original To/Cc lists are ignored; ExtraCc
//     (the caller's own cc arguments) still applies in both modes, exactly
//     as the agent tool has always behaved.
//   - Self-exclusion: when the primary's bare address IS the mailbox's own
//     address, the result carries an empty To — editable empty recipients,
//     never send-to-self and never a guessed replacement.

import (
	"fmt"
	"net/mail"
	"strings"
)

// Reply modes. They mirror the generated MailReplyContextRequest.mode enum.
const (
	ReplyModeReply    = "reply"
	ReplyModeReplyAll = "reply_all"
)

// ReplyInput is the transport-neutral input of the recipient rule: the
// original message's headers (From/ReplyTo as single address strings, To/Cc
// as bare-address lists — the envelope rendering both email.Message and
// MailView already carry), the caller's own additional cc entries (tool
// arguments; the gateway passes nil), the mode, and the mailbox's own
// address for self-exclusion.
type ReplyInput struct {
	From    string
	ReplyTo string
	To      []string
	Cc      []string
	// ExtraCc carries caller-supplied additional cc entries (the reply
	// tool's cc argument). They are parsed with the same rules and merged
	// ahead of the original lists (the tool's historical first-seen order),
	// subject to the same exclusion and de-duplication.
	ExtraCc []string
	// Mode is ReplyModeReply or ReplyModeReplyAll. Anything else is an error.
	Mode string
	// OwnAddress is the mailbox's sending address for self-exclusion; empty
	// disables it (the historical behaviour when the transport exposes no
	// identity).
	OwnAddress string
}

// ReplyRecipients is the rule's output: the primary To (empty when
// self-exclusion left nothing eligible) and the merged Cc.
type ReplyRecipients struct {
	To []mail.Address
	Cc []mail.Address
}

// BuildReplyRecipients applies the shared rule to one input. All parsing
// errors are actionable and name the offending entry; nothing is ever
// silently dropped.
func BuildReplyRecipients(in ReplyInput) (ReplyRecipients, error) {
	if in.Mode != ReplyModeReply && in.Mode != ReplyModeReplyAll {
		return ReplyRecipients{}, fmt.Errorf("reply mode must be %q or %q", ReplyModeReply, ReplyModeReplyAll)
	}

	primaryRaw := in.ReplyTo
	if strings.TrimSpace(primaryRaw) == "" {
		primaryRaw = in.From
	}
	var to []mail.Address
	if strings.TrimSpace(primaryRaw) != "" {
		parsed, bad := ParseRecipientList([]string{primaryRaw})
		if len(bad) > 0 || len(parsed) == 0 {
			return ReplyRecipients{}, fmt.Errorf("reply recipient %q is not a valid email address", primaryRaw)
		}
		to = parsed
	}

	var cc []mail.Address
	if in.Mode == ReplyModeReplyAll {
		// Original To first, then Cc — the rule's own order and the agent
		// tool's historical first-seen de-duplication order ("original To +
		// original Cc minus…", w4 spec US-5.AC-1).
		lists := make([]string, 0, len(in.Cc)+len(in.To))
		lists = append(lists, in.To...)
		lists = append(lists, in.Cc...)
		if len(lists) > 0 {
			merged, bad := ParseRecipientList(lists)
			if len(bad) > 0 {
				return ReplyRecipients{}, fmt.Errorf("original message recipient %q is not a valid email address", bad[0])
			}
			cc = merged
		}
	}
	if len(in.ExtraCc) > 0 {
		extra, bad := ParseRecipientList(in.ExtraCc)
		if len(bad) > 0 {
			return ReplyRecipients{}, fmt.Errorf("recipient %q is not a valid email address", bad[0])
		}
		// Explicit entries keep the tool's historical first-seen precedence:
		// ahead of the original To/Cc representations.
		cc = append(extra, cc...)
	}

	// Exclusion + de-duplication on the lowercased bare address: the primary
	// (already To) and the mailbox's own address never ride Cc, and a
	// display-name duplicate of an already-present address is dropped in
	// favour of the first-seen representation.
	primaryKey := ""
	if len(to) > 0 {
		primaryKey = strings.ToLower(to[0].Address)
	}
	ownKey := strings.ToLower(strings.TrimSpace(in.OwnAddress))
	if primaryKey != "" && ownKey != "" && primaryKey == ownKey {
		// Self-exclusion leaves no eligible primary: editable empty
		// recipients — never send-to-self, never a guessed replacement.
		to = nil
		primaryKey = ""
	}
	seen := make(map[string]bool, len(cc))
	ccClean := make([]mail.Address, 0, len(cc))
	for _, a := range cc {
		key := strings.ToLower(a.Address)
		if key == primaryKey || (ownKey != "" && key == ownKey) || seen[key] {
			continue
		}
		seen[key] = true
		ccClean = append(ccClean, a)
	}
	if to == nil {
		to = []mail.Address{}
	}
	if ccClean == nil {
		ccClean = []mail.Address{}
	}
	return ReplyRecipients{To: to, Cc: ccClean}, nil
}
