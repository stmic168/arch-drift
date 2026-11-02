package docmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LockVersion is the current lock file format.
const LockVersion = 1

// Lock is a reviewed, committed doc model. While the doc is unchanged,
// `archdrift check` reads the mapping from the lock and never calls the API,
// so results are deterministic and free.
type Lock struct {
	Version   int      `json:"version"`
	DocSHA256 string   `json:"doc_sha256"`
	Model     string   `json:"model"`    // model that produced the mapping, or "recorded"
	CodeIDs   []string `json:"code_ids"` // code IDs the mapping was made against
	DocModel  *Model   `json:"doc_model"`
}

// ErrStaleLock means the doc changed since the lock was written.
var ErrStaleLock = errors.New("lock file is stale: the design doc changed since it was written; run `archdrift lock` and review the result")

// DocHash is the hash stored in a lock.
func DocHash(doc string) string {
	h := sha256.Sum256([]byte(doc))
	return hex.EncodeToString(h[:])
}

// DefaultLockPath puts the lock next to the doc: docs/design.md -> docs/design.lock.json.
func DefaultLockPath(docPath string) string {
	return strings.TrimSuffix(docPath, filepath.Ext(docPath)) + ".lock.json"
}

// NewLock builds a lock for doc from a verified model.
func NewLock(doc string, ids []string, model string, m *Model) *Lock {
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	return &Lock{Version: LockVersion, DocSHA256: DocHash(doc), Model: model, CodeIDs: ids, DocModel: m}
}

// LoadLock reads and validates a lock file.
func LoadLock(p string) (*Lock, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var l Lock
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("decode lock %s: %w", p, err)
	}
	if l.Version != LockVersion {
		return nil, fmt.Errorf("lock %s has version %d, want %d", p, l.Version, LockVersion)
	}
	if l.DocModel == nil {
		return nil, fmt.Errorf("lock %s has no doc_model", p)
	}
	return &l, nil
}

// WriteLock writes a lock as stable, indented JSON.
func WriteLock(p string, l *Lock) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(p); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// LockExtractor serves the doc model from a lock.
type LockExtractor struct{ Lock *Lock }

func (e LockExtractor) Extract(_ context.Context, doc string, _ []string) (*Model, Usage, error) {
	if e.Lock.DocSHA256 != DocHash(doc) {
		return nil, Usage{}, ErrStaleLock
	}
	return e.Lock.DocModel, Usage{Source: "lock", Model: e.Lock.Model}, nil
}

// DiffIDs reports code IDs added and removed since the lock was written.
func (l *Lock) DiffIDs(ids []string) (added, removed []string) {
	was := map[string]bool{}
	for _, id := range l.CodeIDs {
		was[id] = true
	}
	now := map[string]bool{}
	for _, id := range ids {
		now[id] = true
		if !was[id] {
			added = append(added, id)
		}
	}
	for _, id := range l.CodeIDs {
		if !now[id] {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
