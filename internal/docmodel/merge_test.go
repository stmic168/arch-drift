package docmodel

import (
	"reflect"
	"testing"
)

func TestMergeLock(t *testing.T) {
	docs := []Doc{{Path: "design.md", Text: "The API serves HTTP.\nThe Store owns the DB.\nThe Cache speeds reads.\nThe API uses the Store.\nThe API calls the Cache over gRPC.\n"}}
	ids := []string{"cmd/api", "internal/store", "internal/cache", "internal/storage"}
	prev := &Model{ // reviewed: the architect fixed Store's mapping by hand
		Components: []Component{
			{Name: "API", CodeIDs: []string{"cmd/api"}, Quote: "The API serves HTTP."},
			{Name: "Store", CodeIDs: []string{"internal/store", "internal/storage"}, Quote: "The Store owns the DB."},
			{Name: "Queue", CodeIDs: []string{}, Quote: "The Queue buffers jobs."}, // removed from the doc since
			{Name: "Cache", CodeIDs: []string{"internal/cache"}, Quote: "The Cache speeds reads."},
		},
		Relations: []Relation{
			{From: "API", To: "Store", Kind: KindRequired, Quote: "The API uses the Store."},
			{From: "API", To: "Cache", Kind: KindRequired, Protocol: "gRPC", Quote: "The API calls the Cache over gRPC."},
		},
	}
	fresh := &Model{ // the model re-extracted the edited doc
		Components: []Component{
			{Name: "API", CodeIDs: []string{"cmd/api"}, Quote: "The API serves HTTP."},
			{Name: "Store", CodeIDs: []string{"internal/store"}, Quote: "The Store owns the DB."},
		},
		Relations: []Relation{
			{From: "API", To: "Store", Kind: KindRequired, Protocol: "network", Quote: "The API uses the Store."},
		},
	}
	merged, changes := MergeLock(prev, fresh, docs, ids)

	var names []string
	for _, c := range merged.Components {
		names = append(names, c.Name+"="+mapping(c))
	}
	wantNames := []string{"API=cmd/api", "Cache=internal/cache", "Store=internal/storage, internal/store"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("components = %v, want %v", names, wantNames)
	}
	if len(merged.Relations) != 2 || merged.Relations[1].Protocol != "" {
		t.Errorf("relations = %+v (reviewed protocol for API -> Store must be kept)", merged.Relations)
	}
	var got []string
	for _, c := range changes {
		got = append(got, c.Kind+" "+c.Claim+" "+c.Name)
	}
	want := []string{
		"conflict component Store",
		"conflict relation required API -> Store",
		"removed component Queue",
		"kept component Cache",
		"kept relation required API -> Cache",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes:\n got  %q\n want %q", got, want)
	}
}
