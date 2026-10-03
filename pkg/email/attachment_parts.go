package email

// Targeted single-part access and MIME structure classification
// (ADR-20261001 "Attachment metadata / selected-part access" rows; w4 spec
// §3.1 — the transfer service and the agent attachment tools inject these).
//
// Two rules shape everything here:
//
//  1. Only the SELECTED part ever moves. Where ReadView fetches
//     BODY.PEEK[] (the whole message) and then picks a part, this file
//     resolves the stable leaf index against the server-reported
//     BODYSTRUCTURE and fetches exactly one section with BODY.PEEK — flags
//     never change, no unrelated part or whole message is read (w4 spec
//     M10: byte counters must show only the selected part's bytes).
//
//  2. A reference is validated on the SAME connection that fetches
//     (grill I-03): a uid: ref's epoch (UIDVALIDITY) is compared against
//     the folder's live epoch after SELECT, before any part data moves; a
//     mismatch is the typed stale-reference refusal, never a best-effort
//     fetch against whatever the folder now holds. (The pair/generation
//     binding lands with the w5 integration wave's generation value —
//     register row 12; the epoch half is enforced here.)

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ErrMailStaleReference marks a reference whose epoch failed validation on
// the connection that would have served it — the typed stale-reference
// refusal (I-03). The gateway maps it to the typed 409; the tools return it
// as an explicit error. It never degrades into a best-effort fetch.
var ErrMailStaleReference = errors.New("stale message reference")

// ErrMailPartNotFound marks a well-formed reference naming a part index the
// message's MIME structure does not carry (the 404 class; distinct from a
// transport failure, which is never classified as absent).
var ErrMailPartNotFound = errors.New("attachment part not found")

// ErrMailPartTooLarge marks a part whose ACTUAL DECODED bytes exceed
// maxViewPartBytes (the 413 class). Reported metadata never substitutes for
// this check — the decoded stream is the cap authority (grill I-05).
var ErrMailPartTooLarge = errors.New("attachment part over the 25 MiB per-part cap")

// AttachmentPartDescriptor is one leaf MIME part's structure-level metadata.
// It carries NO body bytes: listing is a structure read (BODYSTRUCTURE) plus,
// for draft-marker candidates, a per-part MIME-header peek — never a body
// fetch, never a \Seen write.
type AttachmentPartDescriptor struct {
	// PartIndex is the stable leaf-walk index — the same enumeration
	// viewFromRaw counts (draft bookkeeping part INCLUDED; it occupies its
	// own index exactly as MailView.DraftBodyPart does) and the
	// {partIndex} address of the read/download routes.
	PartIndex int
	// Filename is the sanitized declared name (SanitizeAttachmentName
	// semantics); empty when the part declares none.
	Filename string
	// ContentType is the structure's media type ("text/plain" form).
	ContentType string
	// Disposition is the declared Content-Disposition ("attachment",
	// "inline", or "" when undeclared).
	Disposition string
	// ContentID is the bare Content-ID (angles stripped), for cid matching.
	ContentID string
	// ReportedSizeBytes is the server-reported ENCODED size in octets
	// (BODYSTRUCTURE size) — the honest labelled transfer-size claim, never
	// substituted for a decoded byte count. Negative when the server
	// reported none.
	ReportedSizeBytes int64
	// IsAttachment applies viewFromRaw's own classification rule:
	// disposition "attachment" or a declared filename.
	IsAttachment bool
	// OmnipusDraftBody marks the draft's own body bookkeeping part (the
	// X-Omnipus-Part: draft-body marker header — the ONLY recognition
	// signal, never the name/type pair). It is excluded from attachment
	// listings by every consumer, exactly as MailView.Attachments is.
	OmnipusDraftBody bool
}

// AttachmentPart is one fetched leaf part: its descriptor plus the DECODED
// bytes. Data is nil exactly when DataUnavailable is true.
type AttachmentPart struct {
	AttachmentPartDescriptor
	Data []byte
	// DataUnavailable marks a part whose bytes could not be delivered: over
	// the decoded cap (ErrMailPartTooLarge) or a decode failure. The
	// descriptor still travels so callers can name the part honestly.
	DataUnavailable bool
}

// attachmentSection is one leaf of the server-reported BODYSTRUCTURE: its
// stable leaf index, its IMAP section path, its declared metadata and its
// declared content transfer encoding.
type attachmentSection struct {
	descriptor AttachmentPartDescriptor
	section    []int
	encoding   string
}

// walkAttachmentSections maps a BODYSTRUCTURE into leaf order. The DFS leaf
// order of the IMAP body structure equals go-message's multipart leaf walk
// (both descend into multipart/* children in order and treat every other
// node — message/rfc822 included — as a leaf), which is the enumeration
// viewFromRaw counts; that correspondence is what makes part_index stable
// across the whole read surface.
func walkAttachmentSections(bs imap.BodyStructure) []attachmentSection {
	if single, ok := bs.(*imap.BodyStructureSinglePart); ok {
		// A non-multipart message: the leaf IS the whole body — the empty
		// section ("" — BODY[]), not section 1.
		return []attachmentSection{{descriptor: singlePartDescriptor(single, 0), section: nil, encoding: single.Encoding}}
	}
	var out []attachmentSection
	leaf := 0
	var walk func(bs imap.BodyStructure, path []int)
	walk = func(bs imap.BodyStructure, path []int) {
		switch node := bs.(type) {
		case *imap.BodyStructureSinglePart:
			out = append(out, attachmentSection{descriptor: singlePartDescriptor(node, leaf), section: path, encoding: node.Encoding})
			leaf++
		case *imap.BodyStructureMultiPart:
			for i, child := range node.Children {
				walk(child, append(append([]int{}, path...), i+1))
			}
		}
	}
	if mp, ok := bs.(*imap.BodyStructureMultiPart); ok {
		for i, child := range mp.Children {
			walk(child, []int{i + 1})
		}
	}
	return out
}

// singlePartDescriptor maps one BODYSTRUCTURE leaf to its descriptor.
func singlePartDescriptor(sp *imap.BodyStructureSinglePart, idx int) AttachmentPartDescriptor {
	d := AttachmentPartDescriptor{
		PartIndex:         idx,
		ContentType:       sp.MediaType(),
		ReportedSizeBytes: -1,
	}
	if sp.Size > 0 {
		d.ReportedSizeBytes = int64(sp.Size)
	}
	if sp.Extended != nil && sp.Extended.Disposition != nil {
		d.Disposition = strings.ToLower(sp.Extended.Disposition.Value)
	}
	d.Filename = sanitizeMailPartName(sp.Filename())
	d.ContentID = stripContentIDAngles(sp.ID)
	d.IsAttachment = d.Disposition == "attachment" || d.Filename != ""
	return d
}

// fetchBodyStructure BODYSTRUCTUREs the already-selected folder's message on
// the given client (PEEK semantics — a structure read writes no flag).
func fetchBodyStructure(ctx context.Context, client *imapclient.Client, uid uint32) (imap.BodyStructure, error) {
	bufs, err := runIMAP(ctx, "fetch bodystructure", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID:           true,
			BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		}).Collect()
	})
	if err != nil {
		return nil, fmt.Errorf("email transport: fetch bodystructure: %w", err)
	}
	if len(bufs) == 0 || bufs[0] == nil || bufs[0].BodyStructure == nil {
		return nil, fmt.Errorf("email transport: %w: no body structure returned", ErrMessageNotFound)
	}
	return bufs[0].BodyStructure, nil
}

// selectAndValidateRef opens one connection, SELECTs the slug's folder and
// — for a uid: ref — compares the ref's epoch against the folder's live
// UIDVALIDITY BEFORE any part data moves (I-03, same-lease validation). A
// mid: ref self-validates by Message-ID search inside the addressed folder.
// The caller owns the returned client's lifetime.
func (c *Client) selectAndValidateRef(ctx context.Context, slug, ref string) (*imapclient.Client, uint32, error) {
	name, err := c.folderNameFor(slug)
	if err != nil {
		return nil, 0, err
	}
	r, err := parseMailRef(ref)
	if err != nil {
		return nil, 0, err
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, 0, err
	}
	uv, _, err := c.selectFolder(ctx, client, name)
	if err != nil {
		client.Close()
		return nil, 0, err
	}
	uid := r.uid
	if r.kind == "uid" {
		if verr := refEpochMismatch(r, uv); verr != nil {
			// The folder was recreated under a new epoch: the old UID names
			// a different message now. Typed refusal, connection closed,
			// zero fetches — never a fetch against whatever the folder now
			// holds. Same rule as view.go (refEpochMismatch, W2 §3.16).
			client.Close()
			return nil, 0, verr
		}
	} else {
		uid, err = c.searchMessageID(ctx, client, r.messageID)
		if err != nil {
			client.Close()
			return nil, 0, err
		}
		if uid == 0 {
			client.Close()
			return nil, 0, fmt.Errorf("email transport: %w: no message %s in %s", ErrMessageNotFound, ref, slug)
		}
	}
	return client, uid, nil
}

// ListAttachmentParts returns every leaf part descriptor of one message —
// structure metadata only: BODYSTRUCTURE plus, for draft-marker candidates
// (text/markdown leaves), the part's own MIME header peek. No body bytes
// move; no flag changes. The returned slice is in stable leaf order.
func (c *Client) ListAttachmentParts(ctx context.Context, slug, ref string) ([]AttachmentPartDescriptor, error) {
	client, uid, err := c.selectAndValidateRef(ctx, slug, ref)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	bs, err := fetchBodyStructure(ctx, client, uid)
	if err != nil {
		return nil, err
	}
	sections := walkAttachmentSections(bs)

	// Draft-marker candidates: the Omnipus draft-body part is text/markdown
	// by construction (renderMarkdownPart writes it). A genuine user
	// attachment may carry the same shape, so the marker HEADER — never the
	// name/type pair — decides. The peek is per candidate and cheap (a
	// header, not a body); the count is bounded by the server-reported
	// structure itself.
	out := make([]AttachmentPartDescriptor, len(sections))
	for i, sec := range sections {
		d := sec.descriptor
		if d.ContentType == "text/markdown" && d.IsAttachment {
			marker, merr := c.fetchPartIsDraftMarker(ctx, client, uid, sec.section)
			if merr != nil {
				return nil, merr
			}
			d.OmnipusDraftBody = marker
		}
		out[i] = d
	}
	return out, nil
}

// fetchPartIsDraftMarker peeks one part's MIME header and reports whether it
// carries the X-Omnipus-Part: draft-body marker.
func (c *Client) fetchPartIsDraftMarker(ctx context.Context, client *imapclient.Client, uid uint32, section []int) (bool, error) {
	bufs, err := runIMAP(ctx, "fetch part mime header", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID: true,
			BodySection: []*imap.FetchItemBodySection{{
				Specifier: imap.PartSpecifierMIME,
				Part:      section,
				Peek:      true,
			}},
		}).Collect()
	})
	if err != nil {
		return false, fmt.Errorf("email transport: fetch part mime header: %w", err)
	}
	if len(bufs) == 0 || bufs[0] == nil {
		return false, nil
	}
	// Both sides fold case: the header name reaches the peek buffer in
	// whatever case the composing client wrote it, so the needle folds with
	// the haystack (a mixed-case needle against a lowercased haystack can
	// never match — the draft body part then leaks as a fake message.md
	// attachment).
	needle := strings.ToLower(draftBodyPartHeader) + ":"
	for _, sec := range bufs[0].BodySection {
		lower := strings.ToLower(string(sec.Bytes))
		if strings.Contains(lower, needle) &&
			strings.Contains(lower, draftBodyPartValue) {
			return true, nil
		}
	}
	return false, nil
}

// ReadAttachmentPart fetches EXACTLY one leaf part's decoded bytes — the
// targeted single-part reader (part-specific PEEK: flags unchanged; no
// whole-message fetch; no unrelated part). The reference is validated on the
// same connection that performs the fetch; the 25 MiB DECODED cap is
// enforced on the actual decoded stream, never on reported metadata — an
// over-cap part fails with ErrMailPartTooLarge before any success state can
// exist (grill I-05's late-failure ordering).
func (c *Client) ReadAttachmentPart(ctx context.Context, slug, ref string, partIndex int) (*AttachmentPart, error) {
	return c.readAttachmentPart(ctx, slug, ref, partIndex, true)
}

// ReadAttachmentPartFull is the browser-Download role of the same reader:
// NO preview cap — the part streams to completion (grill I-05's two
// distinct byte resources; the preview cap is never applied to this role).
// The transfer is still one part-specific PEEK on the validated connection.
func (c *Client) ReadAttachmentPartFull(ctx context.Context, slug, ref string, partIndex int) (*AttachmentPart, error) {
	return c.readAttachmentPart(ctx, slug, ref, partIndex, false)
}

// ReadPartCapped adapts the reader to the shared transfer service's
// PartReader interface.
func (c *Client) ReadPartCapped(ctx context.Context, slug, ref string, partIndex int) (*AttachmentPart, error) {
	return c.ReadAttachmentPart(ctx, slug, ref, partIndex)
}

// ReadPartFull adapts the uncapped download role to the service interface.
func (c *Client) ReadPartFull(ctx context.Context, slug, ref string, partIndex int) (*AttachmentPart, error) {
	return c.ReadAttachmentPartFull(ctx, slug, ref, partIndex)
}

// DescribeParts adapts the structure classifier to the service interface.
func (c *Client) DescribeParts(ctx context.Context, slug, ref string) ([]AttachmentPartDescriptor, error) {
	return c.ListAttachmentParts(ctx, slug, ref)
}

// readAttachmentPart is the shared core of the two roles; capped selects the
// decoded-cap enforcement (viewer/save) or the completion guarantee
// (browser download).
func (c *Client) readAttachmentPart(ctx context.Context, slug, ref string, partIndex int, capped bool) (*AttachmentPart, error) {
	if partIndex < 0 {
		return nil, fmt.Errorf("%w: negative part index", ErrMailPartNotFound)
	}
	client, uid, err := c.selectAndValidateRef(ctx, slug, ref)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	bs, err := fetchBodyStructure(ctx, client, uid)
	if err != nil {
		return nil, err
	}
	sections := walkAttachmentSections(bs)
	if partIndex >= len(sections) {
		return nil, fmt.Errorf("%w: part index %d of %d leaves", ErrMailPartNotFound, partIndex, len(sections))
	}
	sec := sections[partIndex]

	bufs, err := runIMAP(ctx, "fetch part", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID: true,
			BodySection: []*imap.FetchItemBodySection{{
				Part: sec.section,
				Peek: true,
			}},
		}).Collect()
	})
	if err != nil {
		return nil, fmt.Errorf("email transport: fetch part %d: %w", partIndex, err)
	}
	var raw []byte
	for _, bsec := range bufs[0].BodySection {
		raw = bsec.Bytes
	}
	if len(raw) == 0 {
		return &AttachmentPart{AttachmentPartDescriptor: sec.descriptor, DataUnavailable: true}, nil
	}

	part := &AttachmentPart{AttachmentPartDescriptor: sec.descriptor}
	decoded, derr := decodePartBytes(io.LimitReader(bytes.NewReader(raw), maxEncodedPartBytes()), sec.encoding, capped)
	if derr != nil {
		// Over-cap and decode failures are BOTH honest unavailable outcomes
		// carrying the typed error — never a silent empty read (the
		// loud-degrade contract at the part level).
		part.DataUnavailable = true
		return part, derr
	}
	part.Data = decoded
	return part, nil
}

// maxEncodedPartBytes bounds the RAW network read behind the decoded cap.
// MessageMeta is one message's bounded header facts: the flags that decide
// visibility and the decoded subject — no body bytes, no flag writes.
type MessageMeta struct {
	Flags      []string
	Subject    string
	MessageID  string
	From       string
	FromName   string
	ReplyTo    string
	To         []string
	Cc         []string
	Date       time.Time
	Bcc        []string
	InReplyTo  string
	References string
}

// ReadMessageMeta fetches one message's envelope + flags — the bounded
// metadata read behind the attachment mint's context bar and visibility
// check (no body is fetched, flags never change). The reference is validated
// on the same connection that performs the fetch.
func (c *Client) ReadMessageMeta(ctx context.Context, slug, ref string) (*MessageMeta, error) {
	client, uid, err := c.selectAndValidateRef(ctx, slug, ref)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	bufs, err := runIMAP(ctx, "fetch meta", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID: true, Flags: true, Envelope: true,
		}).Collect()
	})
	if err != nil {
		return nil, fmt.Errorf("email transport: fetch meta: %w", err)
	}
	if len(bufs) == 0 || bufs[0] == nil || bufs[0].Envelope == nil {
		return nil, fmt.Errorf("email transport: %w: no message %s in %s", ErrMessageNotFound, ref, slug)
	}
	buf := bufs[0]
	meta := &MessageMeta{Subject: strings.TrimSpace(buf.Envelope.Subject), MessageID: buf.Envelope.MessageID}
	for _, f := range buf.Flags {
		meta.Flags = append(meta.Flags, string(f))
	}
	if len(buf.Envelope.From) > 0 {
		meta.From = addressString(buf.Envelope.From[0])
		meta.FromName = buf.Envelope.From[0].Name
	}
	if len(buf.Envelope.ReplyTo) > 0 {
		meta.ReplyTo = addressString(buf.Envelope.ReplyTo[0])
	}
	meta.To = splitAddressList(addressListString(buf.Envelope.To))
	meta.Cc = splitAddressList(addressListString(buf.Envelope.Cc))
	if len(buf.Envelope.Bcc) > 0 {
		meta.Bcc = splitAddressList(addressListString(buf.Envelope.Bcc))
	}
	if !buf.Envelope.Date.IsZero() {
		meta.Date = buf.Envelope.Date.UTC()
	}
	if len(buf.Envelope.InReplyTo) > 0 {
		meta.InReplyTo = buf.Envelope.InReplyTo[0]
	}
	return meta, nil
}

// maxEncodedPartBytes bounds the RAW network read behind the decoded cap.
// Every content transfer encoding this path decodes either preserves size
// (7bit/8bit/binary) or expands (base64 4/3; quoted-printable at most 2x on
// pathological input), so an encoded ceiling of cap + cap/2 + slack can
// never truncate a part whose decoded bytes would have passed the cap. The
// DECODED LimitReader in decodePartBytes stays the actual authority.
func maxEncodedPartBytes() int64 {
	capBytes := int64(maxViewPartBytes)
	return capBytes + capBytes/2 + 4096
}

// decodePartBytes decodes one fetched section's transfer encoding. In the
// capped role the 25 MiB cap is enforced on the DECODED stream — the actual
// decoded bytes are always the cap authority, never the reported metadata
// (grill I-05). In the uncapped (download) role no preview cap applies; the
// raw read stays bounded by maxEncodedPartBytes, and the decoded length is
// whatever the part actually is.
func decodePartBytes(r io.Reader, encoding string, capped bool) ([]byte, error) {
	var dec io.Reader
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		dec = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		dec = quotedprintable.NewReader(r)
	default:
		dec = r
	}
	bound := int64(maxViewPartBytes) + 1
	if !capped {
		// No preview cap for the download role; keep a decoding-stream
		// bound at the encoded ceiling's decoding size so a hostile
		// quoted-printable bomb cannot expand unboundedly — the bound is
		// the raw-read ceiling itself, never the 25 MiB preview number.
		bound = maxEncodedPartBytes()
	}
	out, err := io.ReadAll(io.LimitReader(dec, bound))
	if err != nil {
		return nil, fmt.Errorf("email transport: decode part: %w", err)
	}
	if capped && len(out) > maxViewPartBytes {
		return nil, ErrMailPartTooLarge
	}
	return out, nil
}
