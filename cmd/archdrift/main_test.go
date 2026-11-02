package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stmic168/arch-drift/internal/docmodel"
)

const (
	fixtureRepo  = "../../testdata/shop"
	fixtureDoc   = fixtureRepo + "/design.md"
	fixtureModel = fixtureRepo + "/docmodel.json"
	fixtureWant  = fixtureRepo + "/expected.json"
)

type expected struct {
	ExitCode int `json:"exit_code"`
	Findings []struct {
		Type     string   `json:"type"`
		Subject  string   `json:"subject"`
		Evidence []string `json:"code_evidence"`
	} `json:"findings"`
	Dropped []struct {
		Claim  string `json:"claim"`
		Name   string `json:"name"`
		Detail string `json:"detail"`
	} `json:"dropped_claims"`
}

type reportJSON struct {
	Summary struct {
		ExitCode int `json:"exit_code"`
	} `json:"summary"`
	Findings []struct {
		Type     string `json:"type"`
		Subject  string `json:"subject"`
		DocQuote string `json:"doc_quote"`
		Code     []struct {
			File string `json:"file"`
			Line int    `json:"line"`
		} `json:"code_evidence"`
	} `json:"findings"`
	Dropped []struct {
		Claim  string `json:"claim"`
		Name   string `json:"name"`
		Detail string `json:"detail"`
	} `json:"dropped_claims"`
}

func runFixture(t *testing.T, out string) int {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--doc-model", fixtureModel, "--out", out}, &stdout, &stderr, nil)
	if stderr.Len() > 0 {
		t.Logf("stderr: %s", stderr.String())
	}
	return code
}

func TestGoldenEndToEnd(t *testing.T) {
	out := t.TempDir()
	code := runFixture(t, out)

	var want expected
	b, err := os.ReadFile(fixtureWant)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if code != want.ExitCode {
		t.Fatalf("exit code = %d, want %d", code, want.ExitCode)
	}

	var got reportJSON
	b, err = os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary.ExitCode != want.ExitCode {
		t.Errorf("summary exit code = %d", got.Summary.ExitCode)
	}

	type f struct {
		Type, Subject string
		Evidence      []string
	}
	var gotF, wantF []f
	for _, x := range got.Findings {
		ev := []string{}
		for _, c := range x.Code {
			ev = append(ev, c.File+":"+itoa(c.Line))
		}
		gotF = append(gotF, f{x.Type, x.Subject, ev})
		// Every doc quote in a finding must be verbatim (modulo whitespace) in the doc.
		if x.DocQuote != "" {
			doc, _ := os.ReadFile(fixtureDoc)
			if !strings.Contains(docmodel.NormalizeSpace(string(doc)), docmodel.NormalizeSpace(x.DocQuote)) {
				t.Errorf("finding %s quote not in doc: %q", x.Type, x.DocQuote)
			}
		}
	}
	for _, x := range want.Findings {
		wantF = append(wantF, f{x.Type, x.Subject, x.Evidence})
	}
	if !reflect.DeepEqual(gotF, wantF) {
		t.Errorf("findings:\n got  %+v\n want %+v", gotF, wantF)
	}

	type d struct{ Claim, Name, Detail string }
	var gotD, wantD []d
	for _, x := range got.Dropped {
		gotD = append(gotD, d{x.Claim, x.Name, x.Detail})
	}
	for _, x := range want.Dropped {
		wantD = append(wantD, d{x.Claim, x.Name, x.Detail})
	}
	if !reflect.DeepEqual(gotD, wantD) {
		t.Errorf("dropped:\n got  %+v\n want %+v", gotD, wantD)
	}

	md, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"```mermaid", "## Dropped claims", "## Token usage and cost", "`cmd/api/main.go:10`", "> The API Gateway must never access the Store directly"} {
		if !bytes.Contains(md, []byte(s)) {
			t.Errorf("report.md missing %q", s)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestDeterministicOutput(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if runFixture(t, a) != 1 || runFixture(t, b) != 1 {
		t.Fatal("unexpected exit code")
	}
	for _, name := range []string{"report.md", "report.json"} {
		x, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(x, y) {
			t.Errorf("%s differs between runs", name)
		}
	}
}

// fakeExtractor replays the recorded doc model through the cached API path.
type fakeExtractor struct{ calls *int }

func (f fakeExtractor) Extract(_ context.Context, doc string, ids []string) (*docmodel.Model, docmodel.Usage, error) {
	*f.calls++
	m, err := docmodel.LoadFile(fixtureModel)
	return m, docmodel.Usage{Source: "api", Model: "claude-sonnet-5-5", InputTokens: 2000, OutputTokens: 500}, err
}

func TestCachedAPIPath(t *testing.T) {
	out := t.TempDir()
	calls := 0
	newExt := func(model string) docmodel.Extractor {
		if model != docmodel.DefaultModel {
			t.Errorf("model = %q", model)
		}
		return fakeExtractor{&calls}
	}
	args := []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--out", out}
	var so, se bytes.Buffer
	if code := run(context.Background(), args, &so, &se, newExt); code != 1 {
		t.Fatalf("first run exit %d: %s", code, se.String())
	}
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	if !bytes.Contains(md, []byte("| Cost (USD) | $0.0090 |")) { // 2000*$2 + 500*$10 per MTok
		t.Errorf("cost not reported:\n%s", md)
	}
	if code := run(context.Background(), args, &so, &se, newExt); code != 1 {
		t.Fatalf("second run exit %d", code)
	}
	if calls != 1 {
		t.Fatalf("extractor called %d times, want 1 (second run must hit the cache)", calls)
	}
	entries, _ := os.ReadDir(filepath.Join(out, "cache"))
	if len(entries) != 1 {
		t.Fatalf("cache entries = %d", len(entries))
	}
}

func TestNoDriftExitsZero(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/tiny\n")
	write("cmd/app/main.go", "package main\n\nimport _ \"example.com/tiny/internal/core\"\n\nfunc main() {}\n")
	write("internal/core/core.go", "package core\n")
	write("design.md", "The App binary uses Core.\nCore holds the logic.\n")
	write("dm.json", `{"components":[{"name":"App","code_ids":["cmd/app"],"quote":"The App binary uses Core."},{"name":"Core","code_ids":["internal/core"],"quote":"Core holds the logic."}],
"relations":[{"from":"App","to":"Core","kind":"required","protocol":"","quote":"The App binary uses Core."}]}`)
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"check", "--repo", dir, "--doc", filepath.Join(dir, "design.md"), "--doc-model", filepath.Join(dir, "dm.json"), "--out", filepath.Join(dir, "out")}, &so, &se, nil)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, so.String(), se.String())
	}
}

func TestErrorsExitTwo(t *testing.T) {
	var so, se bytes.Buffer
	cases := [][]string{
		{},
		{"check"},
		{"check", "--repo", fixtureRepo},
		{"check", "--repo", "does-not-exist", "--doc", fixtureDoc, "--doc-model", fixtureModel, "--out", t.TempDir()},
		{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--doc-model", "missing.json", "--out", t.TempDir()},
	}
	for _, args := range cases {
		if code := run(context.Background(), args, &so, &se, nil); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}
