package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stmic168/arch-drift/internal/docmodel"
	"github.com/stmic168/arch-drift/internal/drift"
)

// maxComponentSuggestions caps how many undocumented components are suggested.
const maxComponentSuggestions = 5

func firstEvidence(f drift.Finding) string {
	ev := f.Code
	if len(f.NewEvidence) > 0 {
		ev = f.NewEvidence
	}
	if len(ev) == 0 {
		return ""
	}
	s := "`" + ev[0].String() + "`"
	if len(ev) > 1 {
		s += fmt.Sprintf(" and %d more", len(ev)-1)
	}
	return s
}

func quoted(q string) string {
	return "\"" + docmodel.NormalizeSpace(q) + "\""
}

// Suggestions turns findings into concrete edits: what to change in the code
// or which doc sentence to update. Deterministic, no LLM.
func Suggestions(r *Report) []string {
	var out []string
	components := 0
	for _, f := range r.Findings {
		if f.Accepted != nil || f.Status == drift.StatusExisting {
			continue
		}
		switch {
		case f.Type == drift.ForbiddenEdge && f.Failing:
			out = append(out, fmt.Sprintf("**Code:** remove the dependency %s (%s), which the doc forbids. If the rule is outdated, change the doc sentence instead: %s", f.Subject, firstEvidence(f), quoted(f.DocQuote)))
		case f.Type == drift.MissingEdge && f.Failing:
			out = append(out, fmt.Sprintf("**Code or doc:** add the dependency %s, or update the doc sentence if it no longer holds: %s", f.Subject, quoted(f.DocQuote)))
		case f.Type == drift.MissingComponent && f.Failing:
			out = append(out, fmt.Sprintf("**Doc:** %q has no code. Implement it, mark it `\"external\": true` in the lock if it lives outside this repository, or remove it from the doc: %s", f.Subject, quoted(f.DocQuote)))
		case f.Type == drift.UndocumentedEdge:
			out = append(out, fmt.Sprintf("**Doc:** describe the dependency %s (%s), e.g. a sentence or a diagram arrow, or remove the import if it should not exist.", f.Subject, firstEvidence(f)))
		case f.Type == drift.UndocumentedComponent && f.Weight > 0 && components < maxComponentSuggestions:
			// Ranked application-first; Weight > 0 means it imports more components than import it.
			components++
			out = append(out, fmt.Sprintf("**Doc:** describe %s, which looks like an application component (%s). If it is a utility, add it to `undocumented_components.ignore`.", "`"+f.Subject+"`", strings.TrimSuffix(strings.SplitN(f.Message, "(", 2)[1], ") is not described in the doc.")))
		}
	}
	return out
}

func mermaidID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "c_" + b.String()
}

// AsBuilt draws the documented components with the dependencies the code
// actually has between them, under the doc's names, ready to paste into the doc.
func AsBuilt(r *Report) string {
	owners := map[string][]string{}
	ids := map[string][]string{}
	var names []string
	for _, c := range r.DocModel.Components {
		if len(c.CodeIDs) == 0 {
			continue
		}
		names = append(names, c.Name)
		ids[c.Name] = c.CodeIDs
		for _, id := range c.CodeIDs {
			owners[id] = append(owners[id], c.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	type pair struct{ from, to string }
	deps, proto := map[pair]bool{}, map[pair]bool{}
	add := func(set map[pair]bool, from, to string) {
		for _, a := range owners[from] {
			for _, b := range owners[to] {
				if a != b {
					set[pair{a, b}] = true
				}
			}
		}
	}
	for _, e := range r.Code.Edges {
		add(deps, e.From, e.To)
	}
	for _, e := range r.Code.ProtocolEdges {
		add(proto, e.From, e.To)
	}
	var b strings.Builder
	// The marker and the code IDs in the labels let archdrift read the
	// diagram back by code once it is pasted into the doc (--no-llm).
	b.WriteString("```mermaid\nflowchart LR\n  " + docmodel.DiagramMarker + "\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %s[\"%s<br/>%s\"]\n", mermaidID(n), label(n), strings.Join(ids[n], ", "))
	}
	lines := []string{}
	for p := range deps {
		lines = append(lines, fmt.Sprintf("  %s --> %s", mermaidID(p.from), mermaidID(p.to)))
	}
	for p := range proto {
		if !deps[p] {
			lines = append(lines, fmt.Sprintf("  %s -. gRPC client .-> %s", mermaidID(p.from), mermaidID(p.to)))
		}
	}
	sort.Strings(lines)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n```\n")
	return b.String()
}
