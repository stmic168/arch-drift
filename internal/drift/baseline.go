package drift

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// BaselineVersion is the current baseline file format.
const BaselineVersion = 1

// BaselineFile is the default baseline file at the repo root.
const BaselineFile = "archdrift.baseline.json"

// TodoReason is the placeholder `archdrift baseline` writes for new entries.
const TodoReason = "TODO: explain why this drift is accepted"

// Accepted is one known, accepted finding.
type Accepted struct {
	ID      string `json:"id"`                // Finding.ID
	Reason  string `json:"reason"`            // why it is accepted
	Expires string `json:"expires,omitempty"` // YYYY-MM-DD; after this date the finding fails again
}

// Baseline lists accepted findings.
type Baseline struct {
	Version  int        `json:"version"`
	Accepted []Accepted `json:"accepted"`
}

// LoadBaseline reads and validates a baseline file.
func LoadBaseline(p string) (*Baseline, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var bl Baseline
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bl); err != nil {
		return nil, fmt.Errorf("decode %s: %w", p, err)
	}
	if bl.Version != BaselineVersion {
		return nil, fmt.Errorf("%s: version %d, want %d", p, bl.Version, BaselineVersion)
	}
	seen := map[string]bool{}
	for i, a := range bl.Accepted {
		switch {
		case a.ID == "":
			return nil, fmt.Errorf("%s: accepted[%d]: empty id", p, i)
		case strings.TrimSpace(a.Reason) == "":
			return nil, fmt.Errorf("%s: accepted[%d] %q: a reason is required", p, i, a.ID)
		case seen[a.ID]:
			return nil, fmt.Errorf("%s: accepted[%d]: duplicate id %q", p, i, a.ID)
		}
		if a.Expires != "" {
			if _, err := time.Parse(time.DateOnly, a.Expires); err != nil {
				return nil, fmt.Errorf("%s: accepted[%d] %q: expires must be YYYY-MM-DD", p, i, a.ID)
			}
		}
		seen[a.ID] = true
	}
	return &bl, nil
}

// WriteBaseline writes a baseline as stable, indented JSON.
func WriteBaseline(p string, bl *Baseline) error {
	sort.Slice(bl.Accepted, func(i, j int) bool { return bl.Accepted[i].ID < bl.Accepted[j].ID })
	return os.WriteFile(p, marshal(bl), 0o644)
}

// marshal renders indented JSON without HTML escaping, so "->" stays readable
// in files people edit by hand.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v) // plain structs of strings: cannot fail
	return buf.Bytes()
}

// ApplyBaseline marks accepted findings (not failing any more) and returns
// notes about expired, unused and unjustified entries. today is YYYY-MM-DD.
func ApplyBaseline(res *Result, bl *Baseline, today string) []string {
	byID := map[string]Accepted{}
	for _, a := range bl.Accepted {
		byID[a.ID] = a
	}
	used := map[string]bool{}
	var notes []string
	for i := range res.Findings {
		f := &res.Findings[i]
		a, ok := byID[f.ID]
		if !ok {
			continue
		}
		used[a.ID] = true
		if a.Expires != "" && a.Expires < today {
			notes = append(notes, fmt.Sprintf("Baseline entry `%s` expired on %s; the finding fails again.", a.ID, a.Expires))
			continue
		}
		f.Accepted = &a
		f.Failing = false
	}
	for _, a := range bl.Accepted {
		if !used[a.ID] {
			notes = append(notes, fmt.Sprintf("Baseline entry `%s` no longer matches any finding; remove it from the baseline.", a.ID))
		}
		if strings.HasPrefix(a.Reason, "TODO") {
			notes = append(notes, fmt.Sprintf("Baseline entry `%s` has no real reason yet (%q).", a.ID, a.Reason))
		}
	}
	return notes
}

// NewBaseline accepts every failing finding, keeping the reason and expiry of
// entries in prev that still match. New entries get TodoReason and expire
// after the given number of days.
func NewBaseline(res *Result, prev *Baseline, today string, days int) *Baseline {
	old := map[string]Accepted{}
	if prev != nil {
		for _, a := range prev.Accepted {
			old[a.ID] = a
		}
	}
	t, _ := time.Parse(time.DateOnly, today)
	expires := t.AddDate(0, 0, days).Format(time.DateOnly)
	bl := &Baseline{Version: BaselineVersion, Accepted: []Accepted{}}
	for _, f := range res.Findings {
		if !f.Failing && f.Accepted == nil {
			continue // UNVERIFIABLE never fails; nothing to accept
		}
		if a, ok := old[f.ID]; ok {
			bl.Accepted = append(bl.Accepted, a)
			continue
		}
		bl.Accepted = append(bl.Accepted, Accepted{ID: f.ID, Reason: TodoReason, Expires: expires})
	}
	return bl
}
