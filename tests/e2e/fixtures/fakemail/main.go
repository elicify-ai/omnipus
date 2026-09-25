// Command fakemail is the D36 built-in fake IMAP/SMTP server for the mail
// panel end-to-end spec. It speaks plaintext IMAP (the same imapmemserver
// startMemIMAP uses) plus a sink that records SMTP, and a small control API
// the spec uses to seed messages. One JSON line on stdout gives the ports.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func main() {
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("mailbox@test.local", "s3cret")
	for _, name := range []string{"INBOX", "Sent", "Drafts"} {
		if err := user.Create(name, nil); err != nil {
			fatal(err)
		}
	}
	mem.AddUser(user)
	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	go func() { _ = imapSrv.Serve(imapLn) }()

	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	sink := &smtpSink{}
	go sink.serve(smtpLn)

	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	cl, err := imapclient.DialInsecure(imapLn.Addr().String(), nil)
	if err != nil {
		fatal(err)
	}
	if err = cl.Login("mailbox@test.local", "s3cret").Wait(); err != nil {
		fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/append", func(w http.ResponseWriter, r *http.Request) { handleAppend(w, r, cl) })
	mux.HandleFunc("/store", func(w http.ResponseWriter, r *http.Request) { handleStore(w, r, cl) })
	mux.HandleFunc("/smtp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, sink.snapshot())
	})
	go func() { _ = http.Serve(ctrlLn, mux) }()

	out := map[string]string{
		"imap": imapLn.Addr().String(), "smtp": smtpLn.Addr().String(),
		"control": ctrlLn.Addr().String(), "user": "mailbox@test.local", "password": "s3cret",
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
	select {}
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

type smtpSink struct {
	mu   sync.Mutex
	msgs []string
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
	reading := false
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
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "DATA"):
				reading = true
				_, _ = io.WriteString(c, "354 go\r\n")
			case reading && line == ".":
				reading = false
				s.mu.Lock()
				s.msgs = append(s.msgs, msg.String())
				s.mu.Unlock()
				msg.Reset()
				_, _ = io.WriteString(c, "250 ok\r\n")
			case reading:
				msg.WriteString(line)
				msg.WriteString("\n")
			case strings.HasPrefix(upper, "QUIT"):
				_, _ = io.WriteString(c, "221 bye\r\n")
				return
			default:
				_, _ = io.WriteString(c, "250 ok\r\n")
			}
		}
		if err != nil {
			return
		}
	}
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
