package drift

import (
	"testing"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/docmodel"
)

func loc(f string, l int) codemodel.Location { return codemodel.Location{File: f, Line: l} }

func comp(id string) codemodel.Component {
	return codemodel.Component{ID: id, External: len(id) > 4 && id[:4] == "ext:", Anchor: loc(id+"/x.go", 1)}
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
			res := Diff(code, &docmodel.Model{Components: tc.docComps, Relations: tc.relations})
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
	for _, c := range cases {
		if got, _ := Checkable(c.from, c.to, c.proto); got != c.want {
			t.Errorf("Checkable(%v,%v,%q) = %v", c.from, c.to, c.proto, got)
		}
	}
}
