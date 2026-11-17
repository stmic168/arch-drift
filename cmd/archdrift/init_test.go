package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInitProposesLayout(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "server") // a subdirectory of the git repository
	files := map[string]string{
		"go.mod":                         "module example.com/server\n",
		"cmd/server/main.go":             "package main\n\nfunc main() {}\n",
		"service/history/history.go":     "package history\n",
		"service/history/shard/shard.go": "package shard\n",
		"service/matching/matching.go":   "package matching\n",
		"service/fx.go":                  "package service\n",
		"api/historyservicemock/mock.go": "package historyservicemock\n",
		"servertest/servertest.go":       "package servertest\n",
		"tools/gen/main.go":              "package main\n\nfunc main() {}\n",
		"docs/architecture.md":           "# Server\n",
	}
	for i := 0; i < 12; i++ { // a library bucket with many children stays one component
		files["common/lib"+string(rune('a'+i))+"/x.go"] = "package lib\n"
	}
	for p, body := range files {
		full := filepath.Join(repo, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	if _, err := exec.LookPath("git"); err == nil {
		gitRun(t, root, "init", "-q")
	}
	doc := filepath.Join(repo, "docs/architecture.md")
	var so, se bytes.Buffer
	if code := run(t.Context(), []string{"init", "--repo", repo, "--doc", doc}, &so, &se, nil); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	var cfg struct {
		Components []struct {
			ID    string   `json:"id"`
			Paths []string `json:"paths"`
		} `json:"components"`
		Exclude           []string        `json:"exclude"`
		UndocumentedEdges map[string]bool `json:"undocumented_edges"`
		StrictProtocols   bool            `json:"strict_protocols"`
	}
	b, err := os.ReadFile(filepath.Join(repo, "archdrift.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("archdrift.json is not valid JSON: %v\n%s", err, b)
	}
	var ids []string
	for _, c := range cfg.Components {
		ids = append(ids, c.ID)
	}
	if want := []string{"common", "service", "service/history", "service/matching"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("components = %v, want %v", ids, want)
	}
	// api only holds a mock package: the mock is excluded and api gets no component.
	if want := []string{"api/historyservicemock", "servertest", "tools"}; !reflect.DeepEqual(cfg.Exclude, want) {
		t.Errorf("exclude = %v, want %v", cfg.Exclude, want)
	}
	if cfg.UndocumentedEdges["fail"] || cfg.StrictProtocols {
		t.Errorf("report mode options: %+v", cfg)
	}
	if !strings.Contains(so.String(), "coverage") || !strings.Contains(so.String(), "archdrift lock --repo") {
		t.Errorf("stdout: %s", so.String())
	}
	if _, err := exec.LookPath("git"); err == nil {
		wf, err := os.ReadFile(filepath.Join(root, ".github/workflows/archdrift.yml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"repo-path: server\n", "docs: server/docs/architecture.md\n", "uses: stmic168/arch-drift@main", "security-events: write", "pull-requests: write", "comment: \"true\""} {
			if !strings.Contains(string(wf), s) {
				t.Errorf("workflow missing %q:\n%s", s, wf)
			}
		}
	}
	// The generated config loads and covers the code.
	so.Reset()
	if code := run(t.Context(), []string{"scan", "--repo", repo}, &so, &se, nil); code != 0 || !strings.Contains(so.String(), "config: ") {
		t.Errorf("scan with the generated config: exit %d: %s %s", code, so.String(), se.String())
	}

	// A second run keeps existing files.
	so.Reset()
	os.WriteFile(filepath.Join(repo, "archdrift.json"), []byte("{}\n"), 0o644)
	run(t.Context(), []string{"init", "--repo", repo, "--doc", doc}, &so, &se, nil)
	if b, _ := os.ReadFile(filepath.Join(repo, "archdrift.json")); string(b) != "{}\n" || !strings.Contains(so.String(), "kept") {
		t.Errorf("init overwrote an existing config without --force: %s", so.String())
	}
}

func TestInitGateModeStartsRulesDoc(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "shop")
	copyTree(t, fixtureRepo, repo)
	os.Remove(filepath.Join(repo, "archdrift.json"))
	doc := filepath.Join(repo, "docs", "rules.md")
	var so, se bytes.Buffer
	if code := run(t.Context(), []string{"init", "--repo", repo, "--doc", doc, "--mode", "gate"}, &so, &se, nil); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	cfg, _ := os.ReadFile(filepath.Join(repo, "archdrift.json"))
	if !bytes.Contains(cfg, []byte(`"strict_protocols": true`)) || bytes.Contains(cfg, []byte(`"components"`)) {
		t.Errorf("gate config for a cmd/internal/pkg repo:\n%s", cfg)
	}
	rules, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"# ADR: component boundaries", "- **cmd/api** (`cmd/api`) (deployable): TODO describe.", "## Prohibited dependencies"} {
		if !bytes.Contains(rules, []byte(s)) {
			t.Errorf("rules doc missing %q:\n%s", s, rules)
		}
	}
	// report mode with a missing doc is an error, not a template.
	if code := run(t.Context(), []string{"init", "--repo", repo, "--doc", filepath.Join(repo, "nope.md")}, &so, &se, nil); code != 2 {
		t.Errorf("report mode, missing doc: exit %d", code)
	}
}

// initRepo writes files into a new repo and runs init with doc, returning
// the generated archdrift.json and stdout.
func initRepo(t *testing.T, files map[string]string, doc string) (initConfigFile, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "app")
	for p, body := range files {
		full := filepath.Join(repo, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	var so, se bytes.Buffer
	if code := run(t.Context(), []string{"init", "--repo", repo, "--doc", filepath.Join(repo, doc)}, &so, &se, nil); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	b, err := os.ReadFile(filepath.Join(repo, "archdrift.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg initConfigFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("archdrift.json: %v\n%s", err, b)
	}
	return cfg, so.String()
}

type initConfigFile struct {
	Components []struct {
		ID    string   `json:"id"`
		Paths []string `json:"paths"`
	} `json:"components"`
	Exclude  []string `json:"exclude"`
	Services []string `json:"services"`
}

func TestInitServicesFromFilePerService(t *testing.T) {
	files := map[string]string{
		"go.mod":                    "module example.com/app\n",
		"docs/a.md":                 "# App\n",
		"cmd/app/main.go":           "package main\n\nfunc main() {}\n",
		"cmd/app/rule.go":           "package main\n\nimport _ \"example.com/app/pkg/rules\"\n",         // plural package
		"cmd/app/query_frontend.go": "package main\n\nimport _ \"example.com/app/pkg/queryfrontend\"\n", // file name with _
		"cmd/app/config.go":         "package main\n\nimport _ \"example.com/app/pkg/util\"\n",          // no package named config
		"cmd/app/store.go":          "package main\n",                                                   // named like pkg/store, does not import it
		"pkg/rules/r.go":            "package rules\n",
		"pkg/queryfrontend/q.go":    "package queryfrontend\n",
		"pkg/store/s.go":            "package store\n",
		"pkg/util/u.go":             "package util\n",
	}
	cfg, so := initRepo(t, files, "docs/a.md")
	if want := []string{"pkg/queryfrontend", "pkg/rules"}; !reflect.DeepEqual(cfg.Services, want) {
		t.Errorf("services = %v, want %v", cfg.Services, want)
	}
	if !strings.Contains(so, "services (run separately inside one binary): pkg/queryfrontend, pkg/rules") {
		t.Errorf("stdout: %s", so)
	}

	// One matching file is one binary with one mode, not a multi-service binary.
	delete(files, "cmd/app/query_frontend.go")
	if cfg, _ := initRepo(t, files, "docs/a.md"); len(cfg.Services) != 0 {
		t.Errorf("single service file: services = %v", cfg.Services)
	}
}

func TestInitServicesFromModuleList(t *testing.T) {
	files := map[string]string{
		"go.mod":          "module example.com/app\n",
		"docs/a.md":       "# App\n",
		"cmd/app/main.go": "package main\n\nfunc main() {}\n",
		"pkg/app/modules.go": `package app

import (
	"example.com/app/pkg/lokifrontend/frontend"
	"example.com/app/pkg/ring"
	"example.com/app/pkg/util"
)

const (
	Ring          string = "ring"
	StoreGateway  string = "store-gateway"
	QueryFrontend string = "query-frontend"
	Exporter      string = "overrides-exporter"
	All           string = "all"
)

func (t *App) initRing() error          { ring.New(); return nil }
func (t *App) initQueryFrontend() error { frontend.New(); util.Log(); return nil }
func (t *App) initExporter() error      { util.Log(); return nil }

func (t *App) setupModuleManager() {
	mm.RegisterModule(Ring, t.initRing, modules.UserInvisibleModule)
	mm.RegisterModule(StoreGateway, nil)
	mm.RegisterModule(QueryFrontend, t.initQueryFrontend)
	mm.RegisterModule(Exporter, t.initExporter)
	mm.RegisterModule(All, nil)
}
`,
		"pkg/ring/r.go":                  "package ring\n",
		"pkg/storegateway/s.go":          "package storegateway\n",
		"pkg/lokifrontend/frontend/f.go": "package frontend\n",
		"pkg/util/u.go":                  "package util\n",
		"pkg/app/modules_test.go":        "package app\n",
	}
	cfg, _ := initRepo(t, files, "docs/a.md")
	// ring is a helper (UserInvisibleModule), overrides-exporter has no
	// package with its name, all is every module.
	if want := []string{"pkg/lokifrontend", "pkg/storegateway"}; !reflect.DeepEqual(cfg.Services, want) {
		t.Errorf("services = %v, want %v", cfg.Services, want)
	}
}

func TestInitSplitsBucketTheDocNames(t *testing.T) {
	files := map[string]string{
		"go.mod":              "module example.com/app\n",
		"pilot/cmd/main.go":   "package main\n\nfunc main() {}\n",
		"pilot/pkg/test/t.go": "package test\n",
		"docs/pilot.md":       "# Pilot\n\n## Config Store\n\nThe `PushContext` is built by **xds**.\n\nThe tool's `writer` prints it.\n",
		// pilot/pkg: the doc names 3 of its 5 children (configstore by
		// directory, model by exported type, xds by directory).
		"pilot/pkg/configstore/c.go": "package configstore\n",
		"pilot/pkg/model/m.go":       "package model\n\ntype PushContext struct{}\n",
		"pilot/pkg/xds/x.go":         "package xds\n",
		"pilot/pkg/util/u.go":        "package util\n",
		"pilot/pkg/features/f.go":    "package features\n",
	}
	// cli/pkg: the doc names one of its children; it stays one component.
	for _, c := range []string{"writer", "a", "b", "c"} {
		files["cli/pkg/"+c+"/x.go"] = "package " + c + "\n"
	}
	cfg, so := initRepo(t, files, "docs/pilot.md")
	var ids []string
	for _, c := range cfg.Components {
		ids = append(ids, c.ID+"="+strings.Join(c.Paths, ","))
	}
	want := []string{"cli/pkg=cli/pkg", "pilot/cmd=pilot/cmd", "pilot/configstore=pilot/pkg/configstore", "pilot/features=pilot/pkg/features",
		"pilot/model=pilot/pkg/model", "pilot/util=pilot/pkg/util", "pilot/xds=pilot/pkg/xds"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("components = %v\nwant %v", ids, want)
	}
	if want := []string{"pilot/pkg/test"}; !reflect.DeepEqual(cfg.Exclude, want) {
		t.Errorf("exclude = %v, want %v", cfg.Exclude, want)
	}
	if !strings.Contains(so, "split pilot/pkg into its subdirectories: the doc names code in configstore, model, xds") {
		t.Errorf("stdout: %s", so)
	}
}

func TestInitSplitNeedsAShareOfTheChildren(t *testing.T) {
	files := map[string]string{
		"go.mod":            "module example.com/app\n",
		"pilot/cmd/main.go": "package main\n\nfunc main() {}\n",
		"docs/pilot.md":     "# Pilot\n\n## Alpha\n\n## Beta\n\n## Gamma\n",
	}
	// 3 of 13 children named: under a quarter, so no split.
	for _, c := range []string{"alpha", "beta", "gamma", "d1", "d2", "d3", "d4", "d5", "d6", "d7", "d8", "d9", "d10"} {
		files["pilot/pkg/"+c+"/x.go"] = "package x\n"
	}
	cfg, so := initRepo(t, files, "docs/pilot.md")
	for _, c := range cfg.Components {
		if c.ID != "pilot/cmd" && c.ID != "pilot/pkg" {
			t.Errorf("unexpected component %s", c.ID)
		}
	}
	if strings.Contains(so, "split ") {
		t.Errorf("stdout: %s", so)
	}
}

func TestDocNames(t *testing.T) {
	got := docNames("# Title\n\n## CRD Watcher ##\n\nUses `crdclient` and **Tag Watcher**; `crdclient` again.\n```go\nx\n```\n")
	want := []string{"Title", "CRD Watcher", "crdclient", "Tag Watcher"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("docNames = %q, want %q", got, want)
	}
}
