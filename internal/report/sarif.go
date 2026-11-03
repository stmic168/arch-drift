package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/docmodel"
	"github.com/stmic168/arch-drift/internal/drift"
)

// SARIF 2.1.0, the subset GitHub code scanning reads.
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifRule struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	ShortDescription     sarifText         `json:"shortDescription"`
	FullDescription      sarifText         `json:"fullDescription"`
	DefaultConfiguration map[string]string `json:"defaultConfiguration"`
	Help                 sarifText         `json:"help"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          map[string]any    `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

var sarifRules = []struct{ id, name, short, full, level string }{
	{drift.ForbiddenEdge, "ForbiddenEdge", "Forbidden dependency", "The design doc says this component must not depend on the other one, but the code imports it.", "error"},
	{drift.MissingComponent, "MissingComponent", "Documented component missing", "The design doc describes a component that no code package implements.", "error"},
	{drift.MissingEdge, "MissingEdge", "Documented dependency missing", "The design doc says this component depends on the other one, but no import shows it.", "error"},
	{drift.UndocumentedEdge, "UndocumentedEdge", "Undocumented dependency", "The code has a dependency between two documented components that the design doc does not mention.", "warning"},
	{drift.UndocumentedComponent, "UndocumentedComponent", "Undocumented component", "A code component that no doc component maps to.", "note"},
	{drift.Unverifiable, "Unverifiable", "Relation not checkable by imports", "A documented relation that static imports can neither prove nor disprove.", "note"},
}

// maxSARIFLocations caps locations per result; the report lists all evidence.
const maxSARIFLocations = 10

func sarifLevel(f drift.Finding) string {
	switch {
	case !f.Failing:
		return "note"
	case f.Severity == drift.Low:
		return "warning"
	default:
		return "error"
	}
}

// SARIF renders the report for code scanning. Paths are made relative to
// root (the checkout root GitHub resolves them against); repo is the scanned
// repo directory. Locations outside root are left out.
func SARIF(r *Report, root, repo string, docs []docmodel.Doc) []byte {
	absRoot, _ := filepath.Abs(root)
	absRepo, _ := filepath.Abs(repo)
	under := func(p string) (string, bool) {
		rel, err := filepath.Rel(absRoot, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	codeLoc := func(l codemodel.Location) (sarifLocation, bool) {
		uri, ok := under(filepath.Join(absRepo, filepath.FromSlash(l.File)))
		return sarifLocation{sarifPhysical{sarifArtifact{uri}, sarifRegion{l.Line}}}, ok
	}
	docLoc := func(f drift.Finding) (sarifLocation, bool) {
		for _, d := range docs {
			if f.DocFile != "" && f.DocFile != d.Path {
				continue
			}
			line := quoteLine(d.Text, f.DocQuote)
			if line == 0 {
				continue
			}
			abs, _ := filepath.Abs(d.Path)
			uri, ok := under(abs)
			return sarifLocation{sarifPhysical{sarifArtifact{uri}, sarifRegion{line}}}, ok
		}
		return sarifLocation{}, false
	}

	run := sarifRun{Tool: sarifTool{Driver: sarifDriver{Name: "archdrift", InformationURI: "https://github.com/stmic168/arch-drift"}}, Results: []sarifResult{}}
	for _, rule := range sarifRules {
		run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
			ID: rule.id, Name: rule.name, ShortDescription: sarifText{rule.short}, FullDescription: sarifText{rule.full},
			DefaultConfiguration: map[string]string{"level": rule.level},
			Help:                 sarifText{rule.full + " Evidence: file:line from the code and a verbatim quote from the design doc."},
		})
	}
	for _, f := range r.Findings {
		msg := f.Message
		if f.DocQuote != "" {
			msg += " Doc: \"" + docmodel.NormalizeSpace(f.DocQuote) + "\""
		}
		res := sarifResult{
			RuleID: f.Type, Level: sarifLevel(f), Message: sarifText{msg},
			PartialFingerprints: map[string]string{"archdriftFindingId/v1": f.ID},
			Properties:          map[string]any{"severity": f.Severity, "subject": f.Subject},
		}
		if f.Status != "" {
			res.Properties["status"] = f.Status
		}
		if f.Accepted != nil {
			res.Properties["accepted"] = f.Accepted.Reason
		}
		evidence := f.Code
		if len(f.NewEvidence) > 0 {
			evidence = f.NewEvidence
		}
		for _, l := range evidence {
			if len(res.Locations) == maxSARIFLocations {
				break
			}
			if loc, ok := codeLoc(l); ok {
				res.Locations = append(res.Locations, loc)
			}
		}
		if len(res.Locations) == 0 {
			if loc, ok := docLoc(f); ok {
				res.Locations = append(res.Locations, loc)
			}
		}
		run.Results = append(run.Results, res)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(sarifLog{Schema: "https://json.schemastore.org/sarif-2.1.0.json", Version: "2.1.0", Runs: []sarifRun{run}})
	return buf.Bytes()
}

// WriteSARIF writes report.sarif into dir.
func WriteSARIF(dir string, r *Report, root, repo string, docs []docmodel.Doc) error {
	return os.WriteFile(filepath.Join(dir, "report.sarif"), SARIF(r, root, repo, docs), 0o644)
}

// quoteLine finds the 1-based line where quote starts in doc, comparing with
// whitespace normalized the same way the verifier does. 0 if not found.
func quoteLine(doc, quote string) int {
	q := docmodel.NormalizeSpace(quote)
	if q == "" {
		return 0
	}
	var norm strings.Builder
	var lineOf []int // line number for each byte written to norm
	line, space := 1, false
	for _, r := range doc {
		if unicode.IsSpace(r) {
			if r == '\n' {
				line++
			}
			space = true
			continue
		}
		if space && norm.Len() > 0 {
			norm.WriteByte(' ')
			lineOf = append(lineOf, line)
		}
		space = false
		n := norm.Len()
		norm.WriteRune(r)
		for i := n; i < norm.Len(); i++ {
			lineOf = append(lineOf, line)
		}
	}
	i := strings.Index(norm.String(), q)
	if i < 0 {
		return 0
	}
	return lineOf[i]
}
