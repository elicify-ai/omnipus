package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailSendRejectsInjectedAttachmentMediaTypeBeforeSMTPDial(t *testing.T) {
	env := newMailRedEnv(t)
	port, dials := listenCount(t)
	pointMailboxAt(t, env, port, port)

	rec := mailDo(env.mux, http.MethodPost,
		"/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages",
		nextMailIP(), true,
		`{"to":["human@example.test"],"subject":"attachment","body_markdown":"body","attachments":[{"filename":"report.pdf","content_type":"application/pdf\r\nX-Injected: yes","data_base64":"cGRm"}]}`)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	require.Zero(t, dials.Load(), "invalid MIME content type must be rejected before any IMAP or SMTP dial")
}
