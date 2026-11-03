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
	fixtureLock  = fixtureRepo + "/design.lock.json"
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
	args := []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--no-lock", "--out", out}
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
	empty := filepath.Join(t.TempDir(), "empty.json")
	os.WriteFile(empty, []byte(`{"components":[],"relations":[]}`), 0o644)
	cases = append(cases, []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--doc-model", empty, "--out", t.TempDir()})
	for _, args := range cases {
		if code := run(context.Background(), args, &so, &se, nil); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}

func failingExtractor(t *testing.T) func(string) docmodel.Extractor {
	return func(string) docmodel.Extractor {
		t.Fatal("the API extractor must not be used when a fresh lock exists")
		return nil
	}
}

func findingsOf(t *testing.T, out string) []string {
	t.Helper()
	var got reportJSON
	b, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, f := range got.Findings {
		s = append(s, f.Type+" "+f.Subject)
	}
	return s
}

func expectedFindings(t *testing.T) []string {
	t.Helper()
	var want expected
	b, _ := os.ReadFile(fixtureWant)
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, f := range want.Findings {
		s = append(s, f.Type+" "+f.Subject)
	}
	return s
}

// The committed fixture lock must be exactly what `archdrift lock` produces.
func TestFixtureLockIsUpToDate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "design.lock.json")
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"lock", "--repo", fixtureRepo, "--doc", fixtureDoc, "--doc-model", fixtureModel, "--lock", p}, &so, &se, nil)
	if code != 0 {
		t.Fatalf("lock exit %d: %s", code, se.String())
	}
	got, _ := os.ReadFile(p)
	want, err := os.ReadFile(fixtureLock)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("testdata/shop/design.lock.json is out of date; regenerate it with archdrift lock --doc-model")
	}
	for _, s := range []string{"Notifier", "(no code: will be MISSING_COMPONENT)", "pkg/metrics", "component Billing: quote not found in doc"} {
		if !strings.Contains(so.String(), s) {
			t.Errorf("lock review output missing %q:\n%s", s, so.String())
		}
	}
}

func TestCheckUsesLockWithoutAPI(t *testing.T) {
	out := t.TempDir()
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--out", out}, &so, &se, failingExtractor(t))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	if got, want := findingsOf(t, out), expectedFindings(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("findings:\n got  %v\n want %v", got, want)
	}
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	if !bytes.Contains(md, []byte("lock (`../../testdata/shop/design.lock.json`)")) || !bytes.Contains(md, []byte("read from the reviewed lock file")) {
		t.Errorf("report does not show the lock source:\n%s", md)
	}
}

func TestStaleLockIsAnError(t *testing.T) {
	dir := t.TempDir()
	doc, _ := os.ReadFile(fixtureDoc)
	edited := filepath.Join(dir, "design.md")
	os.WriteFile(edited, append(doc, []byte("\nA new paragraph.\n")...), 0o644)
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"check", "--repo", fixtureRepo, "--doc", edited, "--lock", fixtureLock, "--out", filepath.Join(dir, "out")}, &so, &se, failingExtractor(t))
	if code != 2 || !strings.Contains(se.String(), "lock file is stale") {
		t.Fatalf("exit %d, stderr %q", code, se.String())
	}
	// An explicit --lock that does not exist is an error, not a silent API call.
	code = run(context.Background(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--lock", filepath.Join(dir, "nope.json"), "--out", filepath.Join(dir, "out")}, &so, &se, failingExtractor(t))
	if code != 2 {
		t.Fatalf("missing explicit lock: exit %d", code)
	}
}

func TestLockNotesWhenCodeChanged(t *testing.T) {
	l, err := docmodel.LoadLock(fixtureLock)
	if err != nil {
		t.Fatal(err)
	}
	l.CodeIDs = append([]string{"internal/legacy"}, l.CodeIDs[1:]...) // drop cmd/api, add internal/legacy
	p := filepath.Join(t.TempDir(), "old.lock.json")
	if err := docmodel.WriteLock(p, l); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	var so, se bytes.Buffer
	run(context.Background(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--lock", p, "--out", out}, &so, &se, failingExtractor(t))
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	for _, s := range []string{"## Notes", "added since the lock was written: cmd/api", "removed since the lock was written: internal/legacy"} {
		if !bytes.Contains(md, []byte(s)) {
			t.Errorf("report missing %q", s)
		}
	}
}

func TestConfigAutoDetected(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	write("go.mod", "module example.com/mono\n")
	write("services/billing/main.go", "package main\n\nimport _ \"example.com/mono/libs/ledger\"\n\nfunc main() {}\n")
	write("libs/ledger/ledger.go", "package ledger\n\nimport _ \"example.com/mono/libs/db\"\n")
	write("libs/db/db.go", "package db\n\nimport _ \"github.com/lib/pq\"\n")
	write("archdrift.json", `{"components":[{"id":"billing","paths":["services/billing"]},{"id":"ledger","paths":["libs/ledger"]},{"id":"db","paths":["libs/db"]}],
"default_layout":false,"transitive":{"required":true}}`)
	write("design.md", "Billing records every charge in Postgres.\nBilling is the payments service.\nLedger keeps balances.\nDB wraps the driver.\nPostgres stores data.\n")
	write("dm.json", `{"components":[
 {"name":"Billing","code_ids":["billing"],"quote":"Billing is the payments service."},
 {"name":"Ledger","code_ids":["ledger"],"quote":"Ledger keeps balances."},
 {"name":"DB","code_ids":["db"],"quote":"DB wraps the driver."},
 {"name":"Postgres","code_ids":["ext:postgres"],"quote":"Postgres stores data."}],
"relations":[{"from":"Billing","to":"Postgres","kind":"required","protocol":"SQL","quote":"Billing records every charge in Postgres."}]}`)
	out := filepath.Join(dir, "out")
	var so, se bytes.Buffer
	run(context.Background(), []string{"check", "--repo", dir, "--doc", filepath.Join(dir, "design.md"), "--doc-model", filepath.Join(dir, "dm.json"), "--out", out}, &so, &se, nil)
	b, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatalf("%v: %s", err, se.String())
	}
	var r struct {
		Config   string `json:"config"`
		Findings []struct {
			Type, Subject string
		} `json:"findings"`
	}
	json.Unmarshal(b, &r)
	if !strings.HasSuffix(r.Config, "archdrift.json") {
		t.Errorf("config not recorded: %q", r.Config)
	}
	// Billing -> Postgres holds only through ledger -> db -> pq (transitive.required),
	// so it must not be MISSING_EDGE; the hops themselves are undocumented.
	var types []string
	for _, f := range r.Findings {
		types = append(types, f.Type+" "+f.Subject)
	}
	want := []string{"UNDOCUMENTED_EDGE Billing -> Ledger", "UNDOCUMENTED_EDGE DB -> Postgres", "UNDOCUMENTED_EDGE Ledger -> DB"}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("findings = %v, want %v", types, want)
	}

	// A broken config is an error, not a silent default.
	write("archdrift.json", `{"components":[{"id":"ext:bad","paths":["x"]}]}`)
	if code := run(context.Background(), []string{"check", "--repo", dir, "--doc", filepath.Join(dir, "design.md"), "--doc-model", filepath.Join(dir, "dm.json"), "--out", out}, &so, &se, nil); code != 2 {
		t.Errorf("bad config: exit %d", code)
	}
}

func TestBaselineWorkflow(t *testing.T) {
	dir := t.TempDir()
	bl := filepath.Join(dir, "baseline.json")
	base := []string{"--repo", fixtureRepo, "--doc", fixtureDoc, "--baseline", bl, "--today", "2026-11-02", "--out", filepath.Join(dir, "out")}
	var so, se bytes.Buffer

	// A check with an explicit baseline that does not exist is an error (typo protection).
	if code := run(context.Background(), append([]string{"check"}, base...), &so, &se, nil); code != 2 {
		t.Fatalf("missing explicit baseline: exit %d", code)
	}
	if code := run(context.Background(), append([]string{"baseline"}, base...), &so, &se, nil); code != 0 {
		t.Fatalf("baseline: exit %d: %s", code, se.String())
	}
	if !strings.Contains(so.String(), "5 accepted finding(s) (0 kept, 5 new)") {
		t.Errorf("baseline output: %s", so.String())
	}
	// Everything accepted: check passes, reports the TODO reasons, and lists accepted drift.
	so.Reset()
	if code := run(context.Background(), append([]string{"check"}, base...), &so, &se, nil); code != 0 {
		t.Fatalf("check with full baseline: exit %d: %s", code, so.String())
	}
	md, _ := os.ReadFile(filepath.Join(dir, "out", "report.md"))
	for _, s := range []string{"## Accepted drift (baseline)", "Accepted by baseline (not counted above): 5", "has no real reason yet", "### 1. UNVERIFIABLE (info): Worker -> API Gateway"} {
		if !bytes.Contains(md, []byte(s)) {
			t.Errorf("report missing %q", s)
		}
	}
	// Past the 90-day expiry, everything fails again.
	late := append([]string{"check"}, base...)
	late[len(late)-3] = "2027-02-01"
	if code := run(context.Background(), late, &so, &se, nil); code != 1 {
		t.Fatalf("after expiry: exit %d", code)
	}
}

func TestReportJSONHasNoNullEvidence(t *testing.T) {
	out := t.TempDir()
	runFixture(t, out)
	b, _ := os.ReadFile(filepath.Join(out, "report.json"))
	if bytes.Contains(b, []byte(`"code_evidence": null`)) {
		t.Fatal("code_evidence must be [] not null")
	}
}
