package docmodel

import (
	"reflect"
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
	out, dropped := Verify(in, doc, ids)

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
