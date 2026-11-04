package report

import (
	"fmt"
	"strings"

	"github.com/stmic168/arch-drift/internal/drift"
)

// maxCommentRows caps the findings listed in the pull request comment; the
// full list stays in report.md.
const maxCommentRows = 30

// Comment renders the short pull request comment (comment.md): the verdict,
// one row per failing finding with its status and first evidence, and the
// counts that do not fail. The action posts it once and updates it in place.
func Comment(r *Report) string {
	var b strings.Builder
	s := r.Summary
	if s.Failing == 0 {
		b.WriteString("### archdrift: no architecture drift")
		if r.Base != "" {
			b.WriteString(" introduced by this change")
		}
		b.WriteString("\n\n")
	} else {
		fmt.Fprintf(&b, "### archdrift: %d failing finding(s)\n\n", s.Failing)
	}
	if r.Base != "" {
		fmt.Fprintf(&b, "Compared with `%s`: only drift this change introduces or worsens fails.\n\n", r.Base)
	}

	var rows []drift.Finding
	for _, f := range r.Findings {
		if f.Failing && f.Accepted == nil && f.Status != drift.StatusExisting {
			rows = append(rows, f)
		}
	}
	if len(rows) > 0 {
		status := r.Base != ""
		b.WriteString("| Severity | Finding |")
		if status {
			b.WriteString(" Status |")
		}
		b.WriteString(" Evidence |\n|---|---|")
		if status {
			b.WriteString("---|")
		}
		b.WriteString("---|\n")
		for i, f := range rows {
			if i == maxCommentRows {
				fmt.Fprintf(&b, "\n%d more in the full report.\n", len(rows)-maxCommentRows)
				break
			}
			fmt.Fprintf(&b, "| %s | `%s` %s |", f.Severity, f.Type, mdEscape(f.Subject))
			if status {
				fmt.Fprintf(&b, " %s |", f.Status)
			}
			fmt.Fprintf(&b, " %s |\n", commentEvidence(f))
		}
		b.WriteString("\n")
	}

	var info []string
	if s.Existing > 0 {
		info = append(info, fmt.Sprintf("%d pre-existing", s.Existing))
	}
	if s.Fixed > 0 {
		info = append(info, fmt.Sprintf("%d fixed by this change", s.Fixed))
	}
	if s.Accepted > 0 {
		info = append(info, fmt.Sprintf("%d accepted by the baseline", s.Accepted))
	}
	if n := s.ByType[drift.Unverifiable]; n > 0 {
		info = append(info, fmt.Sprintf("%d unverifiable", n))
	}
	if s.Dropped > 0 {
		info = append(info, fmt.Sprintf("%d dropped doc claim(s)", s.Dropped))
	}
	if len(info) > 0 {
		fmt.Fprintf(&b, "Not failing: %s.\n\n", strings.Join(info, ", "))
	}
	b.WriteString("Full report with doc quotes and suggested fixes: the job summary of this check.\n")
	return b.String()
}

// commentEvidence is the evidence a reviewer opens first: new evidence for a
// worsened finding, else the first code location, else the doc quote.
func commentEvidence(f drift.Finding) string {
	locs := f.Code
	if len(f.NewEvidence) > 0 {
		locs = f.NewEvidence
	}
	if len(locs) > 0 {
		e := "`" + locs[0].String() + "`"
		if len(locs) > 1 {
			e += fmt.Sprintf(" (+%d)", len(locs)-1)
		}
		return e
	}
	if q := strings.TrimSpace(f.DocQuote); q != "" {
		if len(q) > 120 {
			q = q[:117] + "..."
		}
		return "doc: \"" + mdEscape(q) + "\""
	}
	return "none"
}
