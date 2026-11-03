package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stmic168/arch-drift/internal/codemodel"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	c, err := Load(write(t, `{
  "components": [{"id": "billing", "paths": ["services/billing"]}],
  "external": [{"id": "ext:s3", "prefixes": ["github.com/aws/aws-sdk-go-v2/service/s3"]}],
  "exclude": ["gen"],
  "default_layout": false,
  "transitive": {"required": true},
  "undocumented_components": {"fail": true, "ignore": ["pkg/ext*"]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	want := codemodel.Options{
		Components:      []codemodel.ComponentRule{{ID: "billing", Paths: []string{"services/billing"}}},
		External:        []codemodel.ExternalRule{{ID: "ext:s3", Prefixes: []string{"github.com/aws/aws-sdk-go-v2/service/s3"}}},
		Exclude:         []string{"gen"},
		NoDefaultLayout: true,
	}
	if got := c.ScanOptions(); !reflect.DeepEqual(got, want) {
		t.Errorf("ScanOptions = %+v", got)
	}
	if d := c.DiffOptions(); !d.TransitiveRequired || d.TransitiveForbidden || !d.FailUndocumentedComponents || len(d.IgnoreUndocumented) != 1 {
		t.Errorf("DiffOptions = %+v", d)
	}
	// Omitted default_layout keeps the default layout.
	c, _ = Load(write(t, `{}`))
	if c.ScanOptions().NoDefaultLayout {
		t.Error("default_layout should default to true")
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field":   `{"componentz": []}`,
		"empty id":        `{"components": [{"id": "", "paths": ["a"]}]}`,
		"ext component":   `{"components": [{"id": "ext:x", "paths": ["a"]}]}`,
		"no paths":        `{"components": [{"id": "a", "paths": []}]}`,
		"duplicate":       `{"components": [{"id": "a", "paths": ["a"]}, {"id": "a", "paths": ["b"]}]}`,
		"absolute path":   `{"components": [{"id": "a", "paths": ["/etc"]}]}`,
		"parent path":     `{"components": [{"id": "a", "paths": ["../x"]}]}`,
		"bad external id": `{"external": [{"id": "s3", "prefixes": ["x"]}]}`,
		"no prefixes":     `{"external": [{"id": "ext:s3", "prefixes": []}]}`,
		"bad exclude":     `{"exclude": ["/tmp"]}`,
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), FileName) {
			t.Errorf("%s: want error naming the file, got %v", name, err)
		}
	}
}
