// SPDX-License-Identifier: AGPL-3.0-only

package logging

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestEntrypointsBuildLoggerThroughLogging(t *testing.T) {
	files := []string{
		"../cmd/manager/manager.go",
		"../cmd/cell/cell.go",
		"../../cmd/alb-mcp/main.go",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var viaLogging, viaZap bool
			ast.Inspect(parsed, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				switch {
				case pkg.Name == "logging" && sel.Sel.Name == "New":
					viaLogging = true
				case pkg.Name == "zap" && sel.Sel.Name == "New":
					viaZap = true
				}
				return true
			})
			if !viaLogging {
				t.Errorf("%s does not call logging.New", file)
			}
			if viaZap {
				t.Errorf("%s calls zap.New", file)
			}
		})
	}
}
