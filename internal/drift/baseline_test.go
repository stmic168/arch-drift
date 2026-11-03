package drift

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func findings(ids ...string) *Result {
	r := &Result{}
	for _, id := range ids {
		r.Findings = append(r.Findings, Finding{ID: id, Type: strings.SplitN(id, ":", 2)[0], Failing: !strings.HasPrefix(id, Unverifiable)})
	}
	return r
}

func TestApplyBaseline(t *testing.T) {
	res := findings("FORBIDDEN_EDGE:A -> B", "MISSING_EDGE:C -> D", "UNDOCUMENTED_COMPONENT:pkg/x")
	bl := &Baseline{Version: 1, Accepted: []Accepted{
		{ID: "FORBIDDEN_EDGE:A -> B", Reason: "legacy, ARCH-1", Expires: "2026-12-31"},
		{ID: "MISSING_EDGE:C -> D", Reason: "planned", Expires: "2026-11-01"},
		{ID: "UNDOCUMENTED_COMPONENT:pkg/x", Reason: TodoReason},
		{ID: "MISSING_COMPONENT:Gone", Reason: "old"},
	}}
	notes := ApplyBaseline(res, bl, "2026-11-02")

	if f := res.Findings[0]; f.Failing || f.Accepted == nil || f.Accepted.Reason != "legacy, ARCH-1" {
		t.Errorf("accepted finding: %+v", f)
	}
	if f := res.Findings[1]; !f.Failing || f.Accepted != nil {
		t.Errorf("expired entry must fail again: %+v", f)
	}
	if f := res.Findings[2]; f.Failing {
		t.Errorf("entry without expiry stays accepted: %+v", f)
	}
	if !res.Failing() {
		t.Error("result must still fail because of the expired entry")
	}
	want := []string{
		"Baseline entry `MISSING_EDGE:C -> D` expired on 2026-11-01; the finding fails again.",
		"Baseline entry `UNDOCUMENTED_COMPONENT:pkg/x` has no real reason yet (\"" + TodoReason + "\").",
		"Baseline entry `MISSING_COMPONENT:Gone` no longer matches any finding; remove it from the baseline.",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes:\n got  %q\n want %q", notes, want)
	}
	// Expiry day itself is still accepted.
	res = findings("MISSING_EDGE:C -> D")
	ApplyBaseline(res, bl, "2026-11-01")
	if res.Failing() {
		t.Error("an entry is valid through its expiry date")
	}
}

func TestNewBaselineKeepsReasons(t *testing.T) {
	res := findings("FORBIDDEN_EDGE:A -> B", "MISSING_EDGE:C -> D", "UNVERIFIABLE:E -> F")
	prev := &Baseline{Version: 1, Accepted: []Accepted{{ID: "FORBIDDEN_EDGE:A -> B", Reason: "legacy", Expires: "2027-06-30"}, {ID: "MISSING_COMPONENT:Gone", Reason: "old"}}}
	ApplyBaseline(res, prev, "2026-11-02")
	bl := NewBaseline(res, prev, "2026-11-02", 90)
	want := []Accepted{
		{ID: "FORBIDDEN_EDGE:A -> B", Reason: "legacy", Expires: "2027-06-30"},
		{ID: "MISSING_EDGE:C -> D", Reason: TodoReason, Expires: "2027-01-31"},
	}
	if !reflect.DeepEqual(bl.Accepted, want) {
		t.Errorf("accepted = %+v", bl.Accepted)
	}
	p := filepath.Join(t.TempDir(), BaselineFile)
	if err := WriteBaseline(p, bl); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"FORBIDDEN_EDGE:A -> B"`) {
		t.Errorf("arrows must not be HTML-escaped:\n%s", b)
	}
	got, err := LoadBaseline(p)
	if err != nil || !reflect.DeepEqual(got.Accepted, want) {
		t.Errorf("round trip: %v %+v", err, got)
	}
}

func TestLoadBaselineRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field": `{"version":1,"accepted":[],"x":1}`,
		"version":       `{"version":2,"accepted":[]}`,
		"empty id":      `{"version":1,"accepted":[{"id":"","reason":"r"}]}`,
		"no reason":     `{"version":1,"accepted":[{"id":"A","reason":"  "}]}`,
		"duplicate":     `{"version":1,"accepted":[{"id":"A","reason":"r"},{"id":"A","reason":"r"}]}`,
		"bad date":      `{"version":1,"accepted":[{"id":"A","reason":"r","expires":"31/12/2026"}]}`,
	}
	for name, body := range cases {
		p := filepath.Join(t.TempDir(), "b.json")
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := LoadBaseline(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
