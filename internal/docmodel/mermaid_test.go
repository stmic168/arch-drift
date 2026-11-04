package docmodel

import (
	"reflect"
	"testing"
)

const diagramDoc = "# Doc\n\n" +
	"```mermaid\nflowchart LR\n  a --> b\n```\n\n" + // unmarked: left to the LLM
	"```mermaid\n" +
	"flowchart LR\n" +
	"  %% archdrift: dependencies\n" +
	"  subgraph core\n" +
	"  api[\"API Gateway<br/>cmd/api\"]\n" +
	"  end\n" +
	"  db[(\"PostgreSQL<br/>ext:postgres, `ext:pgx`\")]\n" +
	"  api -->|in-process| orders(Orders) & catalog{{Catalog}}\n" +
	"  api -- must not --> store[Store]:::bad\n" +
	"  orders --> store --> db\n" +
	"  worker([Worker]) -. HTTP .-> api\n" +
	"  worker == \"Kafka\" ==> queue\n" +
	"  classDef bad stroke:#f00\n" +
	"  linkStyle 0 stroke:#0f0\n" +
	"```\n"

func TestParseDiagrams(t *testing.T) {
	m, found := ParseDiagrams(diagramDoc, nil)
	if !found {
		t.Fatal("marked block not found")
	}
	var comps []string
	ids := map[string][]string{}
	for _, c := range m.Components {
		comps = append(comps, c.Name)
		ids[c.Name] = c.CodeIDs
	}
	if want := []string{"API Gateway", "PostgreSQL", "Orders", "Catalog", "Store", "Worker", "queue"}; !reflect.DeepEqual(comps, want) {
		t.Errorf("components = %v, want %v", comps, want)
	}
	if got := ids["PostgreSQL"]; !reflect.DeepEqual(got, []string{"ext:postgres", "ext:pgx"}) {
		t.Errorf("PostgreSQL ids = %v", got)
	}
	if got := ids["API Gateway"]; !reflect.DeepEqual(got, []string{"cmd/api"}) {
		t.Errorf("API Gateway ids = %v", got)
	}
	var rels []string
	for _, r := range m.Relations {
		rels = append(rels, r.From+" -> "+r.To+" "+r.Kind+" "+r.Protocol)
	}
	want := []string{
		"API Gateway -> Orders required in-process",
		"API Gateway -> Catalog required in-process",
		"API Gateway -> Store forbidden ",
		"Orders -> Store required ",
		"Store -> PostgreSQL required ",
		"Worker -> API Gateway required HTTP",
		"Worker -> queue required Kafka",
	}
	if !reflect.DeepEqual(rels, want) {
		t.Errorf("relations:\n got  %q\n want %q", rels, want)
	}
	// Every quote is a doc line, so the verifier keeps every claim.
	if _, dropped := Verify(m, []Doc{{Text: diagramDoc}}, []string{"cmd/api", "ext:postgres", "ext:pgx"}); len(dropped) != 0 {
		t.Errorf("verifier dropped diagram claims: %+v", dropped)
	}
}

func TestParseDiagramsNeedsMarker(t *testing.T) {
	if _, found := ParseDiagrams("```mermaid\nflowchart LR\n  producer --> queue --> consumer\n```\n", nil); found {
		t.Error("an unmarked diagram was read as dependencies")
	}
}

func TestMergeDiagrams(t *testing.T) {
	llm := &Model{
		Components: []Component{{Name: "API gateway", Quote: "q"}, {Name: "Orders", CodeIDs: []string{"internal/orders"}, Quote: "q"}},
		Relations:  []Relation{{From: "API gateway", To: "Orders", Kind: KindRequired, Quote: "q"}},
	}
	d := &Model{
		Components: []Component{
			{Name: "API  Gateway", CodeIDs: []string{"cmd/api"}, Quote: "d1"},
			{Name: "Orders", CodeIDs: []string{"internal/other"}, Quote: "d2"},
			{Name: "Store", CodeIDs: []string{"internal/store"}, Quote: "d3"},
		},
		Relations: []Relation{
			{From: "API  Gateway", To: "Orders", Kind: KindForbidden, Quote: "d4"}, // pair already in the LLM model
			{From: "API  Gateway", To: "Store", Kind: KindForbidden, Quote: "d5"},
		},
	}
	m := MergeDiagrams(llm, d)
	if len(m.Components) != 3 || !reflect.DeepEqual(m.Components[0].CodeIDs, []string{"cmd/api"}) ||
		!reflect.DeepEqual(m.Components[1].CodeIDs, []string{"internal/orders"}) || m.Components[2].Name != "Store" {
		t.Errorf("components: %+v", m.Components)
	}
	if len(m.Relations) != 2 || m.Relations[0].Kind != KindRequired ||
		m.Relations[1] != (Relation{From: "API gateway", To: "Store", Kind: KindForbidden, Quote: "d5"}) {
		t.Errorf("relations: %+v", m.Relations)
	}
	if len(llm.Components[0].CodeIDs) != 0 || len(llm.Relations) != 1 {
		t.Error("MergeDiagrams modified its input")
	}
}
