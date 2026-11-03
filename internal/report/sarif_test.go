package report

import "testing"

func TestQuoteLine(t *testing.T) {
	doc := "# Title\n\nThe API calls\n   the Store.\nThe Worker  runs.\n"
	cases := map[string]int{
		"The API calls the Store.": 3, // spans two lines, extra indentation
		"The Worker runs.":         5,
		"# Title":                  1,
		"Not in the doc.":          0,
		"   ":                      0,
	}
	for q, want := range cases {
		if got := quoteLine(doc, q); got != want {
			t.Errorf("quoteLine(%q) = %d, want %d", q, got, want)
		}
	}
}
