package gateway

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailSendRejectsInjectedAttachmentMediaTypeBeforeSMTPDial(t *testing.T) {
	env := newMailRedEnv(t)
	port, dials := listenCount(t)
	pointMailboxAt(t, env, port, port)

	for name, contentType := range map[string]string{
		"CRLF":             "application/pdf\r\nX-Injected: yes",
		"lone CR":          "application/pdf\rX-Injected: yes",
		"lone LF":          "application/pdf\nX-Injected: yes",
		"malformed syntax": `application/pdf; name="unterminated`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := mailDo(env.mux, http.MethodPost,
				"/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages",
				nextMailIP(), true,
				fmt.Sprintf(`{"to":["human@example.test"],"subject":"attachment","body_markdown":"body","attachments":[{"filename":"report.pdf","content_type":%q,"data_base64":"cGRm"}]}`, contentType))

			require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			require.Zero(t, dials.Load(), "invalid MIME content type must be rejected before any IMAP or SMTP dial")
		})
	}
}
