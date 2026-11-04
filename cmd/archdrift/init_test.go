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
