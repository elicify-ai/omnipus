package preview

// T-105's fake soffice and fixtures (§3.4 D8c, R1): the fake records, at
// each start, the folder URE_MORE_SERVICES names and the hash of every file
// in it, then tries to create, change and remove a file there — the in-place
// read-only evidence. No build tag: the dispatch in programs_test.go is
// cross-platform; the T-105 test itself is darwin-only (URE_MORE_SERVICES is
// macOS-only).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
)

// regReadReceipt is one fake soffice start's registry evidence: the folder
// URE_MORE_SERVICES named, the hash of every file read there, and the
// outcome of each write attempt against that folder.
type regReadReceipt struct {
	Start     int               `json:"start"`
	PID       int               `json:"pid"`
	Services  string            `json:"services_path"`
	ReadOK    bool              `json:"read_ok"`
	Files     map[string]string `json:"files,omitempty"`
	Digest    string            `json:"digest,omitempty"`
	Note      string            `json:"note,omitempty"`
	CreateErr string            `json:"create_err,omitempty"`
	ChangeErr string            `json:"change_err,omitempty"`
	RemoveErr string            `json:"remove_err,omitempty"`
}

// regReadServicesPath decodes URE_MORE_SERVICES the way the gateway encodes
// it (programs.go: `<file://path>*`).
func regReadServicesPath(env []string) string {
	for _, entry := range env {
		value, ok := strings.CutPrefix(entry, "URE_MORE_SERVICES=")
		if !ok {
			continue
		}
		value = strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">*")
		value = strings.TrimPrefix(value, "file://")
		return strings.ReplaceAll(value, "%20", " ")
	}
	return ""
}

// regReadProcess is the T-105 fake soffice (as T-78's external converter):
// it converts, records the registry evidence, applies the write attempts,
// and exits. regread_read converts on the first start; regread_81 exits 81
// on the first start (LibreOffice's restart exit), so the call's second
// start repeats the evidence (§3.4 D8c row (d)).
func regReadProcess() int {
	raw, readErr := os.ReadFile("behavior")
	if readErr != nil || !strings.HasPrefix(strings.TrimSpace(string(raw)), "regread_") {
		return 96
	}
	behaviorName := strings.TrimSpace(string(raw))
	starts, globErr := filepath.Glob("regread-start-*.json")
	if globErr != nil {
		return 97
	}
	receipt := regReadReceipt{
		Start:    len(starts) + 1,
		PID:      os.Getpid(),
		Services: regReadServicesPath(os.Environ()),
	}
	// The exit-81 first start writes no output — that is what a real
	// LibreOffice first start does, and it is what sends the caller down the
	// §3.1 D3 retry (a readable PDF here would end the call after one start).
	restart := behaviorName == "regread_81" && receipt.Start == 1
	if receipt.Services == "" {
		receipt.Note = "URE_MORE_SERVICES missing"
	} else if files, hashErr := component.HashDir(receipt.Services); hashErr != nil {
		receipt.Note = hashErr.Error()
	} else {
		receipt.ReadOK = true
		receipt.Files = files
		receipt.Digest = component.FileListDigest(files)
	}
	// The write attempts run against the folder URE_MORE_SERVICES named —
	// the same folder the row's read evidence came from, never another one.
	if receipt.Services != "" {
		probe, createErr := os.Create(filepath.Join(receipt.Services, "t105-probe.rdb"))
		if createErr != nil {
			receipt.CreateErr = createErr.Error()
		} else {
			_ = probe.Close()
		}
		if changeErr := os.WriteFile(filepath.Join(receipt.Services, "services.rdb"), []byte("t105 write attempt"), 0o600); changeErr != nil {
			receipt.ChangeErr = changeErr.Error()
		}
		if removeErr := os.Remove(filepath.Join(receipt.Services, "officeloader.rdb")); removeErr != nil {
			receipt.RemoveErr = removeErr.Error()
		}
	}
	if !restart {
		if os.MkdirAll("out", 0o700) != nil || os.WriteFile(filepath.Join("out", "document.pdf"), officeRetryPDF(), 0600) != nil {
			return 91
		}
	}
	data, mErr := json.Marshal(receipt)
	if mErr != nil || os.WriteFile(fmt.Sprintf("regread-start-%d.json", receipt.Start), data, 0600) != nil {
		return 98
	}
	if restart {
		return 81
	}
	return 0
}
