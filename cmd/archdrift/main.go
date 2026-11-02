// Command archdrift compares a Go repository with a Markdown design doc and
// reports architecture drift with file:line and verbatim-quote evidence.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

const usage = `usage:
  archdrift check --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>] [--lock <path> | --no-lock]
  archdrift lock  --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>] [--lock <path>]

check  compares code with the doc and writes <out>/report.md and report.json.
lock   extracts the doc model once and writes a lock file to review and commit.
       While the doc is unchanged, check reads the lock and never calls the API.

  --repo       Go repository root (must contain go.mod)
  --doc        Markdown design document
  --out        output directory (default "archdrift-out"); the API cache lives in <out>/cache
  --model      Claude model for doc extraction (default "` + docmodel.DefaultModel + `")
  --doc-model  use a recorded doc model JSON instead of the API (ignores any lock)
  --lock       lock file path (default: next to the doc, design.md -> design.lock.json)
  --no-lock    check only: ignore the lock and extract from the doc

Exit codes: check 0 no drift, 1 drift, 2 error; lock 0 written, 2 error.
`

type options struct {
	repo, doc, out, model, docModel, lock string
	noLock                                bool
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run executes the CLI. newExtractor, when non-nil, replaces the live API
// extractor (used by tests).
func run(ctx context.Context, args []string, stdout, stderr io.Writer, newExtractor func(model string) docmodel.Extractor) int {
	if len(args) == 0 || (args[0] != "check" && args[0] != "lock") {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	var o options
	fs.StringVar(&o.repo, "repo", "", "")
	fs.StringVar(&o.doc, "doc", "", "")
	fs.StringVar(&o.out, "out", "archdrift-out", "")
	fs.StringVar(&o.model, "model", docmodel.DefaultModel, "")
	fs.StringVar(&o.docModel, "doc-model", "", "")
	fs.StringVar(&o.lock, "lock", "", "")
	if cmd == "check" {
		fs.BoolVar(&o.noLock, "no-lock", false, "")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return exitError
	}
	if o.repo == "" || o.doc == "" || fs.NArg() > 0 || (o.noLock && o.lock != "") {
		fs.Usage()
		return exitError
	}
	apiExtractor := func() docmodel.Extractor {
		var inner docmodel.Extractor
		if newExtractor != nil {
			inner = newExtractor(o.model)
		} else {
			inner = docmodel.NewAnthropicExtractor(o.model)
		}
		return docmodel.CachedExtractor{Inner: inner, Dir: filepath.Join(o.out, "cache"), ModelName: o.model}
	}
	if cmd == "lock" {
		return runLock(ctx, o, apiExtractor, stdout, stderr)
	}
	return runCheck(ctx, o, apiExtractor, stdout, stderr)
}

// inputs holds what every command reads first.
type inputs struct {
	doc  string
	code *codemodel.Model
}

func load(o options) (*inputs, error) {
	b, err := os.ReadFile(o.doc)
	if err != nil {
		return nil, fmt.Errorf("read doc: %w", err)
	}
	if len(b) == 0 {
		return nil, errors.New("design doc is empty")
	}
	code, err := codemodel.Scan(o.repo)
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	return &inputs{doc: string(b), code: code}, nil
}

func extract(ctx context.Context, ext docmodel.Extractor, in *inputs, model string) (*docmodel.Model, docmodel.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	raw, usage, err := ext.Extract(ctx, in.doc, in.code.IDs())
	if err != nil {
		return nil, usage, fmt.Errorf("extract doc model: %w", err)
	}
	if len(raw.Components) == 0 {
		return nil, usage, fmt.Errorf("doc model has no components (source %s); extraction failed or the doc describes no architecture", usage.Source)
	}
	if usage.Model == "" && usage.Source != "recorded" {
		usage.Model = model
	}
	return raw, usage, nil
}

func runCheck(ctx context.Context, o options, apiExtractor func() docmodel.Extractor, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	in, err := load(o)
	if err != nil {
		return fail(err)
	}

	var (
		ext      docmodel.Extractor
		lockPath string
		notes    []string
	)
	switch {
	case o.docModel != "":
		ext = docmodel.FileExtractor{Path: o.docModel}
	case !o.noLock:
		p := o.lock
		if p == "" {
			p = docmodel.DefaultLockPath(o.doc)
		}
		l, err := docmodel.LoadLock(p)
		switch {
		case err == nil:
			ext, lockPath = docmodel.LockExtractor{Lock: l}, filepath.ToSlash(filepath.Clean(p))
			notes = lockNotes(l, in.code.IDs())
		case errors.Is(err, fs.ErrNotExist) && o.lock == "":
			ext = apiExtractor() // no lock next to the doc: extract live
		default:
			return fail(fmt.Errorf("read lock: %w", err))
		}
	default:
		ext = apiExtractor()
	}

	raw, usage, err := extract(ctx, ext, in, o.model)
	if err != nil {
		return fail(err)
	}
	ids := in.code.IDs()
	verified, dropped := docmodel.Verify(raw, in.doc, ids)
	res := drift.Diff(in.code, verified)
	rep := report.Build(report.Input{
		Repo: filepath.ToSlash(filepath.Clean(o.repo)), Doc: filepath.ToSlash(filepath.Clean(o.doc)),
		RequestedModel: o.model, Code: in.code, DocModel: verified, Dropped: dropped, Result: res, Usage: usage,
		Lock: lockPath, Notes: notes,
	})
	if err := report.Write(o.out, rep); err != nil {
		return fail(fmt.Errorf("write report: %w", err))
	}
	verdict := "no drift"
	if rep.Summary.Drift {
		verdict = "drift detected"
	}
	fmt.Fprintf(stdout, "archdrift: %s: %d failing finding(s), %d unverifiable, %d dropped claim(s) [doc model: %s]\n",
		verdict, rep.Summary.Failing, rep.Summary.ByType[drift.Unverifiable], rep.Summary.Dropped, usage.Source)
	for _, n := range notes {
		fmt.Fprintf(stdout, "note: %s\n", n)
	}
	fmt.Fprintf(stdout, "report: %s\n", filepath.Join(o.out, "report.md"))
	if rep.Summary.Drift {
		return exitDrift
	}
	return exitOK
}

// lockNotes explains code components that changed since the lock was made.
func lockNotes(l *docmodel.Lock, ids []string) []string {
	added, removed := l.DiffIDs(ids)
	var notes []string
	if len(added) > 0 {
		notes = append(notes, fmt.Sprintf("Code components added since the lock was written: %s. The lock does not map them, so they show as UNDOCUMENTED_COMPONENT; if the doc already describes them, re-run `archdrift lock`.", strings.Join(added, ", ")))
	}
	if len(removed) > 0 {
		notes = append(notes, fmt.Sprintf("Code components removed since the lock was written: %s. Lock mappings to them are dropped as unknown code IDs.", strings.Join(removed, ", ")))
	}
	return notes
}

func runLock(ctx context.Context, o options, apiExtractor func() docmodel.Extractor, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	in, err := load(o)
	if err != nil {
		return fail(err)
	}
	ext := apiExtractor()
	if o.docModel != "" {
		ext = docmodel.FileExtractor{Path: o.docModel}
	}
	raw, usage, err := extract(ctx, ext, in, o.model)
	if err != nil {
		return fail(err)
	}
	ids := in.code.IDs()
	verified, dropped := docmodel.Verify(raw, in.doc, ids)
	if len(verified.Components) == 0 {
		return fail(errors.New("no component survived verification; nothing to lock"))
	}
	model := usage.Model
	if usage.Source == "recorded" {
		model = "recorded"
	}
	p := o.lock
	if p == "" {
		p = docmodel.DefaultLockPath(o.doc)
	}
	if err := docmodel.WriteLock(p, docmodel.NewLock(in.doc, ids, model, verified)); err != nil {
		return fail(fmt.Errorf("write lock: %w", err))
	}
	writeLockReview(stdout, p, model, usage, verified, dropped, ids)
	return exitOK
}

// writeLockReview prints what the architect should check before committing the lock.
func writeLockReview(w io.Writer, p, model string, u docmodel.Usage, m *docmodel.Model, dropped []docmodel.Dropped, ids []string) {
	fmt.Fprintf(w, "archdrift: wrote %s (model %s, source %s, %d components, %d relations, %d dropped)\n",
		p, model, u.Source, len(m.Components), len(m.Relations), len(dropped))
	if u.Source == "api" {
		if c, ok := report.Cost(u); ok {
			fmt.Fprintf(w, "usage: %d input / %d output tokens, $%.4f\n", u.InputTokens, u.OutputTokens, c)
		}
	}
	fmt.Fprintln(w, "\nReview this mapping, edit the lock if needed, then commit it.")
	fmt.Fprintln(w, "\nComponents (doc name -> code IDs):")
	mapped := map[string]bool{}
	for _, c := range m.Components {
		target := strings.Join(c.CodeIDs, ", ")
		if target == "" {
			target = "(no code: will be MISSING_COMPONENT)"
		}
		for _, id := range c.CodeIDs {
			mapped[id] = true
		}
		fmt.Fprintf(w, "  %-20s -> %s\n", c.Name, target)
	}
	fmt.Fprintln(w, "\nRelations:")
	for _, r := range m.Relations {
		proto := ""
		if r.Protocol != "" {
			proto = " [" + r.Protocol + "]"
		}
		fmt.Fprintf(w, "  %-9s %s -> %s%s\n", r.Kind, r.From, r.To, proto)
	}
	var unmapped []string
	for _, id := range ids {
		if !mapped[id] {
			unmapped = append(unmapped, id)
		}
	}
	if len(unmapped) > 0 {
		fmt.Fprintf(w, "\nCode IDs no doc component maps to (will be UNDOCUMENTED_COMPONENT): %s\n", strings.Join(unmapped, ", "))
	}
	if len(dropped) > 0 {
		fmt.Fprintln(w, "\nDropped claims (failed verification, not written to the lock):")
		for _, d := range dropped {
			detail := ""
			if d.Detail != "" {
				detail = " (" + d.Detail + ")"
			}
			fmt.Fprintf(w, "  %s %s%s: %s\n", d.Claim, d.Name, detail, d.Reason)
		}
	}
}
