// Package repomap lists files and their top-level symbols, so the model
// knows where things are before it searches.
package repomap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// Build returns one line per file ("path: sym sym"), capped at maxBytes. 0 = off.
func Build(dir string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range files(dir) {
		line := f
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

// files prefers git: it is fast and already skips ignored files.
func files(dir string) []string {
	// Tracked plus new, not-ignored files, so files made this session show up.
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	// Empty output means dir is ignored by a parent repo; walk it instead.
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		return strings.Fields(string(out))
	}
	var list []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() && p != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			list = append(list, rel)
		}
		return nil
	})
	return list
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
