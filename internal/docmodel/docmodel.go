// Package docmodel turns a Markdown design doc into a list of claims
// (components and relations) mapped to code IDs. Claims come from an LLM
// behind the Extractor interface and are verified deterministically before use.
package docmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Relation kinds.
const (
	KindRequired  = "required"
	KindForbidden = "forbidden"
)

// Component is a doc-declared component mapped to zero or more code IDs.
type Component struct {
	Name    string   `json:"name"`
	CodeIDs []string `json:"code_ids"`
	Quote   string   `json:"quote"`
	// External marks something the doc places outside this codebase (users,
	// client apps, third-party or managed systems). With no code IDs it is
	// not reported as MISSING_COMPONENT.
	External bool `json:"external,omitempty"`
}

// Relation is a doc-declared dependency between two doc components (by name).
type Relation struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Quote    string `json:"quote"`
}

// Model is the structured output of the extractor.
type Model struct {
	Components []Component `json:"components"`
	Relations  []Relation  `json:"relations"`
}

// Usage records LLM token usage for one extraction.
type Usage struct {
	Source                   string `json:"source"` // "api", "cache", "recorded"
	Model                    string `json:"model,omitempty"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
}

// Extractor reads the doc text and the list of known code IDs and returns
// the doc's claims. Implementations must never receive source code.
type Extractor interface {
	Extract(ctx context.Context, doc string, ids []string) (*Model, Usage, error)
}

// FileExtractor loads a recorded doc model and never calls the API.
type FileExtractor struct{ Path string }

func (f FileExtractor) Extract(_ context.Context, _ string, _ []string) (*Model, Usage, error) {
	m, err := LoadFile(f.Path)
	if err != nil {
		return nil, Usage{}, err
	}
	return m, Usage{Source: "recorded"}, nil
}

// LoadFile reads a doc model JSON file.
func LoadFile(p string) (*Model, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read doc model: %w", err)
	}
	return Parse(b)
}

// Parse decodes a doc model, rejecting unknown fields.
func Parse(b []byte) (*Model, error) {
	var m Model
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode doc model: %w", err)
	}
	return &m, nil
}
