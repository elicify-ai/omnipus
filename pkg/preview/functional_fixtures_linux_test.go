//go:build linux

package preview

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// T-114 fixture oracles: source-defined 3 worksheets, 6 explicit Word pages,
// 4 slides. Content and page counts come from the spec, never converter output.
type functionalDocument struct {
	ID    string
	Ext   string
	Text  []string
	Pages int
	Data  []byte
}

func functionalDocuments(t *testing.T) []functionalDocument {
	t.Helper()
	return []functionalDocument{
		{ID: "FX-XLSX3", Ext: ".xlsx", Text: functionalSourceText("Workbook", 3), Pages: 3, Data: functionalWorkbook(t)},
		{ID: "FX-DOCX6", Ext: ".docx", Text: functionalSourceText("Word", 6), Pages: 6, Data: functionalWord(t)},
		{ID: "FX-PPTX4", Ext: ".pptx", Text: functionalSourceText("Presentation", 4), Pages: 4, Data: functionalPresentation(t)},
	}
}

func functionalSourceText(kind string, pages int) []string {
	out := make([]string, pages)
	for i := range out {
		out[i] = fmt.Sprintf("%s source page %d", kind, i+1)
	}
	return out
}

func functionalZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, name := range names {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(files[name]))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return out.Bytes()
}

const functionalTypesStart = `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`
const functionalRelsStart = `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`

func functionalRootRel(target string) string {
	return functionalRelsStart + `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="` + target + `"/></Relationships>`
}

func functionalWorkbook(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{"_rels/.rels": functionalRootRel("xl/workbook.xml")}
	types := functionalTypesStart + `<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`
	workbook := `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`
	rels := functionalRelsStart
	for index, text := range functionalSourceText("Workbook", 3) {
		n := index + 1
		types += fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n)
		workbook += fmt.Sprintf(`<sheet name="Page%d" sheetId="%d" r:id="rId%d"/>`, n, n, n)
		rels += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n)
		files[fmt.Sprintf("xl/worksheets/sheet%d.xml", n)] = `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetPr><pageSetUpPr fitToPage="1"/></sheetPr><cols><col min="1" max="1" width="50" customWidth="1"/></cols><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>` + text + `</t></is></c></row></sheetData><pageSetup paperSize="9" orientation="portrait" fitToWidth="1" fitToHeight="1"/></worksheet>`
	}
	files["[Content_Types].xml"] = types + `</Types>`
	files["xl/workbook.xml"] = workbook + `</sheets></workbook>`
	files["xl/_rels/workbook.xml.rels"] = rels + `</Relationships>`
	return functionalZip(t, files)
}

func functionalWord(t *testing.T) []byte {
	t.Helper()
	var body strings.Builder
	for index, text := range functionalSourceText("Word", 6) {
		body.WriteString(`<w:p><w:r>`)
		if index > 0 {
			body.WriteString(`<w:br w:type="page"/>`)
		}
		body.WriteString(`<w:t>` + text + `</w:t></w:r></w:p>`)
	}
	return functionalZip(t, map[string]string{
		"[Content_Types].xml": functionalTypesStart + `<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         functionalRootRel("word/document.xml"),
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>`,
	})
}

func functionalPresentation(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{"_rels/.rels": functionalRootRel("ppt/presentation.xml")}
	types := functionalTypesStart + `<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`
	presentation := `<?xml version="1.0" encoding="UTF-8"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst>`
	rels := functionalRelsStart
	for index, text := range functionalSourceText("Presentation", 4) {
		n := index + 1
		types += fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, n)
		presentation += fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 256+index, n)
		rels += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, n, n)
		files[fmt.Sprintf("ppt/slides/slide%d.xml", n)] = `<?xml version="1.0" encoding="UTF-8"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr><p:sp><p:nvSpPr><p:cNvPr id="2" name="Visible source text"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="914400" y="914400"/><a:ext cx="10058400" cy="1828800"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="2400"/><a:t>` + text + `</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
	}
	files["[Content_Types].xml"] = types + `</Types>`
	files["ppt/presentation.xml"] = presentation + `</p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`
	files["ppt/_rels/presentation.xml.rels"] = rels + `</Relationships>`
	return functionalZip(t, files)
}
