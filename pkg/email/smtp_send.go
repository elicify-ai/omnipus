package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
)

// sendSMTPWithSTARTTLS transmits body to every envelope recipient over a
// STARTTLS session (any port other than 465) — except a loopback address,
// which runs plaintext (AUTH included), mirroring the imapDial exception in
// transport.go: the D36 built-in fake server and the D37 GreenMail UAT
// instance are loopback SMTP servers on dynamic ports, so the local sink is
// identified by ADDRESS — traffic to it never leaves the machine, and a
// name- or port-based exception could not carry a dynamic port. The dial,
// the banner read and every SMTP step are bounded by the caller's context
// deadline (MC-21/#629); with no caller deadline the commandTimeout fallback
// applies.
func sendSMTPWithSTARTTLS(ctx context.Context, addr string, auth smtp.Auth, from string, rcpts []string, body string, tlsCfg *tls.Config) error {
	conn, err := dialSMTPRaw(ctx, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		return smtpStep(ctx, err, "SMTP client (greeting)")
	}
	defer cl.Close()

	// Loopback exception (mirrors imapDial): skip the STARTTLS upgrade and run
	// AUTH/MAIL/RCPT/DATA over the already-dialed plaintext loopback
	// connection. Every non-loopback address still demands trusted-TLS
	// STARTTLS. (net/smtp permits PlainAuth over plaintext only to a
	// localhost-named server, so the plaintext path stays name-checked too.)
	if !isLoopbackAddr(addr) {
		if stepErr := smtpStep(ctx, cl.StartTLS(tlsCfg), "STARTTLS"); stepErr != nil {
			return stepErr
		}
	}
	if stepErr := smtpStep(ctx, cl.Auth(auth), "auth"); stepErr != nil {
		return stepErr
	}
	return smtpSendEnvelope(ctx, cl, from, rcpts, body)
}

// sendSMTPS transmits body to every envelope recipient over implicit TLS
// (port 465). Bounded by ctx exactly like the STARTTLS path.
func sendSMTPS(ctx context.Context, addr, username, password, from string, rcpts []string, body string, tlsCfg *tls.Config) error {
	raw, err := dialSMTPRaw(ctx, addr)
	if err != nil {
		return err
	}
	defer raw.Close()

	conn := tls.Client(raw, tlsCfg)
	cl, err := smtp.NewClient(conn, tlsCfg.ServerName)
	if err != nil {
		return smtpStep(ctx, err, "SMTP client (greeting)")
	}
	defer cl.Close()

	// AUTH identity is the configured username (the parameter's promise, and
	// what the STARTTLS path in transport.go already does); from stays the
	// envelope sender in smtpSendEnvelope. The sole caller passes the same
	// value for both, so this is behavior-neutral today — it only removes the
	// latent identity confusion if a future caller passes them differently.
	auth := smtp.PlainAuth("", username, password, tlsCfg.ServerName)
	if stepErr := smtpStep(ctx, cl.Auth(auth), "auth"); stepErr != nil {
		return stepErr
	}
	return smtpSendEnvelope(ctx, cl, from, rcpts, body)
}

// smtpSendEnvelope runs the post-auth envelope sequence — MAIL FROM → RCPT
// TO → DATA → body write → body close → best-effort QUIT — on an
// already-authenticated client, bounded by ctx exactly like every step
// before it. One shared copy for both the STARTTLS and the implicit-TLS
// path, so the two wire sequences cannot drift.
func smtpSendEnvelope(ctx context.Context, cl *smtp.Client, from string, rcpts []string, body string) error {
	if stepErr := smtpStep(ctx, cl.Mail(from), "MAIL FROM"); stepErr != nil {
		return stepErr
	}
	for _, r := range rcpts {
		if stepErr := smtpStep(ctx, cl.Rcpt(r), "RCPT TO"); stepErr != nil {
			return stepErr
		}
	}
	w, err := cl.Data()
	if stepErr := smtpStep(ctx, err, "DATA"); stepErr != nil {
		return stepErr
	}
	if _, err := io.WriteString(w, body); err != nil {
		return smtpStep(ctx, err, "write body")
	}
	if err := smtpStep(ctx, w.Close(), "body close"); err != nil {
		return err
	}
	// Best-effort goodbye; the message is already transmitted.
	_ = smtpStep(ctx, cl.Quit(), "QUIT")
	return nil
}
