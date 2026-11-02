// Package report renders the drift result as Markdown and JSON. Output is a
// pure function of its inputs (no timestamps, sorted keys) so two runs on the
// same inputs produce identical bytes.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/docmodel"
	"github.com/stmic168/arch-drift/internal/drift"
)

// Price is USD per million tokens.
type Price struct{ Input, Output float64 }

// prices are Anthropic first-party list prices per MTok.
var prices = map[string]Price{
	"claude-fable-5-1":  {10, 50},
	"claude-fable-5":    {10, 50},
	"claude-opus-5-5":   {4, 20},
	"claude-opus-5":     {5, 25},
	"claude-opus-4-8":   {5, 25},
	"claude-opus-4-7":   {5, 25},
	"claude-opus-4-6":   {5, 25},
	"claude-sonnet-5-5": {2, 10},
	"claude-sonnet-5":   {2, 10},
	"claude-sonnet-4-6": {3, 15},
	"claude-haiku-5-5":  {0.10, 0.50},
	"claude-haiku-4-5":  {1, 5},
}

// Cost estimates the USD cost of a usage record. ok is false for unknown models.
// Cache writes are priced at 1.25x input (5-minute TTL) and cache reads at 0.1x.
func Cost(u docmodel.Usage) (usd float64, ok bool) {
	if u.Source != "api" {
		return 0, true
	}
	p, ok := prices[u.Model]
	if !ok {
		return 0, false
	}
	in := float64(u.InputTokens)*p.Input + float64(u.CacheCreationInputTokens)*p.Input*1.25 + float64(u.CacheReadInputTokens)*p.Input*0.1
	return (in + float64(u.OutputTokens)*p.Output) / 1e6, true
}

// UsageReport is the usage section of the report.
type UsageReport struct {
	docmodel.Usage
	CostUSD   *float64 `json:"cost_usd"`
	CostNote  string   `json:"cost_note"`
	Requested string   `json:"requested_model"`
}

// Summary counts findings.
type Summary struct {
	Drift    bool           `json:"drift"`
	Failing  int            `json:"failing"`
	ByType   map[string]int `json:"by_type"`
	Dropped  int            `json:"dropped_claims"`
	ExitCode int            `json:"exit_code"`
}

// Report is the JSON document.
type Report struct {
	Tool     string             `json:"tool"`
	Repo     string             `json:"repo"`
	Doc      string             `json:"doc"`
	Module   string             `json:"module"`
	Summary  Summary            `json:"summary"`
	Findings []drift.Finding    `json:"findings"`
	Dropped  []docmodel.Dropped `json:"dropped_claims"`
	Usage    UsageReport        `json:"usage"`
	Code     *codemodel.Model   `json:"code_model"`
	DocModel *docmodel.Model    `json:"doc_model"`
	Graph    []drift.GraphEdge  `json:"graph"`
	Missing  []string           `json:"missing_nodes"`
	Undoc    []string           `json:"undocumented_nodes"`
	Lock     string             `json:"lock,omitempty"`
	Config   string             `json:"config,omitempty"`
	Notes    []string           `json:"notes"`
}

// Input bundles everything needed to build a report.
type Input struct {
	Repo, Doc, RequestedModel string
	Code                      *codemodel.Model
	DocModel                  *docmodel.Model
	Dropped                   []docmodel.Dropped
	Result                    *drift.Result
	Usage                     docmodel.Usage
	Lock                      string   // lock file path when the doc model came from a lock
	Notes                     []string // warnings shown at the top of the report
	Config                    string   // config file path, when one was used
}

// Build assembles the report.
func Build(in Input) *Report {
	s := Summary{ByType: map[string]int{}, Dropped: len(in.Dropped)}
	for _, t := range []string{drift.ForbiddenEdge, drift.MissingComponent, drift.MissingEdge, drift.UndocumentedEdge, drift.UndocumentedComponent, drift.Unverifiable} {
		s.ByType[t] = 0
	}
	for _, f := range in.Result.Findings {
		s.ByType[f.Type]++
		if f.Failing {
			s.Failing++
		}
	}
	s.Drift = s.Failing > 0
	if s.Drift {
		s.ExitCode = 1
	}
	u := UsageReport{Usage: in.Usage, Requested: in.RequestedModel}
	if in.Usage.Source == "recorded" || in.Usage.Source == "lock" {
		u.Requested = "" // --doc-model: no model was called
	}
	if c, ok := Cost(in.Usage); ok {
		u.CostUSD = &c
		switch in.Usage.Source {
		case "api":
			u.CostNote = "estimated from list prices; cache writes at 1.25x input, cache reads at 0.1x input"
		case "cache":
			u.CostNote = "doc model served from local cache; no API call"
		case "lock":
			u.CostNote = "doc model read from the reviewed lock file; no API call"
		default:
			u.CostNote = "doc model loaded from a recorded file; no API call"
		}
	} else {
		u.CostNote = "no price known for model " + in.Usage.Model
	}
	notes := in.Notes
	if notes == nil {
		notes = []string{}
	}
	dropped := in.Dropped
	if dropped == nil {
		dropped = []docmodel.Dropped{}
	}
	return &Report{
		Tool: "archdrift", Repo: in.Repo, Doc: in.Doc, Module: in.Code.Module,
		Summary: s, Findings: in.Result.Findings, Dropped: dropped, Usage: u,
		Code: in.Code, DocModel: in.DocModel, Graph: in.Result.Graph,
		Missing: in.Result.MissingNodes, Undoc: in.Result.UndocumentedNodes,
		Lock: in.Lock, Notes: notes, Config: in.Config,
	}
}

// Write writes report.json and report.md into dir.
func Write(dir string, r *Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(Markdown(r)), 0o644)
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

func quoteBlock(b *strings.Builder, q string) {
	for _, line := range strings.Split(strings.TrimSpace(q), "\n") {
		fmt.Fprintf(b, "> %s\n", line)
	}
}

// Markdown renders the report.
func Markdown(r *Report) string {
	var b strings.Builder
	verdict := "NO DRIFT"
	if r.Summary.Drift {
		verdict = "DRIFT"
	}
	fmt.Fprintf(&b, "# archdrift report\n\n")
	fmt.Fprintf(&b, "- **Result:** %s (exit %d)\n", verdict, r.Summary.ExitCode)
	fmt.Fprintf(&b, "- **Repo:** `%s` (module `%s`)\n", r.Repo, r.Module)
	fmt.Fprintf(&b, "- **Design doc:** `%s`\n", r.Doc)
	if r.Lock != "" {
		fmt.Fprintf(&b, "- **Doc model source:** %s (`%s`)\n", r.Usage.Source, r.Lock)
	} else {
		fmt.Fprintf(&b, "- **Doc model source:** %s\n", r.Usage.Source)
	}
	if r.Config != "" {
		fmt.Fprintf(&b, "- **Config:** `%s`\n", r.Config)
	}
	fmt.Fprintf(&b, "- **Code components:** %d, **import edges:** %d\n\n", len(r.Code.Components), len(r.Code.Edges))

	if len(r.Notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Summary\n\n| Finding | Severity | Fails check | Count |\n|---|---|---|---|\n")
	rows := []struct{ t, sev, fail string }{
		{drift.ForbiddenEdge, drift.High, "yes"},
		{drift.MissingComponent, drift.Medium, "yes"},
		{drift.MissingEdge, drift.Medium, "yes"},
		{drift.UndocumentedEdge, drift.Low, "yes"},
		{drift.UndocumentedComponent, drift.Low, "yes"},
		{drift.Unverifiable, drift.Info, "no"},
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %d |\n", row.t, row.sev, row.fail, r.Summary.ByType[row.t])
	}
	fmt.Fprintf(&b, "\nDropped doc claims: %d\n\n", r.Summary.Dropped)

	b.WriteString("## Findings\n\n")
	if len(r.Findings) == 0 {
		b.WriteString("No findings.\n\n")
	}
	for i, f := range r.Findings {
		fmt.Fprintf(&b, "### %d. %s (%s): %s\n\n", i+1, f.Type, f.Severity, f.Subject)
		b.WriteString(f.Message + "\n\n")
		if f.From != "" || f.To != "" {
			fmt.Fprintf(&b, "- Code IDs: `%s`", f.From)
			if f.To != "" {
				fmt.Fprintf(&b, " -> `%s`", f.To)
			}
			b.WriteString("\n")
		}
		if f.Protocol != "" {
			fmt.Fprintf(&b, "- Protocol (from doc): %s\n", f.Protocol)
		}
		if len(f.Code) > 0 {
			b.WriteString("- Code evidence:\n")
			for _, l := range f.Code {
				fmt.Fprintf(&b, "  - `%s`\n", l)
			}
		} else if f.Type == drift.Unverifiable {
			b.WriteString("- Code evidence: none (static imports cannot prove a runtime relation)\n")
		} else if f.Type == drift.MissingComponent {
			b.WriteString("- Code evidence: none (no package maps to this component)\n")
		} else {
			b.WriteString("- Code evidence: none (the absence of the import is the finding)\n")
		}
		if f.DocQuote != "" {
			b.WriteString("- Doc quote:\n\n")
			quoteBlock(&b, f.DocQuote)
		} else {
			b.WriteString("- Doc quote: none (the doc is silent)\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Diagram\n\n")
	b.WriteString(Mermaid(r))
	b.WriteString("\nLegend: green = documented and present; red = forbidden but present; orange dashed = documented but missing; blue = present but undocumented; grey dotted = runtime relation, not checkable; grey = context. Yellow nodes are undocumented; red dashed nodes and edges are in the doc only.\n\n")

	b.WriteString("## Dropped claims\n\n")
	if len(r.Dropped) == 0 {
		b.WriteString("None. Every LLM claim had a verbatim quote and known code IDs.\n\n")
	} else {
		b.WriteString("Claims from the doc model that failed deterministic verification and were ignored.\n\n| Claim | Name | Detail | Reason | Quote given |\n|---|---|---|---|---|\n")
		for _, d := range r.Dropped {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", d.Claim, mdEscape(d.Name), mdEscape(d.Detail), mdEscape(d.Reason), mdEscape(d.Quote))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Token usage and cost\n\n| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Source | %s |\n", r.Usage.Source)
	if r.Usage.Requested != "" {
		fmt.Fprintf(&b, "| Requested model | %s |\n", r.Usage.Requested)
	}
	if r.Usage.Model != "" {
		fmt.Fprintf(&b, "| Serving model | %s |\n", r.Usage.Model)
	}
	fmt.Fprintf(&b, "| Input tokens | %d |\n| Output tokens | %d |\n| Cache read tokens | %d |\n| Cache write tokens | %d |\n",
		r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CacheReadInputTokens, r.Usage.CacheCreationInputTokens)
	if r.Usage.CostUSD != nil {
		fmt.Fprintf(&b, "| Cost (USD) | $%.4f |\n", *r.Usage.CostUSD)
	} else {
		b.WriteString("| Cost (USD) | unknown |\n")
	}
	fmt.Fprintf(&b, "| Note | %s |\n", r.Usage.CostNote)
	return b.String()
}

func nodeID(s string) string {
	var b strings.Builder
	b.WriteString("n_")
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func label(s string) string { return strings.NewReplacer(`"`, "'", "|", "/", "\n", " ").Replace(s) }

// Mermaid renders a coloured flowchart of components and drift.
func Mermaid(r *Report) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	names := map[string][]string{}
	for _, c := range r.DocModel.Components {
		for _, id := range c.CodeIDs {
			names[id] = append(names[id], c.Name)
		}
	}
	undoc := map[string]bool{}
	for _, id := range r.Undoc {
		undoc[id] = true
	}
	for _, c := range r.Code.Components {
		l := c.ID
		if n := names[c.ID]; len(n) > 0 {
			sort.Strings(n)
			l = strings.Join(n, " / ") + "<br/>" + c.ID
		}
		open, closeB := "[\"", "\"]"
		if c.External {
			open, closeB = "[(\"", "\")]"
		}
		fmt.Fprintf(&b, "  %s%s%s%s", nodeID(c.ID), open, label(l), closeB)
		if undoc[c.ID] {
			b.WriteString(":::undocumented")
		}
		b.WriteString("\n")
	}
	for _, n := range r.Missing {
		fmt.Fprintf(&b, "  %s[\"%s<br/>(doc only)\"]:::missing\n", nodeID(drift.DocNode(n)), label(n))
	}
	styles := map[string][]int{}
	for i, e := range r.Graph {
		arrow := "-->"
		lbl := e.Label
		switch e.Status {
		case drift.EdgeForbidden:
			arrow, lbl = "==>", "FORBIDDEN"
		case drift.EdgeMissing:
			arrow, lbl = "-.->", "MISSING"
		case drift.EdgeUndocumented:
			lbl = "undocumented"
		case drift.EdgeUnverifiable:
			arrow, lbl = "-.->", strings.TrimSpace(lbl+" ?")
		case drift.EdgeRespected:
			arrow, lbl = "--x", "forbidden, absent"
		case drift.EdgeDocOnly:
			arrow, lbl = "-.->", strings.TrimSpace(lbl+" (doc only)")
		}
		if lbl != "" {
			fmt.Fprintf(&b, "  %s %s|\"%s\"| %s\n", nodeID(e.From), arrow, label(lbl), nodeID(e.To))
		} else {
			fmt.Fprintf(&b, "  %s %s %s\n", nodeID(e.From), arrow, nodeID(e.To))
		}
		styles[e.Status] = append(styles[e.Status], i)
	}
	b.WriteString("  classDef undocumented fill:#fff3b0,stroke:#b8860b,color:#000\n")
	b.WriteString("  classDef missing fill:#ffd6d6,stroke:#c00,stroke-dasharray:5 5,color:#000\n")
	colors := []struct{ status, style string }{
		{drift.EdgeOK, "stroke:#2e8b57,stroke-width:2px"},
		{drift.EdgeForbidden, "stroke:#d00000,stroke-width:3px"},
		{drift.EdgeMissing, "stroke:#ff8c00,stroke-width:2px,stroke-dasharray:6 4"},
		{drift.EdgeUndocumented, "stroke:#1e6fd9,stroke-width:2px"},
		{drift.EdgeUnverifiable, "stroke:#888,stroke-width:1px,stroke-dasharray:2 3"},
		{drift.EdgeRespected, "stroke:#888,stroke-width:1px"},
		{drift.EdgeContext, "stroke:#bbb,stroke-width:1px"},
		{drift.EdgeDocOnly, "stroke:#c00,stroke-width:1px,stroke-dasharray:5 5"},
	}
	for _, c := range colors {
		idx := styles[c.status]
		if len(idx) == 0 {
			continue
		}
		parts := make([]string, len(idx))
		for i, n := range idx {
			parts[i] = fmt.Sprint(n)
		}
		fmt.Fprintf(&b, "  linkStyle %s %s\n", strings.Join(parts, ","), c.style)
	}
	b.WriteString("```\n")
	return b.String()
}
