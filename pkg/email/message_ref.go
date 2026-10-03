package email

import "fmt"

// message_ref.go — THE single message-reference issuer (w5-integration
// US-9; mail-live-access-landing-order register rows 8/16; ADR-20261001
// correction I-03). One mint behind every surface that carries an issued
// reference — the client's on-lease issue (transport.go::ReadMessage), the
// agent read_message tool (pkg/tools) and the gateway panel list/detail
// handlers — so no surface ever formats a reference of its own (register
// R-4: no second implementation; the tools never mint locally, w4 spec
// US-3.AC-4).
//
// The wire grammar is W0's landed contract
// (contracts/components/schemas/MailMessageSummary.yaml::message_ref):
// uid:<uidvalidity>:<uid> — the string binds the folder epoch and the UID.
// The pair and configuration generation bind by ISSUANCE CONTEXT, not by
// string content: a reference is only ever issued inside one pair's own
// authorized read (the workspace-resolved tool path, the pair-scoped panel
// handler, the pooled lease), and every consumer validates it with W2's
// same-lease epoch comparison (view.go::refEpochMismatch) before any fetch
// or mutation. This file never imports the gateway, holds no credential
// material, and is transport-neutral (no IMAP types) — w5 spec §7.1 row 16.
//
// Parsing a PRESENTED reference stays W2's validation half of register
// row 16 (view.go::parseMailRef): this issuer mints, it does not decode —
// one half per owner, no shared drift.

// MessageRefClaims is the fetched metadata an issued reference binds.
// UIDValidity is the folder epoch the UID is valid under, as observed on
// the same session that fetched the message. Zero means the issuing
// transport could not prove an epoch — a Transport implementation without
// issue-on-lease support (a fixture double, never the production client,
// which always issues on its lease) — and mints the validator-defined
// consumable shape ("nothing to compare", view.go::refEpochMismatch): the
// reference still addresses folder+UID, it just carries no epoch claim.
type MessageRefClaims struct {
	UIDValidity uint32
	UID         uint32
}

// IssueMessageRef renders the issued opaque reference for one fetched
// message — the one mint behind every list/detail/attachment-metadata
// result that carries message_ref, including results for messages without
// the optional Message-ID (US-9.1). A zero UID addresses nothing; the
// empty string tells the caller to omit the field rather than mint an
// unparsable reference.
func IssueMessageRef(claims MessageRefClaims) string {
	if claims.UID == 0 {
		return ""
	}
	return fmt.Sprintf("uid:%d:%d", claims.UIDValidity, claims.UID)
}
