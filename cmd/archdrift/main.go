// Command archdrift compares a Go repository with a Markdown design doc and
// reports architecture drift with file:line and verbatim-quote evidence.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/docmodel"
	"github.com/stmic168/arch-drift/internal/drift"
	"github.com/stmic168/arch-drift/internal/report"
)

// Exit codes.
const (
	exitOK    = 0
	exitDrift = 1
	exitError = 2
)

const usage = `usage: archdrift check --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>]

  --repo       Go repository root (must contain go.mod)
  --doc        Markdown design document
  --out        output directory (default "archdrift-out")
  --model      Claude model for doc extraction (default "` + docmodel.DefaultModel + `")
  --doc-model  load a recorded doc model JSON and skip the API

Exit codes: 0 no drift, 1 drift, 2 error.
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run executes the CLI. newExtractor, when non-nil, replaces the live API
// extractor (used by tests).
func run(ctx context.Context, args []string, stdout, stderr io.Writer, newExtractor func(model string) docmodel.Extractor) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	repo := fs.String("repo", "", "")
	doc := fs.String("doc", "", "")
	out := fs.String("out", "archdrift-out", "")
	model := fs.String("model", docmodel.DefaultModel, "")
	docModelPath := fs.String("doc-model", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return exitError
	}
	if *repo == "" || *doc == "" || fs.NArg() > 0 {
		fs.Usage()
		return exitError
	}

	var ext docmodel.Extractor
	switch {
	case *docModelPath != "":
		ext = docmodel.FileExtractor{Path: *docModelPath}
	case newExtractor != nil:
		ext = docmodel.CachedExtractor{Inner: newExtractor(*model), Dir: filepath.Join(*out, "cache"), ModelName: *model}
	default:
		ext = docmodel.CachedExtractor{Inner: docmodel.NewAnthropicExtractor(*model), Dir: filepath.Join(*out, "cache"), ModelName: *model}
	}

	rep, err := check(ctx, *repo, *doc, *model, ext)
	if err != nil {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	if err := report.Write(*out, rep); err != nil {
		fmt.Fprintf(stderr, "archdrift: write report: %v\n", err)
		return exitError
	}
	verdict := "no drift"
	if rep.Summary.Drift {
		verdict = "drift detected"
	}
	fmt.Fprintf(stdout, "archdrift: %s: %d failing finding(s), %d unverifiable, %d dropped claim(s)\n",
		verdict, rep.Summary.Failing, rep.Summary.ByType[drift.Unverifiable], rep.Summary.Dropped)
	fmt.Fprintf(stdout, "report: %s\n", filepath.Join(*out, "report.md"))
	if rep.Summary.Drift {
		return exitDrift
	}
	return exitOK
}

func check(ctx context.Context, repo, docPath, model string, ext docmodel.Extractor) (*report.Report, error) {
	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		return nil, fmt.Errorf("read doc: %w", err)
	}
	if len(docBytes) == 0 {
		return nil, errors.New("design doc is empty")
	}
	code, err := codemodel.Scan(repo)
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	ids := code.IDs()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	raw, usage, err := ext.Extract(ctx, string(docBytes), ids)
	if err != nil {
		return nil, fmt.Errorf("extract doc model: %w", err)
	}
	if len(raw.Components) == 0 {
		return nil, fmt.Errorf("doc model has no components (source %s); extraction failed or the doc describes no architecture", usage.Source)
	}
	if usage.Model == "" && usage.Source != "recorded" {
		usage.Model = model
	}
	verified, dropped := docmodel.Verify(raw, string(docBytes), ids)
	res := drift.Diff(code, verified)
	return report.Build(report.Input{
		Repo: filepath.ToSlash(filepath.Clean(repo)), Doc: filepath.ToSlash(filepath.Clean(docPath)),
		RequestedModel: model, Code: code, DocModel: verified, Dropped: dropped, Result: res, Usage: usage,
	}), nil
}
