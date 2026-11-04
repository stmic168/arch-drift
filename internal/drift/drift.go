// Package drift compares the code model with the verified doc model. All
// conclusions here are deterministic: the LLM only supplied the name mapping.
package drift

import (
	"fmt"
	"path"
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
	DocFile  string               `json:"doc_file,omitempty"`  // file holding the quote, when several docs are read
	Protocol string               `json:"protocol,omitempty"`
	Weight   int                  `json:"weight,omitempty"`   // ranking for undocumented components: importers*100 + packages
	Via      []string             `json:"via,omitempty"`      // component path when proven transitively
	Accepted *Accepted            `json:"accepted,omitempty"` // set when the baseline accepts this finding
	// Diff mode (--base): new, worsened or existing, and the evidence in files
	// the base revision's finding did not have.
	Status      string               `json:"status,omitempty"`
	NewEvidence []codemodel.Location `json:"new_evidence,omitempty"`
}

// Options tune the diff.
type Options struct {
	// TransitiveRequired lets an import path through other components satisfy a required relation.
	TransitiveRequired bool
	// TransitiveForbidden makes an import path through other components violate a forbidden relation.
	TransitiveForbidden bool
	// FailUndocumentedComponents makes UNDOCUMENTED_COMPONENT fail the check.
	// Off by default: architecture docs describe a few major components, so
	// every utility package would otherwise be drift.
	FailUndocumentedComponents bool
	// StrictProtocols trusts a runtime protocol on a relation only when the
	// relation's doc quote names it. A protocol the model inferred (e.g.
	// "network" for "A coordinates with B") is ignored, so the relation is
	// checked by imports. For rules docs, where a missed violation is worse
	// than an unverifiable runtime call.
	StrictProtocols bool
	// ReportUndocumentedEdges makes UNDOCUMENTED_EDGE informational (non-failing).
	// For overview docs that state few relations, where every unlisted import
	// between documented components would otherwise fail the check.
	ReportUndocumentedEdges bool
	// IgnoreUndocumented lists component ID patterns (path.Match syntax, e.g.
	// "pkg/ext*") never reported as UNDOCUMENTED_COMPONENT.
	IgnoreUndocumented []string
	// Services lists component ID patterns for components that run as
	// separate processes inside one binary. A required relation between two
	// services with no import is UNVERIFIABLE instead of MISSING_EDGE.
	// Ignored with StrictProtocols.
	Services []string
}

func (o Options) isService(id string) bool {
	for _, p := range o.Services {
		if ok, _ := path.Match(p, id); ok {
			return true
		}
	}
	return false
}

// services returns the IDs among ids that are configured services, or none
// with StrictProtocols (as for betweenServices).
func (o Options) services(ids []string) []string {
	if o.StrictProtocols {
		return nil
	}
	var out []string
	for _, id := range ids {
		if o.isService(id) {
			out = append(out, id)
		}
	}
	return out
}

// betweenServices reports whether a required relation joins two different
// services, so a missing import proves nothing (they can talk at runtime).
func (o Options) betweenServices(from, to []string) bool {
	if o.StrictProtocols || len(o.Services) == 0 {
		return false
	}
	for _, f := range from {
		for _, t := range to {
			if f != t && o.isService(f) && o.isService(t) {
				return true
			}
		}
	}
	return false
}

func (o Options) ignoredUndocumented(id string) bool {
	for _, p := range o.IgnoreUndocumented {
		if ok, _ := path.Match(p, id); ok {
			return true
		}
	}
	return false
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
	// ExternalNodes are doc components outside the codebase with no code ID.
	ExternalNodes []string `json:"external_nodes"`
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
var networkProtocols = []string{"network", "http", "https", "rest", "grpc", "rpc", "graphql", "websocket", "queue", "kafka", "amqp", "rabbitmq", "nats", "sqs", "sns", "pubsub", "pub/sub", "event", "message", "async", "stream", "webhook", "tcp", "udp"}

// ProtocolStated reports whether the relation's quote names its protocol
// (case-insensitive), as opposed to the model inferring it.
func ProtocolStated(r docmodel.Relation) bool {
	p := strings.ToLower(strings.TrimSpace(r.Protocol))
	return p != "" && strings.Contains(strings.ToLower(docmodel.NormalizeSpace(r.Quote)), p)
}

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

func isExternal(id string) bool { return strings.HasPrefix(id, "ext:") }

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
// by static imports, returning the reason when it cannot. isDeployable
// reports whether a code ID is a main package (a separately run program).
func Checkable(fromIDs, toIDs []string, protocol string, isDeployable func(string) bool) (bool, string) {
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

// importPath finds the shortest import path from any of from to any of to, at
// least two hops long (direct edges are handled separately). Deterministic:
// edges are visited in sorted order.
func importPath(code *codemodel.Model, from, to []string) []codemodel.Edge {
	adj := map[string][]codemodel.Edge{}
	for _, e := range code.Edges {
		adj[e.From] = append(adj[e.From], e)
	}
	target := map[string]bool{}
	for _, t := range to {
		target[t] = true
	}
	prev := map[string]codemodel.Edge{}
	seen := map[string]bool{}
	queue := append([]string(nil), from...)
	for _, f := range from {
		seen[f] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range adj[cur] {
			if seen[e.To] {
				continue
			}
			seen[e.To] = true
			prev[e.To] = e
			if target[e.To] {
				var chain []codemodel.Edge
				for n := e.To; ; {
					pe, ok := prev[n]
					if !ok {
						break
					}
					chain = append([]codemodel.Edge{pe}, chain...)
					n = pe.From
				}
				if len(chain) >= 2 {
					return chain
				}
				continue
			}
			queue = append(queue, e.To)
		}
	}
	return nil
}

// wiredByDeployable finds the common way a service reaches an external
// system in a single binary: the deployable's main package builds the client
// (an object-storage bucket, a database handle) and hands it to the service,
// so the service never imports it. It returns a file of a deployable that
// imports a package of from and, in the same file, either the target or a
// component with an import path to it. Callers pass only configured services
// as from: utilities are imported by the same files and would otherwise
// satisfy any claim.
func wiredByDeployable(code *codemodel.Model, from, to []string) (file, deployable string, ok bool) {
	src, dst := set(from), set(to)
	byFrom := map[string][]codemodel.Edge{}
	for _, e := range code.Edges {
		byFrom[e.From] = append(byFrom[e.From], e)
	}
	files := func(e codemodel.Edge) map[string]bool {
		m := map[string]bool{}
		for _, l := range e.Evidence {
			m[l.File] = true
		}
		return m
	}
	var best string
	var bestDep string
	for d, edges := range byFrom {
		if src[d] || !code.IsDeployable(d) {
			continue
		}
		wiring := map[string]bool{} // files of d importing the source
		for _, e := range edges {
			if src[e.To] {
				for f := range files(e) {
					wiring[f] = true
				}
			}
		}
		if len(wiring) == 0 {
			continue
		}
		for _, e := range edges {
			if src[e.To] {
				continue
			}
			if !dst[e.To] && !reaches(byFrom[e.To], dst) && importPath(code, []string{e.To}, to) == nil {
				continue
			}
			for f := range files(e) {
				if wiring[f] && (best == "" || f < best) {
					best, bestDep = f, d
				}
			}
		}
	}
	return best, bestDep, best != ""
}

// reaches reports whether one of edges imports a component in dst directly
// (importPath only returns paths of two or more edges).
func reaches(edges []codemodel.Edge, dst map[string]bool) bool {
	for _, e := range edges {
		if dst[e.To] {
			return true
		}
	}
	return false
}

func set(ids []string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func protocolHit(code *codemodel.Model, from, to []string) (codemodel.Edge, bool) {
	for _, f := range from {
		for _, t := range to {
			if e, ok := code.ProtocolEdge(f, t); ok {
				return e, true
			}
		}
	}
	return codemodel.Edge{}, false
}

func chainEvidence(chain []codemodel.Edge) ([]codemodel.Location, []string) {
	var ev []codemodel.Location
	via := []string{chain[0].From}
	for _, e := range chain {
		ev = append(ev, e.Evidence[0])
		via = append(via, e.To)
	}
	return ev, via
}

// Diff computes findings. Inputs must already be verified.
func Diff(code *codemodel.Model, doc *docmodel.Model, opts Options) *Result {
	res := &Result{Findings: []Finding{}, Graph: []GraphEdge{}, MissingNodes: []string{}, ExternalNodes: []string{}, UndocumentedNodes: []string{}}
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
		if len(c.CodeIDs) == 0 && c.External {
			res.ExternalNodes = append(res.ExternalNodes, c.Name) // outside the codebase by design
			continue
		}
		if len(c.CodeIDs) == 0 {
			res.MissingNodes = append(res.MissingNodes, c.Name)
			res.Findings = append(res.Findings, Finding{
				ID: MissingComponent + ":" + c.Name, Type: MissingComponent, Severity: Medium, Failing: true,
				Subject: c.Name, Message: fmt.Sprintf("Doc component %q has no matching package in code.", c.Name),
				Code: []codemodel.Location{}, DocQuote: c.Quote, DocFile: c.Source,
			})
		}
	}
	importers, imports := map[string]int{}, map[string]int{}
	for _, e := range code.Edges { // edges are unique per (from, to)
		importers[e.To]++
		imports[e.From]++
	}
	for _, cc := range code.Components {
		if len(owners[cc.ID]) > 0 || opts.ignoredUndocumented(cc.ID) {
			continue
		}
		res.UndocumentedNodes = append(res.UndocumentedNodes, cc.ID)
		sev := Info
		if opts.FailUndocumentedComponents {
			sev = Low
		}
		// Rank application components above utilities: a feature or service
		// imports many components and is imported by few (often only the
		// wiring), a utility the other way round. The pilot showed that
		// ranking by importers buried Thanos' Receive at 33 of 44.
		in, out := importers[cc.ID], imports[cc.ID]
		res.Findings = append(res.Findings, Finding{
			ID: UndocumentedComponent + ":" + cc.ID, Type: UndocumentedComponent, Severity: sev, Failing: opts.FailUndocumentedComponents,
			Subject: cc.ID, From: cc.ID, Weight: (out-in)*1000 + out,
			Message: fmt.Sprintf("Code component %s (%d package(s), imports %d component(s), imported by %d) is not described in the doc.", cc.ID, len(cc.Packages), out, in),
			Code:    []codemodel.Location{cc.Anchor},
		})
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
		subject := r.From + " -> " + r.To
		if len(fc.CodeIDs) == 0 || len(tc.CodeIDs) == 0 {
			// Not checkable: say why instead of skipping it silently.
			from, to := DocNode(r.From), DocNode(r.To)
			if len(fc.CodeIDs) > 0 {
				from = fc.CodeIDs[0]
			}
			if len(tc.CodeIDs) > 0 {
				to = tc.CodeIDs[0]
			}
			addGraph(from, to, EdgeDocOnly, r.Protocol)
			var why []string
			for _, c := range []docmodel.Component{fc, tc} {
				switch {
				case len(c.CodeIDs) > 0:
				case c.External:
					why = append(why, c.Name+" is outside this codebase")
				default:
					why = append(why, c.Name+" has no code in this repo")
				}
			}
			res.Findings = append(res.Findings, Finding{
				ID: Unverifiable + ":" + subject, Type: Unverifiable, Severity: Info, Failing: false,
				Subject: subject, From: strings.Join(fc.CodeIDs, ","), To: strings.Join(tc.CodeIDs, ","), Protocol: r.Protocol,
				Message: "Not checked: " + strings.Join(why, "; ") + ".", Code: []codemodel.Location{}, DocQuote: r.Quote, DocFile: r.Source,
			})
			continue
		}
		var hits []codemodel.Edge
		for _, f := range fc.CodeIDs {
			for _, t := range tc.CodeIDs {
				covered[[2]string{f, t}] = true
				if e, ok := code.Edge(f, t); ok {
					hits = append(hits, e)
				}
			}
		}
		evidence := []codemodel.Location{} // never null in report.json
		for _, e := range hits {
			evidence = append(evidence, e.Evidence...)
		}
		switch r.Kind {
		case docmodel.KindForbidden:
			// Importing the target's protocol package (its generated gRPC
			// client) is calling it, so it violates "must not depend on" too.
			if len(hits) == 0 {
				for _, f := range fc.CodeIDs {
					for _, t := range tc.CodeIDs {
						if e, ok := code.ProtocolEdge(f, t); ok {
							hits = append(hits, e)
							evidence = append(evidence, e.Evidence...)
						}
					}
				}
			}
			if len(hits) == 0 && opts.TransitiveForbidden {
				if chain := importPath(code, fc.CodeIDs, tc.CodeIDs); chain != nil {
					ev, via := chainEvidence(chain)
					addGraph(chain[0].From, chain[len(chain)-1].To, EdgeForbidden, "forbidden, via "+strings.Join(via[1:len(via)-1], ", "))
					res.Findings = append(res.Findings, Finding{
						ID: ForbiddenEdge + ":" + subject, Type: ForbiddenEdge, Severity: High, Failing: true,
						Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
						Message: fmt.Sprintf("Doc forbids %s depending on %s, and the code reaches it indirectly: %s.", r.From, r.To, strings.Join(via, " -> ")),
						Code:    ev, DocQuote: r.Quote, DocFile: r.Source, Via: via,
					})
					continue
				}
			}
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
				Code:    evidence, DocQuote: r.Quote, DocFile: r.Source,
			})
		case docmodel.KindRequired:
			proto := r.Protocol
			if opts.StrictProtocols && !ProtocolStated(r) {
				proto = ""
			}
			if ok, why := Checkable(fc.CodeIDs, tc.CodeIDs, proto, code.IsDeployable); !ok {
				addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeUnverifiable, r.Protocol)
				res.Findings = append(res.Findings, Finding{
					ID: Unverifiable + ":" + subject, Type: Unverifiable, Severity: Info, Failing: false,
					Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
					Message: "Not checked: " + why + ".", Code: evidence, DocQuote: r.Quote, DocFile: r.Source,
				})
				continue
			}
			if len(hits) > 0 {
				for _, e := range hits {
					addGraph(e.From, e.To, EdgeOK, r.Protocol)
				}
				continue
			}
			// An import of the target's protocol package (generated gRPC client
			// and types) proves the dependency even though it creates no edge.
			if e, ok := protocolHit(code, fc.CodeIDs, tc.CodeIDs); ok {
				addGraph(e.From, e.To, EdgeOK, "via protocol package")
				continue
			}
			// External systems are usually reached through a client wrapper
			// (store -> cacheutil -> memcached, ha -> ring/kv -> consul), so an
			// import path is enough for them even without transitive.required.
			if opts.TransitiveRequired || allOf(tc.CodeIDs, isExternal) {
				if chain := importPath(code, fc.CodeIDs, tc.CodeIDs); chain != nil {
					_, via := chainEvidence(chain)
					addGraph(chain[0].From, chain[len(chain)-1].To, EdgeOK, "via "+strings.Join(via[1:len(via)-1], ", "))
					continue
				}
			}
			if opts.betweenServices(fc.CodeIDs, tc.CodeIDs) {
				addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeUnverifiable, r.Protocol)
				res.Findings = append(res.Findings, Finding{
					ID: Unverifiable + ":" + subject, Type: Unverifiable, Severity: Info, Failing: false,
					Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
					Message: fmt.Sprintf("Not checked: no import from %s to %s, but both run as separate services (config services), which can call each other at runtime without one.", join(fc.CodeIDs), join(tc.CodeIDs)),
					Code:    []codemodel.Location{}, DocQuote: r.Quote, DocFile: r.Source,
				})
				continue
			}
			// A service's external system can be wired in by the binary.
			if svc := opts.services(fc.CodeIDs); len(svc) > 0 && allOf(tc.CodeIDs, isExternal) {
				if file, dep, ok := wiredByDeployable(code, svc, tc.CodeIDs); ok {
					addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeOK, "wired by "+dep+" ("+file+")")
					continue
				}
			}
			if IsNetworkProtocol(proto) {
				// Components in one binary often call each other over HTTP/gRPC
				// through generic clients, so a missing import proves nothing.
				addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeUnverifiable, r.Protocol)
				res.Findings = append(res.Findings, Finding{
					ID: Unverifiable + ":" + subject, Type: Unverifiable, Severity: Info, Failing: false,
					Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
					Message: fmt.Sprintf("Not checked: no import from %s to %s, but the doc says they talk over %q, which can happen at runtime without one.", join(fc.CodeIDs), join(tc.CodeIDs), r.Protocol),
					Code:    []codemodel.Location{}, DocQuote: r.Quote, DocFile: r.Source,
				})
				continue
			}
			addGraph(fc.CodeIDs[0], tc.CodeIDs[0], EdgeMissing, "missing")
			res.Findings = append(res.Findings, Finding{
				ID: MissingEdge + ":" + subject, Type: MissingEdge, Severity: Medium, Failing: true,
				Subject: subject, From: join(fc.CodeIDs), To: join(tc.CodeIDs), Protocol: r.Protocol,
				Message: fmt.Sprintf("Doc requires %s to depend on %s, but no package of %s imports %s.", r.From, r.To, join(fc.CodeIDs), join(tc.CodeIDs)),
				Code:    []codemodel.Location{}, DocQuote: r.Quote, DocFile: r.Source,
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
		sev, failing := Low, true
		if opts.ReportUndocumentedEdges {
			sev, failing = Info, false
		}
		res.Findings = append(res.Findings, Finding{
			ID: UndocumentedEdge + ":" + e.From + "->" + e.To, Type: UndocumentedEdge, Severity: sev, Failing: failing,
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
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Weight != b.Weight {
			return a.Weight > b.Weight // most-imported undocumented components first
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
	sort.Strings(res.ExternalNodes)
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

// Change statuses, set by CompareBase in diff mode.
const (
	StatusNew      = "new"      // not present on the base revision
	StatusWorsened = "worsened" // present on the base, with evidence in new files
	StatusExisting = "existing" // present on the base, nothing new
)

// CompareBase marks each finding of head against the findings of the base
// revision, so that only drift introduced by a change fails the check.
// A finding is new when its ID is absent on the base, worsened when it has
// code evidence in files the base finding did not have (compared by file,
// not line, so unrelated edits that shift lines do not count), and existing
// otherwise. Existing findings stop failing. It returns the IDs of failing
// base findings that no longer occur (fixed by the change).
func CompareBase(head, base *Result) (fixed []string) {
	byID := map[string]Finding{}
	for _, f := range base.Findings {
		byID[f.ID] = f
	}
	seen := map[string]bool{}
	for i := range head.Findings {
		f := &head.Findings[i]
		seen[f.ID] = true
		b, ok := byID[f.ID]
		if !ok {
			f.Status = StatusNew
			continue
		}
		files := map[string]bool{}
		for _, l := range b.Code {
			files[l.File] = true
		}
		for _, l := range f.Code {
			if !files[l.File] {
				f.NewEvidence = append(f.NewEvidence, l)
			}
		}
		if len(f.NewEvidence) > 0 {
			f.Status = StatusWorsened
			continue
		}
		f.Status = StatusExisting
		f.Failing = false
	}
	for _, f := range base.Findings {
		if f.Failing && !seen[f.ID] {
			fixed = append(fixed, f.ID)
		}
	}
	sort.Strings(fixed)
	return fixed
}
