package email

// RED (round 8, silent-failure-hunter F2 - MEDIUM). Oracle: FR-037 / MC-33 /
// B-42: the REAL dial paths retry ONLY name-resolution failures, bounded
// (3 attempts, 250 ms -> 1 s) inside the overall dial bound, and SUCCEED when
// resolution recovers; a non-DNS failure is never retried.
//
// HEAD: the retry exists only in pkg/email/dial_retry.go::DialWithRetry,
// with ZERO production callers - the specapi test that greens it is a false
// green and FR-037 is dead code. Per the round-8 ruling this file pins the
// retry at the production entry points through the named seam
//
//	var dialResolver Resolver = net.DefaultResolver // package var, pkg/email
//
// consulted by transport.go::dialSMTPRaw (SMTP send dial) and
// transport.go::dialIMAP (panel/tool/watcher dial). The seam does not exist
// on HEAD, so the file's ONLY compile error must be `undefined: dialResolver`
// - that compile error IS the RED evidence (the house RED pattern for a
// missing seam; pkg/email will not compile for go test until backend-lead
// adds it, which is exactly the failure this pack must produce).
//
// Contract pinned (wiring-agnostic): with dialResolver stubbed, the
// production dial (a) consults ONLY the injected resolver for the stubbed
// name - success must not depend on real DNS for that name, so the RESOLVED
// address is what gets dialed; (b) recovers after two resolution failures
// with exactly 3 lookups (B-42: one successful dial, no error); (c) with an
// always-failing resolver stops at 3 lookups inside the overall dial bound
// with a dns-class error; (d) a non-DNS dial failure (connection refused
// after successful resolution) is NOT retried - exactly 1 lookup, refused
// class, never dns.

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"
)

// f2StubResolver is the injected resolver at the process edge: a scripted
// per-call answer list (nil entry = success, answered 127.0.0.1), counting
// lookups so the bounded-retry bound is pinned exactly.
type f2StubResolver struct {
	calls   int
	answers []error
}

func (r *f2StubResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	r.calls++
	idx := r.calls - 1
	if idx < len(r.answers) && r.answers[idx] != nil {
		return nil, r.answers[idx]
	}
	return []string{"127.0.0.1"}, nil
}

type f2AddrResolver struct {
	calls int
	addrs []string
}

func (r *f2AddrResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	r.calls++
	return r.addrs, nil
}

func f2DNSErr(host, text string) error {
	return &net.DNSError{Err: text, Name: host, IsNotFound: false}
}

// f2SwapResolver swaps the dialResolver seam for the stub and restores it on
// cleanup. Returns the typed stub for call-count assertions.
func f2SwapResolver(t *testing.T, r Resolver) *f2StubResolver {
	t.Helper()
	stub, ok := r.(*f2StubResolver)
	require.True(t, ok, "test bug: expected the f2 stub")
	prev := dialResolver
	dialResolver = r
	t.Cleanup(func() { dialResolver = prev })
	return stub
}

func f2SwapAddrResolver(t *testing.T, addrs ...string) *f2AddrResolver {
	t.Helper()
	stub := &f2AddrResolver{addrs: addrs}
	prev := dialResolver
	dialResolver = stub
	t.Cleanup(func() { dialResolver = prev })
	return stub
}

func f2SwapTCPDial(t *testing.T, dial func(context.Context, string) (net.Conn, error)) {
	t.Helper()
	prev := dialTCPContext
	dialTCPContext = dial
	t.Cleanup(func() { dialTCPContext = prev })
}

const (
	f2SMTPName = "smtpqa.invalid"
	f2IMAPName = "imapqa.invalid"
	f2IMAPUser = "f2user@test.local"
	f2IMAPPass = "f2pass"
)

// f2Listener opens one loopback listener whose accepts succeed (read up to
// 16 bytes with a 5 s deadline, then close) - enough for a raw TCP dial.
func f2Listener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 16)
				_, _ = c.Read(buf)
				_ = c.Close()
			}(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// f2ClosedPort returns a loopback addr that refuses connections (bound, then
// immediately released).
func f2ClosedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// ---- SMTP send dial (transport.go::dialSMTPRaw) ----

func TestDialSMTPRaw_RecoversAfterTwoResolutionFailures(t *testing.T) {
	ln := f2Listener(t)
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	stub := f2SwapResolver(t, &f2StubResolver{answers: []error{
		f2DNSErr(f2SMTPName, "qa: failure one"),
		f2DNSErr(f2SMTPName, "qa: failure two"),
		nil,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dialSMTPRaw(ctx, f2SMTPName+":"+portStr)

	require.NoError(t, err, "FR-037/B-42: the SMTP send dial must recover through the injected resolver after two resolution failures - B-42's one successful dial, no error")
	require.NotNil(t, conn)
	if conn != nil {
		_ = conn.Close()
	}
	require.Equal(t, 3, stub.calls, "B-42: exactly 3 lookups (2 failures + the recovered one) for one production send dial")
}

func TestDialSMTPRaw_AlwaysFailingResolverIsDNSClassWithinBound(t *testing.T) {
	stub := f2SwapResolver(t, &f2StubResolver{answers: []error{
		f2DNSErr(f2SMTPName, "qa: no such host"),
		f2DNSErr(f2SMTPName, "qa: no such host"),
		f2DNSErr(f2SMTPName, "qa: no such host"),
		f2DNSErr(f2SMTPName, "qa: no such host"),
	}})

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := dialSMTPRaw(ctx, f2SMTPName+":25")

	require.Error(t, err, "an always-failing resolver must fail the dial")
	require.Equal(t, 3, stub.calls, "MC-33: the DNS retry is bounded at 3 attempts, never infinite")
	require.Less(t, time.Since(start), dialTimeout, "MC-33: the bounded retry sits inside the overall dial bound, never hangs past it")
	require.Equal(t, "dns", ClassifyMailError(err), "MC-33/B-42: the surfaced class is dns (never auth/tls for a name-resolution failure)")
}

func TestDialSMTPRaw_NonDNSDialFailureIsNotRetried(t *testing.T) {
	addr := f2ClosedPort(t)
	stub := f2SwapResolver(t, &f2StubResolver{answers: []error{nil}})

	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = dialSMTPRaw(ctx, f2SMTPName+":"+portStr)

	require.Error(t, err, "the refused dial must fail")
	require.ErrorIs(t, err, syscall.ECONNREFUSED, "the failure is the refused dial itself, not a resolution failure")
	require.Equal(t, 1, stub.calls, "MC-33: a non-DNS dial failure is NEVER retried - resolution happened exactly once, no re-resolution on the refused dial")
	require.NotEqual(t, "dns", ClassifyMailError(err), "MC-33: the refused dial keeps its own class - it must never surface as dns (only name-resolution failures are dns-retried)")
}

func TestDialSMTPRaw_FallsBackAcrossResolvedAddresses(t *testing.T) {
	ln := f2Listener(t)
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")
	realDial := dialTCPContext
	var attemptedMu sync.Mutex
	var attempted []string
	f2SwapTCPDial(t, func(ctx context.Context, addr string) (net.Conn, error) {
		attemptedMu.Lock()
		attempted = append(attempted, addr)
		attemptedMu.Unlock()
		if host, _, splitErr := net.SplitHostPort(addr); splitErr == nil && host == "::1" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return realDial(ctx, addr)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dialSMTPRaw(ctx, f2SMTPName+":"+portStr)

	require.NoError(t, err, "the SMTP dial must fall back from an unreachable first resolved address to the working second address")
	require.NotNil(t, conn)
	if conn != nil {
		_ = conn.Close()
	}
	attemptedMu.Lock()
	require.Equal(t, []string{net.JoinHostPort("::1", portStr), net.JoinHostPort("127.0.0.1", portStr)}, attempted, "a stalled first address must remain cancellable while the working second address is tried")
	attemptedMu.Unlock()
	require.Equal(t, 1, stub.calls, "address fallback reuses one successful DNS answer")
}

func TestDialSMTPRaw_SlowFirstSuccessSurvivesFailedFallback(t *testing.T) {
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close(); _ = serverConn.Close() })
	fallbackStarted := make(chan struct{})
	var attemptedMu sync.Mutex
	var attempted []string
	f2SwapTCPDial(t, func(ctx context.Context, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		attemptedMu.Lock()
		attempted = append(attempted, host)
		attemptedMu.Unlock()
		if host == "::1" {
			select {
			case <-fallbackStarted:
				return clientConn, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		close(fallbackStarted)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialSMTPRaw(ctx, f2SMTPName+":25")

	require.NoError(t, err, "a slow valid first address must remain eligible after the fallback starts and fails")
	require.NotNil(t, conn)
	attemptedMu.Lock()
	require.Equal(t, []string{"::1", "127.0.0.1"}, attempted, "the fallback must start while the slow primary remains eligible")
	attemptedMu.Unlock()
	require.Equal(t, 1, stub.calls, "parallel address fallback must not re-resolve the hostname")
}

func TestDialSMTPRaw_AllAddressAttemptsShareCallerDeadline(t *testing.T) {
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")
	var attemptedMu sync.Mutex
	var attempted []string
	var deadlines []time.Time
	var deadlineOK []bool
	f2SwapTCPDial(t, func(ctx context.Context, addr string) (net.Conn, error) {
		attemptedMu.Lock()
		attempted = append(attempted, addr)
		deadline, ok := ctx.Deadline()
		deadlines = append(deadlines, deadline)
		deadlineOK = append(deadlineOK, ok)
		attemptedMu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), addressFallbackDelay+150*time.Millisecond)
	defer cancel()
	wantDeadline, ok := ctx.Deadline()
	require.True(t, ok)
	_, err := dialSMTPRaw(ctx, f2SMTPName+":25")

	require.ErrorIs(t, err, context.DeadlineExceeded, "every SMTP address attempt must stop at the same caller deadline")
	require.Equal(t, "timeout", ClassifyMailError(err))
	attemptedMu.Lock()
	require.Len(t, attempted, 2, "the fallback starts before the shared caller deadline and cannot outlive it")
	require.Len(t, deadlines, 2)
	for i, deadline := range deadlines {
		require.True(t, deadlineOK[i], "every SMTP address attempt must carry the caller deadline")
		require.True(t, deadline.Equal(wantDeadline), "each SMTP attempt deadline must equal the parent deadline")
	}
	attemptedMu.Unlock()
	require.Equal(t, 1, stub.calls, "parallel address fallback must not re-resolve the hostname")
}

func TestDialSMTPRaw_AllResolvedAddressesFailWithLastConnectError(t *testing.T) {
	addr := f2ClosedPort(t)
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = dialSMTPRaw(ctx, f2SMTPName+":"+portStr)

	require.Error(t, err, "all unreachable resolved addresses must fail loudly")
	require.ErrorIs(t, err, syscall.ECONNREFUSED, "the last connection failure must remain structurally visible")
	require.Contains(t, err.Error(), net.JoinHostPort("127.0.0.1", portStr), "the returned error must describe the last attempted address")
	require.Equal(t, "connect_refused", ClassifyMailError(err), "a refused connection after successful resolution must never classify as dns")
	require.Equal(t, 1, stub.calls, "address fallback reuses one successful DNS answer")
}

// ---- IMAP dial (transport.go::dialIMAP - panel/tool/watcher path) ----

// f2StartMemIMAPPlain boots the in-tree memserver on a loopback port and
// returns a production *Client whose IMAP host is the FAKE name the stub
// resolver maps to loopback, with imapDial swapped to plaintext. The INBOX
// stays empty: the test drives one production dial (login + select), not the
// mailbox content.
func f2StartMemIMAPPlain(t *testing.T) *Client {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(f2IMAPUser, f2IMAPPass)
	require.NoError(t, user.Create("INBOX", nil))
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	prev := imapDial
	imapDial = func(_ context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, cerr := strconv.Atoi(portStr)
	require.NoError(t, cerr)
	cl, cerr := NewClient(Account{IMAPHost: f2IMAPName, IMAPPort: port, SMTPHost: f2SMTPName, Username: f2IMAPUser, Password: f2IMAPPass})
	require.NoError(t, cerr)
	return cl
}

func TestDialIMAP_RecoversAfterTwoResolutionFailures(t *testing.T) {
	cl := f2StartMemIMAPPlain(t)
	stub := f2SwapResolver(t, &f2StubResolver{answers: []error{
		f2DNSErr(f2IMAPName, "qa: imap failure one"),
		f2DNSErr(f2IMAPName, "qa: imap failure two"),
		nil,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := cl.ReadInbox(ctx, InboxOptions{Limit: 25})

	require.NoError(t, err, "FR-037/B-42: the IMAP dial (panel/tool/watcher path) must recover through the injected resolver after two resolution failures - B-42's one successful dial")
	require.Equal(t, 3, stub.calls, "B-42: exactly 3 lookups (2 failures + the recovered one) for one production IMAP dial")
}

func TestDialIMAP_AlwaysFailingResolverIsDNSClassWithinBound(t *testing.T) {
	cl := f2StartMemIMAPPlain(t)
	stub := f2SwapResolver(t, &f2StubResolver{answers: []error{
		f2DNSErr(f2IMAPName, "qa: no such imap host"),
		f2DNSErr(f2IMAPName, "qa: no such imap host"),
		f2DNSErr(f2IMAPName, "qa: no such imap host"),
		f2DNSErr(f2IMAPName, "qa: no such imap host"),
	}})

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := cl.ReadInbox(ctx, InboxOptions{Limit: 25})

	require.Error(t, err, "an always-failing resolver must fail the IMAP dial")
	require.Equal(t, 3, stub.calls, "MC-33: the DNS retry is bounded at 3 attempts on the IMAP dial too, never infinite")
	require.Less(t, time.Since(start), dialTimeout, "MC-33: the bounded retry sits inside the overall dial bound")
	require.Equal(t, "dns", ClassifyMailError(err), "MC-33/B-42: the surfaced class is dns (never auth/tls for a name-resolution failure)")
}

func TestDialIMAP_FallsBackAcrossResolvedAddresses(t *testing.T) {
	cl := f2StartMemIMAPPlain(t)
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")
	realDial := imapDial
	var attemptedMu sync.Mutex
	var attempted []string
	var serverNames []string
	imapDial = func(ctx context.Context, addr string, tlsCfg *tls.Config) (*imapclient.Client, error) {
		attemptedMu.Lock()
		attempted = append(attempted, addr)
		serverNames = append(serverNames, tlsCfg.ServerName)
		attemptedMu.Unlock()
		if host, _, splitErr := net.SplitHostPort(addr); splitErr == nil && host == "::1" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return realDial(ctx, addr, tlsCfg)
	}
	t.Cleanup(func() { imapDial = realDial })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cl.ReadInbox(ctx, InboxOptions{Limit: 25})

	require.NoError(t, err, "the IMAP dial must fall back from an unreachable first resolved address to the working second address")
	attemptedMu.Lock()
	require.Len(t, attempted, 2, "a stalled first address must receive only its fallback window before the working second address is tried")
	require.Equal(t, []string{f2IMAPName, f2IMAPName}, serverNames, "TLS ServerName must remain the configured hostname across resolved-address fallback")
	attemptedMu.Unlock()
	require.Equal(t, 1, stub.calls, "address fallback reuses one successful DNS answer")
}

func TestDialIMAP_AllResolvedAddressesFailWithLastConnectError(t *testing.T) {
	cl, err := NewClient(Account{
		IMAPHost: f2IMAPName,
		IMAPPort: 993,
		SMTPHost: f2SMTPName,
		Username: f2IMAPUser,
		Password: f2IMAPPass,
	})
	require.NoError(t, err)
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")
	prev := imapDial
	var attemptedMu sync.Mutex
	var attempted []string
	imapDial = func(_ context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		attemptedMu.Lock()
		attempted = append(attempted, addr)
		attemptedMu.Unlock()
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}
	t.Cleanup(func() { imapDial = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = cl.ReadInbox(ctx, InboxOptions{Limit: 25})

	require.Error(t, err, "all unreachable resolved addresses must fail loudly")
	require.ErrorIs(t, err, syscall.ECONNREFUSED, "the last IMAP connection failure must remain structurally visible")
	require.Contains(t, err.Error(), net.JoinHostPort("127.0.0.1", "993"), "the returned IMAP error must describe the last attempted address")
	require.Equal(t, "connect_refused", ClassifyMailError(err), "a refused IMAP connection after successful resolution must never classify as dns")
	attemptedMu.Lock()
	require.Equal(t, []string{net.JoinHostPort("::1", "993"), net.JoinHostPort("127.0.0.1", "993")}, attempted, "IMAP must try every resolved address in order")
	attemptedMu.Unlock()
	require.Equal(t, 1, stub.calls, "address fallback reuses one successful DNS answer")
}

func TestDialIMAP_AllAddressAttemptsShareCallerDeadline(t *testing.T) {
	cl, err := NewClient(Account{
		IMAPHost: f2IMAPName,
		IMAPPort: 993,
		SMTPHost: f2SMTPName,
		Username: f2IMAPUser,
		Password: f2IMAPPass,
	})
	require.NoError(t, err)
	stub := f2SwapAddrResolver(t, "::1", "127.0.0.1")
	prev := imapDial
	var attemptedMu sync.Mutex
	var attempted []string
	var deadlines []time.Time
	var deadlineOK []bool
	imapDial = func(ctx context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		attemptedMu.Lock()
		attempted = append(attempted, addr)
		deadline, ok := ctx.Deadline()
		deadlines = append(deadlines, deadline)
		deadlineOK = append(deadlineOK, ok)
		attemptedMu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { imapDial = prev })

	ctx, cancel := context.WithTimeout(context.Background(), addressFallbackDelay+150*time.Millisecond)
	defer cancel()
	wantDeadline, ok := ctx.Deadline()
	require.True(t, ok)
	_, err = cl.ReadInbox(ctx, InboxOptions{Limit: 25})

	require.ErrorIs(t, err, context.DeadlineExceeded, "every IMAP address attempt must stop at the same caller deadline")
	require.Equal(t, "timeout", ClassifyMailError(err))
	attemptedMu.Lock()
	require.Len(t, attempted, 2, "the fallback starts before the shared caller deadline and cannot outlive it")
	require.Len(t, deadlines, 2)
	for i, deadline := range deadlines {
		require.True(t, deadlineOK[i], "every IMAP address attempt must carry the caller deadline")
		require.True(t, deadline.Equal(wantDeadline), "each IMAP attempt deadline must equal the parent deadline")
	}
	attemptedMu.Unlock()
	require.Equal(t, 1, stub.calls, "parallel address fallback must not re-resolve the hostname")
}
