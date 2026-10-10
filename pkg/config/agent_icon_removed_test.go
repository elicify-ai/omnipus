// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Founder decision 2026-10-10: one consistent agent avatar. The legacy
// Phosphor `icon` is removed end to end — contract, generated types, config,
// seeds, tools and REST. These tests are the deletion guard: they fail if any
// of those surfaces regrows an icon field. (An old stored `icon` value is
// simply ignored; there is no migration.)

// structFieldNames returns the Go field names of every struct type named in
// wanted that is declared in the file at path. It fails if a wanted struct is
// missing, so the guard can never pass vacuously.
func structFieldNames(t *testing.T, path string, wanted map[string]bool) map[string][]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string][]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || !wanted[ts.Name.Name] {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		out[ts.Name.Name] = []string{}
		for _, fld := range st.Fields.List {
			for _, nm := range fld.Names {
				out[ts.Name.Name] = append(out[ts.Name.Name], nm.Name)
			}
		}
		return true
	})
	for name := range wanted {
		if _, found := out[name]; !found {
			t.Fatalf("instrument check: struct %s not found in %s — the guard would pass vacuously", name, path)
		}
	}
	return out
}

func TestAgentIcon_RemovedFromGoTypes(t *testing.T) {
	cases := []struct {
		file  string
		types map[string]bool
	}{
		{"config.go", map[string]bool{"AgentConfig": true}},
		{filepath.Join("..", "coreagent", "core.go"), map[string]bool{"CoreAgent": true}},
		{filepath.Join("..", "api", "generated", "openapi_types.gen.go"), map[string]bool{
			"Agent": true, "AgentCreateRequestMain": true, "AgentCreateRequestSubagent": true,
			"AgentCreateRequestSubagent3p": true, "AgentUpdateRequest": true,
		}},
	}
	for _, c := range cases {
		for typ, fields := range structFieldNames(t, c.file, c.types) {
			for _, f := range fields {
				if strings.EqualFold(f, "icon") {
					t.Errorf("%s.%s: the agent icon field must not exist (founder decision 2026-10-10)", typ, f)
				}
			}
		}
	}
}

func TestAgentIcon_RemovedFromContract(t *testing.T) {
	dir := filepath.Join("..", "..", "contracts", "components", "schemas")
	for _, name := range []string{"Agent", "AgentCreateRequestMain", "AgentCreateRequestSubagent",
		"AgentCreateRequestSubagent3p", "AgentUpdateRequest"} {
		data, err := os.ReadFile(filepath.Join(dir, name+".yaml"))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("instrument check: %s.yaml is empty", name)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "  icon:") {
				t.Errorf("%s.yaml:%d declares an `icon` property; the agent icon is removed", name, i+1)
			}
		}
	}
}

// An agent record written before the removal still loads: the stored key is
// ignored and never written back.
func TestAgentIcon_StoredValueIsIgnored(t *testing.T) {
	var ac AgentConfig
	if err := json.Unmarshal([]byte(`{"id":"a1","name":"A","icon":"robot","color":"#22C55E"}`), &ac); err != nil {
		t.Fatalf("an old record carrying icon must still load: %v", err)
	}
	if ac.ID != "a1" || ac.Color != "#22C55E" {
		t.Fatalf("known fields must still load, got %+v", ac)
	}
	out, err := json.Marshal(ac)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "icon") {
		t.Fatalf("re-marshalled agent still carries icon: %s", out)
	}
}
