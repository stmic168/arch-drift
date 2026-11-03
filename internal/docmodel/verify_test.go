package docmodel

import (
	"reflect"
	"strings"
	"testing"
)

const doc = `# Design

The **API** serves HTTP traffic.
The  Store
owns the database.

The API must never access the Store directly.
`

var ids = []string{"cmd/api", "internal/store"}

func TestVerifyDropsFakeQuotesAndUnknownIDs(t *testing.T) {
	in := &Model{
		Components: []Component{
			{Name: "API", CodeIDs: []string{"cmd/api", "internal/ghost"}, Quote: "The **API** serves HTTP traffic."},
			// Whitespace differs from the doc (line break, double space): still verbatim.
			{Name: "Store", CodeIDs: []string{"internal/store", "internal/store"}, Quote: "The Store owns the\tdatabase."},
			{Name: "Cache", CodeIDs: []string{"ext:redis"}, Quote: "Redis caches sessions."},
			{Name: "Empty", CodeIDs: nil, Quote: "   "},
		},
		Relations: []Relation{
			{From: "API", To: "Store", Kind: KindForbidden, Quote: "The API must never access the Store directly."},
			{From: "API", To: "Store", Kind: KindRequired, Quote: "The API calls the Store."},
			{From: "API", To: "Cache", Kind: KindRequired, Quote: "The **API** serves HTTP traffic."},
			{From: "API", To: "Store", Kind: "maybe", Quote: "The API must never access the Store directly."},
		},
	}
	out, dropped := Verify(in, []Doc{{Path: "design.md", Text: doc}}, ids)

	wantComps := []Component{
		{Name: "API", CodeIDs: []string{"cmd/api"}, Quote: "The **API** serves HTTP traffic."},
		{Name: "Store", CodeIDs: []string{"internal/store"}, Quote: "The Store owns the\tdatabase."},
	}
	if !reflect.DeepEqual(out.Components, wantComps) {
		t.Errorf("components = %+v", out.Components)
	}
	if len(out.Relations) != 1 || out.Relations[0].Kind != KindForbidden {
		t.Errorf("relations = %+v", out.Relations)
	}

	type d struct{ claim, name, detail, reason string }
	var got []d
	for _, x := range dropped {
		got = append(got, d{x.Claim, x.Name, x.Detail, x.Reason})
	}
	want := []d{
		{"code_id", "API", "internal/ghost", "unknown code ID"},
		{"component", "Cache", "", "quote not found in doc"},
		{"component", "Empty", "", "quote not found in doc"},
		{"relation", "API -> Cache", "required", "unknown or dropped component: Cache"},
		{"relation", "API -> Store", "maybe", "invalid kind"},
		{"relation", "API -> Store", "required", "quote not found in doc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dropped:\n got  %v\n want %v", got, want)
	}
}

func TestNormalizeSpace(t *testing.T) {
	if got := NormalizeSpace("  a \n\t b  c d "); got != "a b c d" {
		t.Errorf("got %q", got)
	}
}

func TestVerifyMultipleDocsRecordsSource(t *testing.T) {
	docs := []Doc{{Path: "docs/a.md", Text: "The API serves HTTP."}, {Path: "docs/b.md", Text: "The Store owns the DB.\nThe API uses the Store."}}
	in := &Model{
		Components: []Component{{Name: "API", CodeIDs: []string{"cmd/api"}, Quote: "The API serves HTTP."}, {Name: "Store", CodeIDs: []string{"internal/store"}, Quote: "The Store owns the DB."}},
		Relations:  []Relation{{From: "API", To: "Store", Kind: KindRequired, Quote: "The API uses the Store."}, {From: "API", To: "Store", Kind: KindForbidden, Quote: "The API serves HTTP. The Store owns the DB."}},
	}
	out, dropped := Verify(in, docs, ids)
	if out.Components[0].Source != "docs/a.md" || out.Components[1].Source != "docs/b.md" || out.Relations[0].Source != "docs/b.md" {
		t.Errorf("sources: %+v %+v", out.Components, out.Relations)
	}
	// A quote spanning two files is not verbatim in either.
	if len(dropped) != 1 || dropped[0].Claim != "relation" {
		t.Errorf("dropped = %+v", dropped)
	}
	if got := Combine(docs[:1]); got != docs[0].Text {
		t.Errorf("single doc must pass through unchanged: %q", got)
	}
	if got := Combine(docs); !strings.Contains(got, "<!-- archdrift file: docs/b.md -->") {
		t.Errorf("combined: %q", got)
	}
}
