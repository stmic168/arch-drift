package main

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/stmic168/arch-drift/internal/codemodel"
)

// A bucket component (<top>/pkg) is split into its children when the doc
// names code in at least minNamedChildren of them and in at least
// 1/minNamedShare of them: a doc on istiod's internals names 6 of
// pilot/pkg's 20 children, while its passing mentions of istioctl code name
// 4 of istioctl/pkg's 30.
const (
	minNamedChildren = 3
	minNamedShare    = 4
)

// bucketChildren returns the child directories of root that hold packages
// of the component, and whether root itself holds one.
func bucketChildren(comp codemodel.Component, root string) (map[string]bool, bool) {
	children := map[string]bool{}
	rootPkg := false
	for _, p := range comp.Packages {
		if p == root {
			rootPkg = true
		} else if strings.HasPrefix(p, root+"/") {
			children[strings.SplitN(strings.TrimPrefix(p, root+"/"), "/", 2)[0]] = true
		}
	}
	return children, rootPkg
}

var (
	docHeading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*#*\s*$`)
	docCode    = regexp.MustCompile("`([^`\n]+)`")
	docBold    = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
)

// docNames returns the names a doc gives prominence to: headings, code spans
// and bold text. Plain text, no LLM.
func docNames(doc string) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{docHeading, docCode, docBold} {
		for _, m := range re.FindAllStringSubmatch(doc, -1) {
			n := strings.TrimSpace(m[1])
			if n == "" || len(n) > 40 || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// isBucket reports whether a component path is a layout directory such as
// pilot/pkg: a sub-project's package bucket, not a unit of its own.
func isBucket(p string) bool {
	b := path.Base(p)
	return strings.Contains(p, "/") && (b == "pkg" || b == "internal")
}

// splitBuckets splits each bucket component whose children the doc names
// (code with a doc name, found as in v0.3.0's suggestions) into one component
// per child: pilot/pkg/xds becomes pilot/xds. It returns the new plan and,
// per split bucket, the children the doc named.
func splitBuckets(plan initPlan, m *codemodel.Model, names []string) (initPlan, map[string][]string) {
	buckets := map[string]string{} // component ID -> path
	for _, c := range plan.Components {
		if len(c.Paths) == 1 && isBucket(c.Paths[0]) {
			buckets[c.ID] = c.Paths[0]
		}
	}
	if len(buckets) == 0 {
		return plan, nil
	}
	named := map[string]map[string]bool{} // bucket ID -> named children
	for _, n := range names {
		for _, p := range m.PackagesNamed(n) {
			root, ok := buckets[p.Component]
			if !ok || !strings.HasPrefix(p.Dir, root+"/") {
				continue
			}
			child := strings.SplitN(strings.TrimPrefix(p.Dir, root+"/"), "/", 2)[0]
			if named[p.Component] == nil {
				named[p.Component] = map[string]bool{}
			}
			named[p.Component][child] = true
		}
	}
	split := map[string][]string{}
	for id, kids := range named {
		comp, _ := m.Component(id)
		all, _ := bucketChildren(comp, buckets[id])
		if len(kids) < minNamedChildren || len(kids)*minNamedShare < len(all) {
			continue
		}
		for k := range kids {
			split[id] = append(split[id], k)
		}
		sort.Strings(split[id])
	}
	if len(split) == 0 {
		return plan, nil
	}
	taken := map[string]bool{}
	for _, c := range plan.Components {
		taken[c.ID] = true
	}
	var out initPlan
	out.Exclude = plan.Exclude
	for _, c := range plan.Components {
		if _, ok := split[c.ID]; !ok {
			out.Components = append(out.Components, c)
			continue
		}
		root := buckets[c.ID]
		comp, _ := m.Component(c.ID)
		children, rootPkg := bucketChildren(comp, root)
		if rootPkg {
			out.Components = append(out.Components, c)
		}
		var kids []string
		for k := range children {
			kids = append(kids, k)
		}
		sort.Strings(kids)
		for _, k := range kids {
			if initExcludeNames[k] || isTestOrMockDir(k) {
				out.Exclude = append(out.Exclude, path.Join(root, k))
				continue
			}
			id := path.Join(path.Dir(root), k)
			if taken[id] {
				id = path.Join(root, k)
			}
			taken[id] = true
			out.Components = append(out.Components, initComponent{ID: id, Paths: []string{path.Join(root, k)}})
		}
	}
	sort.Slice(out.Components, func(i, j int) bool { return out.Components[i].ID < out.Components[j].ID })
	sort.Strings(out.Exclude)
	return out, split
}

// planOptions is the scan configuration of a plan, as initConfig writes it.
func planOptions(plan initPlan) codemodel.Options {
	var o codemodel.Options
	for _, c := range plan.Components {
		o.Components = append(o.Components, codemodel.ComponentRule{ID: c.ID, Paths: c.Paths})
	}
	o.Exclude = plan.Exclude
	return o
}
