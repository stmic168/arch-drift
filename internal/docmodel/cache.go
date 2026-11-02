package docmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CacheKey hashes everything that determines the extraction result.
func CacheKey(doc string, ids []string, model string) string {
	h := sha256.New()
	fmt.Fprintf(h, "archdrift-docmodel-v1\x00%s\x00%s\x00%s", model, strings.Join(ids, "\n"), doc)
	return hex.EncodeToString(h.Sum(nil))
}

type cacheEntry struct {
	Model *Model `json:"doc_model"`
	Usage Usage  `json:"usage"`
}

// CachedExtractor wraps another extractor with an on-disk cache under Dir.
type CachedExtractor struct {
	Inner     Extractor
	Dir       string
	ModelName string
}

func (c CachedExtractor) Extract(ctx context.Context, doc string, ids []string) (*Model, Usage, error) {
	p := filepath.Join(c.Dir, CacheKey(doc, ids, c.ModelName)+".json")
	if b, err := os.ReadFile(p); err == nil {
		var e cacheEntry
		if json.Unmarshal(b, &e) == nil && e.Model != nil {
			// Cached hits cost nothing; keep the original model name for the report.
			return e.Model, Usage{Source: "cache", Model: e.Usage.Model}, nil
		}
	}
	m, u, err := c.Inner.Extract(ctx, doc, ids)
	if err != nil {
		return nil, u, err
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return nil, u, fmt.Errorf("create cache dir: %w", err)
	}
	b, err := json.MarshalIndent(cacheEntry{Model: m, Usage: u}, "", "  ")
	if err != nil {
		return nil, u, err
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		return nil, u, fmt.Errorf("write cache: %w", err)
	}
	return m, u, nil
}
