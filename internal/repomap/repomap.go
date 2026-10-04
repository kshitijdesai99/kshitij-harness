// Package repomap lists files and their top-level symbols, so the model
// knows where things are before it searches.
package repomap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Build returns one line per file ("path: sym sym"), capped at maxBytes. 0 = off.
// Paths containing whitespace, controls, quotes or backslashes are Go-quoted.
func Build(dir string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range files(dir) {
		line := f
		if strings.ContainsFunc(f, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || r == '"' || r == '\'' || r == '\\'
		}) {
			line = strconv.Quote(f)
		}
		if strings.HasSuffix(f, ".go") {
			if syms := goSymbols(filepath.Join(dir, f)); syms != "" {
				line += ": " + syms
			}
		}
		if b.Len()+len(line)+1 > maxBytes {
			b.WriteString("...[map cut]\n")
			break
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// files lists a git project's tracked and new, not-ignored files (so files
// made this session show up). Outside git there is no map: in a folder like
// ~/Documents it would be thousands of random paths the model pays to read.
func files(dir string) []string {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	out, _ := cmd.Output() // not a repo, or ignored by a parent repo: empty
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// goSymbols returns top-level types, vars, funcs and methods (as Type.Method).
// Consts are left out: many, and rarely what the model is looking for.
func goSymbols(path string) string {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return ""
	}
	var syms []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = recvType(d.Recv.List[0].Type) + "." + name
			}
			syms = append(syms, name)
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					syms = append(syms, s.Name.Name)
				case *ast.ValueSpec:
					if d.Tok == token.VAR {
						for _, n := range s.Names {
							syms = append(syms, n.Name)
						}
					}
				}
			}
		}
	}
	return strings.Join(syms, " ")
}

func recvType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvType(t.X)
	case *ast.IndexExpr: // generic receiver T[K]
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
