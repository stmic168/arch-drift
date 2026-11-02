package docmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLockRoundTripAndStale(t *testing.T) {
	m := &Model{Components: []Component{{Name: "API", CodeIDs: []string{"cmd/api"}, Quote: "API"}}, Relations: []Relation{}}
	p := filepath.Join(t.TempDir(), "sub", "design.lock.json")
	if err := WriteLock(p, NewLock("doc v1", []string{"internal/x", "cmd/api"}, "claude-sonnet-5-5", m)); err != nil {
		t.Fatal(err)
	}
	l, err := LoadLock(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l.CodeIDs, []string{"cmd/api", "internal/x"}) {
		t.Errorf("code IDs not sorted: %v", l.CodeIDs)
	}
	got, u, err := LockExtractor{Lock: l}.Extract(context.Background(), "doc v1", nil)
	if err != nil || u.Source != "lock" || u.Model != "claude-sonnet-5-5" || !reflect.DeepEqual(got, m) {
		t.Fatalf("fresh lock: %v %+v %+v", err, u, got)
	}
	if _, _, err := (LockExtractor{Lock: l}).Extract(context.Background(), "doc v2", nil); !errors.Is(err, ErrStaleLock) {
		t.Fatalf("edited doc: want ErrStaleLock, got %v", err)
	}
}

func TestLoadLockRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"unknown field": `{"version":1,"doc_sha256":"x","model":"m","code_ids":[],"doc_model":{"components":[],"relations":[]},"extra":1}`,
		"wrong version": `{"version":9,"doc_sha256":"x","model":"m","code_ids":[],"doc_model":{"components":[],"relations":[]}}`,
		"no doc model":  `{"version":1,"doc_sha256":"x","model":"m","code_ids":[]}`,
	}
	for name, body := range cases {
		p := filepath.Join(dir, name+".json")
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := LoadLock(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestDiffIDsAndDefaultPath(t *testing.T) {
	l := &Lock{CodeIDs: []string{"a", "b", "c"}}
	added, removed := l.DiffIDs([]string{"b", "c", "d", "e"})
	if !reflect.DeepEqual(added, []string{"d", "e"}) || !reflect.DeepEqual(removed, []string{"a"}) {
		t.Errorf("added=%v removed=%v", added, removed)
	}
	if got := DefaultLockPath("docs/design.md"); got != "docs/design.lock.json" {
		t.Errorf("DefaultLockPath = %q", got)
	}
}
