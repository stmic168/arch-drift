package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/config"
)

// Directory names that hold tests, tooling or examples rather than architecture.
var initExcludeNames = map[string]bool{
	"test": true, "tests": true, "e2e": true, "integration": true, "tools": true, "tool": true,
	"scripts": true, "hack": true, "examples": true, "example": true, "docs": true, "build": true,
	"testutil": true, "mocks": true, "mock": true, "benchmark": true, "benchmarks": true,
	"develop": true, "contrib": true,
}

// initPlan is what init proposes for a repository.
type initPlan struct {
	Components []initComponent
	Exclude    []string
}

type initComponent struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
}

// maxSplitChildren: a top-level directory with more child packages than this
// is one component (a library bucket such as common/), not one per child.
const maxSplitChildren = 10

// isTestOrMockDir matches helper directories such as servicemock, chasmtest
// or testing.
func isTestOrMockDir(name string) bool {
	for _, suf := range []string{"mock", "mocks", "test", "testing"} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// planLayout proposes components for top-level directories the default
// cmd/internal/pkg layout does not cover: one component per child directory
// holding Go code (like cmd/x), plus the directory itself when it has Go
// files of its own; a directory with many children is a single component.
// Test, tooling and mock directories are excluded.
func planLayout(repo string) (initPlan, error) {
	pkgs := map[string]bool{}
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != repo && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
			rel, _ := filepath.Rel(repo, filepath.Dir(p))
			pkgs[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		return initPlan{}, err
	}
	children := map[string]map[string]bool{} // top -> children holding packages
	rootPkg := map[string]bool{}             // top has Go files directly
	for dir := range pkgs {
		parts := strings.Split(dir, "/")
		top := parts[0]
		if top == "." || top == "cmd" || top == "internal" || top == "pkg" {
			continue
		}
		if children[top] == nil {
			children[top] = map[string]bool{}
		}
		if len(parts) == 1 {
			rootPkg[top] = true
		} else {
			children[top][parts[1]] = true
		}
	}
	var plan initPlan
	for top, kids := range children {
		if initExcludeNames[top] || isTestOrMockDir(top) {
			plan.Exclude = append(plan.Exclude, top)
			continue
		}
		var keep []string
		for kid := range kids {
			if initExcludeNames[kid] || isTestOrMockDir(kid) {
				plan.Exclude = append(plan.Exclude, path.Join(top, kid))
				continue
			}
			keep = append(keep, kid)
		}
		if rootPkg[top] || len(keep) > maxSplitChildren {
			plan.Components = append(plan.Components, initComponent{ID: top, Paths: []string{top}})
		}
		if len(keep) <= maxSplitChildren {
			for _, kid := range keep {
				p := path.Join(top, kid)
				plan.Components = append(plan.Components, initComponent{ID: p, Paths: []string{p}})
			}
		}
	}
	sort.Slice(plan.Components, func(i, j int) bool { return plan.Components[i].ID < plan.Components[j].ID })
	sort.Strings(plan.Exclude)
	return plan, nil
}

// initConfig renders archdrift.json for the plan and mode.
func initConfig(plan initPlan, mode string) []byte {
	c := map[string]any{}
	if len(plan.Components) > 0 {
		c["components"] = plan.Components
	}
	if len(plan.Exclude) > 0 {
		c["exclude"] = plan.Exclude
	}
	// Both modes list undocumented dependencies without failing: a rules doc
	// or an overview doc does not enumerate every import. Gate mode trusts
	// only protocols the doc names, so a violated rule is not hidden as a
	// runtime call (see the Cortex gate pilot).
	c["undocumented_edges"] = map[string]bool{"fail": false}
	if mode == "gate" {
		c["strict_protocols"] = true
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	// One component per line reads better than the indented form.
	if len(plan.Components) > 0 {
		var lines []string
		for _, comp := range plan.Components {
			l, _ := json.Marshal(comp)
			lines = append(lines, "    "+string(l))
		}
		compact := "\"components\": [\n" + strings.Join(lines, ",\n") + "\n  ]"
		start := strings.Index(string(b), "\"components\": [")
		end := strings.Index(string(b)[start:], "\n  ]") + start + len("\n  ]")
		b = []byte(string(b)[:start] + compact + string(b)[end:])
	}
	return append(b, '\n')
}

func initWorkflow(repoRel, docRel string) []byte {
	return []byte(fmt.Sprintf(`# Generated by archdrift init. Checks pull requests against the design doc;
# only drift a pull request introduces or worsens fails the check.
name: architecture

on:
  pull_request:

permissions:
  contents: read
  security-events: write # upload-sarif: findings on the changed lines

jobs:
  archdrift:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: stmic168/arch-drift@main
        with:
          repo-path: %s
          docs: %s
          upload-sarif: "true"
          # Only needed without a committed lock next to the doc:
          # anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
`, repoRel, docRel))
}

func initRulesDoc(m *codemodel.Model) []byte {
	var b strings.Builder
	b.WriteString(`# ADR: component boundaries

Status: draft

The dependency rules the code must follow. archdrift checks every rule
against the Go imports and fails a pull request that breaks one.

How to write rules archdrift can check:
- Name each component once in **bold** and say what it does.
- Write one rule per sentence, using the component names exactly:
  - "The **A** sends orders to the **B**." (A must depend on B)
  - "The **A** must not depend on the **B**." (a prohibition)
- Name the protocol only when it is a runtime call: "over HTTP", "over gRPC".

## Components

Detected in the code (rename them, describe them, drop the ones that do not matter):

`)
	n := 0
	for _, c := range m.Components {
		if c.External {
			continue
		}
		kind := ""
		if c.Deployable {
			kind = " (deployable)"
		}
		fmt.Fprintf(&b, "- **%s** (`%s`)%s: TODO describe.\n", c.ID, c.ID, kind)
		n++
	}
	if n == 0 {
		b.WriteString("- **Component** (`path`): TODO describe.\n")
	}
	b.WriteString(`
## Rules that must hold

1. TODO: The **A** sends ... to the **B**.

## Prohibited dependencies

2. TODO: The **A** must not depend on the **B**.
`)
	return []byte(b.String())
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("init", flag.ContinueOnError)
	fset.SetOutput(stderr)
	fset.Usage = func() { fmt.Fprint(stderr, usage) }
	var repo, doc, mode string
	var force bool
	fset.StringVar(&repo, "repo", "", "")
	fset.StringVar(&doc, "doc", "", "")
	fset.StringVar(&mode, "mode", "report", "")
	fset.BoolVar(&force, "force", false, "")
	if err := fset.Parse(args); err != nil {
		return exitError
	}
	if repo == "" || doc == "" || fset.NArg() > 0 || (mode != "gate" && mode != "report") {
		fset.Usage()
		return exitError
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	if _, err := codemodel.ReadModulePath(repo); err != nil {
		return fail(err)
	}
	plan, err := planLayout(repo)
	if err != nil {
		return fail(fmt.Errorf("scan repo: %w", err))
	}

	written := []string{}
	write := func(p string, content []byte) error {
		if _, err := os.Stat(p); err == nil && !force {
			fmt.Fprintf(stdout, "kept     %s (exists; --force to overwrite)\n", p)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			return err
		}
		written = append(written, p)
		fmt.Fprintf(stdout, "wrote    %s\n", p)
		return nil
	}

	cfgPath := filepath.Join(repo, config.FileName)
	if err := write(cfgPath, initConfig(plan, mode)); err != nil {
		return fail(err)
	}
	// Scan with the proposed config so the coverage and the rules template
	// reflect it.
	c, err := config.Load(cfgPath)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", cfgPath, err))
	}
	m, err := codemodel.Scan(repo, c.ScanOptions())
	if err != nil {
		return fail(fmt.Errorf("scan repo: %w", err))
	}

	if _, err := os.Stat(doc); errors.Is(err, fs.ErrNotExist) {
		if mode != "gate" {
			return fail(fmt.Errorf("doc %s does not exist (use --mode gate to start a rules doc from a template)", doc))
		}
		if err := write(doc, initRulesDoc(m)); err != nil {
			return fail(err)
		}
	}

	// The workflow lives at the git root; paths in it are relative to that root.
	root := repo
	if top, err := git(repo, "rev-parse", "--show-toplevel"); err == nil {
		root = top
	}
	absRepo, _ := filepath.Abs(repo)
	absDoc, _ := filepath.Abs(doc)
	if r, err := filepath.EvalSymlinks(absRepo); err == nil {
		absRepo = r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(absDoc)); err == nil {
		absDoc = filepath.Join(r, filepath.Base(absDoc))
	}
	repoRel, _ := filepath.Rel(root, absRepo)
	docRel, _ := filepath.Rel(root, absDoc)
	if err := write(filepath.Join(root, ".github", "workflows", "archdrift.yml"), initWorkflow(filepath.ToSlash(repoRel), filepath.ToSlash(docRel))); err != nil {
		return fail(err)
	}

	fmt.Fprintf(stdout, "\nmode %s; %d components proposed in %s, %d excluded; coverage %d of %d Go packages (%d%%)\n",
		mode, len(plan.Components), config.FileName, len(plan.Exclude), m.Coverage.Modeled, m.Coverage.Packages, m.Coverage.Percent())
	fmt.Fprintf(stdout, `
Next steps:
  1. Review %s (rename or merge components; archdrift scan --repo %s shows the model).
  2. Write or complete the doc %s.
  3. archdrift lock --repo %s --doc %s     (one API call; needs ANTHROPIC_API_KEY)
     Review the printed mapping and edit the lock if needed.
  4. archdrift check --repo %s --doc %s    (no API call from now on)
     If the code already has drift you accept for now: archdrift baseline --repo %s --doc %s
  5. Commit %s, the doc, its .lock.json, any baseline and the workflow.
`, cfgPath, repo, doc, repo, doc, repo, doc, repo, doc, config.FileName)
	return exitOK
}
