package gateway

// S113/H46 v2: source loader only. No export-data or declaration stubs; the
// standard source importer resolves real dependency APIs and go/types checks
// every body in each candidate production package. Errors are never partial PASS.
import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const pgWriterModule = "github.com/elicify-ai/omnipus"

type pgWriterPackage struct {
	files []*ast.File
	info  *types.Info
}

type pgWriterSource struct {
	root     string
	fset     *token.FileSet
	importer types.Importer
	packages map[string]*pgWriterPackage
}

func pgNewWriterSource(root string) *pgWriterSource {
	fset := token.NewFileSet()
	return &pgWriterSource{root: root, fset: fset,
		importer: importer.ForCompiler(fset, "source", nil), packages: map[string]*pgWriterPackage{}}
}

func (s *pgWriterSource) check(dir string) (*pgWriterPackage, error) {
	if old := s.packages[dir]; old != nil {
		return old, nil
	}
	bp, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, name := range bp.GoFiles {
		file, parseErr := parser.ParseFile(s.fset, filepath.Join(dir, name), nil, 0)
		if parseErr != nil {
			return nil, parseErr
		}
		files = append(files, file)
	}
	rel, err := filepath.Rel(s.root, dir)
	if err != nil {
		return nil, err
	}
	return s.checkFiles(pgWriterModule+"/"+filepath.ToSlash(rel), files, dir)
}

func (s *pgWriterSource) checkFiles(path string, files []*ast.File, key string) (*pgWriterPackage, error) {
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	cfg := types.Config{Importer: s.importer, Sizes: types.SizesFor("gc", build.Default.GOARCH)}
	if _, err := cfg.Check(path, s.fset, files, info); err != nil {
		return nil, fmt.Errorf("source type-check %s: %w", path, err)
	}
	pkg := &pgWriterPackage{files: files, info: info}
	s.packages[key] = pkg
	return pkg, nil
}

// Identifier scanning is only a conservative package selector. It never decides
// that a reference is a writer: go/types object/receiver identity does that.
func pgWriterCandidateFiles(root string, names map[string]bool) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			// Go's excluded fixture/hidden directories and the repository's
			// explicitly test-only source tree are not production packages.
			if strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_") || entry.Name() == "testdata" || path == filepath.Join(root, "tests") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir // different module, not this module's source
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		set := token.NewFileSet()
		var scan scanner.Scanner
		var scanErr error
		scan.Init(set.AddFile(path, -1, len(data)), data, func(pos token.Position, msg string) { scanErr = fmt.Errorf("%s: %s", pos, msg) }, 0)
		for {
			_, tok, literal := scan.Scan()
			if tok == token.EOF {
				break
			}
			if tok == token.IDENT && names[literal] {
				result = append(result, path)
				break
			}
		}
		return scanErr
	})
	sort.Strings(result)
	return result, err
}

func pgWriterSymbol(fn *types.Func) string {
	fn = fn.Origin()
	if fn.Pkg() == nil {
		return "builtin." + fn.Name() // e.g. the universe error interface's Error method
	}
	name := fn.Pkg().Path() + "."
	sig, ok := fn.Type().(*types.Signature)
	if ok && sig.Recv() != nil {
		recv := types.Unalias(sig.Recv().Type())
		if ptr, ok := recv.(*types.Pointer); ok {
			recv = types.Unalias(ptr.Elem())
		}
		if named, ok := recv.(*types.Named); ok {
			name += named.Obj().Name() + "."
		}
	}
	return name + fn.Name()
}
