//go:build darwin || linux

package doctor

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// T-87 case (e), §3.2 point 4 + point 6: an overlapping write-access entry
// that was present at gateway start (a refused save leaves nothing to report)
// keeps today's behaviour — the component is disabled for everyone and doctor
// reports it, naming the access. The gateway-side halves of this row live in
// pkg/gateway/preview_config_refusal_office_test.go (this package is internal
// to pkg/app, so the row is split; the doctor oracle is its own surface).
func TestOfficePreviewDoctorReportsInForceConfigEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	root, _ := officeDoctorComponent(t, home, component.KindOffice)
	entry := "^" + root + "(?:/|$)"
	cfg := &config.Config{Tools: config.ToolsConfig{AllowWritePaths: []string{entry}}}
	warnings := checkConfig(cfg)
	matched := officeDoctorWarnings(warnings, "WARN-PREVIEW-002")
	require.Len(t, matched, 1, "T-87 (e): the in-force entry must be reported exactly once")
	require.Contains(t, matched[0], "LibreOffice")
	require.Contains(t, matched[0], "operator write allowance "+entry,
		"the warning must name the overlapping access (§3.2 point 6)")
}
