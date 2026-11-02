// Package drift compares the code model with the verified doc model. All
// conclusions here are deterministic: the LLM only supplied the name mapping.
package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/docmodel"
)

// Finding types.
const (
	ForbiddenEdge         = "FORBIDDEN_EDGE"
	MissingComponent      = "MISSING_COMPONENT"
	MissingEdge           = "MISSING_EDGE"
	UndocumentedEdge      = "UNDOCUMENTED_EDGE"
	UndocumentedComponent = "UNDOCUMENTED_COMPONENT"
	Unverifiable          = "UNVERIFIABLE"
)

// Severities.
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
	Info   = "info"
)

var severityRank = map[string]int{High: 0, Medium: 1, Low: 2, Info: 3}

// Finding is one drift item with its evidence.
type Finding struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Severity string               `json:"severity"`
	Failing  bool                 `json:"failing"`
	Subject  string               `json:"subject"`        // doc names, e.g. "API -> Store"
	From     string               `json:"from,omitempty"` // code IDs (comma separated when a doc component maps to several)
	To       string               `json:"to,omitempty"`
	Message  string               `json:"message"`
	Code     []codemodel.Location `json:"code_evidence"`       // file:line from code
	DocQuote string               `json:"doc_quote,omitempty"` // verbatim from doc
	Protocol string               `json:"protocol,omitempty"`
}

// Status of a documented relation, for diagrams.
const (
	EdgeOK           = "ok"
	EdgeForbidden    = "forbidden"
	EdgeMissing      = "missing"
	EdgeUndocumented = "undocumented"
	EdgeUnverifiable = "unverifiable"
	EdgeRespected    = "respected" // forbidden and absent
	EdgeContext      = "context"   // code edge touching an undocumented component, or internal wiring
	EdgeDocOnly      = "doc_only"  // relation involving a doc component that has no code
)

// DocNode is the graph ID used for a doc component with no code IDs.
func DocNode(name string) string { return "doc:" + name }

// GraphEdge is a code-ID-level edge annotated with its drift status.
type GraphEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"`
	Label  string `json:"label,omitempty"`
}

// Result is the full diff.
type Result struct {
	Findings []Finding   `json:"findings"`
	Graph    []GraphEdge `json:"graph"`
	// MissingNodes are doc components with no code ID (shown in the diagram).
	MissingNodes []string `json:"missing_nodes"`
	// UndocumentedNodes are code IDs not mapped by any doc component.
	UndocumentedNodes []string `json:"undocumented_nodes"`
}

// Failing reports whether any finding counts as drift.
func (r *Result) Failing() bool {
	for _, f := range r.Findings {
		if f.Failing {
			return true
		}
	}
	return false
}

// networkProtocols are runtime transports that static imports cannot prove.
var networkProtocols = []string{"http", "https", "rest", "grpc", "rpc", "graphql", "websocket", "queue", "kafka", "amqp", "rabbitmq", "nats", "sqs", "sns", "pubsub", "pub/sub", "event", "message", "async", "stream", "webhook", "tcp", "udp"}

// IsNetworkProtocol reports whether the doc's protocol string names a runtime transport.
func IsNetworkProtocol(p string) bool {
	p = strings.ToLower(p)
	for _, n := range networkProtocols {
		if strings.Contains(p, n) {
			return true
		}
	}
	return false
}

func isDeployable(id string) bool { return strings.HasPrefix(id, "cmd/") }
func isExternal(id string) bool   { return strings.HasPrefix(id, "ext:") }

func allOf(ids []string, pred func(string) bool) bool {
	for _, id := range ids {
		if !pred(id) {
			return false
		}
	}
	return len(ids) > 0
}

func anyOf(ids []string, pred func(string) bool) bool {
	for _, id := range ids {
		if pred(id) {
			return true
		}
	}
	return false
}

// Checkable decides whether a required relation can be proven or disproven
// by static imports, returning the reason when it cannot.
func Checkable(fromIDs, toIDs []string, protocol string) (bool, string) {
	if anyOf(toIDs, isExternal) {
		return true, ""
	}
	if allOf(toIDs, isDeployable) {
		return false, "target is a deployable (main package); it cannot be imported, so the relation is runtime-only"
	}
	if IsNetworkProtocol(protocol) && (allOf(fromIDs, isDeployable) || allOf(toIDs, isDeployable)) {
		return false, fmt.Sprintf("runtime relation over %q between deployables; imports cannot prove it", protocol)
	}
	return true, ""
}

// Diff computes findings. Inputs must already be verified.
func Diff(code *codemodel.Model, doc *docmodel.Model) *Result {
	res := &Result{Findings: []Finding{}, Graph: []GraphEdge{}, MissingNodes: []string{}, UndocumentedNodes: []string{}}
	byName := map[string]docmodel.Component{}
	owners := map[string][]string{} // code ID -> doc component names
	for _, c := range doc.Components {
		byName[c.Name] = c
		for _, id := range c.CodeIDs {
			owners[id] = append(owners[id], c.Name)
		}
	}
	join := func(ids []string) string { return strings.Join(ids, ",") }

	// Components.
	for _, c := range doc.Components {
		if len(c.CodeIDs) == 0 {
			res.MissingNodes = append(res.MissingNodes, c.Name)
			res.Findings = append(res.Findings, Finding{
				ID: MissingComponent + ":" + c.Name, Type: MissingComponent, Severity: Medium, Failing: true,
				Subject: c.Name, Message: fmt.Sprintf("Doc component %q has no matching package in code.", c.Name),
				Code: []codemodel.Location{}, DocQuote: c.Quote,
			})
		}
	}
	for _, cc := range code.Components {
		if len(owners[cc.ID]) == 0 {
			res.UndocumentedNodes = append(res.UndocumentedNodes, cc.ID)
			res.Findings = append(res.Findings, Finding{
				ID: UndocumentedComponent + ":" + cc.ID, Type: UndocumentedComponent, Severity: Low, Failing: true,
				Subject: cc.ID, From: cc.ID, Message: fmt.Sprintf("Code component %s is not described in the doc.", cc.ID),
				Code: []codemodel.Location{cc.Anchor},
			})
		}
	}

	// Relations.
	covered := map[[2]string]bool{} // code edges explained by the doc (required or forbidden)
	graphSeen := map[[2]string]bool{}
	addGraph := func(from, to, status, label string) {
		k := [2]string{from, to}
		if graphSeen[k] {
			return
		}
		graphSeen[k] = true
		res.Graph = append(res.Graph, GraphEdge{From: from, To: to, Status: status, Label: label})
	}
	for _, r := range doc.Relations {
		fc, tc := byName[r.From], byName[r.To]
		if len(fc.CodeIDs) == 0 || len(tc.CodeIDs) == 0 {
			// The missing component is already reported; only draw the relation.
			from, to := DocNode(r.From), DocNode(r.To)
			if len(fc.CodeIDs) > 0 {
				from = fc.CodeIDs[0]
			}
			if len(tc.CodeIDs) > 0 {
				to = tc.CodeIDs[0]
			}
			addGraph(from, to, EdgeDocOnly, r.Protocol)
			continue
		}
		subject := r.From + " -> " + r.To
		var hits []codemodel.Edge
		for _, f := range fc.CodeIDs {
			for _, t := range tc.CodeIDs {
				covered[[2]string{f, t}] = true
				if e, ok := code.Edge(f, t); ok {
					hits = append(hits, e)
				}
			}
		}
		var evidence []codemodel.Location
		for _, e := range hits {
			evidence = append(evidence, e.Evidence...)
		}
		switch r.Kind {
		case docmodel.KindForbidden:
			if len(hits) == 0 {
				addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeRespected, "forbidden")
				continue
			}
			for _, e := range hits {
				addGraph(e.From, e.To, EdgeForbidden, "forbidden")
			}
			res.Findings = append(res.Findings, Finding{
				ID: ForbiddenEdge + ":" + subject, Type: ForbiddenEdge, Severity: High, Failing: true,
				Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
				Message: fmt.Sprintf("Doc forbids %s depending on %s, but the code imports it.", r.From, r.To),
				Code:    evidence, DocQuote: r.Quote,
			})
		case docmodel.KindRequired:
			if ok, why := Checkable(fc.CodeIDs, tc.CodeIDs, r.Protocol); !ok {
				addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeUnverifiable, r.Protocol)
				res.Findings = append(res.Findings, Finding{
					ID: Unverifiable + ":" + subject, Type: Unverifiable, Severity: Info, Failing: false,
					Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
					Message: "Not checked: " + why + ".", Code: evidence, DocQuote: r.Quote,
				})
				continue
			}
			if len(hits) > 0 {
				for _, e := range hits {
					addGraph(e.From, e.To, EdgeOK, r.Protocol)
				}
				continue
			}
			addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeMissing, "missing")
			res.Findings = append(res.Findings, Finding{
				ID: MissingEdge + ":" + subject, Type: MissingEdge, Severity: Medium, Failing: true,
				Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
				Message: fmt.Sprintf("Doc requires %s to depend on %s, but no package of %s imports %s.", r.From, r.To, join(fc.CodeIDs), join(tc.CodeIDs)),
				Code:    []codemodel.Location{}, DocQuote: r.Quote,
			})
		}
	}

	// Code edges the doc does not explain, between documented components.
	for _, e := range code.Edges {
		if covered[[2]string{e.From, e.To}] || len(owners[e.From]) == 0 || len(owners[e.To]) == 0 {
			continue
		}
		if sameOwner(owners[e.From], owners[e.To]) {
			continue // both IDs belong to one doc component: internal wiring
		}
		addGraph(e.From, e.To, EdgeUndocumented, "")
		subject := strings.Join(owners[e.From], "/") + " -> " + strings.Join(owners[e.To], "/")
		res.Findings = append(res.Findings, Finding{
			ID: UndocumentedEdge + ":" + e.From + "->" + e.To, Type: UndocumentedEdge, Severity: Low, Failing: true,
			Subject: subject, From: e.From, To: e.To,
			Message: fmt.Sprintf("Code has %s importing %s, which the doc does not mention.", e.From, e.To),
			Code:    e.Evidence,
		})
	}

	for _, e := range code.Edges {
		addGraph(e.From, e.To, EdgeContext, "")
	}

	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		return a.ID < b.ID
	})
	sort.SliceStable(res.Graph, func(i, j int) bool {
		if res.Graph[i].From != res.Graph[j].From {
			return res.Graph[i].From < res.Graph[j].From
		}
		return res.Graph[i].To < res.Graph[j].To
	})
	sort.Strings(res.MissingNodes)
	sort.Strings(res.UndocumentedNodes)
	return res
}

func sameOwner(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
