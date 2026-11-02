package docmodel

import (
	"context"
	"reflect"
	"testing"
)

type countingExtractor struct {
	calls int
	m     *Model
}

func (c *countingExtractor) Extract(context.Context, string, []string) (*Model, Usage, error) {
	c.calls++
	return c.m, Usage{Source: "api", Model: "claude-sonnet-5-5", InputTokens: 10, OutputTokens: 5}, nil
}

func TestCachedExtractor(t *testing.T) {
	inner := &countingExtractor{m: &Model{Components: []Component{{Name: "A", CodeIDs: []string{"cmd/a"}, Quote: "A"}}, Relations: []Relation{}}}
	c := CachedExtractor{Inner: inner, Dir: t.TempDir(), ModelName: "claude-sonnet-5-5"}
	ctx := context.Background()

	m1, u1, err := c.Extract(ctx, "doc", []string{"cmd/a"})
	if err != nil || u1.Source != "api" {
		t.Fatalf("first: %v %+v", err, u1)
	}
	m2, u2, err := c.Extract(ctx, "doc", []string{"cmd/a"})
	if err != nil || u2.Source != "cache" || u2.InputTokens != 0 || u2.Model != "claude-sonnet-5-5" {
		t.Fatalf("second: %v %+v", err, u2)
	}
	if inner.calls != 1 || !reflect.DeepEqual(m1, m2) {
		t.Fatalf("calls=%d, equal=%v", inner.calls, reflect.DeepEqual(m1, m2))
	}
	// Any change to doc, IDs or model is a cache miss.
	c.Extract(ctx, "doc2", []string{"cmd/a"})
	c.Extract(ctx, "doc", []string{"cmd/a", "cmd/b"})
	c.ModelName = "claude-opus-5-5"
	c.Extract(ctx, "doc", []string{"cmd/a"})
	if inner.calls != 4 {
		t.Fatalf("calls=%d, want 4", inner.calls)
	}
}

func TestCacheKeyDistinct(t *testing.T) {
	a := CacheKey("ab", []string{"c"}, "m")
	if a == CacheKey("a", []string{"bc"}, "m") || a == CacheKey("ab", []string{"c"}, "n") {
		t.Fatal("cache key collision")
	}
}
