package gateway

// mail_isolation_policy.go - the Mail panel's own Content-Security-Policy
// builder (MC-37). Never the Library's builder, template or fallback: no
// 'self', no allow-scripts, no script-src host sources, no 'unsafe-inline'
// in script-src, no https: in img-src, and every host source path-confined
// to the mail-preview prefix. The no-origin case OMITS the host sources and
// logs a loud WARN (MC-38) - never a 'self' or https: fallback (MC-38).

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// mailPreviewPathPrefix aliases the ONE spelling of the security-boundary
// prefix, defined in the middleware package next to defaultExemptPrefixes
// (the Library prefix's own anti-drift rule): the router and the CSRF
// boundary must never carry two spellings.
const mailPreviewPathPrefix = middleware.MailPreviewPathPrefix

const (
	mailPreviewHTMLPrefix = mailPreviewPathPrefix + "html/"
	mailPreviewPartPrefix = mailPreviewPathPrefix + "part/"
	mailPreviewImgPrefix  = mailPreviewPathPrefix + "img/"
)

// mailIsolationPolicy builds the Mail preview CSP. Every host source is
// path-confined under the mail-preview prefix; the sandbox directive
// mirrors the iframe attribute token-for-token (MC-10(3)).
func mailIsolationPolicy(origin string) string {
	var b strings.Builder
	b.WriteString("default-src 'none'; script-src 'none'; object-src 'none'; base-uri 'none'; connect-src 'none'; form-action 'none'; ")
	b.WriteString("style-src 'unsafe-inline'; ")
	b.WriteString("sandbox allow-popups allow-popups-to-escape-sandbox")
	if origin == "" {
		slog.Warn("mail preview: no canonical gateway origin; CSP host sources omitted (MC-38)")
		b.WriteString("; img-src data:")
	} else {
		b.WriteString("; img-src data: " + origin + mailPreviewPartPrefix + " " + origin + mailPreviewImgPrefix)
	}
	return b.String()
}
