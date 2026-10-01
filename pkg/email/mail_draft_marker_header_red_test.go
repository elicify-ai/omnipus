package email

// Round-6 RED — F1(a), the X-Omnipus-Part draft-body marker (squad-lead
// ruling, qa-lead pack). The draft-body bookkeeping part that
// pkg/email/compose.go::renderMarkdownPart renders for every Omnipus draft
// must carry the dedicated MIME header
//
//	X-Omnipus-Part: draft-body
//
// and it must be the ONLY leaf that does. The marker header — never the
// filename/type pair — is what every bookkeeping-part filter recognizes
// downstream, so a genuine user attachment named message.md (text/markdown)
// can never be mistaken for Omnipus bookkeeping again.
//
// Oracle: the squad-lead ruling (header name and value fixed by the ruling;
// "on that part, and only there"), NOT the current code — today the
// bookkeeping part carries no such header and this test is RED. The
// bookkeeping part's name/type stay message.md + text/markdown, so the
// round-4/5 leak oracles that match that pair keep their meaning.

import (
	"mime"
	"strings"
	"testing"

	gomail "github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/require"
)

// draftBodyMarkerHeader / draftBodyMarkerValue — the ruling's marker.
const (
	draftBodyMarkerHeader = "X-Omnipus-Part"
	draftBodyMarkerValue  = "draft-body"
)

// mdhComposeLeaves walks the MIME leaves of a composed message and returns,
// per leaf: content type, filename (Content-Disposition, else the Content-Type
// name parameter) and the X-Omnipus-Part header value ("" when absent).
// Header lookup is case-insensitive per go-message; the value is trimmed so a
// header written with padding still matches exactly once trimmed.
func mdhComposeLeaves(t *testing.T, raw []byte) []struct {
	ContentType  string
	Filename     string
	OmnipusPart  string
	Disposition  string
	TransfersEnc string
} {
	t.Helper()
	r, err := gomail.CreateReader(strings.NewReader(string(raw)))
	require.NoError(t, err, "instrument: composed output must parse as MIME")
	var leaves []struct {
		ContentType  string
		Filename     string
		OmnipusPart  string
		Disposition  string
		TransfersEnc string
	}
	for {
		p, perr := r.NextPart()
		if perr != nil || p == nil {
			break
		}
		ct, ctParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		disp, dispParams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := dispParams["filename"]
		if name == "" {
			name = ctParams["name"]
		}
		leaves = append(leaves, struct {
			ContentType  string
			Filename     string
			OmnipusPart  string
			Disposition  string
			TransfersEnc string
		}{
			ContentType:  ct,
			Filename:     name,
			OmnipusPart:  strings.TrimSpace(p.Header.Get(draftBodyMarkerHeader)),
			Disposition:  disp,
			TransfersEnc: p.Header.Get("Content-Transfer-Encoding"),
		})
	}
	return leaves
}

func TestComposeDraftBodyBookkeepingPart_MarkerHeader(t *testing.T) {
	// (a): a draft composed by Omnipus (Draft: true) carries
	// X-Omnipus-Part: draft-body on its markdown bookkeeping part — and
	// only there. Every expected value derives from the ruling: exactly one
	// header-marked leaf; that leaf is the bookkeeping part (text/markdown,
	// message.md, disposition attachment); the body leaves and every user
	// attachment carry no marker header. A compose that stops writing the
	// header fails the first require; a compose that stamps the header on
	// other parts (or drops the bookkeeping part entirely) fails the
	// subsequent requires.
	t.Run("the draft's bookkeeping part and only it carries the marker header", func(t *testing.T) {
		out, err := Compose(ComposeInput{
			From:      "mailbox@test.local",
			To:        []string{"a@b.test"},
			Subject:   "marker header fixture",
			Markdown:  "marker header fixture body",
			Draft:     true,
			MessageID: "<mdh-unit-draft@example.test>",
			Attachments: []Attachment{
				{Name: "notes.txt", ContentType: "text/plain", Data: []byte("real user attachment bytes z81kq")},
			},
		})
		require.NoError(t, err)

		leaves := mdhComposeLeaves(t, out.Transmitted)

		// Exactly one leaf carries the marker header...
		var marked int
		for _, l := range leaves {
			if l.OmnipusPart != "" {
				marked++
			}
		}
		require.Equal(t, 1, marked,
			"exactly one leaf must carry %s (the draft's bookkeeping part), found %d across %d leaves",
			draftBodyMarkerHeader, marked, len(leaves))

		// ...and that leaf is the bookkeeping part: text/markdown named
		// message.md with an attachment disposition.
		for _, l := range leaves {
			if l.OmnipusPart == "" {
				// "and only there": the body leaves and the real attachment
				// carry no marker header.
				require.Equal(t, "", l.OmnipusPart,
					"the marker header must appear on the bookkeeping part only; found on %s %q", l.ContentType, l.Filename)
				continue
			}
			require.Equal(t, draftBodyMarkerValue, l.OmnipusPart,
				"the bookkeeping part's %s value must be exactly %q", draftBodyMarkerHeader, draftBodyMarkerValue)
			require.Equal(t, "text/markdown", l.ContentType,
				"the header-marked leaf must be the bookkeeping part (text/markdown)")
			require.Equal(t, "message.md", l.Filename,
				"the header-marked leaf must keep the bookkeeping part's name (message.md) so existing name/type oracles keep their meaning")
			require.Equal(t, "attachment", l.Disposition,
				"the bookkeeping part keeps its attachment disposition")
		}
	})

	// (a), negative side: a NON-draft compose has no bookkeeping part and
	// therefore no marker header anywhere. Guards the fix direction: the
	// marker must never leak onto plain outbound mail.
	t.Run("a non-draft compose carries the marker header nowhere", func(t *testing.T) {
		out, err := Compose(ComposeInput{
			From:      "mailbox@test.local",
			To:        []string{"a@b.test"},
			Subject:   "no marker here",
			Markdown:  "plain outbound body",
			Draft:     false,
			MessageID: "<mdh-unit-plain@example.test>",
			Attachments: []Attachment{
				{Name: "notes.txt", ContentType: "text/plain", Data: []byte("real user attachment bytes z81kq")},
			},
		})
		require.NoError(t, err)
		for _, l := range mdhComposeLeaves(t, out.Transmitted) {
			require.Equal(t, "", l.OmnipusPart,
				"a non-draft message must carry no %s header; found on %s %q", draftBodyMarkerHeader, l.ContentType, l.Filename)
		}
	})
}
