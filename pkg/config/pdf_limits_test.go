package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pdfLimitsTable(t *testing.T, text string) PDFLimitsConfig {
	t.Helper()
	var table PDFLimitsConfig
	require.NoError(t, json.Unmarshal([]byte(text), &table))
	return table
}

func pdfLimitsGetters() []func(PDFLimits) (int64, bool) {
	return []func(PDFLimits) (int64, bool){PDFLimits.MaxFileBytes, PDFLimits.MaxTotalFileBytes, PDFLimits.MaxRequestBytes, PDFLimits.MaxPagesPerFile, PDFLimits.MaxPagesPerRequest, PDFLimits.MaxDocumentsPerMessage, PDFLimits.MaxDocumentsPerRequest}
}

// R2/R3: each dimension has exactly the same positive/zero/absent cascade.
// Values here are artificial, distinct layer markers, not shipped quantities.
func TestPDFLimitsConfig_Cascade(t *testing.T) {
	for i, field := range pdfLimitIntegerNames() {
		for _, tc := range []struct {
			name    string
			d, p, m any
			want    int64
			stated  bool
		}{
			{"model wins", 300, 200, 100, 100, true},
			{"provider wins", 300, 200, nil, 200, true},
			{"default inherited", 300, nil, nil, 300, true},
			{"model zero", 300, 200, 0, 0, true},
			{"provider zero", 300, 0, nil, 0, true},
			{"default zero", 0, nil, nil, 0, true},
			{"one", 0, nil, 1, 1, true},
			{"absent", nil, nil, nil, 0, false},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				row := func(value any) string {
					if value == nil {
						return ""
					}
					return fmt.Sprintf(`%q:%v`, field, value)
				}
				override := func(model string, value any) string {
					entry := fmt.Sprintf(`"provider":"p","model":%q`, model)
					if f := row(value); f != "" {
						entry += "," + f
					}
					return "{" + entry + "}"
				}
				table := pdfLimitsTable(t, `{"default":{`+row(tc.d)+`},"overrides":[`+override("", tc.p)+`,`+override("m", tc.m)+`]}`)
				limits := resolvePDFLimits(PDFLimitsConfig{}, table, "p", "m")
				got, stated := pdfLimitsGetters()[i](limits)
				assert.Equal(t, tc.want, got)
				assert.Equal(t, tc.stated, stated)
			})
		}
	}
	for _, tc := range []struct {
		name    string
		d, p, m any
		want    bool
		stated  bool
	}{
		{"model true", false, false, true, true, true},
		{"model false", true, true, false, false, true},
		{"provider true", false, true, nil, true, true},
		{"provider false", true, false, nil, false, true},
		{"default true", true, nil, nil, true, true},
		{"default false", false, nil, nil, false, true},
		{"absent", nil, nil, nil, false, false},
	} {
		t.Run("inline_pdf/"+tc.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{
				"default":   map[string]any{"inline_pdf": tc.d},
				"overrides": []map[string]any{{"provider": "p", "inline_pdf": tc.p}, {"provider": "p", "model": "m", "inline_pdf": tc.m}},
			})
			require.NoError(t, err)
			table := pdfLimitsTable(t, string(data))
			limits := resolvePDFLimits(PDFLimitsConfig{}, table, "p", "m")
			got, stated := limits.InlinePDF()
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.stated, stated)
		})
	}
	var zero PDFLimits
	assert.True(t, zero.Empty())
	for _, get := range pdfLimitsGetters() {
		got, stated := get(zero)
		assert.Equal(t, int64(0), got)
		assert.False(t, stated)
	}
}

func TestPDFLimitsConfig_PatternPrecedence(t *testing.T) {
	// R4: exact, longest prefix, provider, default; missing fields continue the
	// per-field cascade, including through less-specific matching patterns.
	table := pdfLimitsTable(t, `{
	  "default":{"max_file_bytes":900,"max_request_bytes":901},
	  "overrides":[
	    {"provider":"p","max_file_bytes":800,"max_pages_per_request":8},
	    {"provider":"p","model":"anthropic/*","max_file_bytes":700,"max_total_file_bytes":701,"inline_pdf":false},
	    {"provider":"p","model":"anthropic/claude-*","max_file_bytes":600,"max_pages_per_file":6},
	    {"provider":"p","model":"anthropic/claude-exact","max_file_bytes":0,"inline_pdf":true},
	    {"provider":"other","model":"anthropic/claude-exact","max_file_bytes":1}
	  ]}`)
	for _, reverse := range []bool{false, true} {
		if reverse {
			for i, j := 0, len(table.Overrides)-1; i < j; i, j = i+1, j-1 {
				table.Overrides[i], table.Overrides[j] = table.Overrides[j], table.Overrides[i]
			}
		}
		for _, tc := range []struct {
			model string
			file  int64
		}{
			{"anthropic/claude-exact", 0},
			{"anthropic/claude-new", 600},
			{"anthropic/claude-", 600}, // a prefix may match an empty suffix
			{"anthropic/other", 700},
			{"gemini/model", 800},
			{"Anthropic/claude-exact", 800}, // exact case-sensitive ids
			{"", 800},
		} {
			t.Run(fmt.Sprintf("reverse=%t/%s", reverse, tc.model), func(t *testing.T) {
				limits := resolvePDFLimits(PDFLimitsConfig{}, table, "p", tc.model)
				file, stated := limits.MaxFileBytes()
				assert.Equal(t, tc.file, file)
				assert.True(t, stated)
			})
		}
	}
	limits := resolvePDFLimits(PDFLimitsConfig{}, table, "p", "anthropic/claude-exact")
	want := []int64{0, 701, 901, 6, 8, 0, 0}
	states := []bool{true, true, true, true, true, false, false}
	for i, get := range pdfLimitsGetters() {
		got, stated := get(limits)
		assert.Equal(t, want[i], got, pdfLimitIntegerNames()[i])
		assert.Equal(t, states[i], stated, pdfLimitIntegerNames()[i])
	}
	inline, stated := limits.InlinePDF()
	assert.True(t, inline)
	assert.True(t, stated)
	limits = resolvePDFLimits(PDFLimitsConfig{}, table, "p", "anthropic/other")
	inline, stated = limits.InlinePDF()
	assert.False(t, inline)
	assert.True(t, stated)
	limits = resolvePDFLimits(PDFLimitsConfig{}, table, "unknown", "anthropic/claude-exact")
	file, stated := limits.MaxFileBytes()
	assert.Equal(t, int64(900), file)
	assert.True(t, stated)
}

func TestPDFLimitsConfig_OperatorMergeAndNeutralization(t *testing.T) {
	cfg := &Config{PDFLimits: ptrPDFLimitsTable(t, `{
	  "default":{"max_file_bytes":123,"max_documents_per_request":0},
	  "overrides":[
	    {"provider":"amazon-bedrock","max_file_bytes":0},
	    {"provider":"openrouter","model":"anthropic/*","max_request_bytes":0},
	    {"provider":"xai","inline_pdf":true},
	    {"provider":"azure","model":"claude-*","max_file_bytes":0,"max_total_file_bytes":0,"max_request_bytes":0}
	  ]}`)}
	limits := cfg.ResolvePDFLimits("unknown", "m")
	file, stated := limits.MaxFileBytes()
	assert.Equal(t, int64(123), file)
	assert.True(t, stated)
	docs, stated := limits.MaxDocumentsPerRequest()
	assert.Equal(t, int64(0), docs)
	assert.True(t, stated)
	limits = cfg.ResolvePDFLimits("amazon-bedrock", "anthropic.claude-opus-5")
	file, _ = limits.MaxFileBytes()
	assert.Equal(t, int64(0), file, "operator provider row replaces the same shipped field")
	request, _ := limits.MaxRequestBytes()
	assert.Equal(t, int64(20000000), request, "absent operator field keeps the shipped provider field")
	pages, _ := limits.MaxPagesPerRequest()
	assert.Equal(t, int64(100), pages, "shipped model row still wins over provider/default")
	limits = cfg.ResolvePDFLimits("openrouter", "anthropic/claude-opus-5")
	request, stated = limits.MaxRequestBytes()
	assert.Equal(t, int64(0), request)
	assert.True(t, stated)
	pages, _ = limits.MaxPagesPerRequest()
	assert.Equal(t, int64(100), pages, "partial operator pattern retains other shipped fields")
	limits = cfg.ResolvePDFLimits("xai", "grok")
	inline, stated := limits.InlinePDF()
	assert.True(t, inline)
	assert.True(t, stated)
	limits = cfg.ResolvePDFLimits("azure", "claude-opus-5")
	for _, get := range []func(PDFLimits) (int64, bool){PDFLimits.MaxFileBytes, PDFLimits.MaxTotalFileBytes, PDFLimits.MaxRequestBytes} {
		value, set := get(limits)
		assert.Equal(t, int64(0), value)
		assert.True(t, set)
	}
	pages, _ = limits.MaxPagesPerRequest()
	assert.Equal(t, int64(100), pages, "neutralizing a model row does not erase the provider's independent page ceiling")
	cfg.PDFLimits = nil
	limits = cfg.ResolvePDFLimits("xai", "grok")
	inline, stated = limits.InlinePDF()
	assert.False(t, inline, "deleting the operator row restores shipped behavior")
	assert.True(t, stated)
	limits = cfg.ResolvePDFLimits("azure", "claude-opus-5")
	file, _ = limits.MaxFileBytes()
	assert.Equal(t, int64(20000000), file, "operator resolution never mutates the embedded table")
}

func ptrPDFLimitsTable(t *testing.T, text string) *PDFLimitsConfig {
	t.Helper()
	table := pdfLimitsTable(t, text)
	return &table
}

func TestPDFLimitsConfig_LoadSaveClone(t *testing.T) {
	text := `{"version":1,"future_section":{"keep":true},"pdf_limits":{
	  "default":{"max_file_bytes":0,"inline_pdf":false},
	  "overrides":[
	    {"provider":"p","model":"a/*","max_request_bytes":9223372036854775807,"inline_pdf":true},
	    {"provider":"q","max_pages_per_file":1}
	  ]}}`
	cfg, err := loadConfig([]byte(text))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, SaveConfig(path, cfg))
	loaded, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, cfg.PDFLimits, loaded.PDFLimits)
	assert.JSONEq(t, `{"keep":true}`, string(loaded.UnknownFields["future_section"]))
	cloned, err := loaded.Clone()
	require.NoError(t, err)
	require.Equal(t, loaded.PDFLimits, cloned.PDFLimits)
	*cloned.PDFLimits.Default.MaxFileBytes = 42
	assert.Equal(t, int64(0), *loaded.PDFLimits.Default.MaxFileBytes, "clone must not alias field pointers")
	*cloned.PDFLimits.Overrides[0].InlinePDF = false
	assert.True(t, *loaded.PDFLimits.Overrides[0].InlinePDF)
	assert.Nil(t, loaded.PDFLimits.Default.MaxPagesPerRequest, "absent stays absent on disk")

	fresh := DefaultConfig()
	assert.Nil(t, fresh.PDFLimits, "shipped defaults are not materialized as frozen operator overrides")
	assert.Equal(t, (*Config)(nil).ResolvePDFLimits("p", "m"), fresh.ResolvePDFLimits("p", "m"))
	for _, section := range []string{`{}`, `{"default":null}`, `{"overrides":[]}`, `{"overrides":null}`} {
		table := pdfLimitsTable(t, section)
		assert.Equal(t, fresh.ResolvePDFLimits("p", "m"), (&Config{PDFLimits: &table}).ResolvePDFLimits("p", "m"))
	}
}
