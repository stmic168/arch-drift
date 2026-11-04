package docmodel

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// DiagramMarker opts a Mermaid flowchart into deterministic reading. In a
// block holding this line, every arrow means "depends on": A --> B is a
// required relation A -> B, and an arrow labelled "must not" (or "never",
// "forbidden", "not allowed") is a forbidden one. Other labels are the
// protocol. Without the marker a diagram is left to the LLM, because arrows
// in ordinary diagrams often show data flow (producer --> queue --> consumer)
// rather than dependency.
const DiagramMarker = "%% archdrift: dependencies"

// A node label may carry code IDs after a line break: "Orders<br/>internal/orders".
// IDs are separated by commas or further line breaks.
var (
	brRe        = regexp.MustCompile(`(?i)<br\s*/?>`)
	ampRe       = regexp.MustCompile(`^\s*&`)
	forbiddenRe = regexp.MustCompile(`(?i)\b(must not|must never|never|forbidden|not allowed)\b`)
	// One node reference: id, optionally followed by a shape with a label.
	nodeRe = regexp.MustCompile(`^\s*([A-Za-z0-9_][\w.-]*)\s*(\[\[.*?\]\]|\[\(.*?\)\]|\(\[.*?\]\)|\(\(.*?\)\)|\[.*?\]|\(.*?\)|\{\{.*?\}\}|\{.*?\})?`)
	// An arrow with an optional label: -->|x|, -- x -->, -.->, -. x .->, ==>, == x ==>.
	arrowRe = regexp.MustCompile(`^\s*(?:--\s*([^->|][^>|]*?)\s*-->|-\.\s*([^.>|][^>|]*?)\s*\.->|==\s*([^=>|][^>|]*?)\s*==>|-{2,}>|-\.+->|={2,}>)(?:\s*\|([^|]*)\|)?`)
)

// ParseDiagrams reads every Mermaid block of doc that carries DiagramMarker.
// Quotes are the source lines, so the verifier accepts them; code IDs not in
// ids are kept for the verifier to drop and list. found is false when the doc
// has no marked block.
func ParseDiagrams(doc string, ids []string) (m *Model, found bool) {
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	m = &Model{Components: []Component{}, Relations: []Relation{}}
	for _, block := range mermaidBlocks(doc) {
		marked := false
		for _, l := range block {
			if strings.TrimSpace(l) == DiagramMarker {
				marked = true
			}
		}
		if !marked {
			continue
		}
		found = true
		parseFlowchart(block, m)
	}
	return m, found
}

func mermaidBlocks(doc string) [][]string {
	var blocks [][]string
	var cur []string
	in := false
	for _, l := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case !in && strings.HasPrefix(t, "```mermaid"):
			in, cur = true, nil
		case in && strings.HasPrefix(t, "```"):
			in = false
			blocks = append(blocks, cur)
		case in:
			cur = append(cur, l)
		}
	}
	return blocks
}

type node struct {
	name   string
	ids    []string
	quote  string // line where the label was given (or first seen)
	labels bool
}

func parseFlowchart(lines []string, m *Model) {
	nodes := map[string]*node{}
	var order []string
	seen := func(id, label, line string) {
		n := nodes[id]
		if n == nil {
			n = &node{name: id, quote: line}
			nodes[id] = n
			order = append(order, id)
		}
		if label != "" && !n.labels {
			parts := brRe.Split(label, -1)
			n.name = strings.TrimSpace(parts[0])
			for _, p := range parts[1:] {
				for _, code := range strings.Split(p, ",") {
					if code = strings.Trim(strings.TrimSpace(code), "`"); code != "" {
						n.ids = append(n.ids, code)
					}
				}
			}
			n.quote, n.labels = line, true
		}
	}
	type edge struct{ from, to, label, line string }
	var edges []edge
	for _, raw := range lines {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") || isDirective(l) {
			continue
		}
		line := strings.TrimSpace(raw)
		rest := l
		var from []string // the node group before the pending arrow
		label := ""
		for {
			group, n := nodeGroup(rest)
			if len(group) == 0 {
				break
			}
			for _, g := range group {
				seen(g[0], g[1], line)
			}
			for _, f := range from {
				for _, g := range group {
					edges = append(edges, edge{from: f, to: g[0], label: label, line: line})
				}
			}
			rest = rest[n:]
			am := arrowRe.FindStringSubmatch(rest)
			if am == nil {
				break
			}
			label = strings.Trim(strings.TrimSpace(am[1]+am[2]+am[3]+am[4]), `"`)
			from = from[:0]
			for _, g := range group {
				from = append(from, g[0])
			}
			rest = rest[len(am[0]):]
		}
	}
	for _, id := range order {
		n := nodes[id]
		m.Components = append(m.Components, Component{Name: n.name, CodeIDs: n.ids, Quote: n.quote})
	}
	for _, e := range edges {
		r := Relation{From: nodes[e.from].name, To: nodes[e.to].name, Kind: KindRequired, Quote: e.line}
		if forbiddenRe.MatchString(e.label) {
			r.Kind = KindForbidden
		} else {
			r.Protocol = e.label
		}
		m.Relations = append(m.Relations, r)
	}
}

// nodeGroup reads "a[A] & b[B]" at the start of s: each node's id and label,
// and how many bytes were consumed.
func nodeGroup(s string) (group [][2]string, n int) {
	for {
		mm := nodeRe.FindStringSubmatch(s[n:])
		if mm == nil {
			return group, n
		}
		group = append(group, [2]string{mm[1], shapeLabel(mm[2])})
		n += len(mm[0])
		amp := ampRe.FindString(s[n:])
		if amp == "" {
			return group, n
		}
		n += len(amp)
	}
}

func isDirective(l string) bool {
	f := strings.Fields(l)
	switch f[0] {
	case "flowchart", "graph", "subgraph", "end", "classDef", "class", "style", "linkStyle", "click", "direction":
		return true
	}
	return false
}

// shapeLabel strips the shape brackets and optional quotes from a node label.
func shapeLabel(s string) string {
	if s == "" {
		return ""
	}
	s = strings.TrimLeft(s, "[({")
	s = strings.TrimRight(s, "])}")
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
}

// MergeDiagrams adds the claims of marked diagrams to an LLM doc model, so
// the diagram's relations do not depend on how the model read the doc that
// day. A diagram node matches a model component by name (case and spacing
// ignored); its code IDs fill a matched component that has none, and an
// unmatched node becomes a new component. A diagram relation is added unless
// the model already has a relation between the same two components.
func MergeDiagrams(m, d *Model) *Model {
	out := &Model{Components: append([]Component(nil), m.Components...), Relations: append([]Relation(nil), m.Relations...)}
	key := func(s string) string { return strings.ToLower(NormalizeSpace(s)) }
	byName := map[string]int{}
	for i, c := range out.Components {
		byName[key(c.Name)] = i
	}
	rename := map[string]string{}
	for _, c := range d.Components {
		if i, ok := byName[key(c.Name)]; ok {
			rename[c.Name] = out.Components[i].Name
			if len(out.Components[i].CodeIDs) == 0 && len(c.CodeIDs) > 0 {
				out.Components[i].CodeIDs = c.CodeIDs
			}
			continue
		}
		byName[key(c.Name)] = len(out.Components)
		rename[c.Name] = c.Name
		out.Components = append(out.Components, c)
	}
	type pair struct{ a, b string }
	have := map[pair]bool{}
	for _, r := range out.Relations {
		have[pair{key(r.From), key(r.To)}] = true
	}
	for _, r := range d.Relations {
		r.From, r.To = rename[r.From], rename[r.To]
		if p := (pair{key(r.From), key(r.To)}); !have[p] {
			have[p] = true
			out.Relations = append(out.Relations, r)
		}
	}
	return out
}

// DiagramExtractor builds the doc model from marked diagrams alone: no API call.
type DiagramExtractor struct{}

func (DiagramExtractor) Extract(_ context.Context, doc string, ids []string) (*Model, Usage, error) {
	m, found := ParseDiagrams(doc, ids)
	if !found {
		return nil, Usage{}, errors.New("--no-llm: the doc has no Mermaid block with the line " + DiagramMarker)
	}
	return m, Usage{Source: "diagram"}, nil
}
