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
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return fmt.Errorf("SMTP client (greeting): %w", err)
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
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return fmt.Errorf("SMTP client (greeting): %w", err)
	}
	defer cl.Close()

	auth := smtp.PlainAuth("", from, password, tlsCfg.ServerName)
	if stepErr := smtpStep(ctx, cl.Auth(auth), "auth"); stepErr != nil {
		return stepErr
	}
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
	_ = smtpStep(ctx, cl.Quit(), "QUIT")
	return nil
}
