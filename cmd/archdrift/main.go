// Command archdrift compares a Go repository with a Markdown design doc and
// reports architecture drift with file:line and verbatim-quote evidence.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/config"
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
  archdrift check --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>] [--lock <path> | --no-lock] [--config <json>] [--base <git ref>]
  archdrift baseline --repo <path> --doc <design.md> [same flags as check]
  archdrift init --repo <path> --doc <design.md> [--mode report|gate] [--force]
  archdrift scan --repo <path> [--config <json>] [--json]
  archdrift lock  --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>] [--lock <path>] [--config <json>]
  archdrift version

check  compares code with the doc and writes <out>/report.md, report.json and
       report.sarif (for GitHub code scanning).
lock   extracts the doc model once and writes a lock file to review and commit.
init   sets a repository up: proposes archdrift.json (components for layouts
       other than cmd/internal/pkg, excludes for tests and tools, the options
       for the mode), writes a GitHub workflow using the action, and in gate
       mode starts a rules doc from a template if it does not exist. No API call.
scan   prints the code model (components, deployables, import edges) without
       reading any doc; use it to write or check archdrift.json.
baseline  accepts every current failing finding into archdrift.baseline.json
       (existing reasons are kept; new entries need a reason before committing).
       While the doc is unchanged, check reads the lock and never calls the API.

  --repo       Go repository root (must contain go.mod)
  --doc        Markdown design document; repeat it to read several files as one doc
  --out        output directory (default "archdrift-out"); the API cache lives in <out>/cache
  --model      Claude model for doc extraction (default "` + docmodel.DefaultModel + `")
  --doc-model  use a recorded doc model JSON instead of the API (ignores any lock)
  --lock       lock file path (default: next to the first doc, design.md -> design.lock.json)
  --no-lock    check only: ignore the lock and extract from the doc
  --sarif-root check only: directory SARIF paths are relative to (default --repo);
               set it to the checkout root when --repo is a subdirectory
  --base       check only: git revision to compare with (e.g. origin/main); only
               drift introduced or worsened since then fails, pre-existing drift
               is listed but does not fail
  --config     config file (default: archdrift.json at the repo root, if present)
  --baseline   baseline file (default: archdrift.baseline.json at the repo root, if present)

Exit codes: check 0 no drift, 1 drift, 2 error; lock and baseline 0 written, 2 error.
`

type options struct {
	repo, out, model, docModel, lock, config, baseline, today, base, sarifRoot string
	docs                                                                       docList
	noLock, fresh                                                              bool

	// resolved from the config file
	configPath string
	scan       codemodel.Options
	diff       drift.Options
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run executes the CLI. newExtractor, when non-nil, replaces the live API
// extractor (used by tests).
func run(ctx context.Context, args []string, stdout, stderr io.Writer, newExtractor func(model string) docmodel.Extractor) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(stdout, "archdrift", toolVersion())
		return 0
	}
	if len(args) == 0 || (args[0] != "check" && args[0] != "lock" && args[0] != "baseline" && args[0] != "scan" && args[0] != "init") {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	if args[0] == "init" {
		return runInit(args[1:], stdout, stderr)
	}
	if args[0] == "scan" {
		return runScan(args[1:], stdout, stderr)
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	var o options
	fs.StringVar(&o.repo, "repo", "", "")
	fs.Var(&o.docs, "doc", "")
	fs.StringVar(&o.out, "out", "archdrift-out", "")
	fs.StringVar(&o.model, "model", docmodel.DefaultModel, "")
	fs.StringVar(&o.docModel, "doc-model", "", "")
	fs.StringVar(&o.lock, "lock", "", "")
	fs.StringVar(&o.config, "config", "", "")
	if cmd != "lock" {
		fs.BoolVar(&o.noLock, "no-lock", false, "")
		fs.StringVar(&o.baseline, "baseline", "", "")
		fs.StringVar(&o.today, "today", "", "") // YYYY-MM-DD, for reproducible runs and tests
	}
	if cmd == "lock" {
		fs.BoolVar(&o.fresh, "fresh", false, "")
	}
	if cmd == "check" {
		fs.StringVar(&o.base, "base", "", "")
		fs.StringVar(&o.sarifRoot, "sarif-root", "", "")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return exitError
	}
	if o.repo == "" || len(o.docs) == 0 || fs.NArg() > 0 || (o.noLock && o.lock != "") {
		fs.Usage()
		return exitError
	}
	if err := resolveConfig(&o); err != nil {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
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
	if o.today == "" {
		o.today = time.Now().UTC().Format(time.DateOnly)
	} else if _, err := time.Parse(time.DateOnly, o.today); err != nil {
		fmt.Fprintln(stderr, "archdrift: --today must be YYYY-MM-DD")
		return exitError
	}
	switch cmd {
	case "lock":
		return runLock(ctx, o, apiExtractor, stdout, stderr)
	case "baseline":
		return runBaseline(ctx, o, apiExtractor, stdout, stderr)
	}
	return runCheck(ctx, o, apiExtractor, stdout, stderr)
}

// resolveConfig loads --config, or archdrift.json at the repo root when present.
func resolveConfig(o *options) error {
	p := o.config
	if p == "" {
		p = filepath.Join(o.repo, config.FileName)
	}
	c, err := config.Load(p)
	switch {
	case err == nil:
		o.configPath = filepath.ToSlash(filepath.Clean(p))
		o.scan, o.diff = c.ScanOptions(), c.DiffOptions()
		return nil
	case errors.Is(err, fs.ErrNotExist) && o.config == "":
		return nil
	default:
		return fmt.Errorf("config: %w", err)
	}
}

// docList collects repeated --doc flags.
type docList []string

func (d *docList) String() string     { return strings.Join(*d, ",") }
func (d *docList) Set(v string) error { *d = append(*d, v); return nil }

// inputs holds what every command reads first.
type inputs struct {
	docs []docmodel.Doc
	doc  string // docs combined, as hashed, cached and sent to the extractor
	code *codemodel.Model
}

func load(o options) (*inputs, error) {
	in := &inputs{}
	seen := map[string]bool{}
	for _, p := range o.docs {
		clean := filepath.ToSlash(filepath.Clean(p))
		if seen[clean] {
			return nil, fmt.Errorf("doc %s given twice", p)
		}
		seen[clean] = true
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read doc: %w", err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("design doc %s is empty", p)
		}
		in.docs = append(in.docs, docmodel.Doc{Path: clean, Text: string(b)})
	}
	in.doc = docmodel.Combine(in.docs)
	code, err := codemodel.Scan(o.repo, o.scan)
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	in.code = code
	return in, nil
}

// docLabel is how the docs appear in the report.
func docLabel(in *inputs) string {
	paths := make([]string, len(in.docs))
	for i, d := range in.docs {
		paths[i] = d.Path
	}
	return strings.Join(paths, ", ")
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

// analysis is the result of the check pipeline before baseline and report.
type analysis struct {
	raw      *docmodel.Model // doc model before verification, reused for the base revision
	in       *inputs
	verified *docmodel.Model
	dropped  []docmodel.Dropped
	res      *drift.Result
	usage    docmodel.Usage
	lockPath string
	notes    []string
}

func analyze(ctx context.Context, o options, apiExtractor func() docmodel.Extractor) (*analysis, error) {
	in, err := load(o)
	if err != nil {
		return nil, err
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
			p = docmodel.DefaultLockPath(o.docs[0])
		}
		l, err := docmodel.LoadLock(p)
		switch {
		case err == nil:
			ext, lockPath = docmodel.LockExtractor{Lock: l}, filepath.ToSlash(filepath.Clean(p))
			notes = lockNotes(l, in.code.IDs())
		case errors.Is(err, fs.ErrNotExist) && o.lock == "":
			ext = apiExtractor() // no lock next to the doc: extract live
		default:
			return nil, fmt.Errorf("read lock: %w", err)
		}
	default:
		ext = apiExtractor()
	}

	raw, usage, err := extract(ctx, ext, in, o.model)
	if err != nil {
		return nil, err
	}
	verified, dropped := docmodel.Verify(raw, in.docs, in.code.IDs())
	notes = append(notes, coverageNotes(in.code.Coverage)...)
	return &analysis{raw: raw, in: in, verified: verified, dropped: dropped, res: drift.Diff(in.code, verified, o.diff),
		usage: usage, lockPath: lockPath, notes: notes}, nil
}

// loadBaseline reads --baseline, or archdrift.baseline.json at the repo root
// when present. A missing explicit file is an error for check (a typo would
// silently fail every accepted finding) but fine for baseline, which creates it.
func loadBaseline(o options, creating bool) (*drift.Baseline, string, error) {
	p := o.baseline
	if p == "" {
		p = filepath.Join(o.repo, drift.BaselineFile)
	}
	bl, err := drift.LoadBaseline(p)
	switch {
	case err == nil:
		return bl, p, nil
	case errors.Is(err, fs.ErrNotExist) && (o.baseline == "" || creating):
		return nil, p, nil
	default:
		return nil, p, fmt.Errorf("baseline: %w", err)
	}
}

func runCheck(ctx context.Context, o options, apiExtractor func() docmodel.Extractor, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	bl, blPath, err := loadBaseline(o, false)
	if err != nil {
		return fail(err)
	}
	a, err := analyze(ctx, o, apiExtractor)
	if err != nil {
		return fail(err)
	}
	in, verified, dropped, res, usage, lockPath, notes := a.in, a.verified, a.dropped, a.res, a.usage, a.lockPath, a.notes
	shownBaseline := ""
	if bl != nil {
		notes = append(notes, drift.ApplyBaseline(res, bl, o.today)...)
		shownBaseline = filepath.ToSlash(filepath.Clean(blPath))
	}
	var fixed []string
	if o.base != "" {
		baseRes, sha, err := baseResult(o, a.raw, in.docs)
		if err != nil {
			return fail(fmt.Errorf("base %s: %w", o.base, err))
		}
		fixed = drift.CompareBase(res, baseRes)
		var nNew, nExisting int
		for _, f := range res.Findings {
			switch {
			case f.Failing:
				nNew++
			case f.Status == drift.StatusExisting && f.Severity != drift.Info:
				nExisting++
			}
		}
		notes = append(notes, fmt.Sprintf("Compared with base %s (%s): %d failing finding(s) introduced or worsened by this change, %d pre-existing (not failing), %d fixed.", o.base, sha, nNew, nExisting, len(fixed)))
	}
	rep := report.Build(report.Input{
		Repo: filepath.ToSlash(filepath.Clean(o.repo)), Doc: docLabel(in),
		RequestedModel: o.model, Code: in.code, DocModel: verified, Dropped: dropped, Result: res, Usage: usage,
		Lock: lockPath, Notes: notes, Base: o.base, Fixed: fixed, Config: o.configPath, Baseline: shownBaseline, FailUndocumented: o.diff.FailUndocumentedComponents, ReportUndocumentedEdges: o.diff.ReportUndocumentedEdges,
	})
	if err := report.Write(o.out, rep); err != nil {
		return fail(fmt.Errorf("write report: %w", err))
	}
	root := o.sarifRoot
	if root == "" {
		root = o.repo
	}
	if err := report.WriteSARIF(o.out, rep, root, o.repo, in.docs); err != nil {
		return fail(fmt.Errorf("write sarif: %w", err))
	}
	verdict := "no drift"
	if rep.Summary.Drift {
		verdict = "drift detected"
	}
	accepted := ""
	if rep.Summary.Accepted > 0 {
		accepted = fmt.Sprintf(", %d accepted by baseline", rep.Summary.Accepted)
	}
	fmt.Fprintf(stdout, "archdrift: %s: %d failing finding(s), %d unverifiable, %d dropped claim(s)%s [doc model: %s]\n",
		verdict, rep.Summary.Failing, rep.Summary.ByType[drift.Unverifiable], rep.Summary.Dropped, accepted, usage.Source)
	for _, n := range notes {
		fmt.Fprintf(stdout, "note: %s\n", n)
	}
	fmt.Fprintf(stdout, "report: %s\n", filepath.Join(o.out, "report.md"))
	if rep.Summary.Drift {
		return exitDrift
	}
	return exitOK
}

// baseResult checks out ref in a temporary git worktree and runs the same
// diff there, with the same doc model and options, so findings can be
// compared with the base revision. It returns the base's short commit.
func baseResult(o options, raw *docmodel.Model, docs []docmodel.Doc) (*drift.Result, string, error) {
	abs, err := filepath.Abs(o.repo)
	if err != nil {
		return nil, "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	top, err := git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", fmt.Errorf("--base needs --repo inside a git repository: %w", err)
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil {
		return nil, "", err
	}
	sha, err := git(top, "rev-parse", "--short", "--verify", o.base+"^{commit}")
	if err != nil {
		return nil, "", fmt.Errorf("unknown revision (fetch it first): %w", err)
	}
	tmp, err := os.MkdirTemp("", "archdrift-base-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "base")
	if _, err := git(top, "worktree", "add", "--detach", "--quiet", wt, sha); err != nil {
		return nil, "", err
	}
	defer git(top, "worktree", "remove", "--force", wt) //nolint:errcheck // best effort cleanup
	baseRepo := filepath.Join(wt, rel)
	if _, err := os.Stat(filepath.Join(baseRepo, "go.mod")); errors.Is(err, fs.ErrNotExist) {
		// The repo (e.g. a new service) does not exist on the base revision:
		// everything in it is new.
		return &drift.Result{}, sha + ", where " + filepath.ToSlash(rel) + " does not exist yet", nil
	}
	code, err := codemodel.Scan(baseRepo, o.scan)
	if err != nil {
		return nil, "", fmt.Errorf("scan base: %w", err)
	}
	verified, _ := docmodel.Verify(raw, docs, code.IDs())
	return drift.Diff(code, verified, o.diff), sha, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// baselineDays is how long a new baseline entry stays accepted by default.
const baselineDays = 90

func runBaseline(ctx context.Context, o options, apiExtractor func() docmodel.Extractor, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	prev, p, err := loadBaseline(o, true)
	if err != nil {
		return fail(err)
	}
	a, err := analyze(ctx, o, apiExtractor)
	if err != nil {
		return fail(err)
	}
	bl := drift.NewBaseline(a.res, prev, o.today, baselineDays)
	if err := drift.WriteBaseline(p, bl); err != nil {
		return fail(fmt.Errorf("write baseline: %w", err))
	}
	kept, added := 0, 0
	for _, e := range bl.Accepted {
		if e.Reason == drift.TodoReason {
			added++
		} else {
			kept++
		}
	}
	fmt.Fprintf(stdout, "archdrift: wrote %s: %d accepted finding(s) (%d kept, %d new)\n", p, len(bl.Accepted), kept, added)
	if added > 0 {
		fmt.Fprintf(stdout, "Replace each %q with the real reason and adjust \"expires\" (default %d days) before committing.\n", drift.TodoReason, baselineDays)
	}
	return exitOK
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	var o options
	asJSON := fs.Bool("json", false, "")
	fs.StringVar(&o.repo, "repo", "", "")
	fs.StringVar(&o.config, "config", "", "")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if o.repo == "" || fs.NArg() > 0 {
		fs.Usage()
		return exitError
	}
	if err := resolveConfig(&o); err != nil {
		fmt.Fprintf(stderr, "archdrift: %v\n", err)
		return exitError
	}
	m, err := codemodel.Scan(o.repo, o.scan)
	if err != nil {
		fmt.Fprintf(stderr, "archdrift: scan repo: %v\n", err)
		return exitError
	}
	if *asJSON {
		b, _ := json.MarshalIndent(m, "", "  ")
		stdout.Write(append(b, '\n'))
		return exitOK
	}
	if o.configPath != "" {
		fmt.Fprintf(stdout, "config: %s\n", o.configPath)
	}
	fmt.Fprintf(stdout, "module %s: %d components, %d import edges\n", m.Module, len(m.Components), len(m.Edges))
	fmt.Fprintf(stdout, "coverage: %d of %d Go packages (%d%%) belong to a component; %d shared\n", m.Coverage.Modeled, m.Coverage.Packages, m.Coverage.Percent(), len(m.Coverage.Shared))
	for i, d := range m.Coverage.Unmodeled {
		if i == 0 {
			fmt.Fprint(stdout, "unmodeled:")
		}
		if i == 8 {
			fmt.Fprint(stdout, " ...")
			break
		}
		fmt.Fprintf(stdout, " %s (%d)", d.Dir, d.Packages)
		if i == len(m.Coverage.Unmodeled)-1 {
			fmt.Fprintln(stdout)
		}
	}
	fmt.Fprintln(stdout)
	out, in := map[string]int{}, map[string]int{}
	for _, e := range m.Edges {
		out[e.From]++
		in[e.To]++
	}
	fmt.Fprintf(stdout, "%-32s %-10s %8s %6s %6s\n", "COMPONENT", "KIND", "PACKAGES", "OUT", "IN")
	for _, c := range m.Components {
		kind := "library"
		switch {
		case c.External:
			kind = "external"
		case c.Deployable:
			kind = "deployable"
		}
		fmt.Fprintf(stdout, "%-32s %-10s %8d %6d %6d\n", c.ID, kind, len(c.Packages), out[c.ID], in[c.ID])
	}
	return exitOK
}

// lowCoverage is the share of packages below which check warns that most of
// the repo is not modeled.
const lowCoverage = 50

func coverageNotes(c codemodel.Coverage) []string {
	if c.Percent() >= lowCoverage {
		return nil
	}
	var top []string
	for i, d := range c.Unmodeled {
		if i == 5 {
			break
		}
		top = append(top, fmt.Sprintf("%s (%d)", d.Dir, d.Packages))
	}
	return []string{fmt.Sprintf("Only %d of %d Go packages (%d%%) belong to a component, so most of the repo is not checked. Largest unmodeled directories: %s. Map them in archdrift.json; `archdrift scan` shows what is modeled.",
		c.Modeled, c.Packages, c.Percent(), strings.Join(top, ", "))}
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
	p := o.lock
	if p == "" {
		p = docmodel.DefaultLockPath(o.docs[0])
	}
	ids := in.code.IDs()

	// An existing lock holds the architect's review: keep it unless --fresh.
	var prev *docmodel.Lock
	if !o.fresh {
		switch l, err := docmodel.LoadLock(p); {
		case err == nil:
			prev = l
		case !errors.Is(err, fs.ErrNotExist):
			return fail(fmt.Errorf("read previous lock (use --fresh to replace it): %w", err))
		}
	}
	if prev != nil && prev.DocSHA256 == docmodel.DocHash(in.doc) {
		fmt.Fprintf(stdout, "archdrift: %s is up to date with the doc; nothing to do (no API call). Use --fresh to re-extract from scratch.\n", p)
		for _, n := range lockNotes(prev, ids) {
			fmt.Fprintf(stdout, "note: %s\n", n)
		}
		return exitOK
	}

	ext := apiExtractor()
	if o.docModel != "" {
		ext = docmodel.FileExtractor{Path: o.docModel}
	}
	raw, usage, err := extract(ctx, ext, in, o.model)
	if err != nil {
		return fail(err)
	}
	verified, dropped := docmodel.Verify(raw, in.docs, ids)
	var changes []docmodel.Change
	if prev != nil {
		verified, changes = docmodel.MergeLock(prev.DocModel, raw, in.docs, ids)
	}
	if len(verified.Components) == 0 {
		return fail(errors.New("no component survived verification; nothing to lock"))
	}
	model := usage.Model
	if usage.Source == "recorded" {
		model = "recorded"
	}
	if err := docmodel.WriteLock(p, docmodel.NewLock(in.doc, ids, model, verified)); err != nil {
		return fail(fmt.Errorf("write lock: %w", err))
	}
	writeLockReview(stdout, p, model, usage, verified, dropped, ids)
	if prev != nil {
		writeLockChanges(stdout, changes)
	}
	return exitOK
}

// writeLockChanges prints what a re-lock changed relative to the reviewed lock.
func writeLockChanges(w io.Writer, changes []docmodel.Change) {
	fmt.Fprintln(w, "\nChanges since the previous lock (reviewed claims that still match the doc were kept):")
	if len(changes) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, c := range changes {
		detail := ""
		if c.Detail != "" {
			detail = ": " + c.Detail
		}
		fmt.Fprintf(w, "  %-8s %s %s%s\n", c.Kind, c.Claim, c.Name, detail)
	}
	fmt.Fprintln(w, "Review the conflicts: edit the lock to take the model's suggestion if it is right.")
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
