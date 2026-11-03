# Action demo fixture

A copy of `testdata/shop` (code, `design.md` and its committed lock) used only by the
`demo-gate` job in `.github/workflows/action-selftest.yml`. Go tests do not read it, so a
pull request from a `demo/...` branch can add drift here to see the GitHub Action fail and
annotate the changed lines, without touching the fixtures the tests depend on.
