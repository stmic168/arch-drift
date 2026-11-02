package docmodel

import (
	"sort"
	"strings"
	"unicode"
)

// Dropped is a claim rejected by the verifier.
type Dropped struct {
	Claim  string `json:"claim"` // "component", "code_id", "relation"
	Name   string `json:"name"`  // component name, or "from -> to" for relations
	Detail string `json:"detail,omitempty"`
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
}

// NormalizeSpace collapses every run of whitespace into one space and trims.
func NormalizeSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// Verify keeps only claims backed by a verbatim quote from doc (after
// whitespace normalization) and code IDs that exist in ids. Everything
// removed is returned as Dropped, in deterministic order.
func Verify(m *Model, doc string, ids []string) (*Model, []Dropped) {
	normDoc := NormalizeSpace(doc)
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	quoteOK := func(q string) bool {
		nq := NormalizeSpace(q)
		return nq != "" && strings.Contains(normDoc, nq)
	}

	out := &Model{Components: []Component{}, Relations: []Relation{}}
	var dropped []Dropped
	names := map[string]bool{}

	for _, c := range m.Components {
		name := strings.TrimSpace(c.Name)
		switch {
		case name == "":
			dropped = append(dropped, Dropped{Claim: "component", Name: c.Name, Quote: c.Quote, Reason: "empty component name"})
			continue
		case !quoteOK(c.Quote):
			dropped = append(dropped, Dropped{Claim: "component", Name: name, Quote: c.Quote, Reason: "quote not found in doc"})
			continue
		case names[name]:
			dropped = append(dropped, Dropped{Claim: "component", Name: name, Quote: c.Quote, Reason: "duplicate component name"})
			continue
		}
		kept := Component{Name: name, Quote: c.Quote, CodeIDs: []string{}}
		seen := map[string]bool{}
		for _, id := range c.CodeIDs {
			if !known[id] {
				dropped = append(dropped, Dropped{Claim: "code_id", Name: name, Detail: id, Quote: c.Quote, Reason: "unknown code ID"})
				continue
			}
			if !seen[id] {
				seen[id] = true
				kept.CodeIDs = append(kept.CodeIDs, id)
			}
		}
		sort.Strings(kept.CodeIDs)
		names[name] = true
		out.Components = append(out.Components, kept)
	}

	for _, r := range m.Relations {
		from, to := strings.TrimSpace(r.From), strings.TrimSpace(r.To)
		d := Dropped{Claim: "relation", Name: from + " -> " + to, Detail: r.Kind, Quote: r.Quote}
		switch {
		case r.Kind != KindRequired && r.Kind != KindForbidden:
			d.Reason = "invalid kind"
		case !quoteOK(r.Quote):
			d.Reason = "quote not found in doc"
		case !names[from]:
			d.Reason = "unknown or dropped component: " + from
		case !names[to]:
			d.Reason = "unknown or dropped component: " + to
		case from == to:
			d.Reason = "self relation"
		}
		if d.Reason != "" {
			dropped = append(dropped, d)
			continue
		}
		out.Relations = append(out.Relations, Relation{From: from, To: to, Kind: r.Kind, Protocol: strings.TrimSpace(r.Protocol), Quote: r.Quote})
	}

	sort.SliceStable(out.Components, func(i, j int) bool { return out.Components[i].Name < out.Components[j].Name })
	sort.SliceStable(out.Relations, func(i, j int) bool {
		a, b := out.Relations[i], out.Relations[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	sort.SliceStable(dropped, func(i, j int) bool {
		a, b := dropped[i], dropped[j]
		if a.Claim != b.Claim {
			return a.Claim < b.Claim
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Detail < b.Detail
	})
	return out, dropped
}
