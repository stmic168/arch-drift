package drift

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/docmodel"
)

func loc(f string, l int) codemodel.Location { return codemodel.Location{File: f, Line: l} }

func comp(id string) codemodel.Component {
	return codemodel.Component{ID: id, External: strings.HasPrefix(id, "ext:"), Deployable: strings.HasPrefix(id, "cmd/"), Anchor: loc(id+"/x.go", 1)}
}

func edge(from, to string, l int) codemodel.Edge {
	return codemodel.Edge{From: from, To: to, Evidence: []codemodel.Location{loc(from+"/x.go", l)}}
}

func dc(name string, ids ...string) docmodel.Component {
	if ids == nil {
		ids = []string{}
	}
	return docmodel.Component{Name: name, CodeIDs: ids, Quote: name + " quote"}
}

func rel(from, to, kind, proto string) docmodel.Relation {
	return docmodel.Relation{From: from, To: to, Kind: kind, Protocol: proto, Quote: from + " " + kind + " " + to}
}

func TestDiffFindingTypes(t *testing.T) {
	base := []codemodel.Component{comp("cmd/api"), comp("cmd/worker"), comp("internal/a"), comp("internal/b"), comp("ext:postgres")}
	baseDoc := []docmodel.Component{dc("API", "cmd/api"), dc("Worker", "cmd/worker"), dc("A", "internal/a"), dc("B", "internal/b"), dc("PG", "ext:postgres")}

	tests := []struct {
		name      string
		comps     []codemodel.Component
		edges     []codemodel.Edge
		docComps  []docmodel.Component
		relations []docmodel.Relation
		wantType  string // "" means no findings
		wantSev   string
		wantFail  bool
		wantCode  string // first code evidence, "" for none
		wantQuote string
	}{
		{
			name:  "forbidden edge present",
			comps: base, edges: []codemodel.Edge{edge("cmd/api", "internal/b", 7)}, docComps: baseDoc,
			relations: []docmodel.Relation{rel("API", "B", docmodel.KindForbidden, "")},
			wantType:  ForbiddenEdge, wantSev: High, wantFail: true, wantCode: "cmd/api/x.go:7", wantQuote: "API forbidden B",
		},
		{
			name:  "forbidden edge absent is fine",
			comps: base, docComps: baseDoc,
			relations: []docmodel.Relation{rel("API", "B", docmodel.KindForbidden, "")},
		},
		{
			name:  "missing component",
			comps: base, docComps: append(append([]docmodel.Component{}, baseDoc...), dc("Notifier")),
			wantType: MissingComponent, wantSev: Medium, wantFail: true, wantQuote: "Notifier quote",
		},
		{
			name:  "missing edge",
			comps: base, docComps: baseDoc,
			relations: []docmodel.Relation{rel("A", "B", docmodel.KindRequired, "")},
			wantType:  MissingEdge, wantSev: Medium, wantFail: true, wantQuote: "A required B",
		},
		{
			name:  "required edge present",
			comps: base, edges: []codemodel.Edge{edge("internal/a", "ext:postgres", 3)}, docComps: baseDoc,
			relations: []docmodel.Relation{rel("A", "PG", docmodel.KindRequired, "SQL")},
		},
		{
			name:  "undocumented edge",
			comps: base, edges: []codemodel.Edge{edge("internal/a", "internal/b", 4)}, docComps: baseDoc,
			wantType: UndocumentedEdge, wantSev: Low, wantFail: true, wantCode: "internal/a/x.go:4",
		},
		{
			name:  "undocumented component",
			comps: append(append([]codemodel.Component{}, base...), comp("pkg/metrics")), docComps: baseDoc,
			wantType: UndocumentedComponent, wantSev: Low, wantFail: true, wantCode: "pkg/metrics/x.go:1",
		},
		{
			name:  "unverifiable runtime relation between deployables",
			comps: base, docComps: baseDoc,
			relations: []docmodel.Relation{rel("API", "Worker", docmodel.KindRequired, "gRPC")},
			wantType:  Unverifiable, wantSev: Info, wantFail: false, wantQuote: "API required Worker",
		},
		{
			name:  "unverifiable network call from a deployable to a library",
			comps: base, docComps: baseDoc,
			relations: []docmodel.Relation{rel("API", "A", docmodel.KindRequired, "HTTP")},
			wantType:  Unverifiable, wantSev: Info, wantFail: false, wantQuote: "API required A",
		},
		{
			name:  "edge inside one doc component is not undocumented",
			comps: base, edges: []codemodel.Edge{edge("internal/a", "internal/b", 4)},
			docComps: []docmodel.Component{dc("API", "cmd/api"), dc("Worker", "cmd/worker"), dc("AB", "internal/a", "internal/b"), dc("PG", "ext:postgres")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code := &codemodel.Model{Components: tc.comps, Edges: tc.edges}
			res := Diff(code, &docmodel.Model{Components: tc.docComps, Relations: tc.relations}, Options{})
			if tc.wantType == "" {
				if len(res.Findings) != 0 || res.Failing() {
					t.Fatalf("want no findings, got %+v", res.Findings)
				}
				return
			}
			if len(res.Findings) != 1 {
				t.Fatalf("want 1 finding, got %+v", res.Findings)
			}
			f := res.Findings[0]
			if f.Type != tc.wantType || f.Severity != tc.wantSev || f.Failing != tc.wantFail || res.Failing() != tc.wantFail {
				t.Fatalf("got %+v", f)
			}
			gotCode := ""
			if len(f.Code) > 0 {
				gotCode = f.Code[0].String()
			}
			if gotCode != tc.wantCode || f.DocQuote != tc.wantQuote {
				t.Fatalf("evidence = %q / %q, want %q / %q", gotCode, f.DocQuote, tc.wantCode, tc.wantQuote)
			}
		})
	}
}

func TestCheckable(t *testing.T) {
	cases := []struct {
		from, to []string
		proto    string
		want     bool
	}{
		{[]string{"cmd/api"}, []string{"ext:kafka"}, "Kafka", true},
		{[]string{"cmd/api"}, []string{"cmd/worker"}, "", false},
		{[]string{"cmd/api"}, []string{"internal/x"}, "gRPC", false},
		{[]string{"cmd/api"}, []string{"internal/x"}, "in-process", true},
		{[]string{"internal/a"}, []string{"internal/b"}, "HTTP", true},
	}
	isCmd := func(id string) bool { return strings.HasPrefix(id, "cmd/") }
	for _, c := range cases {
		if got, _ := Checkable(c.from, c.to, c.proto, isCmd); got != c.want {
			t.Errorf("Checkable(%v,%v,%q) = %v", c.from, c.to, c.proto, got)
		}
	}
}

func TestTransitive(t *testing.T) {
	comps := []codemodel.Component{comp("cmd/api"), comp("internal/a"), comp("internal/b"), comp("ext:postgres")}
	edges := []codemodel.Edge{edge("cmd/api", "internal/a", 5), edge("internal/a", "internal/b", 6), edge("internal/b", "ext:postgres", 7)}
	docComps := []docmodel.Component{dc("API", "cmd/api"), dc("A", "internal/a"), dc("B", "internal/b"), dc("PG", "ext:postgres")}
	base := []docmodel.Relation{rel("API", "A", docmodel.KindRequired, ""), rel("A", "B", docmodel.KindRequired, ""), rel("B", "PG", docmodel.KindRequired, "")}
	code := &codemodel.Model{Components: comps, Edges: edges}

	// Required API -> PG: missing when only direct imports count, satisfied transitively.
	req := &docmodel.Model{Components: docComps, Relations: append(append([]docmodel.Relation{}, base...), rel("API", "PG", docmodel.KindRequired, "SQL"))}
	if res := Diff(code, req, Options{}); len(res.Findings) != 1 || res.Findings[0].Type != MissingEdge {
		t.Fatalf("direct only: %+v", res.Findings)
	}
	if res := Diff(code, req, Options{TransitiveRequired: true}); len(res.Findings) != 0 {
		t.Fatalf("transitive required: %+v", res.Findings)
	}

	// Forbidden API -> PG: respected when only direct imports count, violated transitively.
	forb := &docmodel.Model{Components: docComps, Relations: append(append([]docmodel.Relation{}, base...), rel("API", "PG", docmodel.KindForbidden, ""))}
	if res := Diff(code, forb, Options{}); len(res.Findings) != 0 {
		t.Fatalf("direct only: %+v", res.Findings)
	}
	res := Diff(code, forb, Options{TransitiveForbidden: true})
	if len(res.Findings) != 1 || res.Findings[0].Type != ForbiddenEdge {
		t.Fatalf("transitive forbidden: %+v", res.Findings)
	}
	f := res.Findings[0]
	if !reflect.DeepEqual(f.Via, []string{"cmd/api", "internal/a", "internal/b", "ext:postgres"}) || len(f.Code) != 3 || f.Code[0].String() != "cmd/api/x.go:5" {
		t.Fatalf("evidence: via=%v code=%v", f.Via, f.Code)
	}
}

func TestDeployableComesFromModel(t *testing.T) {
	// A main package outside cmd/ is still a deployable.
	svc := codemodel.Component{ID: "services/billing", Deployable: true, Anchor: loc("services/billing/main.go", 1)}
	code := &codemodel.Model{Components: []codemodel.Component{comp("cmd/api"), svc}}
	doc := &docmodel.Model{Components: []docmodel.Component{dc("API", "cmd/api"), dc("Billing", "services/billing")},
		Relations: []docmodel.Relation{rel("API", "Billing", docmodel.KindRequired, "")}}
	res := Diff(code, doc, Options{})
	if len(res.Findings) != 1 || res.Findings[0].Type != Unverifiable {
		t.Fatalf("want UNVERIFIABLE, got %+v", res.Findings)
	}
}
