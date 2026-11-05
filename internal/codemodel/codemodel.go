// Package codemodel builds a deterministic architecture model of a Go
// repository from its import graph, using only go/parser.
package codemodel

import (
	"bufio"
	"errors"
	"fmt"
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
	// ProtocolEdges are imports of another component's shared protocol
	// package (e.g. its generated gRPC client). They prove a required
	// relation but are not reported as undocumented dependencies.
	ProtocolEdges []Edge   `json:"protocol_edges"`
	Coverage      Coverage `json:"coverage"`
	// Names indexes the names in each modeled package (directory segments,
	// file names, exported type names), to suggest code for a doc component
	// the doc model left unmapped. Not written to reports.
	Names []PackageNames `json:"-"`
}

// PackageNames are the names found in one package, normalized to lower-case
// letters and digits.
type PackageNames struct {
	Dir       string
	Component string
	Anchor    Location // first file of the package
	Segments  []string // directory segments
	Files     []string // file names without .go
	Types     []string // exported type names
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
	// NoSharedProtocol disables the default rules that treat generated
	// protobuf packages as shared: a directory name ending in "pb" (e.g.
	// storepb), or a directory holding *.pb.go files below a component root
	// (e.g. api/matchingservice/v1).
	NoSharedProtocol bool

	generated map[string]bool // dirs holding *.pb.go files, filled by Scan
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
	if o.generated[dir] {
		// Generated code below a component root is that component's protocol
		// package; generated code at the root is the component itself.
		if _, root := o.owner(dir); root != dir {
			return true
		}
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
// configured path, or cmd/x for the default layout). Shared dirs belong to
// no component.
func (o Options) component(dir string) (id, root string) {
	if o.IsShared(dir) {
		return "", ""
	}
	return o.owner(dir)
}

// owner is the component a directory lies in, ignoring the shared rules.
func (o Options) owner(dir string) (id, root string) {
	if o.excluded(dir) {
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
	opts.generated = generatedDirs(repo, opts)
	fset := token.NewFileSet()
	comps := map[string]*Component{}
	pkgSeen := map[string]map[string]bool{}
	names := map[string]*PackageNames{}
	edges := map[[2]string]map[Location]bool{}
	protoEdges := map[[2]string]map[Location]bool{}
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
		// A full parse gives the type names for PackagesNamed; it can fail
		// on syntax newer than this toolchain inside function bodies, so
		// imports come from an imports-only parse in that case, as before,
		// and the names from whatever the full parse got through.
		full, ferr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		f := full
		if ferr != nil {
			var err error
			if f, err = parser.ParseFile(fset, p, nil, parser.ImportsOnly); err != nil {
				return fmt.Errorf("parse %s: %w", rel, err)
			}
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
		pn := names[dir]
		if pn == nil {
			pn = &PackageNames{Dir: dir, Component: from, Anchor: Location{File: rel, Line: fset.Position(f.Package).Line}}
			for _, s := range strings.Split(dir, "/") {
				pn.Segments = append(pn.Segments, NormalizeName(s))
			}
			names[dir] = pn
		}
		pn.Files = append(pn.Files, NormalizeName(strings.TrimSuffix(name, ".go")))
		var decls []ast.Decl
		if full != nil {
			decls = full.Decls
		}
		for _, d := range decls {
			if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
				for _, s := range g.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.IsExported() {
						pn.Types = append(pn.Types, NormalizeName(ts.Name.Name))
					}
				}
			}
		}
		for _, imp := range f.Imports {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			loc := Location{File: rel, Line: fset.Position(imp.Pos()).Line}
			var to string
			if ip == module || strings.HasPrefix(ip, module+"/") {
				target := strings.TrimPrefix(strings.TrimPrefix(ip, module), "/")
				to = opts.componentOf(target)
				if to == "" && opts.IsShared(target) {
					if owner, _ := opts.owner(target); owner != "" && owner != from {
						k := [2]string{from, owner}
						if protoEdges[k] == nil {
							protoEdges[k] = map[Location]bool{}
						}
						protoEdges[k][loc] = true
					}
				}
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
	for _, pn := range names {
		m.Names = append(m.Names, *pn)
	}
	sort.Slice(m.Names, func(i, j int) bool { return m.Names[i].Dir < m.Names[j].Dir })
	m.Edges = buildEdges(edges, comps)
	m.ProtocolEdges = buildEdges(protoEdges, comps)
	return m, nil
}

// buildEdges turns collected import sites into sorted edges. An import of a
// dir with no scanned Go files (e.g. only tests) still needs its target to
// exist as a component.
func buildEdges(edges map[[2]string]map[Location]bool, comps map[string]*Component) []Edge {
	out := []Edge{}
	for k, locs := range edges {
		if _, ok := comps[k[1]]; !ok {
			continue
		}
		e := Edge{From: k[0], To: k[1]}
		for l := range locs {
			e.Evidence = append(e.Evidence, l)
		}
		sort.Slice(e.Evidence, func(i, j int) bool { return less(e.Evidence[i], e.Evidence[j]) })
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// generatedDirs lists repo-relative dirs holding *.pb.go files, with the
// same skip rules as the scan.
func generatedDirs(repo string, opts Options) map[string]bool {
	dirs := map[string]bool{}
	_ = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
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
		if strings.HasSuffix(d.Name(), ".pb.go") {
			if rel, err := filepath.Rel(repo, filepath.Dir(p)); err == nil {
				dirs[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	return dirs
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

// ProtocolEdge looks up a protocol edge.
func (m *Model) ProtocolEdge(from, to string) (Edge, bool) {
	for _, e := range m.ProtocolEdges {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return Edge{}, false
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

// NormalizeName lower-cases s and keeps only letters and digits, so
// "CRD Client", "crdclient" and "CrdClient" compare equal.
func NormalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// genericWords do not identify a component on their own ("Ingress
// Controller" is found as ingress).
var genericWords = map[string]bool{"controller": true, "component": true, "service": true, "module": true, "layer": true, "system": true, "the": true}

// PackagesNamed returns the packages whose names match a doc component name,
// best match first, at most three:
//  1. a directory or exported type with the whole name ("CRD Client" ->
//     crdclient/, "Tag Watcher" -> type TagWatcher);
//  2. the same with generic words left out ("Ingress Controller" -> ingress/);
//  3. for two or more remaining words, a package whose directory segments
//     and file names contain every word ("Multicluster Secret" ->
//     multicluster/secretcontroller.go).
//
// It is a suggestion for a human: it never maps a component by itself.
func (m *Model) PackagesNamed(name string) []PackageNames {
	var words []string
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '/' }) {
		if n := NormalizeName(w); n != "" && !genericWords[n] {
			words = append(words, n)
		}
	}
	whole, reduced := NormalizeName(name), strings.Join(words, "")
	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	exact := func(key string) []PackageNames {
		var out []PackageNames
		if key == "" {
			return nil
		}
		for _, p := range m.Names {
			if p.Segments[len(p.Segments)-1] == key || has(p.Types, key) {
				out = append(out, p)
			}
		}
		return out
	}
	found := exact(whole)
	if len(found) == 0 && reduced != whole {
		found = exact(reduced)
	}
	if len(found) == 0 && len(words) >= 2 {
		for _, p := range m.Names {
			tokens := append(append([]string{}, p.Segments...), p.Files...)
			all := true
			for _, w := range words {
				in := false
				for _, t := range tokens {
					if strings.Contains(t, w) {
						in = true
						break
					}
				}
				if !in {
					all = false
					break
				}
			}
			if all {
				found = append(found, p)
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if len(found[i].Segments) != len(found[j].Segments) {
			return len(found[i].Segments) < len(found[j].Segments)
		}
		return found[i].Dir < found[j].Dir
	})
	if len(found) > 3 {
		found = found[:3]
	}
	return found
}
