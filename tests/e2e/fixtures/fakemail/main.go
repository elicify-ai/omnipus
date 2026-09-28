// Command fakemail is the D36 built-in fake IMAP/SMTP server for the mail
// panel end-to-end spec. It speaks plaintext IMAP (the same imapmemserver
// startMemIMAP uses) plus a sink that records SMTP, and a small control API
// the spec uses to seed messages. One JSON line on stdout gives the ports.
//
// With no flags it behaves exactly as the D36 spec server always has: the
// single user mailbox@test.local / s3cret with INBOX, Sent and Drafts, three
// ephemeral loopback ports, and a first stdout line carrying exactly the
// five JSON keys the consumer fixtures/fake-mail-server.ts parses
// (imap, smtp, control, user, password).
//
// The optional flags exist for founder local review of the email feature
// (send and receive test mail without a real mail account):
//
//	-imap 127.0.0.1:1143     IMAP listen address (default 127.0.0.1:0)
//	-smtp 127.0.0.1:1025     SMTP listen address (default 127.0.0.1:0)
//	-control 127.0.0.1:1180  control API listen address (default 127.0.0.1:0)
//	-users "agent@test.local:s3cret,alice@test.local:s3cret"
//	-deliver                 also append SMTP mail to a known local user's INBOX
//
// Every listen address must be loopback: Omnipus allows plaintext IMAP/SMTP
// only to loopback, and the review defaults keep that true. -users users get
// INBOX, Sent, Drafts and Trash; the default user keeps exactly the D36
// folder set (INBOX, Sent, Drafts). Control API:
//
//	POST /append {mailbox, raw_base64, flags} -> {uid}  (first user's mailboxes)
//	POST /store  {mailbox, uid, flags}        -> {ok}  (first user's mailboxes)
//	GET  /smtp                                -> {count, messages} (sink)
//	POST /inject {user, mailbox?, subject?, from?, text?, raw_base64?}
//	    -> {uid}  append a sample inbound message to any known user's mailbox
//
// /append and /store stay bound to the first user in the list (the default
// user when -users is absent), matching today's contract.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

const defaultListenAddr = "127.0.0.1:0"

// defaultUser is the user the D36 spec has always provisioned.
var defaultUser = mailUser{addr: "mailbox@test.local", password: "s3cret"}

// defaultFolders is the folder set the fixture has always created;
// reviewFolders is what -users mode provisions (adds Trash).
var (
	defaultFolders = []string{"INBOX", "Sent", "Drafts"}
	reviewFolders  = []string{"INBOX", "Sent", "Drafts", "Trash"}
)

type mailUser struct {
	addr     string
	password string
}

type config struct {
	imapAddr string
	smtpAddr string
	ctrlAddr string
	users    []mailUser
	// usersFlagGiven separates no-flag mode (byte-identical behaviour) from
	// -users mode (adds Trash, emits the "users" output key).
	usersFlagGiven bool
	deliver        bool
}

// fakeServer is the running fixture; Stop closes the listeners (tests).
type fakeServer struct {
	imapAddr, smtpAddr, ctrlAddr string
	primary                      *imapclient.Client

	imapLn, smtpLn, ctrlLn net.Listener
}

// Stop closes the three listeners and the primary control client. Best
// effort: the fixture process normally just exits.
func (s *fakeServer) Stop() {
	for _, ln := range []net.Listener{s.imapLn, s.smtpLn, s.ctrlLn} {
		if ln != nil {
			_ = ln.Close()
		}
	}
	if s.primary != nil {
		_ = s.primary.Close()
	}
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	if _, err := run(cfg, os.Stdout); err != nil {
		fatal(err)
	}
	select {}
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("fakemail", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // stdout must stay pure: the one JSON line
	imapAddr := fs.String("imap", defaultListenAddr, "IMAP listen address, loopback only")
	smtpAddr := fs.String("smtp", defaultListenAddr, "SMTP listen address, loopback only")
	ctrlAddr := fs.String("control", defaultListenAddr, "control API listen address, loopback only")
	usersSpec := fs.String("users", "", `mail users "addr:password,addr:password"`)
	deliver := fs.Bool("deliver", false, "also append SMTP mail to a known local user's INBOX")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	users, err := parseUsers(*usersSpec)
	if err != nil {
		return config{}, err
	}
	return config{
		imapAddr:       *imapAddr,
		smtpAddr:       *smtpAddr,
		ctrlAddr:       *ctrlAddr,
		users:          users,
		usersFlagGiven: *usersSpec != "",
		deliver:        *deliver,
	}, nil
}

// parseUsers parses the -users spec ("addr:password,addr:password"). An
// empty spec yields the single default user, exactly as today.
func parseUsers(spec string) ([]mailUser, error) {
	if spec == "" {
		return []mailUser{defaultUser}, nil
	}
	var users []mailUser
	seen := map[string]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty user entry in -users %q", spec)
		}
		addr, pass, ok := strings.Cut(part, ":")
		if !ok || addr == "" || pass == "" {
			return nil, fmt.Errorf("user %q must be address:password", part)
		}
		if !strings.Contains(addr, "@") {
			return nil, fmt.Errorf("user address %q must contain @", addr)
		}
		key := strings.ToLower(addr)
		if seen[key] {
			return nil, fmt.Errorf("duplicate user address %q", addr)
		}
		seen[key] = true
		users = append(users, mailUser{addr: addr, password: pass})
	}
	return users, nil
}

// checkLoopback enforces the loopback-only rule: Omnipus allows plaintext
// IMAP/SMTP only to loopback, so the fixture refuses to listen anywhere
// else. An empty host (":port") means all interfaces and is refused.
func checkLoopback(what, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s listen address %q: %w", what, addr, err)
	}
	if host == "" {
		return fmt.Errorf("%s listen address %q must be loopback (empty host means all interfaces)", what, addr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ra, err := net.ResolveIPAddr("ip", host)
		if err != nil {
			return fmt.Errorf("%s listen address %q: cannot resolve host: %w", what, addr, err)
		}
		ip = ra.IP
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("%s listen address %q must be loopback (Omnipus allows plaintext IMAP/SMTP only to loopback)", what, addr)
	}
	return nil
}

// run validates the config, starts the three servers and prints the one
// JSON line to out. In no-flag mode the line is byte-identical to what the
// fixture has always printed; run also defaults empty addresses and the
// empty user list so a zero-value config means today's mode.
func run(cfg config, out io.Writer) (*fakeServer, error) {
	addrs := map[string]string{
		"imap": cfg.imapAddr, "smtp": cfg.smtpAddr, "control": cfg.ctrlAddr,
	}
	for _, what := range []string{"imap", "smtp", "control"} {
		addr := addrs[what]
		if addr == "" {
			addr = defaultListenAddr
			addrs[what] = addr
		}
		if err := checkLoopback(what, addr); err != nil {
			return nil, err
		}
	}

	users := cfg.users
	if len(users) == 0 {
		users = []mailUser{defaultUser}
	}
	folders := defaultFolders
	if cfg.usersFlagGiven {
		folders = reviewFolders
	}

	mem := imapmemserver.New()
	userHandles := make(map[string]*imapmemserver.User)
	for _, u := range users {
		mu := imapmemserver.NewUser(u.addr, u.password)
		for _, name := range folders {
			if err := mu.Create(name, nil); err != nil {
				return nil, fmt.Errorf("create mailbox %s for %s: %w", name, u.addr, err)
			}
		}
		mem.AddUser(mu)
		userHandles[strings.ToLower(u.addr)] = mu
	}

	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	imapLn, err := net.Listen("tcp", addrs["imap"])
	if err != nil {
		return nil, err
	}
	go func() { _ = imapSrv.Serve(imapLn) }()

	smtpLn, err := net.Listen("tcp", addrs["smtp"])
	if err != nil {
		_ = imapLn.Close()
		return nil, err
	}
	sink := &smtpSink{users: append([]mailUser(nil), users...)}
	if cfg.deliver {
		sink.deliver = func(rcpt, raw string) {
			mu, ok := userHandles[strings.ToLower(rcpt)]
			if !ok {
				return
			}
			if _, appendErr := appendRaw(mu, "INBOX", []byte(raw)); appendErr != nil {
				fmt.Fprintf(os.Stderr, "fakemail: deliver to %s INBOX: %v\n", rcpt, appendErr)
			}
		}
	}
	go sink.serve(smtpLn)

	ctrlLn, err := net.Listen("tcp", addrs["control"])
	if err != nil {
		_ = imapLn.Close()
		_ = smtpLn.Close()
		return nil, err
	}
	cl, err := imapclient.DialInsecure(imapLn.Addr().String(), nil)
	if err != nil {
		_ = imapLn.Close()
		_ = smtpLn.Close()
		_ = ctrlLn.Close()
		return nil, err
	}
	if err = cl.Login(users[0].addr, users[0].password).Wait(); err != nil {
		_ = imapLn.Close()
		_ = smtpLn.Close()
		_ = ctrlLn.Close()
		_ = cl.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/append", func(w http.ResponseWriter, r *http.Request) { handleAppend(w, r, cl) })
	mux.HandleFunc("/store", func(w http.ResponseWriter, r *http.Request) { handleStore(w, r, cl) })
	mux.HandleFunc("/smtp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, sink.snapshot())
	})
	mux.HandleFunc("/inject", func(w http.ResponseWriter, r *http.Request) { handleInject(w, r, userHandles) })
	go func() { _ = http.Serve(ctrlLn, mux) }()

	outLine := map[string]any{
		"imap": imapLn.Addr().String(), "smtp": smtpLn.Addr().String(),
		"control": ctrlLn.Addr().String(), "user": users[0].addr, "password": users[0].password,
	}
	if cfg.usersFlagGiven {
		names := make([]string, len(users))
		for i, u := range users {
			names[i] = u.addr
		}
		outLine["users"] = names
	}
	if err := json.NewEncoder(out).Encode(outLine); err != nil {
		return nil, err
	}

	return &fakeServer{
		imapAddr: imapLn.Addr().String(), smtpAddr: smtpLn.Addr().String(),
		ctrlAddr: ctrlLn.Addr().String(), primary: cl,
		imapLn: imapLn, smtpLn: smtpLn, ctrlLn: ctrlLn,
	}, nil
}

// appendRaw appends raw to a memserver user's mailbox directly (no IMAP
// round-trip) -- used by -deliver and /inject. The options pointer must be
// non-nil: the memserver dereferences it unconditionally.
func appendRaw(mu *imapmemserver.User, mailbox string, raw []byte) (imap.UID, error) {
	data, err := mu.Append(mailbox, struct{ *bytes.Reader }{bytes.NewReader(raw)}, &imap.AppendOptions{})
	if err != nil {
		return 0, err
	}
	return data.UID, nil
}

func handleAppend(w http.ResponseWriter, r *http.Request, cl *imapclient.Client) {
	var req struct {
		Mailbox string   `json:"mailbox"`
		RawB64  string   `json:"raw_base64"`
		Flags   []string `json:"flags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.RawB64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	flags := make([]imap.Flag, len(req.Flags))
	for i, f := range req.Flags {
		flags[i] = imap.Flag(f)
	}
	cmd := cl.Append(req.Mailbox, int64(len(raw)), &imap.AppendOptions{Flags: flags})
	if _, err = cmd.Write(raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err = cmd.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	data, err := cmd.Wait()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"uid": data.UID})
}

func handleStore(w http.ResponseWriter, r *http.Request, cl *imapclient.Client) {
	var req struct {
		Mailbox string   `json:"mailbox"`
		UID     uint32   `json:"uid"`
		Flags   []string `json:"flags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := cl.Select(req.Mailbox, nil).Wait(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	flags := make([]imap.Flag, len(req.Flags))
	for i, f := range req.Flags {
		flags[i] = imap.Flag(f)
	}
	store := cl.Store(imap.UIDSetNum(imap.UID(req.UID)), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: flags,
	}, nil)
	if err := store.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleInject appends a sample inbound message to a known user's mailbox
// (default INBOX) -- the injection path for users other than the first (who
// /append reaches) and the convenient one for review seeding.
func handleInject(w http.ResponseWriter, r *http.Request, userHandles map[string]*imapmemserver.User) {
	var req struct {
		User    string `json:"user"`
		Mailbox string `json:"mailbox"`
		Subject string `json:"subject"`
		From    string `json:"from"`
		Text    string `json:"text"`
		RawB64  string `json:"raw_base64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.User == "" {
		http.Error(w, "user is required", http.StatusBadRequest)
		return
	}
	mu, ok := userHandles[strings.ToLower(req.User)]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown user %q", req.User), http.StatusNotFound)
		return
	}
	mailbox := req.Mailbox
	if mailbox == "" {
		mailbox = "INBOX"
	}
	var raw []byte
	if req.RawB64 != "" {
		b, err := base64.StdEncoding.DecodeString(req.RawB64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw = b
	} else {
		from := req.From
		if from == "" {
			from = "someone@example.test"
		}
		subject := req.Subject
		if subject == "" {
			subject = "Sample message"
		}
		text := req.Text
		if text == "" {
			text = "Sample inbound message injected by fakemail."
		}
		raw = []byte(strings.Join([]string{
			"From: " + from,
			"To: " + req.User,
			"Subject: " + subject,
			"Date: " + time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"),
			"MIME-Version: 1.0",
			"Content-Type: text/plain; charset=utf-8",
			"",
			text,
			"",
		}, "\r\n"))
	}
	uid, err := appendRaw(mu, mailbox, raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"uid": uid})
}

type smtpSink struct {
	mu    sync.Mutex
	msgs  []string
	users []mailUser
	// deliver, when non-nil, is called for every completed DATA payload and
	// known recipient. It stays nil in no-flag mode, so the sink behaves
	// exactly as before.
	deliver func(rcpt, raw string)
}

func (s *smtpSink) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.one(c)
	}
}

func (s *smtpSink) one(c net.Conn) {
	defer c.Close()
	_, _ = io.WriteString(c, "220 fakemail\r\n")
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	var msg strings.Builder
	var rcpts []string
	reading := false
	authState := ""
	authUser := ""
	for {
		n, err := c.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		for {
			i := strings.Index(string(buf), "\r\n")
			if i < 0 {
				break
			}
			line := string(buf[:i])
			buf = buf[i+2:]
			if authState != "" {
				switch authState {
				case "plain":
					s.writeAuthResult(c, s.authenticatePlain(line))
					authState = ""
				case "login-user":
					var ok bool
					authUser, ok = decodeSMTPAuth(line)
					if !ok {
						s.writeAuthResult(c, false)
						authState = ""
					} else {
						authState = "login-pass"
						_, _ = io.WriteString(c, "334 UGFzc3dvcmQ6\r\n")
					}
				case "login-pass":
					password, ok := decodeSMTPAuth(line)
					s.writeAuthResult(c, ok && s.authenticate(authUser, password))
					authState = ""
					authUser = ""
				}
				continue
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "DATA"):
				reading = true
				_, _ = io.WriteString(c, "354 go\r\n")
			case reading && line == ".":
				reading = false
				raw := msg.String()
				s.mu.Lock()
				s.msgs = append(s.msgs, raw)
				s.mu.Unlock()
				if s.deliver != nil {
					for _, rcpt := range rcpts {
						s.deliver(rcpt, raw)
					}
				}
				msg.Reset()
				_, _ = io.WriteString(c, "250 ok\r\n")
			case reading:
				msg.WriteString(line)
				msg.WriteString("\n")
			case upper == "EHLO" || strings.HasPrefix(upper, "EHLO "):
				_, _ = io.WriteString(c, "250-fakemail\r\n250-AUTH PLAIN LOGIN\r\n250 ok\r\n")
			case upper == "HELO" || strings.HasPrefix(upper, "HELO "):
				_, _ = io.WriteString(c, "250 ok\r\n")
			case upper == "AUTH PLAIN":
				authState = "plain"
				_, _ = io.WriteString(c, "334 \r\n")
			case strings.HasPrefix(upper, "AUTH PLAIN "):
				fields := strings.Fields(line)
				s.writeAuthResult(c, len(fields) == 3 && s.authenticatePlain(fields[2]))
			case upper == "AUTH LOGIN":
				authState = "login-user"
				_, _ = io.WriteString(c, "334 VXNlcm5hbWU6\r\n")
			case strings.HasPrefix(upper, "AUTH LOGIN "):
				fields := strings.Fields(line)
				if len(fields) != 3 {
					s.writeAuthResult(c, false)
					break
				}
				var ok bool
				authUser, ok = decodeSMTPAuth(fields[2])
				if !ok {
					s.writeAuthResult(c, false)
					break
				}
				authState = "login-pass"
				_, _ = io.WriteString(c, "334 UGFzc3dvcmQ6\r\n")
			case strings.HasPrefix(upper, "QUIT"):
				_, _ = io.WriteString(c, "221 bye\r\n")
				return
			default:
				// RCPT TO is remembered for -deliver but answered exactly
				// as before: every unknown command and recipient gets 250.
				if strings.HasPrefix(upper, "RCPT TO:") {
					rcpts = append(rcpts, extractRcpt(line))
				}
				_, _ = io.WriteString(c, "250 ok\r\n")
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *smtpSink) authenticatePlain(response string) bool {
	decoded, ok := decodeSMTPAuth(response)
	if !ok {
		return false
	}
	parts := strings.Split(decoded, "\x00")
	return len(parts) == 3 && s.authenticate(parts[1], parts[2])
}

func decodeSMTPAuth(response string) (string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(response)
	return string(decoded), err == nil
}

func (s *smtpSink) authenticate(user, password string) bool {
	for _, candidate := range s.users {
		if strings.EqualFold(candidate.addr, user) && candidate.password == password {
			return true
		}
	}
	return false
}

func (s *smtpSink) writeAuthResult(w io.Writer, ok bool) {
	if ok {
		_, _ = io.WriteString(w, "235 2.7.0 Authentication successful\r\n")
		return
	}
	_, _ = io.WriteString(w, "535 5.7.8 Authentication failed\r\n")
}

// extractRcpt pulls the address out of an SMTP "RCPT TO:<a@b>" line.
func extractRcpt(line string) string {
	i := strings.Index(line, ":")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[i+1:])
	rest = strings.TrimPrefix(rest, "<")
	return strings.TrimSuffix(rest, ">")
}

func (s *smtpSink) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.msgs))
	copy(out, s.msgs)
	return map[string]any{"count": len(out), "messages": out}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
