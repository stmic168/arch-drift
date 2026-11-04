package report

import (
	"strings"
	"testing"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/drift"
)

func TestComment(t *testing.T) {
	loc := func(f string, l int) codemodel.Location { return codemodel.Location{File: f, Line: l} }
	r := &Report{
		Base:    "origin/main",
		Summary: Summary{Failing: 2, Existing: 2, Fixed: 1, ByType: map[string]int{drift.Unverifiable: 2}},
		Findings: []drift.Finding{
			{Type: drift.ForbiddenEdge, Severity: "high", Failing: true, Subject: "API -> Store", Status: drift.StatusNew, Code: []codemodel.Location{loc("cmd/api/main.go", 10), loc("cmd/api/x.go", 3)}},
			{Type: drift.UndocumentedEdge, Severity: "low", Failing: true, Subject: "A | B -> C", Status: drift.StatusWorsened,
				Code: []codemodel.Location{loc("a/old.go", 1), loc("a/new.go", 9)}, NewEvidence: []codemodel.Location{loc("a/new.go", 9)}},
			{Type: drift.MissingEdge, Severity: "medium", Failing: true, Subject: "Old -> Gone", Status: drift.StatusExisting},
			{Type: drift.Unverifiable, Severity: "info", Subject: "W -> X"},
			{Type: drift.UndocumentedComponent, Severity: "info", Subject: "pkg/x", Status: drift.StatusExisting}, // pre-existing but never failing: not counted
		},
	}
	got := Comment(r)
	for _, want := range []string{
		"### archdrift: 2 failing finding(s)\n",
		"Compared with `origin/main`",
		"| Severity | Finding | Status | Evidence |",
		"| high | `FORBIDDEN_EDGE` API -> Store | new | `cmd/api/main.go:10` (+1) |",
		"| low | `UNDOCUMENTED_EDGE` A \\| B -> C | worsened | `a/new.go:9` |", // the new evidence, escaped pipe
		"Not failing: 1 pre-existing, 1 fixed by this change, 2 unverifiable.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("comment missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Old -> Gone") || strings.Contains(got, "W -> X") {
		t.Errorf("comment lists findings that do not fail:\n%s", got)
	}

	clean := Comment(&Report{Base: "origin/main", Summary: Summary{ByType: map[string]int{}}})
	if !strings.HasPrefix(clean, "### archdrift: no architecture drift introduced by this change\n") || strings.Contains(clean, "| Severity") {
		t.Errorf("clean comment:\n%s", clean)
	}
}

func TestCommentCapsRows(t *testing.T) {
	r := &Report{Summary: Summary{Failing: maxCommentRows + 5, ByType: map[string]int{}}}
	for i := 0; i < maxCommentRows+5; i++ {
		r.Findings = append(r.Findings, drift.Finding{Type: drift.MissingComponent, Severity: "medium", Failing: true, Subject: "C", DocQuote: strings.Repeat("q", 200)})
	}
	got := Comment(r)
	if n := strings.Count(got, "| medium |"); n != maxCommentRows || !strings.Contains(got, "5 more in the full report.") {
		t.Errorf("%d rows:\n%s", n, got)
	}
	if !strings.Contains(got, `doc: "`+strings.Repeat("q", 117)+`..."`) {
		t.Error("long doc quote not shortened")
	}
}
