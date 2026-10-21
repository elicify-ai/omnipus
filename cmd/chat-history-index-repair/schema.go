package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// These are private operator file schemas, not gateway/SPA wire formats.
//
//go:embed *.schema.json
var repairSchemas embed.FS

func validateRepairJSON(name string, b []byte, into any) error {
	if !json.Valid(b) {
		return errors.New("invalid operator JSON")
	}
	// Reject duplicate members as well as unknown members: neither receipt
	// provenance nor a count-verified inventory may have ambiguous values.
	d := json.NewDecoder(bytes.NewReader(b))
	if err := uniqueJSONValue(d); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple operator JSON values")
	}
	var schemaDoc, doc any
	schemaBytes, err := repairSchemas.ReadFile(name)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&doc); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const uri = "https://omnipus.invalid/private/repair.json"
	if err = compiler.AddResource(uri, schemaDoc); err != nil {
		return err
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		return err
	}
	if err = schema.Validate(doc); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(into)
}

func uniqueJSONValue(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate JSON member")
			}
			seen[name] = true
		}
		if err := uniqueJSONValue(d); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func writeRepairJSON(path, schema string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return integrity("io_error", err)
	}
	b = append(b, '\n')
	var check any
	if err = validateRepairJSON(schema, b, &check); err != nil {
		return integrity("evidence_invalid", err)
	}
	if err = fileutil.WriteFileAtomicSyncDir(path, b, 0o600); err != nil {
		return integrity("io_error", err)
	}
	return nil
}
func builtToolCommit() (string, error) {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && validHex(setting.Value, 40) {
				return setting.Value, nil
			}
		}
	}
	return "", integrity("tool_identity_unknown", errors.New("missing VCS revision"))
}
func validHex(s string, size int) bool {
	if len(s) != size || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func digestBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func writeTuple(h hash.Hash, values ...string) {
	for _, s := range values {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(s)))
		h.Write(size[:])
		h.Write([]byte(s))
	}
}
func utcTimestampValid(s string) bool {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return false
	}
	_, offset := t.Zone()
	return offset == 0
}
