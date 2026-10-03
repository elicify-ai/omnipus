package email

// RED pack — W2 folder discovery (oracle: spec only, never the implementation).
//
// Every expected value below is derived from the specification BEFORE any
// implementation existed (none exists at the time of writing — the symbols
// NewDiscovery/Scope/Overrides/RoleMapping do not compile yet, which is the
// expected RED state for a Wave-C RED pack; see the delivery report):
//
//   - docs/internal/specs/mail-live-access-w2-discovery-and-cache-spec.md
//     §3.1–§3.5 (roles, discovery ladder, overrides, UNKNOWN vs ABSENT),
//     §5 US-1..US-4, §6 scenarios D-1..D-5/O-1..O-5/U-1..U-5, §7.1 test rows,
//     §11 counterexamples CX-1/CX-2.
//   - ADR-20261001 failure-behaviour table ("Missing Sent/Drafts" row): a
//     failed candidate sweep is UNKNOWN/unresolved, never "no such folder"
//     (founder correction M-01); only structurally confirmed absence is empty.
//   - landing-order register row 3: mapping_source has FIVE values
//     (override|special_use|fallback|saved|none) — the register supersedes the
//     ADR's four-value proposal.
//
// Harness: the real go-imap/v2 in-memory server via a counting/stalling
// session wrapper (test-only instrumentation around imapmemserver, per the
// ADR test-strategy row "Extend protocol/session instrumentation in test code
// only"). The global imapDial seam forbids blind parallelism, so no test here
// calls t.Parallel().
//
// Known harness gap (named in the delivery report): imapmemserver.User.Create
// ignores CreateOptions.SpecialUse, so scenario D-1 (source=special_use)
// cannot be driven on this harness. Everything else in the ladder is covered.

import (
	"context"
	"crypto/tls"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// discoveryInstr is the test-only command counter/staller wrapped around the
// real in-memory server session. It observes the wire commands discovery
// issues (STATUS probes per mailbox, LIST enumeration, CREATE/SUBSCRIBE
// mutations) without touching production code.
type discoveryInstr struct {
	imapserver.Session

	mu         sync.Mutex
	statusOf   map[string]int
	listCalls  int
	creates    int
	subscribes int
	stall      map[string]chan struct{} // STATUS on these mailboxes blocks until closed
}

func (s *discoveryInstr) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	s.mu.Lock()
	s.statusOf[mailbox]++
	ch := s.stall[mailbox]
	s.mu.Unlock()
	if ch != nil {
		<-ch // blocked probe: the caller's context deadline must fire first
	}
	return s.Session.Status(mailbox, options)
}

func (s *discoveryInstr) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	return s.Session.List(w, ref, patterns, options)
}

func (s *discoveryInstr) Create(mailbox string, options *imap.CreateOptions) error {
	s.mu.Lock()
	s.creates++
	s.mu.Unlock()
	return s.Session.Create(mailbox, options)
}

func (s *discoveryInstr) Subscribe(mailbox string) error {
	s.mu.Lock()
	s.subscribes++
	s.mu.Unlock()
	return s.Session.Subscribe(mailbox)
}

func (s *discoveryInstr) counts() (status map[string]int, list, creates, subscribes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.statusOf))
	for k, v := range s.statusOf {
		out[k] = v
	}
	return out, s.listCalls, s.creates, s.subscribes
}

func (s *discoveryInstr) releaseStalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.stall {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

// startDiscoveryIMAP boots the real in-memory IMAP server with exactly the
// named folders (INBOX is created only when requested — the missing-INBOX
// regression needs a server without it), the given capability set, and a
// counting/stalling session wrapper on the dial seam. Mirrors the house
// harness startViewIMAPRaw (pkg/email/view_test.go).
func startDiscoveryIMAP(t *testing.T, inbox bool, folders []string, caps imap.CapSet, stall []string) (*Client, string, *discoveryInstr) {
	t.Helper()
	// Discovery clients are source-less: Resolve rides withMailSession, which
	// consults the process-wide FR-W1-2 gate for nil-source clients. Declare
	// the legacy-dial world here so the discovery ladder is exercised under
	// any test order (order-independence fixture) — after any other test
	// wired a shared manager, a nil-source Resolve would be the
	// ErrSessionSourceMissing refusal instead of the probed flow.
	pinLegacyDialWorld(t)

	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	if inbox {
		if err := user.Create("INBOX", nil); err != nil {
			t.Fatalf("create INBOX: %v", err)
		}
	}
	for _, f := range folders {
		if err := user.Create(f, nil); err != nil {
			t.Fatalf("create %s: %v", f, err)
		}
	}
	mem.AddUser(user)

	instr := &discoveryInstr{
		Session:  mem.NewSession(),
		statusOf: map[string]int{},
		stall:    map[string]chan struct{}{},
	}
	for _, name := range stall {
		instr.stall[name] = make(chan struct{})
	}
	t.Cleanup(instr.releaseStalls)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return instr, nil, nil
		},
		InsecureAuth: true,
		Caps:         caps,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	prev := imapDial
	imapDial = func(_ context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	cl, err := NewClient(Account{
		IMAPHost: host,
		IMAPPort: port,
		SMTPHost: host,
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cl, ln.Addr().String(), instr
}

// resolveAutomatic runs one automatic (no overrides) resolution with a
// comfortably bounded context (the R-3.2-3 read-work deadline is the caller's
// context; 2 s leaves orders of magnitude over the local server's latency
// while still firing inside the stall test).
func resolveAutomatic(t *testing.T, cl *Client) (RoleMapping, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return NewDiscovery(cl).Resolve(ctx, Scope{PairID: "pair-disc", Generation: "gen-disc"}, Overrides{})
}

// TestDiscovery_FallbackOrderedProbeStopsAtFirstSuccess — US-1.2, D-2, FR-W2-4.
// Oracle: the candidate order is the spec's fixed list (sent: Sent, Sent
// Items, Sent Messages, [Gmail]/Sent Mail); the first successful probe
// establishes source=fallback and ENDS probing — later candidates are never
// probed (D-2: "the later candidates were not probed after the first
// success"). Only a successful probe establishes a mapping (R-3.2-1).
func TestDiscovery_FallbackOrderedProbeStopsAtFirstSuccess(t *testing.T) {
	// "Sent" is absent; both later candidates exist. A correct sweep stops at
	// "Sent Items"; a sweep that scans the whole list (or reorders it) fails
	// the STATUS counters below.
	cl, _, instr := startDiscoveryIMAP(t, true, []string{"Sent Items", "[Gmail]/Sent Mail", "Drafts"}, imap.CapSet{imap.CapIMAP4rev1: {}}, nil)

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if m.Sent.Availability != "present" {
		t.Fatalf("sent availability = %q, want present", m.Sent.Availability)
	}
	if m.Sent.Name != "Sent Items" {
		t.Fatalf("sent resolved name = %q, want %q (first successful candidate in the fixed order)", m.Sent.Name, "Sent Items")
	}
	if m.Sent.Source != "fallback" {
		t.Fatalf("sent source = %q, want %q (register row 3 five-value enum)", m.Sent.Source, "fallback")
	}
	if m.Sent.UIDValidity == nil || *m.Sent.UIDValidity == 0 {
		t.Fatalf("sent UIDVALIDITY must be a real probed value (R-3.2-1), got %+v", m.Sent.UIDValidity)
	}

	status, _, _, _ := instr.counts()
	if status["Sent"] != 1 {
		t.Errorf("candidate %q probed %d times, want exactly 1 (fixed order, first entry)", "Sent", status["Sent"])
	}
	if status["Sent Items"] != 1 {
		t.Errorf("candidate %q probed %d times, want exactly 1 (probe succeeds here)", "Sent Items", status["Sent Items"])
	}
	if status["Sent Messages"] != 0 {
		t.Errorf("candidate %q probed %d times, want 0 — probing must stop at the first success (D-2)", "Sent Messages", status["Sent Messages"])
	}
	if status["[Gmail]/Sent Mail"] != 0 {
		t.Errorf("candidate %q probed %d times, want 0 — probing must stop at the first success (D-2)", "[Gmail]/Sent Mail", status["[Gmail]/Sent Mail"])
	}

	// Drafts: first candidate "Drafts" exists on this server.
	if m.Drafts.Availability != "present" || m.Drafts.Name != "Drafts" || m.Drafts.Source != "fallback" {
		t.Fatalf("drafts resolution = %+v, want present/Drafts/fallback", m.Drafts)
	}
}

// TestDiscovery_NoCandidatesIsUnknownNeverAbsent — CX-1, D-4, US-1.3, US-3.1.
// Oracle (founder correction M-01, ADR failure table + ADR test-strategy
// "Missing folder negative controls" row): LIST succeeds and an untagged,
// locally named Sent folder exists that no candidate names — from the client's
// seat this is INDISTINGUISHABLE from "no candidates exist", so the role is
// UNKNOWN (source none, discovery_unresolved), never absent, never an error
// page, never a false empty folder. §3.4: unknown counts are not invented
// (the mapping carries no fabricated epoch/total).
func TestDiscovery_NoCandidatesIsUnknownNeverAbsent(t *testing.T) {
	cl, _, _ := startDiscoveryIMAP(t, true, []string{"Elementos-Enviados"}, nil, nil)

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if m.Sent.Availability != "unknown" {
		t.Fatalf("sent availability = %q, want %q — a failed candidate sweep is unresolved, never absent (M-01/CX-1)", m.Sent.Availability, "unknown")
	}
	if m.Sent.Source != "none" {
		t.Fatalf("sent source = %q, want %q (register row 3: nothing resolved maps to none)", m.Sent.Source, "none")
	}
	if m.Sent.Name != "" {
		t.Fatalf("sent resolved name = %q, want empty — no folder may be claimed for an unresolved role", m.Sent.Name)
	}
	if m.Sent.UIDValidity != nil {
		t.Fatalf("sent UIDVALIDITY = %d, want nil — an unresolved role has no epoch (CX-4 family: never a fabricated value)", *m.Sent.UIDValidity)
	}
	if m.Sent.Reason == "" {
		t.Fatalf("unresolved role must carry its safe reason class (§3.12 discovery_unresolved), got empty")
	}
	// The drafts role faces the same evidence and must also stay unresolved —
	// never absent (the sweep failed for it identically).
	if m.Drafts.Availability != "unknown" {
		t.Fatalf("drafts availability = %q, want unknown under the same failed sweep", m.Drafts.Availability)
	}
}

// TestDiscovery_ConfirmedAbsenceIsEmptyWithExplanation — U-1, US-3.3.
// Oracle: absent requires BOTH proofs (§3.4): discovery completed (LIST
// answered) AND every applicable candidate returned the structural
// [NONEXISTENT] the memserver emits for missing mailboxes. With only INBOX on
// the server both proofs hold and the role is genuinely ABSENT — reported as
// empty with an explanation, never as a mailbox-level failure (the list-face
// twin is TestReadFolderPage_ConfirmedAbsentRoleReturnsEmptyNotError).
func TestDiscovery_ConfirmedAbsenceIsEmptyWithExplanation(t *testing.T) {
	cl, _, instr := startDiscoveryIMAP(t, true, nil, nil, nil)

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if m.Sent.Availability != "absent" {
		t.Fatalf("sent availability = %q, want absent — LIST succeeded and every candidate probed [NONEXISTENT] (US-3.3)", m.Sent.Availability)
	}
	if m.Sent.Name != "" {
		t.Fatalf("absent role must not claim a folder name, got %q", m.Sent.Name)
	}
	if m.Sent.UIDValidity != nil {
		t.Fatalf("absent role has no epoch; got %+v, want nil", m.Sent.UIDValidity)
	}
	if m.Sent.Reason == "" {
		t.Fatalf("confirmed absence must carry its safe explanation class (§3.4: 'real zero with the explanation')")
	}

	// The absence verdict must have come from actually probing the fixed
	// candidate list — every sent candidate was probed exactly once.
	status, _, _, _ := instr.counts()
	for _, cand := range []string{"Sent", "Sent Items", "Sent Messages", "[Gmail]/Sent Mail"} {
		if status[cand] != 1 {
			t.Errorf("candidate %q probed %d times, want 1 — absence requires EVERY candidate to return structural not-found", cand, status[cand])
		}
	}
}

// TestDiscovery_ProbeTimeoutIsUnknownNotAbsent — U-2, US-3.2.
// Oracle: a probe failure that is NOT the structural not-found response
// (here: the probe stalls until the caller's deadline fires) leaves the role
// UNKNOWN — never absent, never present. §3.4: "network, permission, timeout,
// auth and TLS failures during discovery or probing all leave the role
// unknown, never empty".
func TestDiscovery_ProbeTimeoutIsUnknownNotAbsent(t *testing.T) {
	// "Sent" EXISTS (its probe would succeed) but its STATUS stalls past the
	// deadline — a transport-class failure, not [NONEXISTENT].
	cl, _, instr := startDiscoveryIMAP(t, true, []string{"Sent"}, nil, []string{"Sent"})

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		// A whole-operation failure is also an acceptable shape for a dead
		// sweep (US-3.2: "unknown — or the read's own failure"), but it must
		// never be a healthy result. The mapping assertions below still hold
		// for the returned mapping if one is produced.
		t.Logf("Resolve returned an operation-level error (allowed by US-3.2): %v", err)
	}

	if m.Sent.Availability == "absent" {
		t.Fatal("sent availability = absent after a probe TIMEOUT — a stalled probe is unresolved, never absent (CX-1 family)")
	}
	if m.Sent.Availability == "present" {
		t.Fatal("sent availability = present after a probe TIMEOUT — a stalled probe proves nothing (R-3.2-1: only a successful probe establishes a mapping)")
	}
	if m.Sent.Availability != "" && m.Sent.Availability != "unknown" {
		t.Fatalf("sent availability = %q, want unknown (or an operation-level error), got a third state", m.Sent.Availability)
	}
	// The stalled probe was attempted exactly once — no retry loop (R-3.2-3:
	// bounded, coalesced; ADR: no hidden chain of retries).
	status, _, _, _ := instr.counts()
	if status["Sent"] > 1 {
		t.Errorf("stalled candidate probed %d times — discovery must not retry within one operation", status["Sent"])
	}
}

// TestDiscovery_OverrideWinsWithoutEnumeration — CX-2 (fallback face), US-2.1.
// Oracle: a non-empty stored override that probes successfully IS the mapping
// (source=override) and outranks every discovery result; §3.2 step 1 adds the
// stronger discrete claim this test pins: "no server enumeration is needed
// for that role". Both roles carry overrides here, so a correct
// implementation issues zero LIST commands and probes only the override names.
// A careless implementation that prefers discovery (CX-2's mutation) probes
// "Sent Items" or enumerates, and fails the counters.
func TestDiscovery_OverrideWinsWithoutEnumeration(t *testing.T) {
	cl, _, instr := startDiscoveryIMAP(t, true, []string{"Archive", "Sent Items", "Drafts"}, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m, err := NewDiscovery(cl).Resolve(ctx, Scope{PairID: "pair-disc", Generation: "gen-disc"}, Overrides{SentFolderName: "Archive", DraftsFolderName: "Drafts"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if m.Sent.Availability != "present" || m.Sent.Name != "Archive" {
		t.Fatalf("sent = %+v, want present/Archive — the operator's override wins (CX-2)", m.Sent)
	}
	if m.Sent.Source != "override" {
		t.Fatalf("sent source = %q, want %q", m.Sent.Source, "override")
	}
	if m.Sent.UIDValidity == nil || *m.Sent.UIDValidity == 0 {
		t.Fatalf("override still validates (R-3.2-1/R-3.3-1): UIDVALIDITY must be a real probed value, got %+v", m.Sent.UIDValidity)
	}
	if m.Drafts.Source != "override" || m.Drafts.Name != "Drafts" {
		t.Fatalf("drafts = %+v, want override/Drafts", m.Drafts)
	}

	status, lists, _, _ := instr.counts()
	if lists != 0 {
		t.Errorf("LIST issued %d times with both overrides resolving — §3.2 step 1: no server enumeration is needed (CX-2)", lists)
	}
	if status["Sent Items"] != 0 {
		t.Errorf("fallback candidate %q probed %d times — discovery must not outrank the override (CX-2)", "Sent Items", status["Sent Items"])
	}
	if status["Archive"] != 1 {
		t.Errorf("override %q probed %d times, want exactly 1 — the override is validated, never obeyed blindly (R-3.3-1)", "Archive", status["Archive"])
	}
}

// TestDiscovery_UnsupportedExtensionDegradesToCandidates — D-3, R-3.2-2.
// Oracle: partial advertisement (LIST-EXTENDED without SPECIAL-USE) is just
// "use the fallback list" — the mailbox as a whole does NOT fail and the
// candidate path resolves normally.
func TestDiscovery_UnsupportedExtensionDegradesToCandidates(t *testing.T) {
	cl, _, _ := startDiscoveryIMAP(t, true, []string{"Sent Items"}, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapListExtended: {}}, nil)

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		t.Fatalf("Resolve: %v — an unsupported/partial extension must not fail the mailbox (R-3.2-2/D-3)", err)
	}
	if m.Sent.Availability != "present" || m.Sent.Name != "Sent Items" || m.Sent.Source != "fallback" {
		t.Fatalf("sent = %+v, want present/Sent Items/fallback via the candidate list", m.Sent)
	}
}

// TestDiscovery_NeverCreatesFolders — D-5, US-1.4, FR-W2-2, R-3.1-4.
// Oracle: across every outcome this pack drives (fallback success, unknown,
// confirmed absence, override), the command trace contains zero CREATE and
// zero SUBSCRIBE. Discovery is a read (R-3.2-4).
func TestDiscovery_NeverCreatesFolders(t *testing.T) {
	setups := []struct {
		name    string
		folders []string
		caps    imap.CapSet
	}{
		{"fallback-success", []string{"Sent Items"}, nil},
		{"unknown-Localized", []string{"Elementos-Enviados"}, nil},
		{"genuinely-empty", nil, nil},
		{"special-use-caps-no-attrs", []string{"Sent Items"}, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapListExtended: {}, imap.CapSpecialUse: {}}},
	}
	for _, s := range setups {
		t.Run(s.name, func(t *testing.T) {
			cl, _, instr := startDiscoveryIMAP(t, true, s.folders, s.caps, nil)
			if _, err := resolveAutomatic(t, cl); err != nil {
				t.Logf("Resolve error in setup %q (the no-CREATE rule holds regardless): %v", s.name, err)
			}
			_, _, creates, subscribes := instr.counts()
			if creates != 0 {
				t.Errorf("CREATE issued %d times — discovery never creates a server folder (R-3.1-4/D-5)", creates)
			}
			if subscribes != 0 {
				t.Errorf("SUBSCRIBE issued %d times — discovery never subscribes (R-3.2-4/D-5)", subscribes)
			}
		})
	}
}

// TestDiscovery_AuthFailureIsNeverEmptyAccount — U-3, US-3.2.
// Oracle: an authentication failure anywhere in the flow surfaces as the
// read's own failure with its safe transport class — the result must never be
// a healthy per-role absent/empty rendering (§3.4; ADR failure table: cache
// timestamps never advance on failure, nothing renders as a healthy empty
// folder).
func TestDiscovery_AuthFailureIsNeverEmptyAccount(t *testing.T) {
	_, addr, _ := startDiscoveryIMAP(t, true, []string{"Sent Items"}, nil, nil)

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	bad, err := NewClient(Account{IMAPHost: host, IMAPPort: port, SMTPHost: host, Username: testIMAPUser, Password: "definitely-wrong"})
	if err != nil {
		t.Fatalf("NewClient (bad credentials): %v", err)
	}

	m, rerr := resolveAutomatic(t, bad)
	if rerr == nil {
		t.Fatal("Resolve with failing authentication must return an error (US-3.2/U-3), got none")
	}
	if strings.Contains(rerr.Error(), "definitely-wrong") {
		t.Fatalf("the error must carry the safe transport class, never the credential value: %v", rerr)
	}
	// If a mapping is returned alongside the error, no role may claim a
	// resolved or absent state off a failed session (§3.4: anything else is
	// unknown).
	if m.Sent.Availability == "absent" || m.Sent.Availability == "present" {
		t.Fatalf("sent availability = %q off a failed authentication — must never render as absent/present", m.Sent.Availability)
	}
	if m.Drafts.Availability == "absent" || m.Drafts.Availability == "present" {
		t.Fatalf("drafts availability = %q off a failed authentication — must never render as absent/present", m.Drafts.Availability)
	}
}

// TestDiscovery_MissingInboxIsFatalEvenForNonExistent — U-4, US-3.4, FR-W2-9.
// REGRESSION GUARD (green today by design — §7.3: must keep passing): INBOX
// takes no discovery and has no absent state; today's FolderCounts already
// fails loudly on a missing INBOX (only Sent/Drafts are softened). This pins
// that the discovery rewrite keeps INBOX fatal — a missing INBOX is a broken
// account, never a healthy empty one.
func TestDiscovery_MissingInboxIsFatalEvenForNonExistent(t *testing.T) {
	cl, _, _ := startDiscoveryIMAP(t, false, []string{"Sent"}, nil, nil)

	stats, err := cl.FolderCounts(context.Background())
	if err == nil {
		t.Fatal("a mailbox whose INBOX cannot be read must fail loudly (US-3.4), got stats")
	}
	if len(stats) != 0 {
		t.Fatalf("no per-folder stats may render off a dead INBOX, got %+v", stats)
	}
}

// TestReadFolderPage_ConfirmedAbsentRoleReturnsEmptyNotError — US-3.3 (list
// face), §3.4 ("list returns an empty array rather than a mailbox-level 502").
// Oracle: with a genuinely empty server (only INBOX; every candidate
// structurally absent — the U-1 setup), the sent role's list is an EMPTY
// page with no error. Today's code fails with a folder error, which is the
// expected RED failure of this test until the resolved mapping backs
// folderNameFor (§2.1). Compiles today; observed red for the right reason
// (error vs empty page).
func TestReadFolderPage_ConfirmedAbsentRoleReturnsEmptyNotError(t *testing.T) {
	cl, _, _ := startDiscoveryIMAP(t, true, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	page, _, truncated, err := cl.ReadFolderPage(ctx, FolderSent, 25, 0)
	if err != nil {
		t.Fatalf("confirmed-absent role must list as EMPTY, not fail: %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("confirmed-absent role must return an empty array, got %d rows", len(page))
	}
	if truncated {
		t.Fatal("an empty folder is not truncated")
	}
}

// specialUseSession is a test-only session wrapper whose LIST reply carries
// the fixture's SPECIAL-USE attributes. imapmemserver drops
// CreateOptions.SpecialUse (the harness gap named in this file's header), so
// the RFC 6154 attribute path is driven by session instrumentation in test
// code only — the ADR test-strategy row this pack already cites. STATUS
// probes and every other command delegate to the real in-memory server, so
// existence and UIDVALIDITY stay server-truth.
type specialUseSession struct {
	*discoveryInstr
	entries []imap.ListData
}

func (s *specialUseSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	for i := range s.entries {
		if err := w.WriteList(&s.entries[i]); err != nil {
			return err
		}
	}
	return nil
}

// startSpecialUseIMAP boots the real in-memory IMAP server with the named
// folders (each really created, so STATUS probes are server-truth), the given
// LIST attributes, and the specialUseSession wrapper on the dial seam. Same
// shape as startDiscoveryIMAP.
func startSpecialUseIMAP(t *testing.T, attrs map[string][]imap.MailboxAttr, caps imap.CapSet) (*Client, *discoveryInstr) {
	t.Helper()
	// Same source-less discovery client as startDiscoveryIMAP: declare the
	// legacy-dial world under any test order (order-independence fixture).
	pinLegacyDialWorld(t)

	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	names := []string{"INBOX"}
	for name := range attrs {
		if name == "INBOX" {
			continue
		}
		if err := user.Create(name, nil); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var entries []imap.ListData
	for _, name := range names {
		entries = append(entries, imap.ListData{Mailbox: name, Delim: '/', Attrs: attrs[name]})
	}
	mem.AddUser(user)

	instr := &discoveryInstr{
		Session:  mem.NewSession(),
		statusOf: map[string]int{},
		stall:    map[string]chan struct{}{},
	}
	t.Cleanup(instr.releaseStalls)
	session := &specialUseSession{discoveryInstr: instr, entries: entries}

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return session, nil, nil
		},
		InsecureAuth: true,
		Caps:         caps,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	prev := imapDial
	imapDial = func(_ context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	cl, err := NewClient(Account{
		IMAPHost: host,
		IMAPPort: port,
		SMTPHost: host,
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cl, instr
}

// TestDiscovery_AmbiguousSpecialUseAsks — A-1, US-4.1, DT-1 "two sent tags",
// R-3.5-2/R-3.5-3; closes the CHECK mutation-audit hole F-3 (survivor M15: an
// arbitrary sorted[0] pick passed the whole discovery family).
// Oracle: two folders carry \Sent and no saved mapping stands — the role is
// unresolved-by-ambiguity: source none, availability unknown, the distinct
// safe reason class mapping_ambiguous, BOTH candidate names surfaced for the
// user to choose, no folder auto-selected, no epoch fabricated, and the
// server not touched for that role beyond the enumeration already performed
// (R-3.5-2). The single-tag drafts role on the SAME server still resolves
// automatically — the decision is per-role, never a whole-read failure.
func TestDiscovery_AmbiguousSpecialUseAsks(t *testing.T) {
	cl, instr := startSpecialUseIMAP(t, map[string][]imap.MailboxAttr{
		"INBOX":      nil,
		"Sent":       {imap.MailboxAttrSent},
		"Sent Items": {imap.MailboxAttrSent},
		"Drafts":     {imap.MailboxAttrDrafts},
	}, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapListExtended: {}, imap.CapSpecialUse: {}})

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if m.Sent.Availability != "unknown" {
		t.Fatalf("sent availability = %q, want unknown — two special-use candidates must surface the ambiguity, never auto-resolve (R-3.5-2/US-4.1)", m.Sent.Availability)
	}
	if m.Sent.Source != "none" {
		t.Fatalf("sent source = %q, want none — an ambiguous role resolved nothing (register row 3)", m.Sent.Source)
	}
	if m.Sent.Reason != "mapping_ambiguous" {
		t.Fatalf("sent reason = %q, want %q — the ambiguity variant of unknown carries its distinct safe class (US-4.1/§3.12)", m.Sent.Reason, "mapping_ambiguous")
	}
	if m.Sent.Name != "" {
		t.Fatalf("sent resolved name = %q, want empty — never send or draft into an arbitrarily picked folder (R-3.5-3)", m.Sent.Name)
	}
	if m.Sent.UIDValidity != nil {
		t.Fatalf("sent UIDVALIDITY = %d, want nil — an ambiguous role has no validated epoch", *m.Sent.UIDValidity)
	}
	if len(m.Sent.Ambiguity) != 2 || !containsFold(m.Sent.Ambiguity, "Sent") || !containsFold(m.Sent.Ambiguity, "Sent Items") {
		t.Fatalf("sent ambiguity list = %+v, want exactly both tagged folders (Sent, Sent Items) — the user must see every candidate (R-3.5-2)", m.Sent.Ambiguity)
	}

	// R-3.5-2: beyond the enumeration, the server is not touched for that
	// role — the ambiguity surfaced from the listing, no probe happened.
	status, _, _, _ := instr.counts()
	if status["Sent"] != 0 || status["Sent Items"] != 0 {
		t.Fatalf("ambiguous role probed the server (Sent=%d, Sent Items=%d) — ambiguity must surface from the enumeration alone (R-3.5-2)", status["Sent"], status["Sent Items"])
	}

	// The single-tag role on the same server resolves automatically: the
	// ambiguity decision is per-role.
	if m.Drafts.Availability != "present" || m.Drafts.Name != "Drafts" || m.Drafts.Source != "special_use" {
		t.Fatalf("drafts = %+v, want present/Drafts/special_use — a single tagged folder resolves without surfacing ambiguity", m.Drafts)
	}
}

// TestDiscovery_SpecialUseResolvesBeforeFallback — D-1, US-1.1, §3.2 step 3.
// The CHECK audit's F-3 related gap (the SPECIAL-USE attribute path had zero
// coverage — imapmemserver drops the attributes) and this file's POSITIVE
// CONTROL for the ambiguity test: ONE tagged folder, named so no fallback
// candidate guesses it, resolves automatically with source special_use, and
// the fallback list is never probed after the tag match (D-1: zero fallback
// probes). Without this control the ambiguity test could pass by an
// implementation that refuses every attribute match.
func TestDiscovery_SpecialUseResolvesBeforeFallback(t *testing.T) {
	cl, instr := startSpecialUseIMAP(t, map[string][]imap.MailboxAttr{
		"INBOX":            nil,
		"Archivio-Inviata": {imap.MailboxAttrSent}, // tagged, outside the candidate list
		"Drafts":           nil,                    // untagged: only the fallback list can resolve it
	}, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapListExtended: {}, imap.CapSpecialUse: {}})

	m, err := resolveAutomatic(t, cl)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if m.Sent.Availability != "present" || m.Sent.Name != "Archivio-Inviata" {
		t.Fatalf("sent = %+v, want present/Archivio-Inviata — the single \\Sent-tagged folder resolves automatically (D-1/US-1.1)", m.Sent)
	}
	if m.Sent.Source != "special_use" {
		t.Fatalf("sent source = %q, want special_use (§3.2 step 3)", m.Sent.Source)
	}
	if m.Sent.UIDValidity == nil || *m.Sent.UIDValidity == 0 {
		t.Fatalf("sent UIDVALIDITY must be a real probed value (R-3.2-1), got %+v", m.Sent.UIDValidity)
	}

	status, _, _, _ := instr.counts()
	if status["Archivio-Inviata"] != 1 {
		t.Fatalf("tagged folder probed %d times, want exactly 1", status["Archivio-Inviata"])
	}
	for _, cand := range []string{"Sent", "Sent Items", "Sent Messages", "[Gmail]/Sent Mail"} {
		if status[cand] != 0 {
			t.Fatalf("fallback candidate %q probed %d times — the special-use match ends the ladder with zero fallback probes (D-1)", cand, status[cand])
		}
	}

	// The untagged folder rides the candidate list — the ladder still reaches
	// step 4 for roles the tags do not fill.
	if m.Drafts.Availability != "present" || m.Drafts.Name != "Drafts" || m.Drafts.Source != "fallback" {
		t.Fatalf("drafts = %+v, want present/Drafts/fallback — an untagged folder resolves through the candidate list", m.Drafts)
	}
}
