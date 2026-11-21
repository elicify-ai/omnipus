//go:build darwin || linux

package preview

// T-102's harmless probe program (spec §13 T-102): started through the same
// confinement hooks with the captured policy (converterStart.hooks.prepare),
// at a barrier inside the call — the test seam hook spawns it synchronously
// while the call is still live, so <id>/work exists and no timer is involved.
// It records what the converter's own policy lets it do: look up (stat) each
// folder above the converter's own folders, list them, read or look up a
// planted file in the data folder, reach a later call folder, this call's
// owner record, a workspace file, <home>/system outside the registry, the
// registry (read must succeed, write must fail), and its own work folder
// (write must succeed). Everything is recorded, nothing is asserted here.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "--office-preview-probe" {
		os.Exit(runPreviewProbeProcess(os.Args[2]))
	}
}

// previewProbeSpec is what the test tells the probe to try, all absolute
// except the receipt and spec names, which are relative to the probe's
// working folder (<id>/work).
type previewProbeSpec struct {
	Receipt       string   `json:"receipt"`
	Lookup        []string `json:"lookup"`        // folders above the converter's own
	Planted       string   `json:"planted"`       // file planted in the data folder
	OtherCall     string   `json:"other_call"`    // a call folder created after this start
	OtherFile     string   `json:"other_file"`    // a file inside it
	Owner         string   `json:"owner"`         // this call's owner record
	WorkspaceFile string   `json:"workspace"`     // an agent workspace file
	Outside       string   `json:"outside"`       // <home>/system outside the registry
	RegistryFile  string   `json:"registry_file"` // a file inside the registry folder
}

// previewProbeReceipt is what the probe found. Empty error strings mean the
// operation succeeded; the test asserts the expected side of every field.
type previewProbeReceipt struct {
	LookupOK         []string `json:"lookup_ok"`
	LookupFail       []string `json:"lookup_fail"`
	ListOK           []string `json:"list_ok,omitempty"`
	ListFail         []string `json:"list_fail"`
	PlantedStatErr   string   `json:"planted_stat_err,omitempty"`
	PlantedReadErr   string   `json:"planted_read_err,omitempty"`
	OtherCallStatErr string   `json:"other_call_stat_err,omitempty"`
	OtherFileReadErr string   `json:"other_file_read_err,omitempty"`
	OwnerReadErr     string   `json:"owner_read_err,omitempty"`
	WorkspaceReadErr string   `json:"workspace_read_err,omitempty"`
	OutsideReadErr   string   `json:"outside_read_err,omitempty"`
	RegistryReadErr  string   `json:"registry_read_err,omitempty"`
	RegistryWriteErr string   `json:"registry_write_err,omitempty"`
	WorkWriteErr     string   `json:"work_write_err,omitempty"`
}

// runPreviewProbeProcess is the probe main: run every check, write the
// receipt, exit. Exit 91 means the receipt could not be written (the test
// treats that as an instrument failure).
func runPreviewProbeProcess(specPath string) int {
	raw, readErr := os.ReadFile(specPath)
	if readErr != nil {
		return 90
	}
	var spec previewProbeSpec
	if json.Unmarshal(raw, &spec) != nil {
		return 90
	}
	receipt := previewProbeReceipt{
		LookupOK:   []string{},
		LookupFail: []string{},
		ListOK:     []string{},
		ListFail:   []string{},
	}
	for _, dir := range spec.Lookup {
		if _, err := os.Stat(dir); err != nil {
			receipt.LookupFail = append(receipt.LookupFail, dir)
		} else {
			receipt.LookupOK = append(receipt.LookupOK, dir)
		}
		if _, err := os.ReadDir(dir); err != nil {
			receipt.ListFail = append(receipt.ListFail, dir)
		} else {
			receipt.ListOK = append(receipt.ListOK, dir)
		}
	}
	if _, err := os.Stat(spec.Planted); err != nil {
		receipt.PlantedStatErr = err.Error()
	}
	if _, err := os.ReadFile(spec.Planted); err != nil {
		receipt.PlantedReadErr = err.Error()
	}
	if _, err := os.Stat(spec.OtherCall); err != nil {
		receipt.OtherCallStatErr = err.Error()
	}
	if _, err := os.ReadFile(spec.OtherFile); err != nil {
		receipt.OtherFileReadErr = err.Error()
	}
	if _, err := os.ReadFile(spec.Owner); err != nil {
		receipt.OwnerReadErr = err.Error()
	}
	if _, err := os.ReadFile(spec.WorkspaceFile); err != nil {
		receipt.WorkspaceReadErr = err.Error()
	}
	if _, err := os.ReadFile(spec.Outside); err != nil {
		receipt.OutsideReadErr = err.Error()
	}
	if _, err := os.ReadFile(spec.RegistryFile); err != nil {
		receipt.RegistryReadErr = err.Error()
	}
	// The write attempt opens for writing only — never truncates — so even an
	// unexpectedly granted open cannot change the registry's bytes.
	if file, err := os.OpenFile(spec.RegistryFile, os.O_WRONLY, 0); err != nil {
		receipt.RegistryWriteErr = err.Error()
	} else {
		_ = file.Close()
	}
	if file, err := os.Create("probe-work-check.txt"); err != nil {
		receipt.WorkWriteErr = err.Error()
	} else {
		_ = file.Close()
	}
	data, marshalErr := json.Marshal(receipt)
	if marshalErr != nil || os.WriteFile(spec.Receipt, data, 0600) != nil {
		return 91
	}
	return 0
}

// previewProbeReceiptPath is the receipt's work-folder name.
const previewProbeReceiptPath = "probe-receipt.json"

// previewReadProbeReceipt reads the probe's receipt from the work folder.
func previewReadProbeReceipt(workDir string) (previewProbeReceipt, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(workDir, previewProbeReceiptPath))
	if err != nil {
		return previewProbeReceipt{}, nil, err
	}
	var receipt previewProbeReceipt
	if unmarshalErr := json.Unmarshal(raw, &receipt); unmarshalErr != nil {
		return receipt, raw, unmarshalErr
	}
	return receipt, raw, nil
}
