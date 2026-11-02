// Package codemodel builds a deterministic architecture model of a Go
// repository from its import graph, using only go/parser.
package codemodel

import (
	"bufio"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Location is a position in a source file, relative to the repo root.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

func (l Location) String() string { return fmt.Sprintf("%s:%d", l.File, l.Line) }

// Component is a top-level unit: cmd/x, internal/x, pkg/x or ext:<name>.
type Component struct {
	ID       string   `json:"id"`
	External bool     `json:"external,omitempty"`
	Packages []string `json:"packages,omitempty"` // repo-relative package dirs rolled up into this component
	Anchor   Location `json:"anchor"`             // first file (package clause) or first import site for ext
}

// Edge is a dependency between two components with every import site.
type Edge struct {
	From     string     `json:"from"`
	To       string     `json:"to"`
	Evidence []Location `json:"evidence"`
}

// Model is the code-derived architecture model.
type Model struct {
	Module     string      `json:"module"`
	Components []Component `json:"components"`
	Edges      []Edge      `json:"edges"`
}

// externalCatalog maps import-path prefixes to external component IDs.
var externalCatalog = []struct{ prefix, id string }{
	{"github.com/jackc/pgx", "ext:postgres"},
	{"github.com/lib/pq", "ext:postgres"},
	{"github.com/redis/go-redis", "ext:redis"},
	{"github.com/go-redis/redis", "ext:redis"},
	{"go.mongodb.org/mongo-driver", "ext:mongodb"},
	{"github.com/segmentio/kafka-go", "ext:kafka"},
	{"github.com/IBM/sarama", "ext:kafka"},
	{"github.com/Shopify/sarama", "ext:kafka"},
	{"google.golang.org/grpc", "ext:grpc"},
}

// ExternalID returns the catalog ID for an import path, or "".
func ExternalID(importPath string) string {
	for _, e := range externalCatalog {
		if importPath == e.prefix || strings.HasPrefix(importPath, e.prefix+"/") {
			return e.id
		}
	}
	return ""
}

// ComponentOf maps a repo-relative package dir (slash separated) to its
// component ID, rolling subpackages up. Returns "" for dirs outside cmd/,
// internal/ and pkg/.
func ComponentOf(dir string) string {
	parts := strings.Split(dir, "/")
	if len(parts) < 2 {
		return ""
	}
	switch parts[0] {
	case "cmd", "internal", "pkg":
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// ReadModulePath extracts the module path from <repo>/go.mod.
func ReadModulePath(repo string) (string, error) {
	f, err := os.Open(filepath.Join(repo, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok {
			rest = strings.TrimSpace(rest)
			if uq, err := strconv.Unquote(rest); err == nil {
				rest = uq
			}
			if rest != "" {
				return rest, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("go.mod has no module directive")
}

// Scan walks repo and builds the model. It skips _test.go files and any
// vendor/, testdata/ or hidden directory below the root.
func Scan(repo string) (*Model, error) {
	module, err := ReadModulePath(repo)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	comps := map[string]*Component{}
	pkgSeen := map[string]map[string]bool{}
	edges := map[[2]string]map[Location]bool{}

	addComp := func(id string, ext bool, anchor Location) *Component {
		c, ok := comps[id]
		if !ok {
			c = &Component{ID: id, External: ext, Anchor: anchor}
			comps[id] = c
			pkgSeen[id] = map[string]bool{}
		} else if less(anchor, c.Anchor) {
			c.Anchor = anchor
		}
		return c
	}

	err = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != repo {
				name := d.Name()
				if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dir := path.Dir(rel)
		from := ComponentOf(dir)
		if from == "" {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		c := addComp(from, false, Location{File: rel, Line: fset.Position(f.Package).Line})
		if !pkgSeen[from][dir] {
			pkgSeen[from][dir] = true
			c.Packages = append(c.Packages, dir)
		}
		for _, imp := range f.Imports {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			loc := Location{File: rel, Line: fset.Position(imp.Pos()).Line}
			var to string
			if ip == module || strings.HasPrefix(ip, module+"/") {
				to = ComponentOf(strings.TrimPrefix(strings.TrimPrefix(ip, module), "/"))
			} else if id := ExternalID(ip); id != "" {
				to = id
				addComp(id, true, loc)
			}
			if to == "" || to == from {
				continue
			}
			k := [2]string{from, to}
			if edges[k] == nil {
				edges[k] = map[Location]bool{}
			}
			edges[k][loc] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	m := &Model{Module: module}
	for _, c := range comps {
		sort.Strings(c.Packages)
		m.Components = append(m.Components, *c)
	}
	sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].ID < m.Components[j].ID })
	for k, locs := range edges {
		// An internal import of a dir with no scanned Go files (e.g. only tests)
		// still counts; make sure the target exists as a component.
		if _, ok := comps[k[1]]; !ok {
			continue
		}
		e := Edge{From: k[0], To: k[1]}
		for l := range locs {
			e.Evidence = append(e.Evidence, l)
		}
		sort.Slice(e.Evidence, func(i, j int) bool { return less(e.Evidence[i], e.Evidence[j]) })
		m.Edges = append(m.Edges, e)
	}
	sort.Slice(m.Edges, func(i, j int) bool {
		if m.Edges[i].From != m.Edges[j].From {
			return m.Edges[i].From < m.Edges[j].From
		}
		return m.Edges[i].To < m.Edges[j].To
	})
	return m, nil
}

func less(a, b Location) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	return a.Line < b.Line
}

// IDs returns all component IDs in sorted order.
func (m *Model) IDs() []string {
	ids := make([]string, len(m.Components))
	for i, c := range m.Components {
		ids[i] = c.ID
	}
	return ids
}

// Component looks up a component by ID.
func (m *Model) Component(id string) (Component, bool) {
	for _, c := range m.Components {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}

// Edge looks up an edge.
func (m *Model) Edge(from, to string) (Edge, bool) {
	for _, e := range m.Edges {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return Edge{}, false
}
