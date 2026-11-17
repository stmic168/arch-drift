package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/stmic168/arch-drift/internal/codemodel"
)

// proposeServices finds components that run as separate services inside one
// binary, from two code signals (no LLM):
//
//  1. A file per service in a deployable: cmd/thanos/rule.go imports
//     pkg/rules and has its name (singular or plural). At least two such
//     files are needed: one binary with one mode is not a multi-service
//     binary.
//  2. A module list: RegisterModule(Name, t.initName, ...) calls (the dskit
//     module manager used by Cortex and Loki). Modules marked
//     UserInvisibleModule are helpers, not targets. A module is the component
//     with its whole name (store-gateway -> pkg/storegateway), else the
//     component its init function uses whose name contains one of its words
//     (query-frontend -> pkg/lokifrontend).
//
// The result is sorted component IDs.
func proposeServices(repo string, m *codemodel.Model, opts codemodel.Options) []string {
	module, err := codemodel.ReadModulePath(repo)
	if err != nil {
		return nil
	}
	owner := map[string]string{} // package dir -> component ID
	var internal []codemodel.Component
	for _, c := range m.Components {
		if c.External {
			continue
		}
		internal = append(internal, c)
		for _, p := range c.Packages {
			owner[p] = c.ID
		}
	}
	ownerOf := func(dir string) string { return owner[dir] }
	compOfImport := func(imp string) string {
		if imp != module && !strings.HasPrefix(imp, module+"/") {
			return ""
		}
		return owner[strings.TrimPrefix(strings.TrimPrefix(imp, module), "/")]
	}
	found := map[string]bool{}
	for _, id := range fileServices(repo, internal, compOfImport) {
		found[id] = true
	}
	for _, id := range moduleServices(repo, internal, compOfImport, ownerOf, opts) {
		found[id] = true
	}
	var out []string
	for id := range found {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// lastName is the normalized last path segment of a component ID.
func lastName(id string) string { return codemodel.NormalizeName(path.Base(id)) }

func sameName(a, b string) bool {
	return a != "" && (a == b || a+"s" == b || a == b+"s")
}

func fileServices(repo string, comps []codemodel.Component, compOfImport func(string) string) []string {
	var out []string
	for _, c := range comps {
		if !c.Deployable || len(c.Packages) == 0 {
			continue
		}
		root := c.Packages[0]
		for _, p := range c.Packages {
			if len(p) < len(root) {
				root = p
			}
		}
		entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(root)))
		if err != nil {
			continue
		}
		var hits []string
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			name := codemodel.NormalizeName(strings.TrimSuffix(n, ".go"))
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repo, filepath.FromSlash(root), n), nil, parser.ImportsOnly)
			if err != nil {
				continue
			}
			for _, imp := range f.Imports {
				ip, _ := strconv.Unquote(imp.Path.Value)
				if t := compOfImport(ip); t != "" && t != c.ID && sameName(name, lastName(t)) {
					hits = append(hits, t)
					break
				}
			}
		}
		if len(hits) >= 2 {
			out = append(out, hits...)
		}
	}
	return out
}

// regModule is one RegisterModule call.
type regModule struct {
	name string   // module name, e.g. "query-frontend"
	refs []string // components the init function uses
}

func moduleServices(repo string, comps []codemodel.Component, compOfImport, ownerOf func(string) string, opts codemodel.Options) []string {
	byName := map[string][]string{} // normalized last segment -> component IDs
	for _, c := range comps {
		byName[lastName(c.ID)] = append(byName[lastName(c.ID)], c.ID)
	}
	var out []string
	for _, mod := range registeredModules(repo, compOfImport, ownerOf, opts) {
		if mod.name == "all" {
			continue
		}
		if ids := byName[codemodel.NormalizeName(mod.name)]; len(ids) == 1 {
			out = append(out, ids[0])
			continue
		}
		words := strings.FieldsFunc(mod.name, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
		var match []string
		for _, ref := range mod.refs {
			ln := lastName(ref)
			for _, w := range words {
				if w = codemodel.NormalizeName(w); len(w) >= 3 && strings.Contains(ln, w) {
					match = append(match, ref)
					break
				}
			}
		}
		if len(match) == 1 {
			out = append(out, match[0])
		}
	}
	return out
}

// registeredModules parses the packages that call RegisterModule and returns
// the modules a user can run as a target.
func registeredModules(repo string, compOfImport, ownerOf func(string) string, opts codemodel.Options) []regModule {
	dirs := map[string]bool{}
	filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if p != repo && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || excludedDir(rel, opts.Exclude)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte("RegisterModule(")) {
			dirs[path.Dir(rel)] = true
		}
		return nil
	})
	var out []regModule
	var sorted []string
	for d := range dirs {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	for _, dir := range sorted {
		out = append(out, modulesInPackage(repo, dir, ownerOf(dir), compOfImport)...)
	}
	return out
}

func excludedDir(rel string, exclude []string) bool {
	for _, e := range exclude {
		e = strings.Trim(e, "/")
		if rel == e || strings.HasPrefix(rel, e+"/") {
			return true
		}
	}
	return false
}

func modulesInPackage(repo, dir, self string, compOfImport func(string) string) []regModule {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	consts := map[string]string{}                    // const name -> string value
	methods := map[string]*ast.FuncDecl{}            // func or method name -> declaration
	imports := map[*ast.FuncDecl]map[string]string{} // alias -> import path, per declaring file
	type call struct {
		name, init string
		invisible  bool
	}
	var calls []call
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(repo, filepath.FromSlash(dir), n), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		aliases := map[string]string{}
		for _, imp := range f.Imports {
			ip, _ := strconv.Unquote(imp.Path.Value)
			alias := path.Base(ip)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = ip
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					for i, id := range vs.Names {
						if i < len(vs.Values) {
							if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
								consts[id.Name], _ = strconv.Unquote(bl.Value)
							}
						}
					}
				}
			case *ast.FuncDecl:
				methods[d.Name.Name] = d
				imports[d] = aliases
			}
		}
		ast.Inspect(f, func(node ast.Node) bool {
			ce, ok := node.(*ast.CallExpr)
			if !ok || len(ce.Args) < 2 {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RegisterModule" {
				return true
			}
			c := call{name: identName(ce.Args[0]), init: identName(ce.Args[1])}
			for _, a := range ce.Args[2:] {
				if identName(a) == "UserInvisibleModule" {
					c.invisible = true
				}
			}
			calls = append(calls, c)
			return true
		})
	}
	var out []regModule
	for _, c := range calls {
		if c.invisible {
			continue
		}
		name, ok := consts[c.name]
		if !ok {
			continue
		}
		mod := regModule{name: name}
		if fd := methods[c.init]; fd != nil && fd.Body != nil {
			seen := map[string]bool{}
			aliases := imports[fd]
			ast.Inspect(fd.Body, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				x, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if ip, ok := aliases[x.Name]; ok {
					if comp := compOfImport(ip); comp != "" && comp != self && !seen[comp] {
						seen[comp] = true
						mod.refs = append(mod.refs, comp)
					}
				}
				return true
			})
		}
		out = append(out, mod)
	}
	return out
}

// identName returns the last identifier of x (Name, pkg.Name or t.Name).
func identName(x ast.Expr) string {
	switch v := x.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}
