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

// Component is a top-level unit: cmd/x, internal/x, pkg/x, a configured
// component, or ext:<name>.
type Component struct {
	ID         string   `json:"id"`
	External   bool     `json:"external,omitempty"`
	Deployable bool     `json:"deployable,omitempty"` // a main package sits at the component's root directory
	Packages   []string `json:"packages,omitempty"`   // repo-relative package dirs rolled up into this component
	Anchor     Location `json:"anchor"`               // first file (package clause) or first import site for ext
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
	Coverage   Coverage    `json:"coverage"`
}

// Coverage says how much of the repo the components cover. Excluded
// directories are not counted.
type Coverage struct {
	Packages  int        `json:"packages"`  // Go packages scanned (non-test)
	Modeled   int        `json:"modeled"`   // packages that belong to a component
	Shared    []string   `json:"shared"`    // shared packages: no component, no edges
	Unmodeled []DirCount `json:"unmodeled"` // packages in no component, by top-level directory
}

// DirCount is a package count for a top-level directory.
type DirCount struct {
	Dir      string `json:"dir"`
	Packages int    `json:"packages"`
}

// Percent is the share of packages that belong to a component.
func (c Coverage) Percent() int {
	if c.Packages == 0 {
		return 100
	}
	return c.Modeled * 100 / c.Packages
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
	{"github.com/gocql/gocql", "ext:cassandra"},
	{"github.com/apache/cassandra-gocql-driver", "ext:cassandra"},
	{"github.com/go-sql-driver/mysql", "ext:mysql"},
	{"modernc.org/sqlite", "ext:sqlite"},
	{"github.com/mattn/go-sqlite3", "ext:sqlite"},
	{"github.com/elastic/go-elasticsearch", "ext:elasticsearch"},
	{"github.com/olivere/elastic", "ext:elasticsearch"},
	{"github.com/thanos-io/objstore", "ext:objstore"},
	{"github.com/aws/aws-sdk-go-v2/service/s3", "ext:s3"},
	{"github.com/aws/aws-sdk-go/service/s3", "ext:s3"},
	{"github.com/minio/minio-go", "ext:s3"},
	{"cloud.google.com/go/storage", "ext:gcs"},
	{"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob", "ext:azure-blob"},
	{"github.com/nats-io/nats.go", "ext:nats"},
	{"github.com/rabbitmq/amqp091-go", "ext:rabbitmq"},
	{"github.com/streadway/amqp", "ext:rabbitmq"},
	{"go.etcd.io/etcd/client", "ext:etcd"},
	{"github.com/hashicorp/consul/api", "ext:consul"},
	{"github.com/bradfitz/gomemcache", "ext:memcached"},
}

// ComponentRule maps repo-relative directories to a component ID.
type ComponentRule struct {
	ID    string
	Paths []string // a path matches itself and everything below it
}

// ExternalRule maps import-path prefixes to an external ID (ext:...).
type ExternalRule struct {
	ID       string
	Prefixes []string
}

// Options customize the scan. The zero value is the default layout.
type Options struct {
	Components      []ComponentRule
	External        []ExternalRule
	Exclude         []string // repo-relative directories to skip
	NoDefaultLayout bool     // only configured components; no cmd/internal/pkg rule
	// Shared lists directories of shared code (protocol types, small helpers)
	// that live inside a component directory. They belong to no component and
	// importing them creates no edge.
	Shared []string
	// NoSharedProtocol disables the default rule that treats generated
	// protobuf packages (directory name ending in "pb", e.g. storepb) as shared.
	NoSharedProtocol bool
}

// IsShared reports whether a repo-relative directory holds shared code.
func (o Options) IsShared(dir string) bool {
	for _, p := range o.Shared {
		if underPath(dir, p) {
			return true
		}
	}
	if o.NoSharedProtocol {
		return false
	}
	base := path.Base(dir)
	if len(base) <= 2 || !strings.HasSuffix(base, "pb") {
		return false
	}
	for _, r := range base {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func underPath(dir, p string) bool {
	p = strings.Trim(p, "/")
	return p == "" || dir == p || strings.HasPrefix(dir, p+"/")
}

func matchPrefix(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

// ExternalID returns the built-in catalog ID for an import path, or "".
func ExternalID(importPath string) string {
	for _, e := range externalCatalog {
		if matchPrefix(importPath, e.prefix) {
			return e.id
		}
	}
	return ""
}

// externalID checks configured rules (longest prefix wins) before the catalog.
func (o Options) externalID(importPath string) string {
	best, bestLen := "", -1
	for _, r := range o.External {
		for _, p := range r.Prefixes {
			if matchPrefix(importPath, p) && len(p) > bestLen {
				best, bestLen = r.ID, len(p)
			}
		}
	}
	if best != "" {
		return best
	}
	return ExternalID(importPath)
}

// componentOf applies configured rules (longest path wins), then the default layout.
func (o Options) componentOf(dir string) string {
	id, _ := o.component(dir)
	return id
}

// component also returns the root directory of the matched rule (the
// configured path, or cmd/x for the default layout).
func (o Options) component(dir string) (id, root string) {
	if o.excluded(dir) || o.IsShared(dir) {
		return "", ""
	}
	bestLen := -1
	for _, r := range o.Components {
		for _, p := range r.Paths {
			p = strings.Trim(p, "/")
			if underPath(dir, p) && len(p) > bestLen {
				id, root, bestLen = r.ID, p, len(p)
			}
		}
	}
	if id != "" || o.NoDefaultLayout {
		return id, root
	}
	if id = ComponentOf(dir); id != "" {
		return id, id
	}
	return "", ""
}

func (o Options) excluded(dir string) bool {
	for _, p := range o.Exclude {
		if underPath(dir, p) {
			return true
		}
	}
	return false
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

// Scan walks repo and builds the model. It skips _test.go files, excluded
// directories and any vendor/, testdata/ or hidden directory below the root.
func Scan(repo string, opts Options) (*Model, error) {
	module, err := ReadModulePath(repo)
	if err != nil {
		return nil, err
	}
	// WalkDir does not follow a symlinked root; resolve it so --repo can be a link.
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	fset := token.NewFileSet()
	comps := map[string]*Component{}
	pkgSeen := map[string]map[string]bool{}
	edges := map[[2]string]map[Location]bool{}
	pkgDirs := map[string]bool{}     // every scanned package dir
	modeledDirs := map[string]bool{} // dirs that belong to a component
	sharedDirs := map[string]bool{}

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
				if rel, err := filepath.Rel(repo, p); err == nil && opts.excluded(filepath.ToSlash(rel)) {
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
		pkgDirs[dir] = true
		from, root := opts.component(dir)
		if from == "" {
			if opts.IsShared(dir) {
				sharedDirs[dir] = true
			}
			return nil
		}
		modeledDirs[dir] = true
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		c := addComp(from, false, Location{File: rel, Line: fset.Position(f.Package).Line})
		// Only a main package at the component root makes it a deployable; a
		// helper tool nested deeper (e.g. a replay tester) does not.
		if f.Name.Name == "main" && dir == root {
			c.Deployable = true
		}
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
				to = opts.componentOf(strings.TrimPrefix(strings.TrimPrefix(ip, module), "/"))
			} else if id := opts.externalID(ip); id != "" {
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
	m.Coverage = Coverage{Packages: len(pkgDirs), Modeled: len(modeledDirs), Shared: []string{}, Unmodeled: []DirCount{}}
	byTop := map[string]int{}
	for d := range pkgDirs {
		if modeledDirs[d] {
			continue
		}
		if sharedDirs[d] {
			m.Coverage.Shared = append(m.Coverage.Shared, d)
			continue
		}
		top := strings.SplitN(d, "/", 2)[0]
		if top == "." {
			top = "(repo root)"
		}
		byTop[top]++
	}
	sort.Strings(m.Coverage.Shared)
	for d, n := range byTop {
		m.Coverage.Unmodeled = append(m.Coverage.Unmodeled, DirCount{Dir: d, Packages: n})
	}
	sort.Slice(m.Coverage.Unmodeled, func(i, j int) bool {
		a, b := m.Coverage.Unmodeled[i], m.Coverage.Unmodeled[j]
		if a.Packages != b.Packages {
			return a.Packages > b.Packages
		}
		return a.Dir < b.Dir
	})
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

// IsDeployable reports whether id names a component with a main package.
func (m *Model) IsDeployable(id string) bool {
	c, ok := m.Component(id)
	return ok && c.Deployable
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
