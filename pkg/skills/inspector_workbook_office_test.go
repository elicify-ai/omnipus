package skills

// Office-preview AD-14/T-72: workbook oracles come from the merged inspector
// contract, not observed script output. Real openpyxl and ZIP/XML parsing run;
// cached values and deliberately false dimensions are literal fixture inputs.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func buildOfficeWorkbook(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.xlsx")
	code := "import sys\nfrom openpyxl import Workbook\nw = Workbook()\n" + body + "\nw.save(sys.argv[1])\n"
	out, err := exec.Command(pythonBin(t), "-I", "-B", "-c", code, path).CombinedOutput()
	if err != nil {
		t.Fatalf("openpyxl workbook fixture failed: %v\n%s", err, out)
	}
	return path
}

func patchOfficeWorkbook(t *testing.T, path string, replacements map[string][2]string, extras map[string]string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	patched := make(map[string]bool)
	for _, entry := range reader.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(file)
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if edit, ok := replacements[entry.Name]; ok {
			if !strings.Contains(string(body), edit[0]) {
				t.Fatalf("fixture %s lacks declared input fragment %q", entry.Name, edit[0])
			}
			body = []byte(strings.Replace(string(body), edit[0], edit[1], 1))
			patched[entry.Name] = true
		}
		dest, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dest.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for name := range replacements {
		if !patched[name] {
			t.Fatalf("fixture lacks XML part %s", name)
		}
	}
	for name, body := range extras {
		dest, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(dest, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertOfficeWorkbookReport(t *testing.T, path, expected string) {
	t.Helper()
	before := sha256of(t, path)
	got := runInspector(t, xlsxScript, fmt.Sprintf("insp.inspect_workbook(%q)", path))
	var want any
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatalf("invalid contract oracle: %v", err)
	}
	if !reflect.DeepEqual(got["ok"], want) {
		t.Fatalf("workbook report = %#v (error=%v), want contract %#v", got["ok"], got["err"], want)
	}
	assertUnchanged(t, path, before)
}

func runWorkbookCases(t *testing.T) {
	t.Helper()
	t.Run("empty_sheet_is_1_by_1", func(t *testing.T) {
		path := buildOfficeWorkbook(t, "")
		assertOfficeWorkbookReport(t, path, `{"sheets":[{"name":"Sheet","rows":1,"columns":1}],"formulas":[],"missing_formula_caches":[],"errors":[],"has_macros":false,"has_external_links":false}`)
	})
	t.Run("formula_without_cache_is_unknown_not_zero", func(t *testing.T) {
		path := buildOfficeWorkbook(t, `w.active["A1"] = "=1+1"`)
		assertOfficeWorkbookReport(t, path, `{"sheets":[{"name":"Sheet","rows":1,"columns":1}],"formulas":[{"sheet":"Sheet","cell":"A1"}],"missing_formula_caches":[{"sheet":"Sheet","cell":"A1"}],"errors":[],"has_macros":false,"has_external_links":false}`)
	})
	t.Run("cached_zero_and_stored_and_cached_errors", func(t *testing.T) {
		path := buildOfficeWorkbook(t, "w.active['A1'] = '=1-1'\nw.active['B1'] = '=1/0'\nw.active['C1'] = '#VALUE!'")
		patchOfficeWorkbook(t, path, map[string][2]string{
			"xl/worksheets/sheet1.xml": {`<c r="A1"><f>1-1</f><v></v></c>`, `<c r="A1"><f>1-1</f><v>0</v></c>`},
		}, nil)
		patchOfficeWorkbook(t, path, map[string][2]string{
			"xl/worksheets/sheet1.xml": {`<c r="B1"><f>1/0</f><v></v></c>`, `<c r="B1" t="e"><f>1/0</f><v>#DIV/0!</v></c>`},
		}, nil)
		assertOfficeWorkbookReport(t, path, `{"sheets":[{"name":"Sheet","rows":1,"columns":3}],"formulas":[{"sheet":"Sheet","cell":"A1"},{"sheet":"Sheet","cell":"B1"}],"missing_formula_caches":[],"errors":[{"sheet":"Sheet","cell":"B1","value":"#DIV/0!"},{"sheet":"Sheet","cell":"C1","value":"#VALUE!"}],"has_macros":false,"has_external_links":false}`)
	})
	t.Run("sheet_order_real_dimensions_and_inert_package_parts", func(t *testing.T) {
		path := buildOfficeWorkbook(t, "w.active.title = 'First'\nw.active['A1'] = 1\nw.active['C3'] = 3\nw.create_sheet('Second')['B2'] = 2")
		patchOfficeWorkbook(t, path, map[string][2]string{
			"xl/worksheets/sheet1.xml": {`<dimension ref="A1:C3"/>`, `<dimension ref="A1:A1"/>`},
		}, map[string]string{
			"xl/vbaProject.bin":                  "inert macro fixture; never executed",
			"xl/externalLinks/externalLink1.xml": `<externalLink/>`,
		})
		assertOfficeWorkbookReport(t, path, `{"sheets":[{"name":"First","rows":3,"columns":3},{"name":"Second","rows":2,"columns":2}],"formulas":[],"missing_formula_caches":[],"errors":[],"has_macros":true,"has_external_links":true}`)
	})
	t.Run("unsupported_extension", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "unsupported.xls")
		wantContractError(t, runInspector(t, xlsxScript, fmt.Sprintf("insp.inspect_workbook(%q)", path)), "ValueError", "unsupported workbook extension")
	})
	t.Run("missing_file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.xlsx")
		wantContractError(t, runInspector(t, xlsxScript, fmt.Sprintf("insp.inspect_workbook(%q)", path)), "FileNotFoundError", "")
	})
	t.Run("corrupt_package", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.xlsx")
		if err := os.WriteFile(path, []byte("not a ZIP"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := sha256of(t, path)
		wantContractError(t, runInspector(t, xlsxScript, fmt.Sprintf("insp.inspect_workbook(%q)", path)), "ValueError", "invalid workbook package")
		assertUnchanged(t, path, before)
	})
	t.Run("valid_zip_without_workbook_part", func(t *testing.T) {
		path := buildDocx(t, map[string]string{"word/document.xml": "<document/>"})
		xlsx := filepath.Join(t.TempDir(), "wrong.xlsx")
		if err := os.Rename(path, xlsx); err != nil {
			t.Fatal(err)
		}
		before := sha256of(t, xlsx)
		wantContractError(t, runInspector(t, xlsxScript, fmt.Sprintf("insp.inspect_workbook(%q)", xlsx)), "ValueError", "invalid workbook package")
		assertUnchanged(t, xlsx, before)
	})
}
