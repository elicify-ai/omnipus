// Loaded schema filenames follow the vault-relative source_path declared by
// RecordType. Expected paths are fixture literals, not loader output.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestKnowledgeLoadedTypes_SourcePathsAreVaultRelative(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		t.Run(location, func(t *testing.T) {
			f := buildRelativePathVault(t, location == "nested")
			// A different basename and .yml extension must survive: the
			// declaration's actual filename is not reconstructed from its type.
			writeNote(t, f.root, ".omnipus-vault/records/widget-definitions.yml", recordTestWidgetSchema)
			wantPaths := map[string]string{
				"invoice": ".omnipus-vault/records/invoice.yaml",
				"widget":  ".omnipus-vault/records/widget-definitions.yml",
			}

			w := knowledgeGet(t, f.api, "/api/v1/library/"+f.workspaceID+"/knowledge/record-schema")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			out := decodeJSON[gen.RecordSchema](t, w)
			require.NotNil(t, out.Problems, "a successful schema answer has an empty array, not null")
			require.Empty(t, out.Problems, "only the selected vault's valid schemas belong in this answer")
			require.Len(t, out.Types, len(wantPaths), "both valid schema declarations must load")
			types := make([]string, len(out.Types))
			for i, rt := range out.Types {
				types[i] = rt.Type
			}
			require.Equal(t, []string{"invoice", "widget"}, types, "RecordSchema lists all loaded types in name order")

			for _, rt := range out.Types {
				require.NotNilf(t, rt.SourcePath, "loaded record type %q must name its schema file", rt.Type)
				path := *rt.SourcePath
				assert.Equalf(t, wantPaths[rt.Type], path,
					"loaded record type %q source_path must name its schema file relative to its owning vault", rt.Type)
				assert.Falsef(t, filepath.IsAbs(path),
					"loaded record type %q source_path must be vault-relative, got absolute path %q", rt.Type, path)
				for _, root := range []string{f.api.homePath, workDir(f.api, f.workspaceID), f.root} {
					resolved, err := filepath.EvalSymlinks(root)
					require.NoError(t, err, "host-root comparison is fixture setup")
					for _, hostPath := range []string{root, resolved} {
						assert.NotContainsf(t, path, hostPath,
							"loaded record type %q source_path must not contain a host root", rt.Type)
						assert.NotContainsf(t, path, filepath.ToSlash(hostPath),
							"loaded record type %q source_path must not contain a slash-normalized host root", rt.Type)
					}
				}
			}
		})
	}
}
