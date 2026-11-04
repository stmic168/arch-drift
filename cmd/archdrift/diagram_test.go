package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const fixtureDiagramDoc = fixtureRepo + "/design-diagram.md"

// The fixture's rules written as a marked dependency diagram give exactly the
// findings the LLM path gives on design.md, with no API call.
func TestNoLLMDiagramMatchesExpected(t *testing.T) {
	out := t.TempDir()
	var so, se bytes.Buffer
	code := run(t.Context(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDiagramDoc, "--no-llm", "--out", out}, &so, &se, failingExtractor(t))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	if got, want := findingsOf(t, out), expectedFindings(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("findings:\n got  %v\n want %v", got, want)
	}
	var rep struct {
		Usage   struct{ Source string } `json:"usage"`
		Dropped []json.RawMessage        `json:"dropped_claims"`
	}
	b, _ := os.ReadFile(filepath.Join(out, "report.json"))
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Usage.Source != "diagram" || len(rep.Dropped) != 0 {
		t.Errorf("usage source %q, %d dropped claims", rep.Usage.Source, len(rep.Dropped))
	}
}

func TestNoLLMLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "design-diagram.lock.json")
	var so, se bytes.Buffer
	if code := run(t.Context(), []string{"lock", "--repo", fixtureRepo, "--doc", fixtureDiagramDoc, "--no-llm", "--lock", lock}, &so, &se, failingExtractor(t)); code != 0 {
		t.Fatalf("lock exit %d: %s", code, se.String())
	}
	if b, _ := os.ReadFile(lock); !bytes.Contains(b, []byte(`"model": "diagram"`)) {
		t.Errorf("lock does not record the diagram source:\n%s", b)
	}
	out := t.TempDir()
	if code := run(t.Context(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDiagramDoc, "--lock", lock, "--out", out}, &so, &se, failingExtractor(t)); code != 1 {
		t.Fatalf("check exit %d: %s", code, se.String())
	}
	if got, want := findingsOf(t, out), expectedFindings(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("findings from the lock:\n got  %v\n want %v", got, want)
	}
}

func TestNoLLMNeedsMarkedDiagram(t *testing.T) {
	var so, se bytes.Buffer
	code := run(t.Context(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--no-llm", "--out", t.TempDir()}, &so, &se, failingExtractor(t))
	if code != 2 || !strings.Contains(se.String(), "%% archdrift: dependencies") {
		t.Fatalf("exit %d, stderr %q", code, se.String())
	}
	if code := run(t.Context(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--no-llm", "--doc-model", fixtureModel}, &so, &se, nil); code != 2 {
		t.Errorf("--no-llm with --doc-model: exit %d, want a usage error", code)
	}
}

// A marked diagram next to prose adds its relations to what the LLM read.
func TestDiagramMergedIntoLLMModel(t *testing.T) {
	prose, err := os.ReadFile(fixtureDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(t.TempDir(), "design.md")
	extra := "\n```mermaid\nflowchart LR\n  %% archdrift: dependencies\n  catalog[Catalog] --> payments[Payments]\n```\n"
	os.WriteFile(doc, append(prose, extra...), 0o644)
	out := t.TempDir()
	var so, se bytes.Buffer
	if code := run(t.Context(), []string{"check", "--repo", fixtureRepo, "--doc", doc, "--doc-model", fixtureModel, "--out", out}, &so, &se, nil); code != 1 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	got := findingsOf(t, out)
	want := append([]string{}, expectedFindings(t)...)
	if !contains(got, "MISSING_EDGE Catalog -> Payments") || len(got) != len(want)+1 {
		t.Fatalf("findings:\n got  %v\n want %v plus MISSING_EDGE Catalog -> Payments", got, want)
	}
}

// The as-built diagram, pasted into a doc, is read back by code and describes
// the code exactly: no failing finding.
func TestAsBuiltDiagramRoundTrip(t *testing.T) {
	out := t.TempDir()
	runFixture(t, out)
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	block := regexp.MustCompile("(?s)### As-built diagram.*?(```mermaid\n.*?```\n)").FindSubmatch(md)
	if block == nil {
		t.Fatalf("no as-built diagram in report:\n%s", md)
	}
	doc := filepath.Join(t.TempDir(), "as-built.md")
	os.WriteFile(doc, append([]byte("# As built\n\n"), block[1]...), 0o644)
	out2 := t.TempDir()
	var so, se bytes.Buffer
	if code := run(t.Context(), []string{"check", "--repo", fixtureRepo, "--doc", doc, "--no-llm", "--out", out2}, &so, &se, failingExtractor(t)); code != 0 {
		r, _ := os.ReadFile(filepath.Join(out2, "report.md"))
		t.Fatalf("exit %d: %s\n%s\n%s", code, se.String(), block[1], r)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
