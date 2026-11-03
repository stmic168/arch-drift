package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stmic168/arch-drift/internal/docmodel"
	"github.com/stmic168/arch-drift/internal/drift"
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
	for _, name := range []string{"report.md", "report.json", "report.sarif"} {
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
	if !strings.Contains(so.String(), "4 accepted finding(s) (0 kept, 4 new)") {
		t.Errorf("baseline output: %s", so.String())
	}
	// Everything accepted: check passes, reports the TODO reasons, and lists accepted drift.
	so.Reset()
	if code := run(context.Background(), append([]string{"check"}, base...), &so, &se, nil); code != 0 {
		t.Fatalf("check with full baseline: exit %d: %s", code, so.String())
	}
	md, _ := os.ReadFile(filepath.Join(dir, "out", "report.md"))
	for _, s := range []string{"## Accepted drift (baseline)", "Accepted by baseline (not counted above): 4", "has no real reason yet", "### 1. UNDOCUMENTED_COMPONENT (info): pkg/metrics"} {
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

func TestMultipleDocsAndCoverageNote(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	write("go.mod", "module example.com/m\n")
	write("cmd/app/main.go", "package main\n\nimport _ \"example.com/m/internal/core\"\n\nfunc main() {}\n")
	write("internal/core/core.go", "package core\n")
	for _, d := range []string{"a", "b", "c"} { // three unmodeled packages: 2 of 5 modeled = 40%
		write("service/"+d+"/x.go", "package "+d+"\n")
	}
	write("docs/overview.md", "The App is the CLI.\n")
	write("docs/core.md", "Core holds the logic.\nThe App calls Core.\n")
	write("dm.json", `{"components":[
 {"name":"App","code_ids":["cmd/app"],"quote":"The App is the CLI."},
 {"name":"Core","code_ids":["internal/core"],"quote":"Core holds the logic."}],
"relations":[{"from":"App","to":"Core","kind":"forbidden","protocol":"","quote":"The App calls Core."}]}`)
	out := filepath.Join(dir, "out")
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"check", "--repo", dir, "--doc", filepath.Join(dir, "docs/overview.md"), "--doc", filepath.Join(dir, "docs/core.md"),
		"--doc-model", filepath.Join(dir, "dm.json"), "--out", out}, &so, &se, nil)
	if code != 1 {
		t.Fatalf("exit %d: %s %s", code, so.String(), se.String())
	}
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	for _, s := range []string{"overview.md, ", "core.md`", "Doc quote (`" + filepath.ToSlash(filepath.Join(dir, "docs/core.md")) + "`)", "Only 2 of 5 Go packages (40%)", "service (3)"} {
		if !bytes.Contains(md, []byte(s)) {
			t.Errorf("report missing %q", s)
		}
	}
	// The same file twice is an error.
	if code := run(context.Background(), []string{"check", "--repo", dir, "--doc", filepath.Join(dir, "docs/core.md"), "--doc", filepath.Join(dir, "docs/core.md"), "--doc-model", filepath.Join(dir, "dm.json"), "--out", out}, &so, &se, nil); code != 2 {
		t.Errorf("duplicate doc: exit %d", code)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestBaseDiffMode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "svc") // a subdirectory of the git repo, like testdata/shop
	copyTree(t, fixtureRepo, repo)
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")
	// The change: another file of the API imports the Store (worsens the
	// forbidden edge) and Catalog starts importing Payments (new edge).
	os.WriteFile(filepath.Join(repo, "cmd/api/admin.go"), []byte("package main\n\nimport _ \"example.com/shop/internal/store\"\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "internal/catalog/pricing.go"), []byte("package catalog\n\nimport _ \"example.com/shop/internal/payments\"\n"), 0o644)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "change")

	doc := filepath.Join(repo, "design.md")
	out := filepath.Join(root, "out")
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"check", "--repo", repo, "--doc", doc, "--base", "HEAD~1", "--out", out}, &so, &se, failingExtractor(t))
	if code != 1 {
		t.Fatalf("exit %d: %s %s", code, so.String(), se.String())
	}
	var r struct {
		Summary struct {
			Failing, Existing int
		} `json:"summary"`
		Findings []struct {
			ID, Status  string
			Failing     bool
			NewEvidence []struct{ File string } `json:"new_evidence"`
		} `json:"findings"`
	}
	b, _ := os.ReadFile(filepath.Join(out, "report.json"))
	json.Unmarshal(b, &r)
	failing := map[string]string{}
	for _, f := range r.Findings {
		if f.Failing {
			failing[f.ID] = f.Status
		}
	}
	want := map[string]string{
		"FORBIDDEN_EDGE:API Gateway -> Store":                   drift.StatusWorsened,
		"UNDOCUMENTED_EDGE:internal/catalog->internal/payments": drift.StatusNew,
	}
	if !reflect.DeepEqual(failing, want) {
		t.Fatalf("failing = %v, want %v", failing, want)
	}
	if r.Summary.Failing != 2 || r.Summary.Existing == 0 {
		t.Errorf("summary = %+v", r.Summary)
	}
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	for _, s := range []string{"[worsened]", "New in this change:", "`cmd/api/admin.go:3`", "## Pre-existing drift (on `HEAD~1`, not failing)", "Compared with base HEAD~1"} {
		if !bytes.Contains(md, []byte(s)) {
			t.Errorf("report missing %q", s)
		}
	}
	// Against itself nothing is new: the check passes.
	if code := run(context.Background(), []string{"check", "--repo", repo, "--doc", doc, "--base", "HEAD", "--out", out}, &so, &se, failingExtractor(t)); code != 0 {
		t.Errorf("--base HEAD: exit %d", code)
	}
	// An unknown revision is an error.
	if code := run(context.Background(), []string{"check", "--repo", repo, "--doc", doc, "--base", "no-such-ref", "--out", out}, &so, &se, failingExtractor(t)); code != 2 {
		t.Errorf("unknown base: exit %d", code)
	}
	// The temporary worktree is removed.
	wt, _ := exec.Command("git", "-C", root, "worktree", "list").Output()
	if strings.Count(string(wt), "\n") != 1 {
		t.Errorf("worktrees left behind:\n%s", wt)
	}
}

func TestSARIF(t *testing.T) {
	out := t.TempDir()
	if code := runFixture(t, out); code != 1 {
		t.Fatalf("exit %d", code)
	}
	type result struct {
		RuleID    string `json:"ruleId"`
		Level     string `json:"level"`
		Locations []struct {
			PhysicalLocation struct {
				ArtifactLocation struct{ URI string } `json:"artifactLocation"`
				Region           struct {
					StartLine int `json:"startLine"`
				} `json:"region"`
			} `json:"physicalLocation"`
		} `json:"locations"`
		PartialFingerprints map[string]string `json:"partialFingerprints"`
	}
	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Rules []struct{ ID string } `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []result `json:"results"`
		} `json:"runs"`
	}
	read := func(dir string) {
		b, err := os.ReadFile(filepath.Join(dir, "report.sarif"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &log); err != nil {
			t.Fatal(err)
		}
	}
	read(out)
	if log.Version != "2.1.0" || len(log.Runs) != 1 || len(log.Runs[0].Tool.Driver.Rules) != 6 {
		t.Fatalf("sarif header: %+v", log)
	}
	byID := map[string]result{}
	for _, r := range log.Runs[0].Results {
		byID[r.PartialFingerprints["archdriftFindingId/v1"]] = r
	}
	loc := func(id string) (string, int, string) {
		r := byID[id]
		if len(r.Locations) == 0 {
			return "", 0, r.Level
		}
		p := r.Locations[0].PhysicalLocation
		return p.ArtifactLocation.URI, p.Region.StartLine, r.Level
	}
	check := func(id, uri string, line int, level string) {
		t.Helper()
		gu, gl, glv := loc(id)
		if gu != uri || gl != line || glv != level {
			t.Errorf("%s: %s:%d %s, want %s:%d %s", id, gu, gl, glv, uri, line, level)
		}
	}
	check("FORBIDDEN_EDGE:API Gateway -> Store", "cmd/api/main.go", 10, "error")
	check("UNDOCUMENTED_EDGE:internal/orders->ext:postgres", "internal/orders/repo/repo.go", 5, "warning")
	check("UNDOCUMENTED_COMPONENT:pkg/metrics", "pkg/metrics/metrics.go", 2, "note")
	// No code evidence: the location is the quote's line in the doc.
	check("MISSING_COMPONENT:Notifier", "design.md", 17, "error")
	check("MISSING_EDGE:Catalog -> Store", "design.md", 28, "error")

	// With the checkout root one level up, paths gain the subdirectory.
	out2 := t.TempDir()
	var so, se bytes.Buffer
	run(context.Background(), []string{"check", "--repo", fixtureRepo, "--doc", fixtureDoc, "--doc-model", fixtureModel, "--sarif-root", "../../testdata", "--out", out2}, &so, &se, nil)
	read(out2)
	byID = map[string]result{}
	for _, r := range log.Runs[0].Results {
		byID[r.PartialFingerprints["archdriftFindingId/v1"]] = r
	}
	check("FORBIDDEN_EDGE:API Gateway -> Store", "shop/cmd/api/main.go", 10, "error")
	check("MISSING_COMPONENT:Notifier", "shop/design.md", 17, "error")
}
