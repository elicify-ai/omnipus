package email

// W2 three-role folder discovery (spec mail-live-access-w2-discovery-and-
// cache-spec §3.1–§3.5; ADR-20261001).
//
// The three stable UI roles — inbox, sent, drafts — are logical roles, never
// folder names (§3.1): the slug is the job the folder does. Resolution maps
// a role to the real server folder name at runtime, through this ladder, in
// order, each step only when the previous produced no result:
//
//  1. A non-empty operator override (sent_folder_name / drafts_folder_name)
//     IS the candidate and outranks every discovery result; it is still
//     probed (validated), and a structurally missing override is an
//     actionable settings warning — never silently replaced, never ignored.
//     With both roles overridden no server enumeration is needed (§3.2
//     step 1).
//  2. SPECIAL-USE role attributes (RFC 6154) via LIST-EXTENDED, only where
//     the server advertises BOTH extensions (post-auth capability set);
//     an unsupported or partial extension degrades to the candidate list and
//     never fails the mailbox (R-3.2-2). One matching folder is probed;
//     several are ambiguity, never an arbitrary pick (§3.5).
//  3. The fixed candidate fallback list, in the spec's deterministic order;
//     the FIRST successful probe establishes source=fallback and ends
//     probing — only a successful probe ever establishes a mapping (R-3.2-1).
//  4. Nothing resolved: UNKNOWN with a safe reason class and the per-mailbox
//     setting offered — never ABSENT on a failed sweep (M-01/CX-1).
//
// UNKNOWN vs ABSENT (§3.4): absent requires BOTH proofs — the enumeration
// (LIST) answered without error AND every applicable candidate returned the
// structural [NONEXISTENT] response — AND the enumeration must show no other
// folder that could fill the role: a locally named folder outside the
// candidate list makes the sweep's failure indistinguishable from "the Sent
// folder has a name we did not guess", which is unresolved, never absent.
// INBOX takes no discovery and has no absent state: a failed INBOX is an
// account error and fails the read loudly (§3.4).
//
// Discovery is a read (R-3.2-4): zero CREATE/SUBSCRIBE/EXPUNGE, probes ride
// the same structural existence check the count path already interprets, and
// every probe is bounded by the caller's context through the ordinary read
// deadline (R-3.2-3). No retry loops: a stalled probe is attempted once.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Mapping sources — the landing-order register row-3 five-value enum
// (superseding the ADR's four-value proposal): override | special_use |
// fallback | saved | none, with none as the not-resolved wire value.
const (
	MappingSourceOverride   = "override"
	MappingSourceSpecialUse = "special_use"
	MappingSourceFallback   = "fallback"
	MappingSourceSaved      = "saved"
	MappingSourceNone       = "none"
)

// Availability values (the landed contracts/components/schemas/MailFolder.yaml
// availability enum: present | absent | unknown).
const (
	AvailabilityPresent = "present"
	AvailabilityAbsent  = "absent"
	AvailabilityUnknown = "unknown"
)

// Safe reason classes for unresolved and absent roles (§3.12 closed class
// list — never folder names, never raw upstream text).
const (
	ReasonDiscoveryUnresolved = "discovery_unresolved"
	ReasonMappingAmbiguous    = "mapping_ambiguous"
	ReasonOverrideMissing     = "override_missing"
	ReasonFolderAbsent        = "folder_absent"
)

// RoleResolution is one role's resolved mapping: the server folder name (how
// it resolved (source), the folder's UIDVALIDITY when probed (nil = never
// validated — never a fabricated 0), the availability state, and the safe
// reason class plus ambiguity list for the unresolved states.
type RoleResolution struct {
	Name         string
	Source       string
	UIDValidity  *uint32
	Availability string
	Reason       string
	Ambiguity    []string
}

// RoleMapping is the three-role resolution result (§4.1: the single producer
// of the values behind the landed MailFolder wire fields).
type RoleMapping struct {
	Inbox  RoleResolution
	Sent   RoleResolution
	Drafts RoleResolution
}

// Overrides carries the raw stored per-mailbox override fields exactly as
// persisted (empty/absent = automatic; non-empty = operator intent, whatever
// its provenance — R-3.3-3).
type Overrides struct {
	SentFolderName   string
	DraftsFolderName string
}

// sentFolderCandidates / draftsFolderCandidates are the finite candidate
// fallback lists in the spec's fixed deterministic order (§3.2 step 4). The
// first entry is today's withDefaults literal, demoted to a proposal: its
// presence in this list proves nothing about the server.
var (
	sentFolderCandidates   = []string{"Sent", "Sent Items", "Sent Messages", "[Gmail]/Sent Mail"}
	draftsFolderCandidates = []string{"Drafts", "Draft", "[Gmail]/Drafts"}
)

// Discovery resolves the three roles against the live server (§4.1: one
// service, consumed by the gateway handlers; it never dials outside the
// Client's ordinary session discipline).
type Discovery struct {
	client *Client
}

// NewDiscovery builds a discovery service over the mailbox client.
func NewDiscovery(cl *Client) *Discovery {
	return &Discovery{client: cl}
}

// Resolve runs the discovery ladder and returns the three-role mapping. The
// caller's context bounds the whole operation (R-3.2-3); a stalled probe
// leaves its role unknown, never absent. A failed INBOX (any reason,
// including structural not-found) fails the whole read — a mailbox without
// an inbox is a broken account (§3.4).
func (d *Discovery) Resolve(ctx context.Context, scope Scope, overrides Overrides) (RoleMapping, error) {
	client, _, err := d.client.dialIMAP(ctx)
	if err != nil {
		return RoleMapping{}, fmt.Errorf("email discovery: %s: %w", ClassifyMailError(err), err)
	}
	defer client.Close()

	// INBOX is protocol-fixed (R-3.1-3): no discovery, no absent state; the
	// probe records its validated epoch for the snapshot. Its mapping source
	// is none-of-the-discovery-values: the name is fixed by the protocol, not
	// resolved by any ladder step.
	inboxEpoch, err := statusUIDValidity(ctx, client, "INBOX")
	if err != nil {
		return RoleMapping{}, fmt.Errorf("email discovery: inbox: %s: %w", ClassifyMailError(err), err)
	}
	mapping := RoleMapping{
		Inbox: RoleResolution{
			Name:         "INBOX",
			Source:       MappingSourceNone,
			UIDValidity:  inboxEpoch,
			Availability: AvailabilityPresent,
		},
	}

	// Capability detection from the post-auth set (OQ-3 default): BOTH names
	// are required for the attribute path; anything less is "use the
	// candidate list".
	caps := client.Caps()
	useAttrs := caps.Has(imap.CapListExtended) && caps.Has(imap.CapSpecialUse)

	// One enumeration serves every role that needs discovery and carries the
	// absence evidence (§3.4 proof 1). With both roles overridden no server
	// enumeration is needed at all (§3.2 step 1, CX-2's discrete claim).
	var listing []folderListingEntry
	if overrides.SentFolderName == "" || overrides.DraftsFolderName == "" {
		listing, err = enumerateFolders(ctx, client, useAttrs)
		if err != nil {
			return RoleMapping{}, fmt.Errorf("email discovery: %s: %w", ClassifyMailError(err), err)
		}
	}

	mapping.Sent = resolveRole(ctx, client, overrides.SentFolderName, useAttrs, listing,
		imap.MailboxAttrSent, sentFolderCandidates)
	mapping.Drafts = resolveRole(ctx, client, overrides.DraftsFolderName, useAttrs, listing,
		imap.MailboxAttrDrafts, draftsFolderCandidates)
	return mapping, nil
}

// folderListingEntry is one enumerated mailbox: its name and, where the
// server advertised SPECIAL-USE, its role attributes.
type folderListingEntry struct {
	name  string
	attrs []imap.MailboxAttr
}

// resolveRole runs the ladder for one role: override → special-use →
// candidate list → unknown/absent verdict (§3.2, §3.4).
func resolveRole(ctx context.Context, client *imapclient.Client, override string, useAttrs bool, listing []folderListingEntry, attr imap.MailboxAttr, candidates []string) RoleResolution {
	// 1. The override wins outright; validation never replaces it (R-3.3-1).
	if override != "" {
		epoch, err := statusUIDValidity(ctx, client, override)
		switch {
		case err == nil:
			return RoleResolution{Name: override, Source: MappingSourceOverride, UIDValidity: epoch, Availability: AvailabilityPresent}
		case isNonexistentFolder(err):
			// The stored name no longer exists on the server: an actionable
			// settings warning — the override is never silently swapped for
			// another folder and never silently ignored (§3.2 step 1, O-2).
			return RoleResolution{Name: override, Source: MappingSourceOverride, Availability: AvailabilityUnknown, Reason: ReasonOverrideMissing}
		default:
			return RoleResolution{Name: override, Source: MappingSourceOverride, Availability: AvailabilityUnknown, Reason: ClassifyMailError(err)}
		}
	}

	// 2. SPECIAL-USE role attributes (§3.2 step 3).
	if useAttrs {
		var matches []string
		for _, entry := range listing {
			for _, a := range entry.attrs {
				if a == attr {
					matches = append(matches, entry.name)
					break
				}
			}
		}
		switch len(matches) {
		case 1:
			epoch, err := statusUIDValidity(ctx, client, matches[0])
			if err == nil {
				return RoleResolution{Name: matches[0], Source: MappingSourceSpecialUse, UIDValidity: epoch, Availability: AvailabilityPresent}
			}
			if !isNonexistentFolder(err) {
				return RoleResolution{Source: MappingSourceNone, Availability: AvailabilityUnknown, Reason: ClassifyMailError(err)}
			}
			// A tagged folder that answers structural not-found proves
			// nothing; fall through to the candidate list.
		case 0:
			// Fall through to the candidate list.
		default:
			// Several candidate folders for one role: surface the ambiguity
			// and let the user choose through the setting — never an
			// arbitrary pick (R-3.5-2/R-3.5-3).
			sorted := append([]string(nil), matches...)
			sort.Strings(sorted)
			return RoleResolution{Source: MappingSourceNone, Availability: AvailabilityUnknown, Reason: ReasonMappingAmbiguous, Ambiguity: sorted}
		}
	}

	// 3. Candidate fallback list: the FIRST successful probe establishes the
	// mapping and ends probing; only a structural not-found is absence
	// evidence; any other probe failure leaves the role unknown (R-3.2-1,
	// §3.4).
	for _, cand := range candidates {
		epoch, err := statusUIDValidity(ctx, client, cand)
		switch {
		case err == nil:
			return RoleResolution{Name: cand, Source: MappingSourceFallback, UIDValidity: epoch, Availability: AvailabilityPresent}
		case isNonexistentFolder(err):
			// Structural not-found on this candidate: keep sweeping.
		default:
			return RoleResolution{Source: MappingSourceNone, Availability: AvailabilityUnknown, Reason: ClassifyMailError(err)}
		}
	}

	// 4. The sweep failed. Absent ONLY where both §3.4 proofs hold AND the
	// enumeration shows no folder the sweep did not examine: a locally named
	// folder outside the candidate list makes the failure indistinguishable
	// from "the role's folder has a name we did not guess" — unresolved,
	// never absent (M-01/CX-1).
	for _, entry := range listing {
		if strings.EqualFold(entry.name, "INBOX") {
			continue
		}
		if entry.hasAttr(attr) { // examined (and handled) by the attribute step
			continue
		}
		if containsFold(candidates, entry.name) { // examined by the sweep
			continue
		}
		return RoleResolution{Source: MappingSourceNone, Availability: AvailabilityUnknown, Reason: ReasonDiscoveryUnresolved}
	}
	return RoleResolution{Source: MappingSourceNone, Availability: AvailabilityAbsent, Reason: ReasonFolderAbsent}
}

// enumerateFolders lists every mailbox once. Where the server advertises the
// extensions the listing also returns SPECIAL-USE attributes (RETURN
// (SPECIAL-USE) — full enumeration WITH attrs, never the attribute-filtered
// SELECT form, so the same reply carries the §3.4 absence evidence); without
// them it is a plain LIST.
func enumerateFolders(ctx context.Context, client *imapclient.Client, withAttrs bool) ([]folderListingEntry, error) {
	var opts *imap.ListOptions
	if withAttrs {
		opts = &imap.ListOptions{ReturnSpecialUse: true}
	}
	items, err := runIMAP(ctx, "list folders", func() ([]*imap.ListData, error) {
		return client.List("", "*", opts).Collect()
	})
	if err != nil {
		return nil, err
	}
	out := make([]folderListingEntry, 0, len(items))
	for _, item := range items {
		if item == nil || item.Mailbox == "" {
			continue
		}
		out = append(out, folderListingEntry{name: item.Mailbox, attrs: item.Attrs})
	}
	return out, nil
}

// statusUIDValidity probes one folder with STATUS (the same structural
// existence check the count path interprets) and returns its UIDVALIDITY.
// A present folder that reports no usable epoch yields nil — an unknown
// epoch is nullable state, never a fabricated 0 (CX-4).
func statusUIDValidity(ctx context.Context, client *imapclient.Client, name string) (*uint32, error) {
	status, err := runIMAP(ctx, "status probe", func() (*imap.StatusData, error) {
		return client.Status(name, &imap.StatusOptions{UIDValidity: true}).Wait()
	})
	if err != nil {
		return nil, err
	}
	if status == nil || status.UIDValidity == 0 {
		return nil, nil
	}
	v := status.UIDValidity
	return &v, nil
}

// hasAttr reports whether the listing entry carries the role attribute.
func (e folderListingEntry) hasAttr(attr imap.MailboxAttr) bool {
	for _, a := range e.attrs {
		if a == attr {
			return true
		}
	}
	return false
}

// containsFold reports whether the candidate list names the folder
// (case-insensitive: IMAP names are case-sensitive in general but INBOX is
// not, and candidates are compared with the same tolerance the probe used).
func containsFold(candidates []string, name string) bool {
	for _, c := range candidates {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}
