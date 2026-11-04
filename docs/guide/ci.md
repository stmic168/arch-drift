# CI and pull requests

[README](../../README.md) · [Usage](usage.md) · [Configuration](configuration.md) · [CI and pull requests](ci.md) · [How it works](how-it-works.md) · [Development](development.md)

## GitHub Action

`action.yml` makes the repository usable as a step. With a committed lock no API key is needed; on `pull_request` events it compares with the PR base by default, so only drift the PR introduces fails.

```yaml
name: architecture
on: pull_request
permissions:
  contents: read
  security-events: write   # only for upload-sarif
jobs:
  archdrift:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: stmic168/arch-drift@v0.1.0  # pin a release; @main follows development
        with:
          docs: docs/architecture.md        # comma- or newline-separated
          # config: archdrift.json          # default: archdrift.json in repo-path
          # lock: docs/architecture.lock.json  # default: next to the first doc
          # base: none                      # check everything instead of the PR diff
          upload-sarif: "true"              # annotate the PR's changed lines
          # anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}  # only without a lock
```

Inputs: `docs`, `repo-path`, `config`, `lock`, `base`, `model`, `anthropic-api-key`, `out`, `upload-sarif`, `fail-on-drift`. Outputs: `exit-code`, `report`, `sarif`. `report.md` is added to the job summary. `.github/workflows/action-selftest.yml` runs the action against the fixture on every PR.

## Pull requests: `--base` and SARIF

`archdrift check --base origin/main ...` checks the base revision out in a temporary git worktree, runs the same doc model and options there, and compares. Each finding gets a status:

- `new`: absent on the base. Fails.
- `worsened`: present on the base, but with code evidence in files the base finding did not have (compared by file, so shifted lines do not count). Fails, and the report lists the new evidence.
- `existing`: already on the base and nothing new. Does not fail; listed under "Pre-existing drift".

Failing base findings that disappeared are listed as "Fixed by this change". `--repo` may be a subdirectory of the git repository; the base commit must be available locally (fetch it first in a shallow clone).

Every `check` also writes `<out>/report.sarif` (SARIF 2.1.0) for GitHub code scanning: one rule per finding type, failing findings as errors (warnings for low severity), others as notes, located at the code evidence or, for findings without code, at the quote's line in the doc.
