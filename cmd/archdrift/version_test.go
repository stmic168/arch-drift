package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestActionRef(t *testing.T) {
	for v, want := range map[string]string{
		"v0.1.0":                               "v0.1.0",
		"v1.12.3":                              "v1.12.3",
		"dev":                                  "main",
		"v0.1.1-0.20261104040458-8037eb1a0a0d": "main", // pseudo-version from go install @main
		"v0.2.0-rc.1":                          "main",
	} {
		if got := actionRef(v); got != want {
			t.Errorf("actionRef(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	old := version
	version = "v9.8.7"
	defer func() { version = old }()
	for _, arg := range []string{"version", "--version"} {
		var so, se bytes.Buffer
		if code := run(t.Context(), []string{arg}, &so, &se, nil); code != 0 || strings.TrimSpace(so.String()) != "archdrift v9.8.7" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", arg, code, so.String(), se.String())
		}
	}
	if got := initWorkflow("server", "server/docs/a.md", actionRef(version)); !bytes.Contains(got, []byte("uses: stmic168/arch-drift@v9.8.7\n")) {
		t.Errorf("workflow not pinned to the release:\n%s", got)
	}
}
