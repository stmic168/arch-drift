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
		opts      Options
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
			name:  "undocumented edge is informational when configured",
			comps: base, edges: []codemodel.Edge{edge("internal/a", "internal/b", 4)}, docComps: baseDoc,
			opts:     Options{ReportUndocumentedEdges: true},
			wantType: UndocumentedEdge, wantSev: Info, wantFail: false, wantCode: "internal/a/x.go:4",
		},
		{
			name:  "undocumented component is listed but does not fail by default",
			comps: append(append([]codemodel.Component{}, base...), comp("pkg/metrics")), docComps: baseDoc,
			wantType: UndocumentedComponent, wantSev: Info, wantFail: false, wantCode: "pkg/metrics/x.go:1",
		},
		{
			name:  "undocumented component fails when configured",
			comps: append(append([]codemodel.Component{}, base...), comp("pkg/metrics")), docComps: baseDoc,
			opts:     Options{FailUndocumentedComponents: true},
			wantType: UndocumentedComponent, wantSev: Low, wantFail: true, wantCode: "pkg/metrics/x.go:1",
		},
		{
			name:  "ignored undocumented component",
			comps: append(append([]codemodel.Component{}, base...), comp("pkg/extprom")), docComps: baseDoc,
			opts: Options{IgnoreUndocumented: []string{"pkg/ext*"}},
		},
		{
			name:  "external actor without code is not missing",
			comps: base, docComps: append(append([]docmodel.Component{}, baseDoc...), docmodel.Component{Name: "Users", CodeIDs: []string{}, Quote: "Users quote", External: true}),
		},
		{
			name:  "relation to an external actor is unverifiable",
			comps: base, docComps: append(append([]docmodel.Component{}, baseDoc...), docmodel.Component{Name: "Users", CodeIDs: []string{}, Quote: "Users quote", External: true}),
			relations: []docmodel.Relation{rel("Users", "API", docmodel.KindRequired, "HTTP")},
			wantType:  Unverifiable, wantSev: Info, wantFail: false, wantQuote: "Users required API",
		},
		{
			name:  "network relation between libraries without an import is unverifiable",
			comps: base, docComps: baseDoc,
			relations: []docmodel.Relation{rel("A", "B", docmodel.KindRequired, "gRPC")},
			wantType:  Unverifiable, wantSev: Info, wantFail: false, wantQuote: "A required B",
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
			res := Diff(code, &docmodel.Model{Components: tc.docComps, Relations: tc.relations}, tc.opts)
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

	// Required API -> PG: an external target is always reached through a
	// client wrapper, so an import path satisfies it even by default.
	req := &docmodel.Model{Components: docComps, Relations: append(append([]docmodel.Relation{}, base...), rel("API", "PG", docmodel.KindRequired, "SQL"))}
	if res := Diff(code, req, Options{}); len(res.Findings) != 0 {
		t.Fatalf("external target via path: %+v", res.Findings)
	}
	// Required API -> B (internal): missing when only direct imports count, satisfied transitively.
	reqB := &docmodel.Model{Components: docComps, Relations: append(append([]docmodel.Relation{}, base...), rel("API", "B", docmodel.KindRequired, ""))}
	if res := Diff(code, reqB, Options{}); len(res.Findings) != 1 || res.Findings[0].Type != MissingEdge {
		t.Fatalf("direct only: %+v", res.Findings)
	}
	if res := Diff(code, reqB, Options{TransitiveRequired: true}); len(res.Findings) != 0 {
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

func TestMissingEndpointRelationIsUnverifiable(t *testing.T) {
	code := &codemodel.Model{Components: []codemodel.Component{comp("cmd/api")}}
	doc := &docmodel.Model{Components: []docmodel.Component{dc("API", "cmd/api"), dc("Notifier")},
		Relations: []docmodel.Relation{rel("API", "Notifier", docmodel.KindRequired, "HTTP")}}
	res := Diff(code, doc, Options{})
	var types []string
	for _, f := range res.Findings {
		types = append(types, f.Type+" "+f.Subject+" "+f.Message)
	}
	want := []string{
		"MISSING_COMPONENT Notifier Doc component \"Notifier\" has no matching package in code.",
		"UNVERIFIABLE API -> Notifier Not checked: Notifier has no code in this repo.",
	}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("got %q", types)
	}
}

func TestUndocumentedComponentsRankApplicationsFirst(t *testing.T) {
	// pkg/feature imports three components and is imported only by the
	// wiring: an application component. pkg/util is imported by everyone.
	code := &codemodel.Model{
		Components: []codemodel.Component{comp("cmd/app"), comp("internal/a"), comp("internal/b"), comp("pkg/feature"), comp("pkg/util"), comp("pkg/idle"), comp("ext:grpc")},
		Edges: []codemodel.Edge{
			edge("cmd/app", "pkg/feature", 1), edge("cmd/app", "pkg/util", 2),
			edge("pkg/feature", "internal/a", 3), edge("pkg/feature", "internal/b", 4), edge("pkg/feature", "pkg/util", 5),
			edge("internal/a", "pkg/util", 6), edge("internal/b", "pkg/util", 7), edge("internal/a", "ext:grpc", 8),
		},
	}
	doc := &docmodel.Model{Components: []docmodel.Component{dc("App", "cmd/app"), dc("A", "internal/a"), dc("B", "internal/b")}}
	var got []string
	for _, f := range Diff(code, doc, Options{}).Findings {
		got = append(got, f.Subject)
	}
	if want := []string{"pkg/feature", "pkg/idle", "ext:grpc", "pkg/util"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestProtocolEdgeSatisfiesRequired(t *testing.T) {
	code := &codemodel.Model{Components: []codemodel.Component{comp("internal/querier"), comp("internal/frontend")},
		ProtocolEdges: []codemodel.Edge{edge("internal/querier", "internal/frontend", 16)}}
	doc := &docmodel.Model{Components: []docmodel.Component{dc("Querier", "internal/querier"), dc("Frontend", "internal/frontend")},
		Relations: []docmodel.Relation{rel("Querier", "Frontend", docmodel.KindRequired, "")}}
	if res := Diff(code, doc, Options{}); len(res.Findings) != 0 {
		t.Fatalf("a protocol edge must satisfy a required relation: %+v", res.Findings)
	}
	// It is never an undocumented dependency.
	doc.Relations = nil
	if res := Diff(code, doc, Options{}); len(res.Findings) != 0 {
		t.Fatalf("a protocol edge must not be UNDOCUMENTED_EDGE: %+v", res.Findings)
	}
}

func TestProtocolEdgeViolatesForbidden(t *testing.T) {
	code := &codemodel.Model{Components: []codemodel.Component{comp("internal/storage"), comp("internal/ingester")},
		ProtocolEdges: []codemodel.Edge{edge("internal/storage", "internal/ingester", 6)}}
	doc := &docmodel.Model{Components: []docmodel.Component{dc("Storage", "internal/storage"), dc("Ingester", "internal/ingester")},
		Relations: []docmodel.Relation{rel("Storage", "Ingester", docmodel.KindForbidden, "")}}
	res := Diff(code, doc, Options{})
	if len(res.Findings) != 1 || res.Findings[0].Type != ForbiddenEdge || res.Findings[0].Code[0].String() != "internal/storage/x.go:6" {
		t.Fatalf("importing the target's protocol package must violate a forbidden relation: %+v", res.Findings)
	}
}

func TestStrictProtocols(t *testing.T) {
	code := &codemodel.Model{Components: []codemodel.Component{comp("internal/purger"), comp("internal/compactor")}}
	inferred := docmodel.Relation{From: "Purger", To: "Compactor", Kind: docmodel.KindRequired, Protocol: "network", Quote: "The Purger must coordinate with the Compactor."}
	stated := docmodel.Relation{From: "Purger", To: "Compactor", Kind: docmodel.KindRequired, Protocol: "gRPC", Quote: "The Purger calls the Compactor over gRPC."}
	comps := []docmodel.Component{dc("Purger", "internal/purger"), dc("Compactor", "internal/compactor")}
	typeOf := func(r docmodel.Relation, o Options) string {
		res := Diff(code, &docmodel.Model{Components: comps, Relations: []docmodel.Relation{r}}, o)
		if len(res.Findings) != 1 {
			t.Fatalf("findings = %+v", res.Findings)
		}
		return res.Findings[0].Type
	}
	if got := typeOf(inferred, Options{}); got != Unverifiable {
		t.Errorf("lenient, inferred protocol: %s, want UNVERIFIABLE", got)
	}
	if got := typeOf(inferred, Options{StrictProtocols: true}); got != MissingEdge {
		t.Errorf("strict, inferred protocol: %s, want MISSING_EDGE", got)
	}
	if got := typeOf(stated, Options{StrictProtocols: true}); got != Unverifiable {
		t.Errorf("strict, stated protocol: %s, want UNVERIFIABLE", got)
	}
}

func TestServices(t *testing.T) {
	code := &codemodel.Model{
		Components: []codemodel.Component{comp("pkg/rules"), comp("pkg/query"), comp("pkg/alert"), comp("pkg/dedup")},
		Edges:      []codemodel.Edge{edge("pkg/alert", "pkg/dedup", 3)},
	}
	comps := []docmodel.Component{dc("Rule", "pkg/alert", "pkg/rules"), dc("Querier", "pkg/query", "pkg/dedup")}
	discover := rel("Rule", "Querier", docmodel.KindRequired, "") // "Rule nodes discover query nodes"
	typeOf := func(code *codemodel.Model, comps []docmodel.Component, o Options) string {
		res := Diff(code, &docmodel.Model{Components: comps, Relations: []docmodel.Relation{discover}}, o)
		for _, f := range res.Findings {
			if f.Subject == "Rule -> Querier" {
				return f.Type
			}
		}
		return "satisfied"
	}
	services := Options{Services: []string{"pkg/rules", "pkg/quer*"}}
	noImport := &codemodel.Model{Components: code.Components}
	if got := typeOf(noImport, comps, Options{}); got != MissingEdge {
		t.Errorf("no services config: %s, want MISSING_EDGE", got)
	}
	if got := typeOf(noImport, comps, services); got != Unverifiable {
		t.Errorf("two services, no import: %s, want UNVERIFIABLE", got)
	}
	if got := typeOf(code, comps, services); got != "satisfied" {
		t.Errorf("two services with an import between their packages: %s, want satisfied", got)
	}
	strict := services
	strict.StrictProtocols = true
	if got := typeOf(noImport, comps, strict); got != MissingEdge {
		t.Errorf("services with strict_protocols: %s, want MISSING_EDGE (rules docs stay import-checked)", got)
	}
	libOnly := []docmodel.Component{dc("Rule", "pkg/rules"), dc("Querier", "pkg/dedup")}
	if got := typeOf(noImport, libOnly, services); got != MissingEdge {
		t.Errorf("target is not a service: %s, want MISSING_EDGE", got)
	}
}

func TestWiredByDeployable(t *testing.T) {
	bin := codemodel.Component{ID: "cmd/thanos", Deployable: true}
	at := func(from, to, file string) codemodel.Edge {
		return codemodel.Edge{From: from, To: to, Evidence: []codemodel.Location{loc(file, 1)}}
	}
	code := &codemodel.Model{
		Components: []codemodel.Component{bin, comp("pkg/rules"), comp("pkg/query"), comp("pkg/logging"), comp("pkg/shipper"), comp("ext:objstore")},
		Edges: []codemodel.Edge{
			at("cmd/thanos", "pkg/rules", "cmd/thanos/rule.go"),
			at("cmd/thanos", "pkg/logging", "cmd/thanos/rule.go"),
			at("cmd/thanos", "pkg/shipper", "cmd/thanos/rule.go"), // rule.go builds the uploader
			at("cmd/thanos", "pkg/query", "cmd/thanos/query.go"),
			at("cmd/thanos", "ext:objstore", "cmd/thanos/compact.go"),
			at("pkg/shipper", "ext:objstore", "pkg/shipper/shipper.go"),
		},
	}
	comps := []docmodel.Component{dc("Rule", "pkg/rules"), dc("Querier", "pkg/query"), dc("Logging", "pkg/logging"), dc("Object Storage", "ext:objstore")}
	typeOf := func(from string, o Options) string {
		r := rel(from, "Object Storage", docmodel.KindRequired, "")
		for _, f := range Diff(code, &docmodel.Model{Components: comps, Relations: []docmodel.Relation{r}}, o).Findings {
			if f.Subject == from+" -> Object Storage" {
				return f.Type
			}
		}
		return "satisfied"
	}
	svc := Options{Services: []string{"pkg/rules", "pkg/query"}}
	if got := typeOf("Rule", svc); got != "satisfied" {
		t.Errorf("service wired to object storage in the binary's rule.go: %s, want satisfied", got)
	}
	if got := typeOf("Rule", Options{}); got != MissingEdge {
		t.Errorf("without services config: %s, want MISSING_EDGE", got)
	}
	if got := typeOf("Logging", svc); got != MissingEdge {
		t.Errorf("utility imported by the same file: %s, want MISSING_EDGE", got)
	}
	if got := typeOf("Querier", svc); got != MissingEdge {
		t.Errorf("service whose wiring file has no object storage: %s, want MISSING_EDGE", got)
	}
	strict := svc
	strict.StrictProtocols = true
	if got := typeOf("Rule", strict); got != MissingEdge {
		t.Errorf("strict_protocols: %s, want MISSING_EDGE", got)
	}
}

func TestCompareBase(t *testing.T) {
	mk := func(id string, failing bool, files ...string) Finding {
		f := Finding{ID: id, Type: strings.SplitN(id, ":", 2)[0], Failing: failing}
		for i, file := range files {
			f.Code = append(f.Code, loc(file, 10+i))
		}
		return f
	}
	base := &Result{Findings: []Finding{
		mk("FORBIDDEN_EDGE:A -> B", true, "a/x.go"),
		mk("MISSING_EDGE:C -> D", true),
		mk("UNDOCUMENTED_EDGE:e->f", true, "e/y.go"),
		mk("MISSING_COMPONENT:Gone", true),
		mk("UNVERIFIABLE:G -> H", false),
	}}
	head := &Result{Findings: []Finding{
		mk("FORBIDDEN_EDGE:A -> B", true, "a/x.go", "a/new.go"), // a new violating file
		mk("MISSING_EDGE:C -> D", true),                         // unchanged
		{ID: "UNDOCUMENTED_EDGE:e->f", Type: UndocumentedEdge, Failing: true, Code: []codemodel.Location{loc("e/y.go", 99)}}, // line moved only
		mk("UNDOCUMENTED_EDGE:p->q", true, "p/z.go"), // new
	}}
	fixed := CompareBase(head, base)
	want := map[string]struct {
		status  string
		failing bool
	}{
		"FORBIDDEN_EDGE:A -> B":  {StatusWorsened, true},
		"MISSING_EDGE:C -> D":    {StatusExisting, false},
		"UNDOCUMENTED_EDGE:e->f": {StatusExisting, false},
		"UNDOCUMENTED_EDGE:p->q": {StatusNew, true},
	}
	for _, f := range head.Findings {
		w := want[f.ID]
		if f.Status != w.status || f.Failing != w.failing {
			t.Errorf("%s: status %q failing %v, want %q %v", f.ID, f.Status, f.Failing, w.status, w.failing)
		}
	}
	if ne := head.Findings[0].NewEvidence; len(ne) != 1 || ne[0].File != "a/new.go" {
		t.Errorf("new evidence = %v", ne)
	}
	if !reflect.DeepEqual(fixed, []string{"MISSING_COMPONENT:Gone"}) {
		t.Errorf("fixed = %v (a non-failing base finding is not 'fixed')", fixed)
	}
}
