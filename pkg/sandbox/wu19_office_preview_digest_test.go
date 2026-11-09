package sandbox

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/scanner"
	"go/token"
	"sort"
)

const wu19OfficeDigestVersion = "office-preview-env-pins/go-tokens-v1"

// This is a versioned byte format, NOT go/format output or AST rendering.
// Positions/comments and numeric token IDs are absent. Optional semicolons
// before a closing brace/parenthesis or EOF are omitted; other semicolons stay.
func wu19OfficeTokenDigest(source []byte, bindings map[string]string) (string, error) {
	var lexer scanner.Scanner
	var firstError string
	fset := token.NewFileSet()
	lexer.Init(fset.AddFile("closed-env", -1, len(source)), source, func(_ token.Position, message string) {
		if firstError == "" {
			firstError = message
		}
	}, 0)
	type item struct {
		token token.Token
		value string
	}
	var tokens []item
	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind == token.SEMICOLON {
			literal = ";"
		} else if !kind.IsLiteral() {
			literal = ""
		}
		tokens = append(tokens, item{kind, literal})
	}
	if lexer.ErrorCount != 0 {
		return "", fmt.Errorf("invalid Go token stream: %s", firstError)
	}
	var encoded []byte
	put := func(value string) {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(value)))
		encoded = append(encoded, value...)
	}
	put(wu19OfficeDigestVersion)
	put("tokens")
	for i, value := range tokens {
		if value.token == token.SEMICOLON && (i+1 == len(tokens) || tokens[i+1].token == token.RBRACE || tokens[i+1].token == token.RPAREN) {
			continue
		}
		put(value.token.String())
		put(value.value)
	}
	put("imports")
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		put(key)
		put(bindings[key])
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type wu19OfficeDeclaration struct {
	node ast.Node
	kind token.Token
}

func wu19OfficeDeclarations(file *wu19File) (map[string]wu19OfficeDeclaration, error) {
	out := map[string]wu19OfficeDeclaration{}
	add := func(name string, node ast.Node, kind token.Token) error {
		if _, exists := out[name]; exists {
			return fmt.Errorf("duplicate declaration %s", name)
		}
		out[name] = wu19OfficeDeclaration{node, kind}
		return nil
	}
	for _, decl := range file.ast.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			name := node.Name.Name
			if node.Recv != nil {
				name = wu19TypeName(node.Recv.List[0].Type) + "." + name
			}
			if err := add(name, node, token.ILLEGAL); err != nil {
				return nil, err
			}
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch value := spec.(type) {
				case *ast.TypeSpec:
					if err := add(value.Name.Name, value, node.Tok); err != nil {
						return nil, err
					}
				case *ast.ValueSpec:
					for _, name := range value.Names {
						if name.Name != "_" {
							if err := add(name.Name, value, node.Tok); err != nil {
								return nil, err
							}
						}
					}
				}
			}
		}
	}
	return out, nil
}

func wu19OfficeDeclarationDigest(file *wu19File, declaration wu19OfficeDeclaration) (string, error) {
	if file.tokens == nil || len(file.source) == 0 {
		return "", fmt.Errorf("original declaration bytes unavailable")
	}
	start, end := file.tokens.Offset(declaration.node.Pos()), file.tokens.Offset(declaration.node.End())
	if start < 0 || end > len(file.source) || start >= end {
		return "", fmt.Errorf("invalid declaration source offsets")
	}
	source := file.source[start:end]
	if declaration.kind != token.ILLEGAL {
		source = append([]byte(declaration.kind.String()+" "), source...)
	}
	bindings := map[string]string{"@package": file.ast.Name.Name}
	// Dot/blank imports can affect unqualified names or initialization. Other
	// imports matter only when their selector spelling occurs in this declaration.
	for _, special := range []string{".", "_"} {
		if location := file.imports[special]; location != "" {
			bindings[special] = location
		}
	}
	ast.Inspect(declaration.node, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if qualifier, ok := selector.X.(*ast.Ident); ok {
				if location := file.imports[qualifier.Name]; location != "" {
					bindings[qualifier.Name] = location
				}
			}
		}
		return true
	})
	return wu19OfficeTokenDigest(source, bindings)
}
