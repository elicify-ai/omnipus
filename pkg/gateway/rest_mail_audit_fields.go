package gateway

// rest_mail_audit_fields.go — the MC-19/D46 full audit fields for the mail
// panel events (spec MC-19 + FR-025): every mail panel audit event carries
// the full recipient address list (incl. Bcc — MAJ-005), the origin derived
// from the acted-on draft's authorship (MIN-006: human | agent-draft |
// owner-draft), the argument hash (FR-080 ArgsHash, 64 lowercase hex) and —
// for attachment-bearing messages — per-attachment filename+size records
// (D33). The count-only shape and its "addresses are PII the auditor
// redacts" rationale are superseded by the founder's ruling (D46).

import (
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailAuditArgHash returns the event's arg_hash: the FR-080 ArgsHash of the
// request arguments (64 lowercase hex). A nil arg set (the argless discard)
// hashes deterministically like any other nil JSON.
func mailAuditArgHash(args any) string {
	h, err := audit.ArgsHash(args)
	if err != nil {
		// ArgsHash only fails on unmarshalable args; the request types are
		// JSON wire types, so this is unreachable — return "" rather than
		// fabricating a value.
		return ""
	}
	return h
}

// mailDraftOrigin derives the MIN-006 origin: the action acted on an Omnipus
// draft (X-Omnipus-Draft present) → "agent-draft"; otherwise the owner-
// authored (foreign) draft → "owner-draft".
func mailDraftOrigin(isOmnipusDraft bool) string {
	if isOmnipusDraft {
		return "agent-draft"
	}
	return "owner-draft"
}

// mailAuditAttachments renders per-attachment filename+size records (D33):
// filename and DECODED byte size — the same shape the wire's MailMessage
// attachment objects use.
func mailAuditAttachments(atts []email.Attachment) []map[string]any {
	out := make([]map[string]any, 0, len(atts))
	for _, at := range atts {
		out = append(out, map[string]any{
			"filename":   at.Name,
			"size_bytes": len(at.Data),
		})
	}
	return out
}

// mailViewDraftBodyPart reports whether a view part (email.MailPart) is the
// draft's own body bookkeeping part — recognized by the dedicated
// X-Omnipus-Part: draft-body marker header ONLY (renderMarkdownPart writes
// it), never by the filename/content type, which a genuine user attachment
// may equally carry. Such a part is Omnipus bookkeeping, never a user
// attachment: the draft carry paths skip it silently and unconditionally,
// and the attachment listings never list it. The audit path needs no filter
// of its own: every audited carried list is built through the same
// header-keyed skip, so a carried list never contains the bookkeeping part
// while a genuine user message.md stays in it.
func mailViewDraftBodyPart(p email.MailPart) bool {
	return p.OmnipusDraftBody
}

// mailAuditRecipients concatenates the recipient lists into one address list
// (MC-19: addresses, not a count — incl. Bcc, MAJ-005).
func mailAuditRecipients(lists ...[]string) []string {
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	out := make([]string, 0, total)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
