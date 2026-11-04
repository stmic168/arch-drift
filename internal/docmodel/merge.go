package docmodel

import (
	"fmt"
	"sort"
	"strings"
)

// Change describes how a re-lock differs from the previous, reviewed lock.
type Change struct {
	Kind   string `json:"kind"` // added, removed, conflict, kept
	Claim  string `json:"claim"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// Change kinds.
const (
	ChangeAdded    = "added"    // new in the doc, taken from the fresh extraction
	ChangeRemoved  = "removed"  // its quote is gone from the doc
	ChangeConflict = "conflict" // reviewed claim kept; the model now suggests something else
	ChangeKept     = "kept"     // still in the doc but not extracted this time; kept from the reviewed lock
)

func relKey(r Relation) string { return r.From + "\x00" + r.To + "\x00" + r.Kind }

// MergeLock combines the previous, reviewed doc model with a fresh extraction
// of the current docs, so a doc edit does not throw away the architect's
// review. Claims of the previous lock that still verify against the current
// docs and code IDs win; the fresh extraction only adds what is new. Both
// inputs are verified first; the result is verified too.
func MergeLock(prev, fresh *Model, docs []Doc, ids []string) (*Model, []Change) {
	oldV, oldDropped := Verify(prev, docs, ids)
	newV, _ := Verify(fresh, docs, ids)
	var changes []Change

	oldComp := map[string]Component{}
	for _, c := range oldV.Components {
		oldComp[c.Name] = c
	}
	merged := &Model{Components: []Component{}, Relations: []Relation{}}
	seen := map[string]bool{}
	for _, c := range newV.Components {
		seen[c.Name] = true
		o, ok := oldComp[c.Name]
		if !ok {
			merged.Components = append(merged.Components, c)
			changes = append(changes, Change{Kind: ChangeAdded, Claim: "component", Name: c.Name, Detail: mapping(c)})
			continue
		}
		merged.Components = append(merged.Components, o)
		if mapping(o) != mapping(c) {
			changes = append(changes, Change{Kind: ChangeConflict, Claim: "component", Name: c.Name,
				Detail: fmt.Sprintf("kept reviewed %s; model now suggests %s", mapping(o), mapping(c))})
		}
	}
	for _, o := range oldV.Components {
		if !seen[o.Name] {
			merged.Components = append(merged.Components, o)
			changes = append(changes, Change{Kind: ChangeKept, Claim: "component", Name: o.Name, Detail: mapping(o)})
		}
	}

	oldRel := map[string]Relation{}
	for _, r := range oldV.Relations {
		oldRel[relKey(r)] = r
	}
	seenRel := map[string]bool{}
	for _, r := range newV.Relations {
		k := relKey(r)
		seenRel[k] = true
		o, ok := oldRel[k]
		if !ok {
			merged.Relations = append(merged.Relations, r)
			changes = append(changes, Change{Kind: ChangeAdded, Claim: "relation", Name: r.Kind + " " + r.From + " -> " + r.To, Detail: protoLabel(r.Protocol)})
			continue
		}
		merged.Relations = append(merged.Relations, o)
		if o.Protocol != r.Protocol {
			changes = append(changes, Change{Kind: ChangeConflict, Claim: "relation", Name: r.Kind + " " + r.From + " -> " + r.To,
				Detail: fmt.Sprintf("kept reviewed protocol %s; model now suggests %s", protoLabel(o.Protocol), protoLabel(r.Protocol))})
		}
	}
	for _, o := range oldV.Relations {
		if !seenRel[relKey(o)] {
			merged.Relations = append(merged.Relations, o)
			changes = append(changes, Change{Kind: ChangeKept, Claim: "relation", Name: o.Kind + " " + o.From + " -> " + o.To, Detail: protoLabel(o.Protocol)})
		}
	}

	for _, d := range oldDropped {
		if d.Reason == "quote not found in doc" {
			changes = append(changes, Change{Kind: ChangeRemoved, Claim: d.Claim, Name: d.Name, Detail: "quote no longer in the doc"})
		}
	}
	out, _ := Verify(merged, docs, ids) // drops relations whose components went away
	order := map[string]int{ChangeConflict: 0, ChangeAdded: 1, ChangeRemoved: 2, ChangeKept: 3}
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if order[a.Kind] != order[b.Kind] {
			return order[a.Kind] < order[b.Kind]
		}
		if a.Claim != b.Claim {
			return a.Claim < b.Claim
		}
		return a.Name < b.Name
	})
	return out, changes
}

func mapping(c Component) string {
	ids := "(no code)"
	if len(c.CodeIDs) > 0 {
		ids = strings.Join(c.CodeIDs, ", ")
	}
	if c.External {
		ids += " [external]"
	}
	return ids
}

func protoLabel(p string) string {
	if p == "" {
		return "(none)"
	}
	return p
}
